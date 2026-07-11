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

	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host}
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
		// Hik HEVC — unchanged from pre-Imou: always prepend SDP VPS/SPS/PPS.
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
				parts := [][]byte{h265.VPS, h265.SPS, h265.PPS}
				parts = append(parts, au...)
				return annexB(parts...)
			},
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
) {
	var dec *C.VPLDecoder
	if rc := C.vpl_decoder_new(&dec, C.int(codec)); rc != 0 {
		fmt.Fprintf(os.Stderr, "%s sub: vpl init %d codec=%d\n", w.cfg.Name, int(rc), codec)
		return
	}
	defer C.vpl_decoder_free(dec)

	decodeRTP, err := makeDec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	if _, err = c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", w.cfg.Name, err)
		return
	}

	var prevY [thumbSize]byte
	var havePrev bool
	var loggedErr bool
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

		// Motion thumb only (same as pre-looksee-RGB path).
		rc := C.vpl_decoder_thumb(dec,
			(*C.uint8_t)(unsafe.Pointer(&bitstream[0])), C.size_t(len(bitstream)),
			(*C.uint8_t)(unsafe.Pointer(&thumb[0])), C.int(len(thumb)))
		if rc == 1 {
			return
		}
		if rc != 0 {
			if !loggedErr {
				fmt.Fprintf(os.Stderr, "%s sub: thumb error %d\n", w.cfg.Name, int(rc))
				loggedErr = true
			}
			return
		}

		if onDecodedFrame != nil {
			onDecodedFrame()
		}

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
				if rc := C.vpl_decoder_rgb(dec,
					(*C.uint8_t)(unsafe.Pointer(&bitstream[0])), C.size_t(len(bitstream)),
					(*C.uint8_t)(unsafe.Pointer(&rgbBuf[0])), C.int(len(rgbBuf)),
					C.int(inferSize), C.int(inferSize)); rc == 0 {
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
