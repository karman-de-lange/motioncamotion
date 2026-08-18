package main

import "testing"

func TestThumbRing_meanAndTemporal(t *testing.T) {
	// Use current thumbSize from motion.go
	r := newThumbRing(16)
	recent := make([]byte, thumbSize)
	older := make([]byte, thumbSize)

	// Fill with constant frames: no motion
	frame := make([]byte, thumbSize)
	for i := 0; i < 12; i++ {
		r.push(frame)
	}
	sad, blob, _, ready := motionScoreTemporal(r, 3, 6, nil, recent, older)
	if !ready {
		t.Fatal("expected ready")
	}
	if sad != 0 || blob != 0 {
		t.Fatalf("static: sad=%v blob=%d", sad, blob)
	}

	// Push bright recent frames (simulate person appearing)
	bright := make([]byte, thumbSize)
	for i := range bright {
		bright[i] = 200
	}
	for i := 0; i < 3; i++ {
		r.push(bright)
	}
	// Need more history: older still dark, recent bright
	// Ring now: ... dark, dark, bright,bright,bright
	sad, blob, _, ready = motionScoreTemporal(r, 3, 6, nil, recent, older)
	if !ready {
		t.Fatal("expected ready after bright")
	}
	if sad < 50 {
		t.Fatalf("expected large sad when recent bright vs older dark, got %v", sad)
	}
	if blob < 100 {
		t.Fatalf("expected large blob, got %d", blob)
	}
}

func TestThumbRing_singleFrameGlitch(t *testing.T) {
	r := newThumbRing(16)
	recent := make([]byte, thumbSize)
	older := make([]byte, thumbSize)
	dark := make([]byte, thumbSize)
	// One-frame local flash (few cells), not whole frame — birds/OSD ticks.
	flash := make([]byte, thumbSize)
	for i := 0; i < 20; i++ {
		flash[i] = 255
	}
	for i := 0; i < 10; i++ {
		r.push(dark)
	}
	r.push(flash)
	r.push(dark)
	r.push(dark)
	sad, blob, _, ready := motionScoreTemporal(r, 3, 6, nil, recent, older)
	if !ready {
		t.Fatal("ready")
	}
	// Mean dilutes the flash: 20 cells * 255/3 ≈ 85 over noise → small %
	if sad > 5 {
		t.Fatalf("local glitch should be attenuated by avg, sad=%v blob=%d", sad, blob)
	}
}
