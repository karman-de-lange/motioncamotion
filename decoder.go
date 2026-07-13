package main

import "fmt"

// Video codec for FrameDecoder sessions. Values match VPL_CODEC_* in vpl_decode.h
// so the Intel backend can pass them through unchanged.
const (
	CodecHEVC = 0
	CodecAVC  = 1
)

// Decode return codes used by the universal core (motion stall, warm-up).
// Backends must preserve this semantics:
//
//	0  = frame ready
//	1  = need more data (normal pre-IDR warm-up)
//	other = hard error
const (
	DecOK       = 0
	DecMoreData = 1
)

const inferSize = 640

// FrameDecoder turns annex-B access units into portable pixels.
// Implementations are platform-specific (VPL, future NVDEC, VideoToolbox).
// The universal core never sees backend types — only []byte and these codes.
type FrameDecoder interface {
	// Thumb writes a thumbW×thumbH Y plane into yOut (len >= thumbSize).
	Thumb(au []byte, yOut []byte) int
	// RGB scales into outW×outH RGB (len(rgbOut) >= outW*outH*3).
	RGB(au []byte, rgbOut []byte, outW, outH int) int
	// RGBFit scales into maxW×maxH preserving aspect; returns actual size.
	RGBFit(au []byte, rgbOut []byte, maxW, maxH int) (outW, outH int, rc int)
	Close()
}

// NewFrameDecoder creates a backend decoder for codec (CodecHEVC or CodecAVC).
// rc != 0 means init failed; d is nil.
// Backend is selected at build time (see decoder_vpl.go / decoder_nvdec.go).
func NewFrameDecoder(codec int) (d FrameDecoder, rc int) {
	return newFrameDecoder(codec)
}

// decodeRGBBest picks the brightest successfully decoded frame (avoids black/partial).
// Used for primary (main-ring) stage-2 detect — same AU list for every backend.
func decodeRGBBest(segments [][]byte, outW, outH int) ([]byte, int, bool) {
	// Main ring is H.265 in current deployments (Hik + Imou main).
	dec, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || dec == nil {
		return nil, 0, false
	}
	defer dec.Close()

	rgb := make([]byte, outW*outH*3)
	best := make([]byte, outW*outH*3)
	frames := 0
	bestMean := -1
	for _, seg := range segments {
		if len(seg) == 0 {
			continue
		}
		if dec.RGB(seg, rgb, outW, outH) != DecOK {
			continue
		}
		frames++
		mean := rgbMean(rgb)
		if mean > bestMean {
			bestMean = mean
			copy(best, rgb)
		}
	}
	if frames > 0 && bestMean > 0 {
		return best, frames, true
	}
	return nil, 0, false
}

// decodeAUsToRGBFit feeds AUs into a fresh decoder → RGB fit (looksee).
func decodeAUsToRGBFit(aus [][]byte, maxW, maxH, codec int) (rgb []byte, outW, outH int, err error) {
	if len(aus) == 0 {
		return nil, 0, 0, fmt.Errorf("no sub frame yet")
	}
	previewDecMu.Lock()
	defer previewDecMu.Unlock()

	dec, rc := NewFrameDecoder(codec)
	if rc != 0 || dec == nil {
		return nil, 0, 0, fmt.Errorf("decoder new %d", rc)
	}
	defer dec.Close()

	buf := make([]byte, maxW*maxH*3)
	var lastRC int
	for _, au := range aus {
		if len(au) == 0 {
			continue
		}
		w, h, r := dec.RGBFit(au, buf, maxW, maxH)
		lastRC = r
		if r == DecOK {
			out := make([]byte, w*h*3)
			copy(out, buf[:w*h*3])
			return out, w, h, nil
		}
	}
	if lastRC == DecMoreData {
		return nil, 0, 0, fmt.Errorf("no frame yet")
	}
	return nil, 0, 0, fmt.Errorf("decoder rgb_fit %d", lastRC)
}
