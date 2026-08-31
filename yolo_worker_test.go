package main

import (
	"bytes"
	"image/jpeg"
	"os"
	"testing"
	"time"
)

func TestCocoNames_noSafariFurnitureClasses(t *testing.T) {
	if cocoNames[20] != "" || cocoNames[22] != "" || cocoNames[23] != "" {
		t.Fatal("elephant/zebra/giraffe must not be alert classes")
	}
	if cocoNames[14] != "animal" || cocoNames[15] != "animal" || cocoNames[19] != "animal" {
		t.Fatalf("wildlife COCO ids should display as animal, got bird=%q cat=%q cow=%q", cocoNames[14], cocoNames[15], cocoNames[19])
	}
	if cocoNames[1] != "bicycle" || cocoNames[3] != "motorcycle" {
		t.Fatalf("bike classes, got bicycle=%q motorcycle=%q", cocoNames[1], cocoNames[3])
	}
}

func TestProcessDetect_oneClipPerOpen(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	logQ := make(chan string, 64)
	occ := newOccupancy()
	yc := yoloConfig{MainConf: 0.65}
	ev := DetectEvent{
		Cam:      "c1",
		At:       time.Now(),
		Label:    "motion",
		SAD:      4,
		Segments: [][]byte{fakeHEVCKeyframe()},
	}
	processDetect(ev, true, logQ, store, nil, yc, occ, nil)
	ev.At = ev.At.Add(time.Second)
	processDetect(ev, false, logQ, store, nil, yc, occ, nil)

	n := 0
	for _, r := range store.List(20, "motion") {
		if r.Type == "motion" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 motion clip per open, got %d", n)
	}
	rec := store.List(20, "motion")[0]
	if rec.ClipRel == "" {
		t.Fatal("motion ClipRel empty")
	}
	abs, err := store.AbsMedia(rec.ClipRel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatal("motion clip must not be written from detect GOP")
	}
}

func TestBeginAlertClip_prefersCapture(t *testing.T) {
	ev := DetectEvent{
		Cam:      "c1",
		At:       time.Now(),
		Segments: [][]byte{fakeHEVCKeyframe()},
	}
	rel := "alerts/20260829/cap.h265"
	called := false
	beginAlertClip(ev, rel, func(cam, gotRel string, at time.Time) {
		called = true
		if cam != "c1" || gotRel != rel || !at.Equal(ev.At) {
			t.Fatalf("capture args %s %s %v", cam, gotRel, at)
		}
	})
	if !called {
		t.Fatal("capture not called")
	}
}

type detectSpyDec struct {
	rgbN int
	fitN int
	outW int
	outH int
	fitW int
	fitH int
}

func (f *detectSpyDec) Thumb([]byte, []byte) int { return -1 }

func (f *detectSpyDec) RGB(_ []byte, rgbOut []byte, outW, outH int) int {
	f.rgbN++
	f.outW, f.outH = outW, outH
	for i := range rgbOut {
		rgbOut[i] = 80
	}
	return DecOK
}

func (f *detectSpyDec) RGBFit(_ []byte, rgbOut []byte, maxW, maxH int) (int, int, int) {
	f.fitN++
	f.fitW, f.fitH = maxW, maxH
	n := maxW * maxH * 3
	if n > len(rgbOut) {
		n = len(rgbOut)
	}
	for i := 0; i < n; i++ {
		rgbOut[i] = 80
	}
	return maxW, maxH, DecOK
}

func (f *detectSpyDec) Close() {}

func stubPersonInfer(t *testing.T) {
	t.Helper()
	hookInfer(func([]byte, float64) ([]DetBox, float64, error) {
		return []DetBox{{Class: "person", Score: 0.9, X1: 8, Y1: 8, X2: 40, Y2: 40, ClassID: 0}}, 1, nil
	})
	t.Cleanup(func() { hookInfer(nil) })
}

func jpegSize(t *testing.T, store *EventStore, rel string) (w, h int) {
	t.Helper()
	abs, err := store.AbsMedia(rel)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestProcessDetect_infer640GalleryFit(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	sessions := 0
	orig := openFrameDecoder
	t.Cleanup(func() { openFrameDecoder = orig })
	spy := &detectSpyDec{}
	openFrameDecoder = func(int) (FrameDecoder, int) {
		sessions++
		return spy, 0
	}
	stubPersonInfer(t)

	logQ := make(chan string, 64)
	occ := newOccupancy()
	yc := yoloConfig{MainConf: 0.65}
	idr := fakeHEVCKeyframe()
	p := fakePFrame()
	ev := DetectEvent{
		Cam:      "c1",
		At:       time.Now(),
		Label:    "motion",
		SAD:      4,
		Segments: [][]byte{idr, p, idr, p},
	}
	processDetect(ev, true, logQ, store, nil, yc, occ, nil)

	if sessions != 2 {
		t.Fatalf("decoder sessions=%d want 2 (infer+gallery)", sessions)
	}
	if spy.rgbN != 4 {
		t.Fatalf("RGB calls=%d want 4 (lastIRAPIndex slice?)", spy.rgbN)
	}
	if spy.outW != inferSize || spy.outH != inferSize {
		t.Fatalf("RGB size %dx%d want %d", spy.outW, spy.outH, inferSize)
	}
	if spy.fitN == 0 || spy.fitW != stillMaxW || spy.fitH != stillMaxH {
		t.Fatalf("RGBFit n=%d %dx%d want %dx%d", spy.fitN, spy.fitW, spy.fitH, stillMaxW, stillMaxH)
	}

	mot := store.List(20, "motion")
	if len(mot) != 1 || mot[0].StillRel == "" {
		t.Fatalf("motion %+v", mot)
	}
	mw, mh := jpegSize(t, store, mot[0].StillRel)
	if mw != stillMaxW || mh != stillMaxH {
		t.Fatalf("motion still %dx%d want %dx%d", mw, mh, stillMaxW, stillMaxH)
	}
	al := store.List(20, "alert")
	if len(al) != 1 || al[0].StillRel == "" {
		t.Fatalf("alert %+v", al)
	}
	aw, ah := jpegSize(t, store, al[0].StillRel)
	if aw != stillMaxW || ah != stillMaxH {
		t.Fatalf("alert still %dx%d want %dx%d", aw, ah, stillMaxW, stillMaxH)
	}
	for _, rec := range []EventRecord{mot[0], al[0]} {
		abs, err := store.AbsMedia(rec.ClipRel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(abs); !os.IsNotExist(err) {
			t.Fatalf("%s clip written from detect GOP", rec.Type)
		}
	}
}

func TestProcessDetect_openPassCapturesOnce(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	orig := openFrameDecoder
	t.Cleanup(func() { openFrameDecoder = orig })
	openFrameDecoder = func(int) (FrameDecoder, int) {
		return &detectSpyDec{}, 0
	}
	stubPersonInfer(t)

	var rels []string
	ev := DetectEvent{
		Cam:      "c1",
		At:       time.Now(),
		Label:    "motion",
		SAD:      4,
		Segments: [][]byte{fakeHEVCKeyframe()},
	}
	processDetect(ev, true, make(chan string, 64), store, nil, yoloConfig{MainConf: 0.65}, newOccupancy(),
		func(cam, rel string, at time.Time) {
			if cam != "c1" || !at.Equal(ev.At) {
				t.Fatalf("capture %s %v", cam, at)
			}
			rels = append(rels, rel)
		})
	if len(rels) != 1 {
		t.Fatalf("captures=%d want 1 (open+pass double write?)", len(rels))
	}
	mot := store.List(1, "motion")
	al := store.List(1, "alert")
	if len(mot) != 1 || len(al) != 1 {
		t.Fatalf("motion=%d alert=%d", len(mot), len(al))
	}
	if rels[0] != mot[0].ClipRel {
		t.Fatalf("captured %s want motion %s", rels[0], mot[0].ClipRel)
	}
	if al[0].ClipRel != mot[0].ClipRel {
		t.Fatalf("alert clip %s want motion %s", al[0].ClipRel, mot[0].ClipRel)
	}
}

func TestProcessDetect_extraAlertCaptures(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	orig := openFrameDecoder
	t.Cleanup(func() { openFrameDecoder = orig })
	openFrameDecoder = func(int) (FrameDecoder, int) {
		return &detectSpyDec{}, 0
	}
	stubPersonInfer(t)

	var rels []string
	ev := DetectEvent{
		Cam:      "c1",
		At:       time.Now(),
		Label:    "motion",
		SAD:      4,
		Segments: [][]byte{fakeHEVCKeyframe()},
	}
	processDetect(ev, false, make(chan string, 64), store, nil, yoloConfig{MainConf: 0.65}, newOccupancy(),
		func(cam, rel string, at time.Time) {
			rels = append(rels, rel)
		})
	if len(store.List(5, "motion")) != 0 {
		t.Fatal("extra must not save motion")
	}
	al := store.List(1, "alert")
	if len(al) != 1 {
		t.Fatal("missing extra alert")
	}
	if len(rels) != 1 || rels[0] != al[0].ClipRel {
		t.Fatalf("captures %v want alert %s", rels, al[0].ClipRel)
	}
}
