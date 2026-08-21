package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/pion/rtp"
)

func h264AUHasIDR(au [][]byte) bool {
	for _, n := range au {
		if len(n) > 0 && n[0]&0x1F == 5 {
			return true
		}
	}
	return false
}

func h264AUHasSPS(au [][]byte) bool {
	for _, n := range au {
		if len(n) > 0 && n[0]&0x1F == 7 {
			return true
		}
	}
	return false
}

// H.265 NAL type in first byte: (nalu[0] >> 1) & 0x3F
func h265AUHasIRAP(au [][]byte) bool {
	for _, n := range au {
		if len(n) == 0 {
			continue
		}
		t := (n[0] >> 1) & 0x3F
		// BLA/IDR/CRA (16–21)
		if t >= 16 && t <= 21 {
			return true
		}
	}
	return false
}

func h265AUHasVPS(au [][]byte) bool {
	for _, n := range au {
		if len(n) > 0 && (n[0]>>1)&0x3F == 32 {
			return true
		}
	}
	return false
}

// isVideoKeyframe is true for H.264 IDR or H.265 IRAP access units.
func isVideoKeyframe(au [][]byte) bool {
	return h264AUHasIDR(au) || h265AUHasIRAP(au)
}

func (w *CameraWorker) runSub(ctx context.Context) {
	defer w.clearLooksee()

	u, err := base.ParseURL(w.cfg.Sub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	c := rtspTCPClient(u)
	if err := c.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}
	defer c.Close()
	defer w.clearLooksee()
	go func() {
		<-ctx.Done()
		c.Close()
	}()

	desc, _, err := c.Describe(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	var h265 *format.H265
	if medi := desc.FindFormat(&h265); medi != nil {
		w.setSubCodec(CodecHEVC)
		// HEVC: prepend SDP VPS/SPS/PPS only when AU lacks in-band VPS (avoid double-inject).
		w.runSubDecoded(ctx, c, desc, medi, h265, CodecHEVC,
			func() (func(*rtp.Packet) ([][]byte, error), error) {
				d, err := h265.CreateDecoder()
				if err != nil {
					return nil, err
				}
				return d.Decode, nil
			},
			func(err error) bool {
				return errors.Is(err, rtph265.ErrMorePacketsNeeded) ||
					errors.Is(err, rtph265.ErrNonStartingPacketAndNoPrevious)
			},
			func(au [][]byte) []byte {
				if h265AUHasVPS(au) {
					return annexB(au...)
				}
				parts := [][]byte{h265.VPS, h265.SPS, h265.PPS}
				parts = append(parts, au...)
				return annexB(parts...)
			},
			nil,
			nil,
		)
		return
	}

	var h264 *format.H264
	if medi := desc.FindFormat(&h264); medi != nil {
		w.setSubCodec(CodecAVC)
		w.setSubH264Params(h264.SPS, h264.PPS)
		// H.264: some backends break if SPS/PPS are re-injected on every frame after init.
		// Prepend SDP params only until the first decoded frame, and on IDR thereafter.
		var decReady bool
		w.runSubDecoded(ctx, c, desc, medi, h264, CodecAVC,
			func() (func(*rtp.Packet) ([][]byte, error), error) {
				d, err := h264.CreateDecoder()
				if err != nil {
					return nil, err
				}
				return d.Decode, nil
			},
			func(err error) bool {
				return errors.Is(err, rtph264.ErrMorePacketsNeeded) ||
					errors.Is(err, rtph264.ErrNonStartingPacketAndNoPrevious)
			},
			func(au [][]byte) []byte {
				needParams := !decReady || h264AUHasIDR(au)
				if needParams && !h264AUHasSPS(au) {
					parts := [][]byte{h264.SPS, h264.PPS}
					parts = append(parts, au...)
					return annexB(parts...)
				}
				return annexB(au...)
			},
			func() { decReady = true },
			func() { decReady = false },
		)
		return
	}

	fmt.Fprintf(os.Stderr, "%s sub: no H265/H264\n", w.cfg.Name)
}

func (w *CameraWorker) runSubDecoded(
	ctx context.Context,
	c *gortsplib.Client,
	desc *description.Session,
	medi *description.Media,
	forma format.Format,
	codec int,
	makeDec func() (func(*rtp.Packet) ([][]byte, error), error),
	softErr func(error) bool,
	toAnnexB func([][]byte) []byte,
	onDecodedFrame func(), // optional (H.264: stop re-injecting SPS/PPS)
	onHardErr func(), // optional (H.264: re-allow SPS after decoder recreate)
) {
	dec, rc := NewFrameDecoder(codec)
	if rc != 0 || dec == nil {
		fmt.Fprintf(os.Stderr, "%s sub: decoder init %d codec=%d\n", w.cfg.Name, rc, codec)
		return
	}
	// Closure so recreate can reassign dec and exit still frees the live session.
	defer func() {
		if dec != nil {
			dec.Close()
		}
	}()

	decodeRTP, err := makeDec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	if _, err = c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	const (
		moreDataStallN = 45
		recreateMinGap = 500 * time.Millisecond
		// Large gap between live thumbs → clear thumb ring (avoid false 99% after stall).
		thumbGapRebase = 2 * time.Second
	)

	// Temporal motion: mean(recent avg) vs mean(older at lag). See docs/MOTION_DESIGN.md.
	tRing := newThumbRing(w.lagFrames + w.avgFrames + 8)
	recentBuf := make([]byte, thumbSize)
	olderBuf := make([]byte, thumbSize)

	var loggedErr bool
	var lastRecreate time.Time
	var lastThumbAt time.Time
	var moreDataStreak int
	var streak int
	var quietN int
	var floor quietBlob
	thumb := make([]byte, thumbSize)

	// Rate log: AUs vs live thumbs per second (no oneshot path).
	var auN, liveThumbN int
	rateWindowStart := time.Now()

	c.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
		if ctx.Err() != nil {
			return
		}

		au, err := decodeRTP(pkt)
		if err != nil {
			if softErr(err) {
				return
			}
			return
		}

		bitstream := toAnnexB(au)
		if len(bitstream) == 0 {
			return
		}
		auN++

		// Keep recent sub AUs for looksee HTTP snap only (not motion fallback).
		w.setLatestSubAU(bitstream)

		// Live decoder may be nil after a failed reinit until stream reconnects.
		var rc int = 1
		gotThumb := false
		if dec != nil {
			rc = dec.Thumb(bitstream, thumb)
			gotThumb = rc == DecOK
			if gotThumb {
				liveThumbN++
			}
		}
		if rc == DecMoreData {
			moreDataStreak++
		} else if rc == DecOK {
			moreDataStreak = 0
		}

		if !gotThumb {
			if rc != DecMoreData && rc != DecOK {
				if !loggedErr {
					fmt.Fprintf(os.Stderr, "%s sub: thumb error %d\n", w.cfg.Name, rc)
					loggedErr = true
				}
			}
			// Stuck live session: hard error or long MORE_DATA streak → recreate live decoder.
			stalled := rc != DecMoreData || moreDataStreak >= moreDataStallN
			if stalled && time.Since(lastRecreate) >= recreateMinGap {
				lastRecreate = time.Now()
				moreDataStreak = 0
				if dec != nil {
					dec.Close()
					dec = nil
				}
				nd, nrc := NewFrameDecoder(codec)
				if nrc != 0 || nd == nil {
					fmt.Fprintf(os.Stderr, "%s sub: decoder reinit %d — reconnecting\n", w.cfg.Name, nrc)
					dec = nil
					c.Close()
					return
				}
				dec = nd
				tRing.clear()
				streak = 0
				fmt.Fprintf(os.Stderr, "%s sub: decoder recreated after stall\n", w.cfg.Name)
				if onHardErr != nil {
					onHardErr()
				}
			}
			return
		}

		if onDecodedFrame != nil && rc == DecOK {
			onDecodedFrame()
		}

		now := time.Now()
		if el := now.Sub(rateWindowStart); el >= time.Second {
			sec := el.Seconds()
			need := 0
			if floor.ready() {
				need = floor.need(w.minBlob)
			}
			w.logQ <- fmt.Sprintf("%s: rate au=%.1f/s live_thumb=%.1f/s blob≥%d",
				w.cfg.Name, float64(auN)/sec, float64(liveThumbN)/sec, need)
			auN, liveThumbN = 0, 0
			rateWindowStart = now
		}
		if !lastThumbAt.IsZero() && now.Sub(lastThumbAt) > thumbGapRebase {
			tRing.clear()
			streak = 0
		}
		lastThumbAt = now

		tRing.push(thumb)
		sad, blob, nCh, ready := motionScoreTemporal(tRing, w.avgFrames, w.lagFrames, w.ignore, recentBuf, olderBuf)
		if !ready {
			return
		}
		// Reconnect / first GOP is ~99% SAD vs an empty lag window — not a person.
		if sad >= 90 {
			streak = 0
			return
		}

		need := 0
		if floor.ready() {
			need = floor.need(w.minBlob)
		}
		if motionHit(sad, blob, nCh, w.minBlob, need, w.minCompact) {
			quietN = 0
			if streak < w.sustainFrames {
				streak++
			}
			if streak >= w.sustainFrames {
				w.fireMotion(sad)
				streak = 0
			}
		} else {
			if blobLooksQuiet(blob, nCh) {
				floor.push(blob)
			}
			streak = 0
			if w.episode {
				quietN++
				needQuiet := w.lagFrames
				if needQuiet < 1 {
					needQuiet = 1
				}
				if quietN >= needQuiet {
					w.episode = false
					quietN = 0
					tRing.clear()
				}
			}
		}
	})

	if _, err = c.Play(nil); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}
	if ctx.Err() == nil {
		w.setStatus("connected")
	}

	// Frozen sub: RTSP stays up but AUs stop (looksee stuck, motion never fires).
	// Close after 5s with no AU so Run() reconnects.
	const subAUStale = 5 * time.Second
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return
		case <-time.After(subAUStale):
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if w.subAUStale(subAUStale) {
					fmt.Fprintf(os.Stderr, "%s sub: no AU for %s — reconnecting\n", w.cfg.Name, subAUStale)
					w.setStatus("reconnecting")
					w.clearLooksee()
					c.Close()
					return
				}
			}
		}
	}()

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	<-done
}
