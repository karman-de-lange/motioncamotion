package main

import "testing"

func TestRgbUsable_contentVsBlack(t *testing.T) {
	n := inferSize * inferSize * 3
	green := make([]byte, n)
	for i := 0; i < n; i += 3 {
		green[i+1] = 200
	}
	if !rgbUsable(green) {
		t.Fatal("foliage green should pass")
	}
	gray := make([]byte, n)
	for i := range gray {
		gray[i] = 80
	}
	if !rgbUsable(gray) {
		t.Fatal("gray should pass")
	}
	black := make([]byte, n)
	if rgbUsable(black) {
		t.Fatal("empty decode should fail")
	}
}

func TestRgbCenterMean_usesAllChannels(t *testing.T) {
	n := inferSize * inferSize * 3
	red := make([]byte, n)
	green := make([]byte, n)
	for i := 0; i < n; i += 3 {
		red[i] = 90
		green[i+1] = 90
	}
	if rgbCenterMean(red) != rgbCenterMean(green) {
		t.Fatalf("mean should use all channels, red=%d green=%d", rgbCenterMean(red), rgbCenterMean(green))
	}
}

type fakeRGBDec struct {
	rgbN, fitN int
	outW, outH int
}

func (f *fakeRGBDec) Thumb([]byte, []byte) int { return -1 }

func (f *fakeRGBDec) RGB(_ []byte, rgbOut []byte, outW, outH int) int {
	f.rgbN++
	f.outW, f.outH = outW, outH
	for i := range rgbOut {
		rgbOut[i] = 80
	}
	return DecOK
}

func (f *fakeRGBDec) RGBFit([]byte, []byte, int, int) (int, int, int) {
	f.fitN++
	return 0, 0, -1
}

func (f *fakeRGBDec) Close() {}

func TestDecodeRGBNearEnd_usesRGB(t *testing.T) {
	f := &fakeRGBDec{}
	rgb, frames, ok := decodeRGBNearEndWith(f, [][]byte{{1}, {2}}, inferSize, inferSize)
	if !ok || frames != 2 || len(rgb) != inferSize*inferSize*3 {
		t.Fatalf("ok=%v frames=%d len=%d", ok, frames, len(rgb))
	}
	if f.rgbN != 2 || f.fitN != 0 {
		t.Fatalf("RGB=%d RGBFit=%d want RGB=2 Fit=0", f.rgbN, f.fitN)
	}
	if f.outW != inferSize || f.outH != inferSize {
		t.Fatalf("size %dx%d want %d", f.outW, f.outH, inferSize)
	}
}

type meanSeqDec struct {
	means []int
	i     int
}

func (f *meanSeqDec) Thumb([]byte, []byte) int { return -1 }
func (f *meanSeqDec) RGBFit([]byte, []byte, int, int) (int, int, int) {
	return 0, 0, -1
}
func (f *meanSeqDec) Close() {}
func (f *meanSeqDec) RGB(_ []byte, rgbOut []byte, outW, outH int) int {
	if f.i >= len(f.means) {
		return DecMoreData
	}
	m := f.means[f.i]
	f.i++
	for i := 0; i < len(rgbOut); i += 3 {
		rgbOut[i] = byte(m)
		rgbOut[i+1] = byte(m)
		rgbOut[i+2] = byte(m)
	}
	return DecOK
}

func TestDecodeRGBNearEnd_keepsLastNotBrightest(t *testing.T) {
	f := &meanSeqDec{means: []int{200, 40, 90}}
	rgb, frames, ok := decodeRGBNearEndWith(f, [][]byte{{1}, {2}, {3}}, inferSize, inferSize)
	if !ok || frames != 3 {
		t.Fatalf("ok=%v frames=%d", ok, frames)
	}
	if rgb[0] != 90 {
		t.Fatalf("want last frame mean 90, got %d (brightest fallback?)", rgb[0])
	}
}

func TestDecodeRGBNearEnd_startsAtLastIRAP(t *testing.T) {
	idr := fakeHEVCKeyframe()
	p := []byte{0, 0, 0, 1, 2}
	f := &fakeRGBDec{}
	_, frames, ok := decodeRGBNearEndWith(f, [][]byte{idr, p, idr, p, p}, inferSize, inferSize)
	if !ok || frames != 3 {
		t.Fatalf("ok=%v frames=%d want 3 from last IRAP", ok, frames)
	}
	if f.rgbN != 3 {
		t.Fatalf("RGB calls=%d want 3 (skip early GOP)", f.rgbN)
	}
}

func TestRgbChromaPlausible_rejectsGreenPurple(t *testing.T) {
	n := inferSize * inferSize * 3
	gray := make([]byte, n)
	for i := range gray {
		gray[i] = 80
	}
	if !rgbChromaPlausible(gray) {
		t.Fatal("gray should pass")
	}
	green := make([]byte, n)
	for i := 0; i < n; i += 3 {
		green[i+1] = 220
	}
	if rgbChromaPlausible(green) {
		t.Fatal("U=V=0 green should fail")
	}
	mag := make([]byte, n)
	for i := 0; i < n; i += 3 {
		mag[i] = 200
		mag[i+2] = 200
	}
	if rgbChromaPlausible(mag) {
		t.Fatal("magenta UV-junk should fail")
	}
}

func TestDecodeRGBNearEnd_skipsBlackKeepsPrior(t *testing.T) {
	n := inferSize * inferSize * 3
	f := &seqRGBDec{frames: [][]byte{
		flatRGB(n, 80, 80, 80),
		flatRGB(n, 90, 90, 90),
		flatRGB(n, 0, 0, 0),
	}}
	rgb, frames, ok := decodeRGBNearEndWith(f, [][]byte{{1}, {2}, {3}}, inferSize, inferSize)
	if !ok || frames != 3 {
		t.Fatalf("ok=%v frames=%d", ok, frames)
	}
	if rgb[0] != 90 {
		t.Fatalf("want last usable 90 after black tail, got %d,%d,%d", rgb[0], rgb[1], rgb[2])
	}

	f2 := &seqRGBDec{frames: [][]byte{
		flatRGB(n, 0, 0, 0),
		flatRGB(n, 0, 0, 0),
	}}
	_, frames, ok = decodeRGBNearEndWith(f2, [][]byte{{1}, {2}}, inferSize, inferSize)
	if ok || frames != 2 {
		t.Fatalf("all-black ok=%v frames=%d", ok, frames)
	}
}

func flatRGB(n int, r, g, b byte) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i += 3 {
		out[i], out[i+1], out[i+2] = r, g, b
	}
	return out
}

type seqRGBDec struct {
	frames [][]byte
	i      int
}

func (f *seqRGBDec) Thumb([]byte, []byte) int { return -1 }
func (f *seqRGBDec) RGBFit([]byte, []byte, int, int) (int, int, int) {
	return 0, 0, -1
}
func (f *seqRGBDec) Close() {}
func (f *seqRGBDec) RGB(_ []byte, rgbOut []byte, _, _ int) int {
	if f.i >= len(f.frames) {
		return DecMoreData
	}
	copy(rgbOut, f.frames[f.i])
	f.i++
	return DecOK
}

func TestLastIRAPIndex(t *testing.T) {
	idr := fakeHEVCKeyframe()
	cra := []byte{0, 0, 0, 1, 32, 0, 1, 2} // NAL type 16
	p := []byte{0, 0, 0, 1, 2}
	if i := lastIRAPIndex([][]byte{idr, p, idr, p}); i != 2 {
		t.Fatalf("last IRAP %d want 2", i)
	}
	if i := lastIRAPIndex([][]byte{idr, p, cra}); i != 2 {
		t.Fatalf("CRA last IRAP %d want 2", i)
	}
	if i := lastIRAPIndex([][]byte{p, p}); i != 0 {
		t.Fatalf("no IRAP %d want 0", i)
	}
	if i := lastIRAPIndex(nil); i != 0 {
		t.Fatalf("nil %d want 0", i)
	}
}
