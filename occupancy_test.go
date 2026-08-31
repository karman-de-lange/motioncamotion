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
	for i := 0; i < occVehicleGoneSamples; i++ {
		o.consume("hik2", other, now.Add(time.Duration(31+i)*time.Minute))
	}
	if n := o.consume("hik2", car, now.Add(40*time.Minute)); len(n) != 1 {
		t.Fatalf("truck after leave should be fresh, got %d", len(n))
	}
}

func TestOccupancy_personWalkOutWalkInIsFresh(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	p := []DetBox{{Class: "person", ClassID: 0, Score: 0.8, X1: 0, Y1: 290, X2: 72, Y2: 496}}
	if n := o.consume("hik1", p, now); len(n) != 1 {
		t.Fatalf("first walk-in: %d", len(n))
	}
	for i := 0; i < occGoneSamples; i++ {
		o.consume("hik1", nil, now.Add(time.Duration(i+1)*time.Second))
	}
	if n := o.consume("hik1", p, now.Add(4*time.Second)); len(n) != 1 {
		t.Fatalf("walk-in after leaving should alert, got %d", len(n))
	}
}

func TestOccupancy_personTTLAllowsReturn(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	p := []DetBox{{Class: "person", ClassID: 0, Score: 0.8, X1: 0, Y1: 290, X2: 72, Y2: 496}}
	if n := o.consume("hik1", p, now); len(n) != 1 {
		t.Fatalf("first: %d", len(n))
	}
	if n := o.consume("hik1", p, now.Add(occPersonTTL+time.Second)); len(n) != 1 {
		t.Fatalf("same door after person TTL should alert, got %d", len(n))
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

func TestOccupancy_twoParkedMissOneDoesNotReAlert(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	bus := DetBox{Class: "bus", ClassID: 5, Score: 0.83, X1: 371, Y1: 311, X2: 518, Y2: 381}
	truck := DetBox{Class: "truck", ClassID: 7, Score: 0.61, X1: 60, Y1: 301, X2: 155, Y2: 381}
	if n := o.consume("cam118", []DetBox{bus, truck}, now); len(n) != 2 {
		t.Fatalf("first look both fresh, got %d", len(n))
	}
	// YOLO only returns the bus for a long burst (cam118 22:54–22:55).
	for i := 0; i < 20; i++ {
		fresh := o.consume("cam118", []DetBox{bus}, now.Add(time.Duration(i+1)*time.Second))
		if len(fresh) != 0 {
			t.Fatalf("bus-only look %d re-alerted: %+v", i, fresh)
		}
	}
	car := DetBox{Class: "car", ClassID: 2, Score: 0.46, X1: 66, Y1: 301, X2: 152, Y2: 380}
	if n := o.consume("cam118", []DetBox{bus, car}, now.Add(30*time.Second)); len(n) != 0 {
		t.Fatalf("left wreck as car after misses re-alerted: %+v", n)
	}
}

func TestOccupancy_personWalksInBesideParkedCar(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	truck := DetBox{Class: "truck", ClassID: 7, Score: 0.67, X1: 360, Y1: 278, X2: 619, Y2: 396}
	if n := o.consume("hik2", []DetBox{truck}, now); len(n) != 1 {
		t.Fatalf("first truck: %d", len(n))
	}
	// Person overlapping the bakkie must still alert.
	person := DetBox{Class: "person", ClassID: 0, Score: 0.72, X1: 400, Y1: 250, X2: 470, Y2: 420}
	fresh := o.consume("hik2", []DetBox{truck, person}, now.Add(time.Second))
	if len(fresh) != 1 || fresh[0].ClassID != 0 {
		t.Fatalf("walk-in beside parked truck want person, got %+v", fresh)
	}
	fresh = o.consume("hik2", []DetBox{truck, person}, now.Add(2*time.Second))
	if len(fresh) != 0 {
		t.Fatalf("same person still in frame should not re-alert: %+v", fresh)
	}
	animal := DetBox{Class: "animal", ClassID: 15, Score: 0.55, X1: 80, Y1: 360, X2: 120, Y2: 400}
	fresh = o.consume("hik2", []DetBox{truck, person, animal}, now.Add(3*time.Second))
	if len(fresh) != 1 || fresh[0].ClassID != 15 {
		t.Fatalf("cat beside parked truck want animal, got %+v", fresh)
	}
}

func TestOccupancy_parkedTruckAsCarIsSame(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	truck := []DetBox{{Class: "truck", ClassID: 7, Score: 0.67, X1: 360, Y1: 278, X2: 619, Y2: 396}}
	if n := o.consume("hik2", truck, now); len(n) != 1 {
		t.Fatalf("first truck: %d", len(n))
	}
	car := []DetBox{{Class: "car", ClassID: 2, Score: 0.52, X1: 155, Y1: 347, X2: 628, Y2: 497}}
	// Huge overlapping "car" on the same bakkie must not re-alert.
	both := []DetBox{truck[0], car[0]}
	if n := o.consume("hik2", both, now.Add(time.Second)); len(n) != 0 {
		t.Fatalf("truck+car same bakkie re-alerted: %d", len(n))
	}
	if n := o.consume("hik2", car, now.Add(2*time.Second)); len(n) != 0 {
		t.Fatalf("class flip truck→car re-alerted: %d", len(n))
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

func TestOccupancy_sittingAnimalEmptyInfersDoNotReAlert(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	// cam57 18:29 stump/bird: ~17×26 at the same pixel every few seconds.
	bird := DetBox{Class: "animal", ClassID: 14, Score: 0.48, X1: 113, Y1: 369, X2: 130, Y2: 395}
	if n := o.consume("cam57", []DetBox{bird}, now); len(n) != 1 {
		t.Fatalf("first bird: %d", len(n))
	}
	for i := 0; i < occGoneSamples+2; i++ {
		o.consume("cam57", nil, now.Add(time.Duration(i+1)*time.Second))
	}
	if n := o.consume("cam57", []DetBox{bird}, now.Add(8*time.Second)); len(n) != 0 {
		t.Fatalf("sitting bird re-alerted after empty infers: %d", len(n))
	}
	later := bird
	later.Score = 0.45
	if n := o.consume("cam57", []DetBox{later}, now.Add(2*time.Minute)); len(n) != 0 {
		t.Fatalf("same stump after 2min re-alerted: %d", len(n))
	}
}

func TestOccupancy_animalClassFlipSameBox(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	bird := DetBox{Class: "animal", ClassID: 14, Score: 0.48, X1: 113, Y1: 369, X2: 130, Y2: 395}
	sheep := DetBox{Class: "animal", ClassID: 18, Score: 0.48, X1: 112, Y1: 370, X2: 130, Y2: 394}
	if n := o.consume("cam57", []DetBox{bird}, now); len(n) != 1 {
		t.Fatalf("first: %d", len(n))
	}
	if n := o.consume("cam57", []DetBox{sheep}, now.Add(time.Second)); len(n) != 0 {
		t.Fatalf("bird→sheep same stump re-alerted: %d", len(n))
	}
}

func TestOccupancy_newAnimalElsewhereIsFresh(t *testing.T) {
	o := newOccupancy()
	now := time.Now()
	stump := DetBox{Class: "animal", ClassID: 14, Score: 0.48, X1: 113, Y1: 369, X2: 130, Y2: 395}
	deer := DetBox{Class: "animal", ClassID: 19, Score: 0.7, X1: 400, Y1: 200, X2: 480, Y2: 280}
	if n := o.consume("cam57", []DetBox{stump}, now); len(n) != 1 {
		t.Fatalf("stump: %d", len(n))
	}
	fresh := o.consume("cam57", []DetBox{stump, deer}, now.Add(time.Second))
	if len(fresh) != 1 || fresh[0].ClassID != 19 {
		t.Fatalf("deer elsewhere want 1 cow, got %+v", fresh)
	}
}
