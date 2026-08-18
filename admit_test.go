package main

import (
	"testing"
	"time"
)

type fakeCPU float64

func (f fakeCPU) Percent() float64 { return float64(f) }

func TestAdmit_openNeverDroppedForCPU(t *testing.T) {
	q := make(chan DetectEvent, 16)
	a := newAdmissions(q, fakeCPU(99))
	if !a.submit("c1", sampleOpen, DetectEvent{Cam: "c1"}) {
		t.Fatal("open dropped for CPU")
	}
	select {
	case ev := <-q:
		if ev.Cam != "c1" {
			t.Fatalf("cam %s", ev.Cam)
		}
	default:
		t.Fatal("open not queued")
	}
}

func TestAdmit_hotDroppedWhenCPUHigh(t *testing.T) {
	q := make(chan DetectEvent, 16)
	a := newAdmissions(q, fakeCPU(99))
	if a.submit("c1", sampleHot, DetectEvent{Cam: "c1"}) {
		t.Fatal("hot admitted at 99% CPU")
	}
	select {
	case <-q:
		t.Fatal("hot queued")
	default:
	}
}

func TestAdmit_hotDroppedWhenQueueBusy(t *testing.T) {
	q := make(chan DetectEvent, 4)
	q <- DetectEvent{Cam: "fill"}
	a := newAdmissions(q, fakeCPU(0))
	a.qBusy = 1
	if a.submit("c1", sampleHot, DetectEvent{Cam: "c1"}) {
		t.Fatal("hot admitted with full queue")
	}
}

func TestAdmit_pendingReplacedOnRelease(t *testing.T) {
	q := make(chan DetectEvent, 1)
	a := newAdmissions(q, fakeCPU(0))
	if !a.submit("c1", sampleOpen, DetectEvent{Cam: "c1", Label: "first"}) {
		t.Fatal("open")
	}
	if !a.submit("c1", sampleHot, DetectEvent{Cam: "c1", Label: "second"}) {
		t.Fatal("pending hot")
	}
	first := <-q
	if first.Label != "first" {
		t.Fatalf("first %s", first.Label)
	}
	first.finish()
	select {
	case ev := <-q:
		if ev.Label != "second" {
			t.Fatalf("pending want second got %s", ev.Label)
		}
		ev.finish()
	case <-time.After(time.Second):
		t.Fatal("pending not sent after release")
	}
}

func TestAdmit_hotGap(t *testing.T) {
	q := make(chan DetectEvent, 8)
	a := newAdmissions(q, fakeCPU(0))
	a.hotGap = time.Hour
	if !a.submit("c1", sampleOpen, DetectEvent{Cam: "c1"}) {
		t.Fatal("open")
	}
	(<-q).finish()
	if !a.submit("c1", sampleHot, DetectEvent{Cam: "c1"}) {
		t.Fatal("first hot")
	}
	(<-q).finish()
	if a.submit("c1", sampleHot, DetectEvent{Cam: "c1"}) {
		t.Fatal("second hot inside gap")
	}
}
