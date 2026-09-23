//go:build !nvidia_nvdec && !darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

func pix(rgb []byte, w, x, y int) (r, g, b byte) {
	i := (y*w + x) * 3
	return rgb[i], rgb[i+1], rgb[i+2]
}

func TestNV12LetterboxYExpand(t *testing.T) {
	// 2×2 studio black / white, 1:1 into 2×2. uv=nil → gray with Y expanded.
	black := []byte{16, 16, 16, 16}
	rgb := nv12LetterboxRGB(black, nil, 2, 2, 2, 2, 2)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			r, g, b := pix(rgb, 2, x, y)
			if r != 0 || g != 0 || b != 0 {
				t.Fatalf("Y=16 → want RGB 0,0,0 at %d,%d got %d,%d,%d", x, y, r, g, b)
			}
		}
	}

	white := []byte{235, 235, 235, 235}
	rgb = nv12LetterboxRGB(white, nil, 2, 2, 2, 2, 2)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			r, g, b := pix(rgb, 2, x, y)
			if r != 255 || g != 255 || b != 255 {
				t.Fatalf("Y=235 → want RGB 255,255,255 at %d,%d got %d,%d,%d", x, y, r, g, b)
			}
		}
	}
}

func TestNV12LetterboxPad114(t *testing.T) {
	// 4×2 → 4×4: pad_y=1, pad 114.
	y := []byte{
		235, 235, 235, 235,
		235, 235, 235, 235,
	}
	rgb := nv12LetterboxRGB(y, nil, 4, 4, 2, 4, 4)
	for x := 0; x < 4; x++ {
		r, g, b := pix(rgb, 4, x, 0)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("top pad want 114 got %d,%d,%d at x=%d", r, g, b, x)
		}
		r, g, b = pix(rgb, 4, x, 3)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("bottom pad want 114 got %d,%d,%d at x=%d", r, g, b, x)
		}
		r, g, b = pix(rgb, 4, x, 1)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content row1 want 255 got %d,%d,%d at x=%d", r, g, b, x)
		}
		r, g, b = pix(rgb, 4, x, 2)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content row2 want 255 got %d,%d,%d at x=%d", r, g, b, x)
		}
	}

	// 2×4 → 4×4: pad_x=1, pad 114.
	yp := []byte{
		235, 235,
		235, 235,
		235, 235,
		235, 235,
	}
	rgb = nv12LetterboxRGB(yp, nil, 2, 2, 4, 4, 4)
	for row := 0; row < 4; row++ {
		r, g, b := pix(rgb, 4, 0, row)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("left pad want 114 got %d,%d,%d at y=%d", r, g, b, row)
		}
		r, g, b = pix(rgb, 4, 3, row)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("right pad want 114 got %d,%d,%d at y=%d", r, g, b, row)
		}
		r, g, b = pix(rgb, 4, 1, row)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content want 255 got %d,%d,%d at 1,%d", r, g, b, row)
		}
		r, g, b = pix(rgb, 4, 2, row)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content want 255 got %d,%d,%d at 2,%d", r, g, b, row)
		}
	}
}

func TestNV12LetterboxDownscale2x2(t *testing.T) {
	// 4×4 → 2×2; horizontal 16/235 pairs average to Y=125 → RGB 127.
	y := make([]byte, 16)
	for i := range y {
		if i%2 == 0 {
			y[i] = 16
		} else {
			y[i] = 235
		}
	}
	rgb := nv12LetterboxRGB(y, nil, 4, 4, 4, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != 127 || g != 127 || b != 127 {
				t.Fatalf("2×2 avg want 127 got %d,%d,%d at %d,%d", r, g, b, px, py)
			}
		}
	}
}

func TestNV12LetterboxColour(t *testing.T) {
	// BT.601 limited red: Y=81 U=90 V=240 → R=255 G=0 B=0.
	y := []byte{81, 81, 81, 81}
	uv := []byte{90, 240, 90, 240}
	rgb := nv12LetterboxRGB(y, uv, 2, 2, 2, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != 255 || g != 0 || b != 0 {
				t.Fatalf("red want 255,0,0 at %d,%d got %d,%d,%d", px, py, r, g, b)
			}
		}
	}

	y4 := make([]byte, 16)
	for i := range y4 {
		y4[i] = 81
	}
	uv4 := []byte{90, 240, 90, 240, 90, 240, 90, 240}
	rgb = nv12LetterboxRGB(y4, uv4, 4, 4, 4, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != 255 || g != 0 || b != 0 {
				t.Fatalf("downscale red want 255,0,0 at %d,%d got %d,%d,%d", px, py, r, g, b)
			}
		}
	}
}

func TestNV12LetterboxPitchAndOdd(t *testing.T) {
	// pitch=8, srcW=4: padding 0xFF must not leak into RGB.
	y := make([]byte, 8*2)
	for i := range y {
		y[i] = 255
	}
	for row := 0; row < 2; row++ {
		for x := 0; x < 4; x++ {
			y[row*8+x] = 16
		}
	}
	rgb := nv12LetterboxRGB(y, nil, 8, 4, 2, 4, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 4; px++ {
			r, g, b := pix(rgb, 4, px, py)
			if r != 0 || g != 0 || b != 0 {
				t.Fatalf("padded pitch want 0 got %d,%d,%d at %d,%d", r, g, b, px, py)
			}
		}
	}

	// odd 3×3 1:1, Y=235 → 255, no read past pitch=3.
	odd := []byte{
		235, 235, 235,
		235, 235, 235,
		235, 235, 235,
	}
	rgb = nv12LetterboxRGB(odd, nil, 3, 3, 3, 3, 3)
	for py := 0; py < 3; py++ {
		for px := 0; px < 3; px++ {
			r, g, b := pix(rgb, 3, px, py)
			if r != 255 || g != 255 || b != 255 {
				t.Fatalf("odd want 255 got %d,%d,%d at %d,%d", r, g, b, px, py)
			}
		}
	}
}

func TestLetterboxInfer_zeroUVGrayNotGreen(t *testing.T) {
	y := []byte{128, 128, 128, 128}
	uv := []byte{0, 0, 0, 0}
	if useChroma(uv, 2, 2, 2) {
		t.Fatal("zero UV must not use chroma")
	}
	rgb := letterboxInferRGB(y, uv, 2, 2, 2, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != g || g != b {
				t.Fatalf("zero UV must stay gray, got %d,%d,%d at %d,%d", r, g, b, px, py)
			}
		}
	}

	// Same pixels through NV12 (useChroma wrongly true) are green, not gray.
	green := nv12LetterboxRGB(y, uv, 2, 2, 2, 2, 2)
	gr, gg, gb := pix(green, 2, 0, 0)
	if gr == gg && gg == gb {
		t.Fatal("control: raw zero-UV NV12 should not be gray")
	}
	if gg <= gr || gg <= gb {
		t.Fatalf("control: raw zero-UV NV12 should go green, got %d,%d,%d", gr, gg, gb)
	}
}

func TestLetterboxInfer_liveUVColour(t *testing.T) {
	y := []byte{81, 81, 81, 81}
	uv := []byte{90, 240, 90, 240}
	if !useChroma(uv, 2, 2, 2) {
		t.Fatal("live UV must use chroma")
	}
	rgb := letterboxInferRGB(y, uv, 2, 2, 2, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != 255 || g != 0 || b != 0 {
				t.Fatalf("live UV want red at %d,%d got %d,%d,%d", px, py, r, g, b)
			}
		}
	}
}

func TestUVPlaneDead(t *testing.T) {
	if !uvPlaneDead(nil, 8, 8, 8) {
		t.Fatal("nil should be dead")
	}
	if !uvPlaneDead([]byte{1, 2}, 1, 8, 8) {
		t.Fatal("tiny pitch should be dead")
	}
	if !uvPlaneDead([]byte{1, 2, 3, 4}, 8, 1, 8) {
		t.Fatal("tiny width should be dead")
	}
	if !uvPlaneDead([]byte{1, 2, 3, 4}, 8, 8, 1) {
		t.Fatal("tiny height should be dead")
	}

	// 8×8 luma → 8×4 UV. All-zero crop is dead even if stride padding is live.
	uv := make([]byte, 16*4)
	for i := range uv {
		uv[i] = 0
	}
	for row := 0; row < 4; row++ {
		for col := 8; col < 16; col++ {
			uv[row*16+col] = 0xFF
		}
	}
	if !uvPlaneDead(uv, 16, 8, 8) {
		t.Fatal("zeros in crop should be dead (padding must not count)")
	}

	uv[0] = 5
	uv[1] = 3
	if !uvPlaneDead(uv, 16, 8, 8) {
		t.Fatal("tiny UV garbage should stay dead (else stills go green)")
	}
	for row := 0; row < 4; row++ {
		for col := 0; col < 8; col += 2 {
			uv[row*16+col] = 128
			uv[row*16+col+1] = 128
		}
	}
	if uvPlaneDead(uv, 16, 8, 8) {
		t.Fatal("chroma-like crop samples should be live")
	}
}

func annexBAccessUnits(data []byte) [][]byte {
	var aus [][]byte
	var prefix []byte
	forEachNALU(data, func(nal []byte, nalType int) {
		if nalType <= 31 {
			au := make([]byte, 0, len(prefix)+len(nal))
			au = append(au, prefix...)
			au = append(au, nal...)
			aus = append(aus, au)
			prefix = nil
		} else {
			prefix = append(prefix, nal...)
		}
	})
	return aus
}

func hevcParamPrefix(data []byte) []byte {
	var out []byte
	seen := [3]bool{}
	forEachNALU(data, func(nal []byte, nalType int) {
		i := nalType - 32
		if i < 0 || i > 2 || seen[i] {
			return
		}
		seen[i] = true
		out = append(out, nal...)
	})
	return out
}

func hevcGOPAUs(data []byte, fromLastIRAP bool) [][]byte {
	aus := annexBAccessUnits(data)
	if len(aus) == 0 {
		return aus
	}
	start := 0
	if fromLastIRAP {
		start = lastIRAPIndex(aus)
	} else {
		for i, au := range aus {
			if segmentHasIRAP(au) {
				start = i
				break
			}
		}
	}
	aus = aus[start:]
	if len(aus) == 0 {
		return aus
	}
	hasVPS := false
	forEachNALU(aus[0], func(_ []byte, nalType int) {
		if nalType == 32 {
			hasVPS = true
		}
	})
	if !hasVPS {
		if ps := hevcParamPrefix(data); len(ps) > 0 {
			head := append([]byte{}, ps...)
			aus[0] = append(head, aus[0]...)
		}
	}
	return aus
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func rgbHasLiveChroma(rgb []byte) bool {
	const w, h = inferSize, inferSize
	if len(rgb) < w*h*3 {
		return false
	}
	maxd := 0
	for y := h / 4; y < 3*h/4; y += 4 {
		off := y * w * 3
		for x := w / 8; x < 7*w/8; x += 8 {
			i := off + x*3
			r, g, b := int(rgb[i]), int(rgb[i+1]), int(rgb[i+2])
			d := absInt(r - g)
			if v := absInt(g - b); v > d {
				d = v
			}
			if v := absInt(r - b); v > d {
				d = v
			}
			if d > maxd {
				maxd = d
			}
		}
	}
	return maxd > 0
}

func assertLetterboxPad(t *testing.T, rgb []byte, w, h int) {
	t.Helper()
	if len(rgb) < w*h*3 {
		t.Fatalf("rgb len %d", len(rgb))
	}
	for _, y := range []int{0, h - 1} {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			if rgb[i] != 114 || rgb[i+1] != 114 || rgb[i+2] != 114 {
				t.Fatalf("pad row %d x=%d want 114 got %d,%d,%d", y, x, rgb[i], rgb[i+1], rgb[i+2])
			}
		}
	}
	cy := h / 2
	saw := false
	for x := 0; x < w; x++ {
		i := (cy*w + x) * 3
		if rgb[i] != 114 || rgb[i+1] != 114 || rgb[i+2] != 114 {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatal("content row is pad 114 (stretch-fill?)")
	}
}

func assertInferRGB(t *testing.T, rgb []byte, what string) {
	t.Helper()
	if !rgbChromaPlausible(rgb) {
		t.Fatalf("%s green/purple", what)
	}
	if !rgbUsable(rgb) {
		t.Fatalf("%s unusable", what)
	}
	if !rgbHasLiveChroma(rgb) {
		t.Fatalf("%s grayscale (lost UV)", what)
	}
	assertLetterboxPad(t, rgb, inferSize, inferSize)
}

func decodeClipRGB(t *testing.T, dec FrameDecoder, aus [][]byte) (n int, first, last []byte) {
	t.Helper()
	buf := make([]byte, inferSize*inferSize*3)
	for i, au := range aus {
		if len(au) == 0 {
			continue
		}
		if dec.RGB(au, buf, inferSize, inferSize) != DecOK {
			continue
		}
		n++
		assertInferRGB(t, buf, fmt.Sprintf("frame %d", i))
		if first == nil {
			first = append([]byte{}, buf...)
		}
		last = append([]byte{}, buf...)
	}
	return n, first, last
}

func TestVPLRGB_hevcPFramesNotGreen(t *testing.T) {
	data, err := os.ReadFile("clips/hik1_20260611_224034.h265")
	if err != nil {
		t.Fatal(err)
	}
	aus := hevcGOPAUs(data, false)
	if len(aus) < 3 {
		t.Fatalf("need several AUs, got %d", len(aus))
	}
	dec, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || dec == nil {
		t.Fatalf("decoder new %d", rc)
	}
	v := dec.(*vplDecoder)
	defer dec.Close()

	n, first, last := decodeClipRGB(t, dec, aus)
	if n < len(aus)*3/4 {
		t.Fatalf("pictures=%d aus=%d (COMPLETE_FRAME missing?)", n, len(aus))
	}
	if first == nil || last == nil {
		t.Fatal("need IDR and P")
	}
	fm, lm := rgbCenterMean(first), rgbCenterMean(last)
	if lm < fm*7/10 {
		t.Fatalf("P-frame mean %d collapsed vs IDR %d", lm, fm)
	}
	dec.Close()
	if v.y != nil || v.uv != nil || v.dec != nil {
		t.Fatal("Close left session planes")
	}
}

func countClipRGBOK(t *testing.T, dec FrameDecoder, aus [][]byte) int {
	t.Helper()
	buf := make([]byte, inferSize*inferSize*3)
	n := 0
	for _, au := range aus {
		if len(au) == 0 {
			continue
		}
		if dec.RGB(au, buf, inferSize, inferSize) == DecOK {
			n++
		}
	}
	return n
}

func countClipThumbs(t *testing.T, dec FrameDecoder, aus [][]byte) int {
	t.Helper()
	y := make([]byte, thumbSize)
	n := 0
	for _, au := range aus {
		if len(au) == 0 {
			continue
		}
		if dec.Thumb(au, y) == DecOK {
			n++
		}
	}
	return n
}

func countClipRGBFit(t *testing.T, dec FrameDecoder, aus [][]byte) int {
	t.Helper()
	buf := make([]byte, inferSize*inferSize*3)
	n := 0
	for _, au := range aus {
		if len(au) == 0 {
			continue
		}
		_, _, rc := dec.RGBFit(au, buf, inferSize, inferSize)
		if rc == DecOK {
			n++
		}
	}
	return n
}

func TestVPL_hevcThumbWithoutCompleteFrame(t *testing.T) {
	data, err := os.ReadFile("clips/hik1_20260611_224034.h265")
	if err != nil {
		t.Fatal(err)
	}
	aus := hevcGOPAUs(data, false)
	if len(aus) < 3 {
		t.Fatalf("need several AUs, got %d", len(aus))
	}
	nv12Bar := len(aus) * 3 / 4

	nv12, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || nv12 == nil {
		t.Fatalf("nv12 decoder new %d", rc)
	}
	nv12N := countClipRGBOK(t, nv12, aus)
	nv12.Close()
	if nv12N < nv12Bar {
		t.Fatalf("nv12 pictures=%d aus=%d", nv12N, len(aus))
	}

	thumb, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || thumb == nil {
		t.Fatalf("thumb decoder new %d", rc)
	}
	tn := countClipThumbs(t, thumb, aus)
	thumb.Close()
	if tn < 2 || tn*4 < len(aus) {
		t.Fatalf("thumb pictures=%d aus=%d (stalled?)", tn, len(aus))
	}
	if tn >= nv12Bar || tn >= nv12N {
		t.Fatalf("thumb pictures=%d at NV12 complete-frame bar %d nv12=%d", tn, nv12Bar, nv12N)
	}

	fit, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || fit == nil {
		t.Fatalf("rgb_fit decoder new %d", rc)
	}
	fn := countClipRGBFit(t, fit, aus)
	fit.Close()
	if fn < 2 || fn*4 < len(aus) {
		t.Fatalf("rgb_fit pictures=%d aus=%d (stalled?)", fn, len(aus))
	}
	if fn >= nv12Bar || fn >= nv12N {
		t.Fatalf("rgb_fit pictures=%d at NV12 complete-frame bar %d nv12=%d", fn, nv12Bar, nv12N)
	}

	base, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || base == nil {
		t.Fatalf("base decoder new %d", rc)
	}
	y := make([]byte, thumbSize)
	_ = base.Thumb(aus[0], y)
	wantRest := countClipThumbs(t, base, aus[1:])
	base.Close()
	if wantRest < 1 {
		t.Fatal("no thumbs after IDR")
	}

	mix, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || mix == nil {
		t.Fatalf("mix decoder new %d", rc)
	}
	defer mix.Close()
	rgbBuf := make([]byte, inferSize*inferSize*3)
	if mix.RGB(aus[0], rgbBuf, inferSize, inferSize) != DecOK {
		t.Fatal("rgb IDR")
	}
	after := countClipThumbs(t, mix, aus[1:])
	if after < 1 {
		t.Fatal("thumb after rgb stalled")
	}
	if after != wantRest {
		t.Fatalf("thumb after rgb=%d want %d (no COMPLETE_FRAME)", after, wantRest)
	}
}

func TestVPLRGB_decodeRGBNearEndNotGreen(t *testing.T) {
	data, err := os.ReadFile("clips/hik1_20260611_224034.h265")
	if err != nil {
		t.Fatal(err)
	}
	aus := hevcGOPAUs(data, true)
	if len(aus) < 2 {
		t.Fatalf("last GOP too short: %d AUs", len(aus))
	}
	raw, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || raw == nil {
		t.Fatalf("decoder new %d", rc)
	}
	rawN, first, lastP := decodeClipRGB(t, raw, aus)
	raw.Close()
	if rawN < 2 {
		t.Fatal("need IRAP and P in last GOP")
	}
	if rawN < len(aus)*3/4 {
		t.Fatalf("last GOP pictures=%d aus=%d", rawN, len(aus))
	}
	if bytes.Equal(first, lastP) {
		t.Fatal("last P-frame identical to IRAP")
	}

	dec, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || dec == nil {
		t.Fatalf("decoder new %d", rc)
	}
	defer dec.Close()
	rgb, frames, ok := decodeRGBNearEndWith(dec, aus, inferSize, inferSize)
	if !ok {
		t.Fatalf("ok=%v frames=%d", ok, frames)
	}
	if frames != rawN {
		t.Fatalf("picker frames=%d independent=%d", frames, rawN)
	}
	if bytes.Equal(rgb, first) && !bytes.Equal(rgb, lastP) {
		t.Fatal("picker kept IRAP, not last P-frame")
	}
	if !bytes.Equal(rgb, lastP) {
		t.Fatal("picker output != last P-frame")
	}
	assertInferRGB(t, rgb, "near-end")
}

func TestVPLRGB_copyNV12Resize(t *testing.T) {
	v := &vplDecoder{}
	calls := 0
	const w, h = 2000, 1080
	y, uv, pitch, gotW, gotH, rc := v.copyNV12(func(ybuf, uvbuf []byte) (int, int, int) {
		calls++
		yneed := w * h
		uvneed := w * ((h + 1) / 2)
		if len(ybuf) < yneed || len(uvbuf) < uvneed {
			return w, h, -2
		}
		ybuf[0] = 7
		uvbuf[0] = 9
		return w, h, 0
	})
	if rc != 0 || calls != 2 || gotW != w || gotH != h || pitch != w {
		t.Fatalf("rc=%d calls=%d %dx%d pitch=%d", rc, calls, gotW, gotH, pitch)
	}
	if len(y) != w*h || len(uv) != w*((h+1)/2) || y[0] != 7 || uv[0] != 9 {
		t.Fatalf("y=%d uv=%d", len(y), len(uv))
	}

	zero := &vplDecoder{}
	calls = 0
	_, _, _, _, _, rc = zero.copyNV12(func([]byte, []byte) (int, int, int) {
		calls++
		return 0, 0, -2
	})
	if rc != -2 || calls != 1 {
		t.Fatalf("zero size rc=%d calls=%d", rc, calls)
	}

	stuck := &vplDecoder{}
	calls = 0
	_, _, _, _, _, rc = stuck.copyNV12(func([]byte, []byte) (int, int, int) {
		calls++
		return 3000, 2000, -2
	})
	if rc != -2 || calls != 2 {
		t.Fatalf("second -2 rc=%d calls=%d", rc, calls)
	}
}

func TestVPLRGB_jobFramesOneSurface(t *testing.T) {
	data, err := os.ReadFile("clips/hik1_20260611_224034.h265")
	if err != nil {
		t.Fatal(err)
	}
	aus := hevcGOPAUs(data, true)
	if len(aus) < 2 {
		t.Fatalf("last GOP too short: %d AUs", len(aus))
	}
	dec, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || dec == nil {
		t.Fatalf("decoder new %d", rc)
	}
	defer dec.Close()
	infer, still, sw, sh, frames, ok := dec.JobFrames(aus)
	if !ok || frames < 2 {
		t.Fatalf("ok=%v frames=%d", ok, frames)
	}
	if len(infer) != inferSize*inferSize*3 {
		t.Fatalf("infer len %d", len(infer))
	}
	near, nearRC := NewFrameDecoder(CodecHEVC)
	if nearRC != 0 || near == nil {
		t.Fatalf("near decoder new %d", nearRC)
	}
	defer near.Close()
	want, wantFrames, wantOK := decodeRGBNearEndWith(near, aus, inferSize, inferSize)
	if !wantOK || wantFrames != frames || !bytes.Equal(infer, want) {
		t.Fatalf("job infer != near-end ok=%v frames %d vs %d equal=%v", wantOK, frames, wantFrames, bytes.Equal(infer, want))
	}
	assertFitRGB(t, still, sw, sh, "job still")
	if sw <= inferSize && sh <= inferSize {
		t.Fatalf("still %dx%d is not a fit above the 640 infer image", sw, sh)
	}
}

func assertFitRGB(t *testing.T, rgb []byte, w, h int, what string) {
	t.Helper()
	if w < 2 || h < 2 || len(rgb) != w*h*3 {
		t.Fatalf("%s %dx%d len %d", what, w, h, len(rgb))
	}
	var s, n, greenish, magentish int
	maxd := 0
	for y := h / 4; y < 3*h/4; y += 4 {
		off := y * w * 3
		for x := w / 8; x < 7*w/8; x += 8 {
			i := off + x*3
			r, g, b := int(rgb[i]), int(rgb[i+1]), int(rgb[i+2])
			s += r + g + b
			n += 3
			d := absInt(r - g)
			if v := absInt(g - b); v > d {
				d = v
			}
			if v := absInt(r - b); v > d {
				d = v
			}
			if d > maxd {
				maxd = d
			}
			if g > r+48 && g > b+48 {
				greenish++
			}
			if r > g+48 && b > g+48 {
				magentish++
			}
		}
	}
	if n == 0 || s/n < 16 {
		t.Fatalf("%s unusable", what)
	}
	if maxd == 0 {
		t.Fatalf("%s grayscale", what)
	}
	samples := n / 3
	if greenish*5 >= samples*2 || magentish*5 >= samples*2 {
		t.Fatalf("%s green/purple", what)
	}
}

func TestVPLRGB_takeKeepsSurfaceOnMinusTwo(t *testing.T) {
	data, err := os.ReadFile("clips/hik1_20260611_224034.h265")
	if err != nil {
		t.Fatal(err)
	}
	aus := hevcGOPAUs(data, true)
	dec, rc := NewFrameDecoder(CodecHEVC)
	if rc != 0 || dec == nil {
		t.Fatalf("decoder new %d", rc)
	}
	defer dec.Close()
	v := dec.(*vplDecoder)
	fed := false
	for _, au := range aus {
		if len(au) == 0 {
			continue
		}
		if v.Feed(au) == DecOK {
			fed = true
			break
		}
	}
	if !fed {
		t.Fatal("no frame")
	}
	tiny := make([]byte, 8)
	w, h, rc := v.readHeld(tiny, tiny)
	if rc != -2 || w < 2 || h < 2 {
		t.Fatalf("small take rc=%d %dx%d", rc, w, h)
	}
	y := make([]byte, w*h)
	uv := make([]byte, w*((h+1)/2))
	w2, h2, rc := v.readHeld(y, uv)
	if rc != 0 || w2 != w || h2 != h {
		t.Fatalf("second take rc=%d %dx%d", rc, w2, h2)
	}
	sum := 0
	for i := 0; i < len(y); i += 64 {
		sum += int(y[i])
	}
	if sum == 0 {
		t.Fatal("second take dropped the picture")
	}
}
