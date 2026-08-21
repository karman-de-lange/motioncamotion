package main

import (
	"testing"
	"time"
)

func TestFilterDets_stumpVsPerson(t *testing.T) {
	// hik51_1 16:50:14 person (large, 0.48) vs 16:50:15 stump (58px, 0.49).
	person := DetBox{Class: "person", ClassID: 0, Score: 0.48, X1: 374, Y1: 158, X2: 485, Y2: 375}
	stump := DetBox{Class: "person", ClassID: 0, Score: 0.49, X1: 205, Y1: 441, X2: 253, Y2: 500}
	got := filterDets([]DetBox{stump, person}, 0.45)
	if len(got) != 1 || got[0].Y1 != 158 {
		t.Fatalf("want large person only, got %+v", got)
	}
	tinyHigh := DetBox{Class: "person", ClassID: 0, Score: 0.80, X1: 10, Y1: 10, X2: 30, Y2: 50}
	got = filterDets([]DetBox{tinyHigh}, 0.45)
	if len(got) != 1 {
		t.Fatalf("high-conf small box should keep: %+v", got)
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
