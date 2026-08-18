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
