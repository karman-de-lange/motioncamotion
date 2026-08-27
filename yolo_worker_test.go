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
