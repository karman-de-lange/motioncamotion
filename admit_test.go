package main

import "testing"

type fakeCPU float64

func (f fakeCPU) Percent() float64 { return float64(f) }

func TestAdmit_latestOverwrite(t *testing.T) {
	a := newAdmissions(fakeCPU(0))
	a.mark("c1", DetectEvent{Cam: "c1", Label: "first"}, true)
	a.mark("c1", DetectEvent{Cam: "c1", Label: "second"}, false)
	ev, opened, ok := a.pull()
	if !ok {
		t.Fatal("expected latest")
	}
	if ev.Label != "second" {
		t.Fatalf("want overwritten second, got %s", ev.Label)
	}
	if !opened {
		t.Fatal("first look after open must survive overwrite")
	}
	if _, _, ok := a.pull(); ok {
		t.Fatal("only one latest per cam")
	}
}

func TestAdmit_cpuHighSkipsNonOpen(t *testing.T) {
	a := newAdmissions(fakeCPU(99))
	a.mark("c1", DetectEvent{Cam: "c1", Label: "open"}, true)
	ev, opened, ok := a.pull()
	if !ok || !opened || ev.Label != "open" {
		t.Fatalf("open at high CPU: ok=%v opened=%v %s", ok, opened, ev.Label)
	}
	a.mark("c1", DetectEvent{Cam: "c1", Label: "hot"}, false)
	if _, _, ok := a.pull(); ok {
		t.Fatal("non-open pulled at 99% CPU")
	}
}

func TestAdmit_openNeverDroppedForCPU(t *testing.T) {
	a := newAdmissions(fakeCPU(99))
	a.mark("c1", DetectEvent{Cam: "c1", Label: "open"}, true)
	ev, opened, ok := a.pull()
	if !ok || !opened || ev.Cam != "c1" {
		t.Fatalf("open dropped for CPU ok=%v opened=%v cam=%s", ok, opened, ev.Cam)
	}
}
