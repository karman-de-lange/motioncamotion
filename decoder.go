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
const stillMaxW = 1920
const stillMaxH = 1080

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

var openFrameDecoder = newFrameDecoder

// NewFrameDecoder creates a backend decoder for codec (CodecHEVC or CodecAVC).
// rc != 0 means init failed; d is nil.
// Backend is selected at build time (see decoder_vpl.go / decoder_nvdec.go).
func NewFrameDecoder(codec int) (d FrameDecoder, rc int) {
	return openFrameDecoder(codec)
}

func decodeRGBNearEnd(segments [][]byte, outW, outH int) ([]byte, int, bool) {
	dec, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || dec == nil {
		return nil, 0, false
	}
	defer dec.Close()
	return decodeRGBFeedWith(dec, segments, outW, outH)
}

func decodeRGBNearEndWith(dec FrameDecoder, segments [][]byte, outW, outH int) ([]byte, int, bool) {
	if start := lastIRAPIndex(segments); start > 0 {
		segments = segments[start:]
	}
	return decodeRGBFeedWith(dec, segments, outW, outH)
}

func decodeRGBFeedWith(dec FrameDecoder, segments [][]byte, outW, outH int) ([]byte, int, bool) {
	rgb := make([]byte, outW*outH*3)
	last := make([]byte, outW*outH*3)
	frames := 0
	got := false
	for _, seg := range segments {
		if len(seg) == 0 {
			continue
		}
		if dec.RGB(seg, rgb, outW, outH) != DecOK {
			continue
		}
		frames++
		m := rgbCenterMean(rgb)
		if !rgbUsableMean(rgb, m) {
			continue
		}
		copy(last, rgb)
		got = true
	}
	if !got {
		return nil, frames, false
	}
	return last, frames, true
}

func decodeRGBFitNearEnd(segments [][]byte, maxW, maxH int) ([]byte, int, int, int, bool) {
	dec, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || dec == nil {
		return nil, 0, 0, 0, false
	}
	defer dec.Close()
	if start := lastIRAPIndex(segments); start > 0 {
		segments = segments[start:]
	}
	buf := make([]byte, maxW*maxH*3)
	var last []byte
	var lw, lh, frames int
	for _, seg := range segments {
		if len(seg) == 0 {
			continue
		}
		w, h, r := dec.RGBFit(seg, buf, maxW, maxH)
		if r != DecOK || w <= 0 || h <= 0 {
			continue
		}
		frames++
		n := w * h * 3
		if n > len(buf) {
			continue
		}
		last = append(last[:0], buf[:n]...)
		lw, lh = w, h
	}
	if len(last) == 0 {
		return nil, 0, 0, frames, false
	}
	return last, lw, lh, frames, true
}

func rgbCenterMean(rgb []byte) int {
	const w, h = inferSize, inferSize
	if len(rgb) < w*h*3 {
		return 0
	}
	var s, n int
	for y := h / 4; y < 3*h/4; y += 2 {
		off := y * w * 3
		for x := 0; x < w; x += 8 {
			i := off + x*3
			s += int(rgb[i]) + int(rgb[i+1]) + int(rgb[i+2])
			n += 3
		}
	}
	if n == 0 {
		return 0
	}
	return s / n
}

func rgbUsableMean(rgb []byte, mean int) bool {
	return mean >= 16 && len(rgb) >= 3
}

func rgbUsable(rgb []byte) bool { return rgbUsableMean(rgb, rgbCenterMean(rgb)) }

// rgbChromaPlausible is a test probe: U=V=0 green and UV-offset purple/magenta.
func rgbChromaPlausible(rgb []byte) bool {
	const w, h = inferSize, inferSize
	if len(rgb) < w*h*3 {
		return false
	}
	var n, greenish, magentish int
	for y := h / 4; y < 3*h/4; y += 4 {
		off := y * w * 3
		for x := w / 8; x < 7*w/8; x += 8 {
			i := off + x*3
			r, g, b := int(rgb[i]), int(rgb[i+1]), int(rgb[i+2])
			n++
			if g > r+48 && g > b+48 {
				greenish++
			}
			if r > g+48 && b > g+48 {
				magentish++
			}
		}
	}
	if n == 0 {
		return false
	}
	return greenish*5 < n*2 && magentish*5 < n*2
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
