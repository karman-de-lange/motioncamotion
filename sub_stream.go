package main

/*
#cgo CFLAGS: -I/usr/include/vpl
#cgo LDFLAGS: -lmfx-gen -lstdc++

#include <stdlib.h>
#include "vpl_decode.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/pion/rtp"
)

const inferSize = 640

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

	desc, _, err := c.Describe(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	var h265 *format.H265
	if medi := desc.FindFormat(&h265); medi != nil {
		w.setSubCodec(int(C.VPL_CODEC_HEVC))
		// HEVC: prepend SDP VPS/SPS/PPS only when AU lacks in-band VPS (avoid double-inject).
		w.runSubDecoded(ctx, c, desc, medi, h265, int(C.VPL_CODEC_HEVC),
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
		w.setSubCodec(int(C.VPL_CODEC_AVC))
		w.setSubH264Params(h264.SPS, h264.PPS)
		// VPL H.264 breaks if SPS/PPS are re-injected on every frame after init.
		// Prepend SDP params only until the first decoded frame, and on IDR thereafter.
		var decReady bool
		w.runSubDecoded(ctx, c, desc, medi, h264, int(C.VPL_CODEC_AVC),
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

// vplThumbFromAUs feeds AUs into a fresh VPL session; returns true if any thumb succeeded.
// Keeps the last successful thumb in yOut (looksee-style multi-AU warm-up).
func vplThumbFromAUs(codec int, aus [][]byte, yOut []byte) bool {
	var tmp *C.VPLDecoder
	if C.vpl_decoder_new(&tmp, C.int(codec)) != 0 {
		return false
	}
	defer C.vpl_decoder_free(tmp)
	ok := false
	for _, a := range aus {
		if len(a) == 0 {
			continue
		}
		if C.vpl_decoder_thumb(tmp,
			(*C.uint8_t)(unsafe.Pointer(&a[0])), C.size_t(len(a)),
			(*C.uint8_t)(unsafe.Pointer(&yOut[0])), C.int(len(yOut))) == 0 {
			ok = true
		}
	}
	return ok
}

// vplRGBFromAUs mirrors vplThumbFromAUs for stage1 RGB (last successful frame kept).
func vplRGBFromAUs(codec int, aus [][]byte, rgbOut []byte, outW, outH int) bool {
	var tmp *C.VPLDecoder
	if C.vpl_decoder_new(&tmp, C.int(codec)) != 0 {
		return false
	}
	defer C.vpl_decoder_free(tmp)
	ok := false
	for _, a := range aus {
		if len(a) == 0 {
			continue
		}
		if C.vpl_decoder_rgb(tmp,
			(*C.uint8_t)(unsafe.Pointer(&a[0])), C.size_t(len(a)),
			(*C.uint8_t)(unsafe.Pointer(&rgbOut[0])), C.int(len(rgbOut)),
			C.int(outW), C.int(outH)) == 0 {
			ok = true
		}
	}
	return ok
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
	onHardErr func(),      // optional (H.264: re-allow SPS after decoder recreate)
) {
	var dec *C.VPLDecoder
	if rc := C.vpl_decoder_new(&dec, C.int(codec)); rc != 0 {
		fmt.Fprintf(os.Stderr, "%s sub: vpl init %d codec=%d\n", w.cfg.Name, int(rc), codec)
		return
	}
	// Closure so recreate can reassign dec and exit still frees the live session.
	defer func() {
		if dec != nil {
			C.vpl_decoder_free(dec)
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
		moreDataStallN   = 45
		oneShotAUWindow  = 12
		oneShotMinGap    = 200 * time.Millisecond
		recreateMinGap   = 500 * time.Millisecond
		thumbGapRebase   = 500 * time.Millisecond // large gap → re-baseline SAD (no false 99%)
	)

	var prevY [thumbSize]byte
	var havePrev bool
	var loggedErr bool
	var loggedOneShot bool
	var lastRecreate time.Time
	var lastOneShot time.Time
	var lastThumbAt time.Time
	var moreDataStreak int
	var streak int
	thumb := make([]byte, thumbSize)
	rgbBuf := make([]byte, inferSize*inferSize*3)

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

		// Pre-H264 looksee: keep AUs in RAM; HTTP snap decodes on demand.
		w.setLatestSubAU(bitstream)

		// Live VPL may be nil after a failed reinit until stream reconnects.
		var rc int = 1
		gotThumb := false
		if dec != nil {
			rc = int(C.vpl_decoder_thumb(dec,
				(*C.uint8_t)(unsafe.Pointer(&bitstream[0])), C.size_t(len(bitstream)),
				(*C.uint8_t)(unsafe.Pointer(&thumb[0])), C.int(len(thumb))))
			gotThumb = rc == 0
		}
		if rc == 1 {
			moreDataStreak++
		} else if rc == 0 {
			moreDataStreak = 0
		}

		if !gotThumb {
			if rc != 1 && rc != 0 {
				if !loggedErr {
					fmt.Fprintf(os.Stderr, "%s sub: thumb error %d\n", w.cfg.Name, rc)
					loggedErr = true
				}
			}
			// Stuck live session: hard error or long MORE_DATA streak.
			stalled := rc != 1 || moreDataStreak >= moreDataStallN
			if stalled && time.Since(lastRecreate) >= recreateMinGap {
				lastRecreate = time.Now()
				moreDataStreak = 0
				if dec != nil {
					C.vpl_decoder_free(dec)
					dec = nil
				}
				if nrc := C.vpl_decoder_new(&dec, C.int(codec)); nrc != 0 {
					fmt.Fprintf(os.Stderr, "%s sub: vpl reinit %d — reconnecting\n", w.cfg.Name, int(nrc))
					dec = nil
					c.Close()
					return
				}
				havePrev = false
				streak = 0
				fmt.Fprintf(os.Stderr, "%s sub: vpl recreated after stall\n", w.cfg.Name)
				if onHardErr != nil {
					onHardErr()
				}
			}
			// One-shot only after a real stall — not during normal pre-IDR MORE_DATA warm-up.
			if !stalled {
				return
			}
			if time.Since(lastOneShot) < oneShotMinGap {
				return
			}
			lastOneShot = time.Now()
			aus := w.copyLatestSubsN(oneShotAUWindow)
			if len(aus) == 0 {
				aus = [][]byte{bitstream}
			}
			if !vplThumbFromAUs(codec, aus, thumb) {
				return
			}
			gotThumb = true
			if !loggedOneShot {
				fmt.Fprintf(os.Stderr, "%s sub: one-shot thumb after stall\n", w.cfg.Name)
				loggedOneShot = true
			}
		}

		if onDecodedFrame != nil && rc == 0 {
			onDecodedFrame()
		}

		now := time.Now()
		// Large gap between thumbs (stall / one-shot spacing): re-baseline, avoid 99% SAD spikes.
		if havePrev && !lastThumbAt.IsZero() && now.Sub(lastThumbAt) > thumbGapRebase {
			havePrev = false
			streak = 0
		}
		lastThumbAt = now

		if !havePrev {
			copy(prevY[:], thumb)
			havePrev = true
			return
		}

		sad, changed := sadMotion(prevY[:], thumb, w.ignore)
		blob := maxBlob(changed, thumbW, thumbH)
		if sad >= w.sadCutoff && blob >= w.minBlob {
			if streak < w.sustainFrames {
				streak++
			}
			if streak >= w.sustainFrames && w.tryMotionFire() {
				var subRGB []byte
				// Live RGB if healthy; else multi-AU one-shot (same window as thumb).
				rgbOK := false
				if dec != nil && rc == 0 {
					if C.vpl_decoder_rgb(dec,
						(*C.uint8_t)(unsafe.Pointer(&bitstream[0])), C.size_t(len(bitstream)),
						(*C.uint8_t)(unsafe.Pointer(&rgbBuf[0])), C.int(len(rgbBuf)),
						C.int(inferSize), C.int(inferSize)) == 0 {
						rgbOK = true
					}
				}
				if !rgbOK {
					aus := w.copyLatestSubsN(oneShotAUWindow)
					if len(aus) == 0 {
						aus = [][]byte{bitstream}
					}
					rgbOK = vplRGBFromAUs(codec, aus, rgbBuf, inferSize, inferSize)
				}
				if rgbOK {
					subRGB = make([]byte, len(rgbBuf))
					copy(subRGB, rgbBuf)
				}
				w.enqueueDetect("motion", sad, subRGB)
			}
		} else {
			streak = 0
		}
		copy(prevY[:], thumb)
	})

	if _, err = c.Play(nil); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	// Frozen sub: RTSP stays up but AUs stop (looksee stuck, motion never fires).
	// Close after 5s with no AU so Run() reconnects.
	const subAUStale = 5 * time.Second
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		// Grace period after Play before treating "no AU yet" as stale.
		time.Sleep(subAUStale)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if w.subAUStale(subAUStale) {
					fmt.Fprintf(os.Stderr, "%s sub: no AU for %s — reconnecting\n", w.cfg.Name, subAUStale)
					c.Close()
					return
				}
			}
		}
	}()

	go func() {
		<-ctx.Done()
		c.Close()
	}()

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()

	select {
	case <-ctx.Done():
	case <-done:
	}
}
