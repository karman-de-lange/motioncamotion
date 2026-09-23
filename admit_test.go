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

func TestAdmit_overwriteOldestExtraWhenFull(t *testing.T) {
	a := newAdmissions()
	a.perCam = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "a", SAD: 1}, false) {
		t.Fatal("a")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "b", SAD: 2}, false) {
		t.Fatal("b")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "c", SAD: 3}, false) {
		t.Fatal("full cam must replace oldest extra, not drop the new frame")
	}
	if !a.push(DetectEvent{Cam: "c2", Label: "other"}, true) {
		t.Fatal("other cam should still enqueue")
	}
	ev, opened, ok := a.pull()
	if !ok || ev.Cam != "c1" || ev.Label != "b" || opened {
		t.Fatalf("want c1/b extra got %s/%s opened=%v ok=%v (kept oldest?)", ev.Cam, ev.Label, opened, ok)
	}
	ev, _, ok = a.pull()
	if !ok || ev.Cam != "c1" || ev.Label != "c" {
		t.Fatalf("want c1/c latest extra got %s/%s ok=%v", ev.Cam, ev.Label, ok)
	}
}

func TestAdmit_neverSkipOpen(t *testing.T) {
	a := newAdmissions()
	a.perCam = 2
	a.cap = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "a", SAD: 1}, false) {
		t.Fatal("a")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "b", SAD: 2}, false) {
		t.Fatal("b")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "open", SAD: 3}, true) {
		t.Fatal("open/saveClip must enqueue when per-cam and global cap are full")
	}
	found := false
	for i := 0; i < 8; i++ {
		ev, opened, ok := a.pull()
		if !ok {
			break
		}
		if ev.Label == "open" && opened {
			found = true
		}
	}
	if !found {
		t.Fatal("open job was skipped")
	}
}

func TestAdmit_openStealsOtherCamExtra(t *testing.T) {
	a := newAdmissions()
	a.cap = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "c1x", SAD: 1}, false) {
		t.Fatal("c1")
	}
	if !a.push(DetectEvent{Cam: "c2", Label: "c2x", SAD: 2}, false) {
		t.Fatal("c2")
	}
	if !a.push(DetectEvent{Cam: "c3", Label: "open", SAD: 3}, true) {
		t.Fatal("open must steal another cam extra when global cap is full")
	}
	if len(a.q) != 2 {
		t.Fatalf("q=%d want cap 2 (appended over cap?)", len(a.q))
	}
	var got []string
	open := false
	c2 := false
	for i := 0; i < 8; i++ {
		ev, opened, ok := a.pull()
		if !ok {
			break
		}
		got = append(got, ev.Cam)
		if ev.Cam == "c3" && ev.Label == "open" && opened {
			open = true
		}
		if ev.Cam == "c2" && ev.Label == "c2x" && !opened {
			c2 = true
		}
		if ev.Cam == "c1" {
			t.Fatalf("c1 extra still queued: %v", got)
		}
	}
	if !open || !c2 {
		t.Fatalf("want c2 extra and c3 open, got %v", got)
	}
}

func TestAdmit_openWhenFullOfOpens(t *testing.T) {
	a := newAdmissions()
	a.cap = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "o1"}, true) {
		t.Fatal("o1")
	}
	if !a.push(DetectEvent{Cam: "c2", Label: "o2"}, true) {
		t.Fatal("o2")
	}
	if !a.push(DetectEvent{Cam: "c3", Label: "o3"}, true) {
		t.Fatal("open must still enqueue when the queue is full of opens")
	}
	n := 0
	for i := 0; i < 8; i++ {
		_, opened, ok := a.pull()
		if !ok {
			break
		}
		if opened {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("opens=%d want 2 (oldest open replaced)", n)
	}
}

func TestAdmit_secondOpenReplacesSameCam(t *testing.T) {
	a := newAdmissions()
	if !a.push(DetectEvent{Cam: "c1", Label: "old"}, true) {
		t.Fatal("old")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "new"}, true) {
		t.Fatal("new")
	}
	if len(a.q) != 1 {
		t.Fatalf("q=%d want 1", len(a.q))
	}
	ev, opened, ok := a.pull()
	if !ok || ev.Label != "new" || !opened {
		t.Fatalf("want new open, got %s opened=%v ok=%v", ev.Label, opened, ok)
	}
	if _, _, ok := a.pull(); ok {
		t.Fatal("old open still queued")
	}
}

func TestAdmit_fullOfOpensReplacesOldest(t *testing.T) {
	a := newAdmissions()
	a.cap = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "o1"}, true) {
		t.Fatal("o1")
	}
	if !a.push(DetectEvent{Cam: "c2", Label: "o2"}, true) {
		t.Fatal("o2")
	}
	if !a.push(DetectEvent{Cam: "c3", Label: "o3"}, true) {
		t.Fatal("o3")
	}
	if len(a.q) != 2 {
		t.Fatalf("q=%d want cap 2", len(a.q))
	}
	ev, _, ok := a.pull()
	if !ok || ev.Cam != "c2" {
		t.Fatalf("want c2, got %s ok=%v", ev.Cam, ok)
	}
	ev, _, ok = a.pull()
	if !ok || ev.Cam != "c3" {
		t.Fatalf("want c3, got %s ok=%v", ev.Cam, ok)
	}
}

func TestAdmit_holdReplaceSurvivesPull(t *testing.T) {
	a := newAdmissions()
	a.perCam = 1
	if !a.push(DetectEvent{Cam: "c1", Label: "old", SAD: 1}, false) {
		t.Fatal("old")
	}
	h, ok := a.hold("c1", false)
	if !ok {
		t.Fatal("hold replace")
	}
	if _, _, pulled := a.pull(); pulled {
		t.Fatal("claimed extra still pullable")
	}
	if !a.fill(h, DetectEvent{Cam: "c1", Label: "new", SAD: 2}) {
		t.Fatal("fill newer")
	}
	ev, opened, ok := a.pull()
	if !ok || ev.Label != "new" || opened {
		t.Fatalf("want new extra, got %s opened=%v ok=%v", ev.Label, opened, ok)
	}
}

func TestAdmit_holdReplaceReleaseRestores(t *testing.T) {
	a := newAdmissions()
	a.perCam = 1
	if !a.push(DetectEvent{Cam: "c1", Label: "old", SAD: 1}, false) {
		t.Fatal("old")
	}
	h, ok := a.hold("c1", false)
	if !ok {
		t.Fatal("hold")
	}
	a.release(h)
	ev, _, ok := a.pull()
	if !ok || ev.Label != "old" {
		t.Fatalf("release must restore extra, got %s ok=%v", ev.Label, ok)
	}
}

func TestAdmit_globalFullReplacesExtra(t *testing.T) {
	a := newAdmissions()
	a.cap = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "old", SAD: 1}, false) {
		t.Fatal("old")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "mid", SAD: 2}, false) {
		t.Fatal("mid")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "new", SAD: 3}, false) {
		t.Fatal("global full must replace oldest extra")
	}
	ev, _, ok := a.pull()
	if !ok || ev.Label != "mid" {
		t.Fatalf("want mid, got %s ok=%v (kept oldest?)", ev.Label, ok)
	}
	ev, _, ok = a.pull()
	if !ok || ev.Label != "new" {
		t.Fatalf("want new, got %s ok=%v", ev.Label, ok)
	}
	if _, _, ok := a.pull(); ok {
		t.Fatal("queue should be empty")
	}
}

func TestAdmit_openAtCapDropsExtraNotOpen(t *testing.T) {
	a := newAdmissions()
	a.cap = 2
	if !a.push(DetectEvent{Cam: "c1", Label: "o1"}, true) {
		t.Fatal("c1")
	}
	if !a.push(DetectEvent{Cam: "c2", Label: "x2"}, false) {
		t.Fatal("c2")
	}
	if !a.push(DetectEvent{Cam: "c3", Label: "o3"}, true) {
		t.Fatal("c3")
	}
	ev, opened, ok := a.pull()
	if !ok || ev.Cam != "c1" || ev.Label != "o1" || !opened {
		t.Fatalf("want c1 open, got %s/%s opened=%v ok=%v", ev.Cam, ev.Label, opened, ok)
	}
	ev, opened, ok = a.pull()
	if !ok || ev.Cam != "c3" || ev.Label != "o3" || !opened {
		t.Fatalf("want c3 open, got %s/%s opened=%v ok=%v", ev.Cam, ev.Label, opened, ok)
	}
	if _, _, ok := a.pull(); ok {
		t.Fatal("c2 extra still queued")
	}
}

func TestAdmit_openReplacesOwnExtraNotOther(t *testing.T) {
	a := newAdmissions()
	a.cap = 2
	if !a.push(DetectEvent{Cam: "c2", Label: "x2"}, false) {
		t.Fatal("c2")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "x1"}, false) {
		t.Fatal("c1")
	}
	if !a.push(DetectEvent{Cam: "c1", Label: "o1"}, true) {
		t.Fatal("open")
	}
	ev, opened, ok := a.pull()
	if !ok || ev.Cam != "c2" || ev.Label != "x2" || opened {
		t.Fatalf("want c2 extra, got %s/%s opened=%v ok=%v", ev.Cam, ev.Label, opened, ok)
	}
	ev, opened, ok = a.pull()
	if !ok || ev.Cam != "c1" || ev.Label != "o1" || !opened {
		t.Fatalf("want c1 open, got %s/%s opened=%v ok=%v", ev.Cam, ev.Label, opened, ok)
	}
	if _, _, ok := a.pull(); ok {
		t.Fatal("c1 extra still queued")
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

func TestAdmit_recvGetsPush(t *testing.T) {
	a := newAdmissions()
	if !a.push(DetectEvent{Cam: "c1", Label: "job", SAD: 4}, true) {
		t.Fatal("push")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ev, opened, ok := a.recv(ctx)
	if !ok || ev.Label != "job" || !opened {
		t.Fatalf("recv got ok=%v opened=%v %s", ok, opened, ev.Label)
	}
}
