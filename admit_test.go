package main

import (
	"context"
	"testing"
	"time"
)

func TestAdmit_fifoQueue(t *testing.T) {
	a := newAdmissions()
	if !a.push(DetectEvent{Cam: "c1", Label: "first", SAD: 1}, true) {
		t.Fatal("first")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "second", SAD: 2}, false) {
		t.Fatal("second")
	}
	ev, opened, ok := a.pull()
	if !ok || ev.Label != "first" || !opened {
		t.Fatalf("first job: ok=%v opened=%v %s", ok, opened, ev.Label)
	}
	ev, opened, ok = a.pull()
	if !ok || ev.Label != "second" || opened {
		t.Fatalf("second job: ok=%v opened=%v %s", ok, opened, ev.Label)
	}
	if _, _, ok := a.pull(); ok {
		t.Fatal("queue should be empty")
	}
}

func TestAdmit_skipWhenPerCamFull(t *testing.T) {
	a := newAdmissions()
	a.perCam = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "a"}, true) {
		t.Fatal("a")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "b"}, false) {
		t.Fatal("b")
	}
	if a.push(DetectEvent{Cam: "c1", Label: "c"}, false) {
		t.Fatal("same cam should skip when per-cam cap hit")
	}
	if !a.push(DetectEvent{Cam: "c2", Label: "other"}, true) {
		t.Fatal("other cam should still enqueue")
	}
	ev, _, ok := a.pull()
	if !ok || ev.Cam != "c1" || ev.Label != "a" {
		t.Fatalf("want c1/a got %s/%s ok=%v", ev.Cam, ev.Label, ok)
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "d"}, false) {
		t.Fatal("after pull, c1 should have a free slot")
	}
}

func TestAdmit_recvCancel(t *testing.T) {
	a := newAdmissions()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, ok := a.recv(ctx)
	if ok {
		t.Fatal("recv should fail on cancel")
	}
}
