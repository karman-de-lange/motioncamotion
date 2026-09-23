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

func (f *fakeRGBDec) JobFrames([][]byte) ([]byte, []byte, int, int, int, bool) {
	return nil, nil, 0, 0, 0, false
}

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

func (f *meanSeqDec) JobFrames([][]byte) ([]byte, []byte, int, int, int, bool) {
	return nil, nil, 0, 0, 0, false
}
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

func (f *seqRGBDec) JobFrames([][]byte) ([]byte, []byte, int, int, int, bool) {
	return nil, nil, 0, 0, 0, false
}
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

type feedTakeDec struct {
	feeds   int
	takes   int
	feedRC  int
	takeRC  int
	w, h    int
	plane   []byte
	uvPlane []byte
	yBuf    []byte
	uvBuf   []byte
}

func (f *feedTakeDec) Feed([]byte) int {
	f.feeds++
	if f.feedRC != 0 {
		return f.feedRC
	}
	return DecOK
}

func (f *feedTakeDec) TakeNV12() (y, uv []byte, pitch, w, h, rc int) {
	f.takes++
	if f.takeRC != 0 {
		return nil, nil, 0, 0, 0, f.takeRC
	}
	w, h = f.w, f.h
	yn := w * h
	uvn := w * ((h + 1) / 2)
	if len(f.yBuf) != yn {
		f.yBuf = make([]byte, yn)
	}
	if len(f.uvBuf) != uvn {
		f.uvBuf = make([]byte, uvn)
	}
	yFill := byte(80)
	if f.plane != nil {
		yFill = f.plane[f.takes-1]
	}
	uvFill := byte(128)
	if f.uvPlane != nil {
		uvFill = f.uvPlane[f.takes-1]
	}
	for i := range f.yBuf {
		f.yBuf[i] = yFill
	}
	for i := range f.uvBuf {
		f.uvBuf[i] = uvFill
	}
	return f.yBuf, f.uvBuf, w, w, h, 0
}

func TestDecodeRGB_jobFramesFeedTake(t *testing.T) {
	idr := fakeHEVCKeyframe()
	p := fakePFrame()
	segs := [][]byte{idr, p, idr, {}, p}
	paints := 0
	paint := func(y, uv []byte, pitch, w, h int) ([]byte, []byte, int, int, bool) {
		paints++
		if len(y) != w*h || pitch != w {
			return nil, nil, 0, 0, false
		}
		infer := make([]byte, inferSize*inferSize*3)
		for i := range infer {
			infer[i] = 40
		}
		still := make([]byte, w*h*3)
		still[0] = 41
		return infer, still, w, h, true
	}

	okDec := &feedTakeDec{w: 16, h: 8}
	infer, still, sw, sh, frames, ok := jobFrames(okDec, segs, paint)
	if !ok || frames != 2 || okDec.feeds != 2 || okDec.takes != 2 || paints != 1 {
		t.Fatalf("ok=%v frames=%d feeds=%d takes=%d paints=%d", ok, frames, okDec.feeds, okDec.takes, paints)
	}
	if len(infer) != inferSize*inferSize*3 || infer[0] != 40 {
		t.Fatalf("infer len=%d byte0=%d", len(infer), infer[0])
	}
	if sw != 16 || sh != 8 || len(still) != 16*8*3 || still[0] != 41 {
		t.Fatalf("still %dx%d len=%d byte0=%d", sw, sh, len(still), still[0])
	}

	noFrame := &feedTakeDec{feedRC: DecMoreData, w: 16, h: 8}
	_, _, _, _, _, ok = jobFrames(noFrame, segs, paint)
	if ok || noFrame.takes != 0 {
		t.Fatalf("no frame ok=%v takes=%d", ok, noFrame.takes)
	}

	badTake := &feedTakeDec{takeRC: -1, w: 16, h: 8}
	_, still, _, _, frames, ok = jobFrames(badTake, segs, paint)
	if ok || still != nil || frames != 2 || badTake.takes != 2 {
		t.Fatalf("bad take ok=%v frames=%d takes=%d still=%v", ok, frames, badTake.takes, still)
	}

	var gotY, gotUV byte
	paintKept := func(y, uv []byte, pitch, w, h int) ([]byte, []byte, int, int, bool) {
		paints++
		gotY, gotUV = y[0], uv[0]
		inf := make([]byte, inferSize*inferSize*3)
		inf[0] = y[0]
		st := make([]byte, w*h*3)
		st[0] = y[0]
		return inf, st, w, h, true
	}
	kept := &feedTakeDec{w: 16, h: 8, plane: []byte{80, 0}, uvPlane: []byte{90, 10}}
	paints = 0
	infer, still, sw, sh, frames, ok = jobFrames(kept, [][]byte{idr, p}, paintKept)
	if !ok || frames != 2 || kept.takes != 2 || paints != 1 || gotY != 80 || gotUV != 90 {
		t.Fatalf("black tail ok=%v frames=%d takes=%d paints=%d y=%d uv=%d", ok, frames, kept.takes, paints, gotY, gotUV)
	}
	if infer[0] != 80 || still[0] != 80 || sw != 16 || sh != 8 {
		t.Fatalf("kept first frame infer=%d still=%d %dx%d", infer[0], still[0], sw, sh)
	}

	limited := &feedTakeDec{w: 16, h: 8, plane: []byte{80, 16}, uvPlane: []byte{90, 10}}
	paints = 0
	gotY, gotUV = 0, 0
	infer, still, sw, sh, frames, ok = jobFrames(limited, [][]byte{idr, p}, paintKept)
	if !ok || frames != 2 || limited.takes != 2 || paints != 1 || gotY != 80 || gotUV != 90 {
		t.Fatalf("Y=16 tail ok=%v frames=%d takes=%d paints=%d y=%d uv=%d", ok, frames, limited.takes, paints, gotY, gotUV)
	}
}

func TestDecodeRGB_jobFramesRGBUsesLastUsable(t *testing.T) {
	segs := [][]byte{{1}, {2}, {3}}
	infer, still, sw, sh, frames, ok := jobFramesRGB(segs,
		func(seg, dst []byte) int {
			fill := byte(40)
			if seg[0] == 2 {
				fill = 90
			}
			if seg[0] == 3 {
				fill = 0
			}
			for i := range dst {
				dst[i] = fill
			}
			return DecOK
		},
		func(seg, dst []byte) (int, int, int) {
			for i := range dst {
				dst[i] = seg[0]
			}
			return 32, 16, DecOK
		},
	)
	if !ok || frames != 3 || sw != 32 || sh != 16 {
		t.Fatalf("ok=%v frames=%d %dx%d", ok, frames, sw, sh)
	}
	if infer[0] != 90 || still[0] != 2 {
		t.Fatalf("infer=%d still=%d want last usable seg 2", infer[0], still[0])
	}
}

func TestAnnexBIsKeyframe(t *testing.T) {
	if !annexBIsKeyframe(fakeHEVCKeyframe()) {
		t.Fatal("HEVC IRAP")
	}
	cra := []byte{0, 0, 0, 1, 32, 0, 1, 2} // NAL type 16
	if !annexBIsKeyframe(cra) {
		t.Fatal("HEVC CRA")
	}
	if !annexBIsKeyframe([]byte{0, 0, 0, 1, 0x65, 0, 1}) {
		t.Fatal("H.264 IDR")
	}
	if annexBIsKeyframe([]byte{0, 0, 0, 1, 2}) {
		t.Fatal("P-frame")
	}
	if annexBIsKeyframe(nil) {
		t.Fatal("empty")
	}
}
