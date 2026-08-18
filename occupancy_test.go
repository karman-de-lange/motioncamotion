package main

import (
	"testing"
	"time"
)

func TestOccupancy_newPersonIsFresh(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	dets := []DetBox{{Class: "person", ClassID: 0, Score: 0.8, X1: 100, Y1: 100, X2: 180, Y2: 300}}
	fresh := o.consume("hik1", dets, now)
	if len(fresh) != 1 {
		t.Fatalf("first person want 1 got %d", len(fresh))
	}
	fresh = o.consume("hik1", dets, now.Add(time.Second))
	if len(fresh) != 0 {
		t.Fatalf("same person should not re-alert, got %d", len(fresh))
	}
}

func TestOccupancy_parkedCarNeedsNegativeObserve(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	car := []DetBox{{Class: "truck", ClassID: 7, Score: 0.7, X1: 200, Y1: 200, X2: 400, Y2: 320}}
	if n := o.consume("hik2", car, now); len(n) != 1 {
		t.Fatalf("first truck: %d", len(n))
	}
	// Quiet 20 minutes with no infer — next look still sees the truck.
	if n := o.consume("hik2", car, now.Add(20*time.Minute)); len(n) != 0 {
		t.Fatalf("parked truck re-alerted after quiet: %d", len(n))
	}
	// Foliage / empty infers must not evict a parked vehicle.
	for i := 0; i < occGoneSamples+2; i++ {
		o.consume("hik2", nil, now.Add(time.Duration(21+i)*time.Minute))
	}
	if n := o.consume("hik2", car, now.Add(30*time.Minute)); len(n) != 0 {
		t.Fatalf("empty infers evicted parked truck: %d", len(n))
	}
	// Another vehicle elsewhere for N looks → this track is gone.
	other := []DetBox{{Class: "car", ClassID: 2, Score: 0.7, X1: 10, Y1: 10, X2: 40, Y2: 40}}
	for i := 0; i < occGoneSamples; i++ {
		o.consume("hik2", other, now.Add(time.Duration(31+i)*time.Minute))
	}
	if n := o.consume("hik2", car, now.Add(40*time.Minute)); len(n) != 1 {
		t.Fatalf("truck after leave should be fresh, got %d", len(n))
	}
}

func TestOccupancy_twoPeopleSideBySide(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	dets := []DetBox{
		{Class: "person", ClassID: 0, X1: 40, Y1: 80, X2: 240, Y2: 480},
		{Class: "person", ClassID: 0, X1: 280, Y1: 80, X2: 480, Y2: 480},
	}
	fresh := o.consume("hik1", dets, now)
	if len(fresh) != 2 {
		t.Fatalf("two full-height people both fresh, got %d", len(fresh))
	}
}

func TestOccupancy_smallMoverAssociates(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	a := []DetBox{{Class: "person", ClassID: 0, X1: 100, Y1: 200, X2: 120, Y2: 240}}
	o.consume("hik2", a, now)
	b := []DetBox{{Class: "person", ClassID: 0, X1: 110, Y1: 200, X2: 130, Y2: 240}}
	if n := o.consume("hik2", b, now.Add(500*time.Millisecond)); len(n) != 0 {
		t.Fatalf("hik2-scale 10px move should match, got fresh %d", len(n))
	}
}
