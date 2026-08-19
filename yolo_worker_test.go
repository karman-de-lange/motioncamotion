package main

import (
	"testing"
	"time"
)

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
