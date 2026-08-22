package main

import (
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
}

func TestFilterDets_stumpVsPerson(t *testing.T) {
	// hik51_1 16:50:14 person (large, 0.48) vs 16:50:15 stump (58px, 0.49).
	person := DetBox{Class: "person", ClassID: 0, Score: 0.48, X1: 374, Y1: 158, X2: 485, Y2: 375}
	stump := DetBox{Class: "person", ClassID: 0, Score: 0.49, X1: 205, Y1: 441, X2: 253, Y2: 500}
	distant := DetBox{Class: "person", ClassID: 0, Score: 0.48, X1: 300, Y1: 400, X2: 322, Y2: 458}
	got := filterDets([]DetBox{stump, person, distant}, 0.45)
	if len(got) != 2 {
		t.Fatalf("want large+distant person, not stump, got %+v", got)
	}
	for _, d := range got {
		if d.Y1 == 441 {
			t.Fatalf("stump kept: %+v", got)
		}
	}
	if got[0].Y1 != 158 && got[1].Y1 != 158 {
		t.Fatalf("missing large person: %+v", got)
	}
	if got[0].Y1 != 400 && got[1].Y1 != 400 {
		t.Fatalf("tall distant person at 0.48 dropped: %+v", got)
	}
	tinyHigh := DetBox{Class: "person", ClassID: 0, Score: 0.80, X1: 10, Y1: 10, X2: 30, Y2: 50}
	got = filterDets([]DetBox{tinyHigh}, 0.45)
	if len(got) != 1 {
		t.Fatalf("high-conf small box should keep: %+v", got)
	}
	aspect15 := DetBox{Class: "person", ClassID: 0, Score: 0.48, X1: 0, Y1: 0, X2: 40, Y2: 60}
	got = filterDets([]DetBox{aspect15}, 0.45)
	if len(got) != 1 {
		t.Fatalf("h/w==1.5 at 0.48 should keep: %+v", got)
	}
	squatHigh := DetBox{Class: "person", ClassID: 0, Score: 0.65, X1: 205, Y1: 441, X2: 253, Y2: 499}
	got = filterDets([]DetBox{squatHigh}, 0.45)
	if len(got) != 1 {
		t.Fatalf("high-conf stump should keep: %+v", got)
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
	processDetect(ev, true, logQ, store, nil, yc, occ)
	ev.At = ev.At.Add(time.Second)
	processDetect(ev, false, logQ, store, nil, yc, occ)

	n := 0
	for _, r := range store.List(20, "motion") {
		if r.Type == "motion" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 motion clip per open, got %d", n)
	}
}
