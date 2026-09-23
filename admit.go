package main

import (
	"context"
	"sync"
)

const (
	detectQCap    = 24
	detectQPerCam = 4
)

type detectJob struct {
	ev       DetectEvent
	saveClip bool
}

type holdKind uint8

const (
	holdAppend holdKind = iota + 1
	holdReplace
	holdReplaceAny
)

type admitHold struct {
	cam      string
	saveClip bool
	kind     holdKind
	claimed  detectJob
}

// RTP must not block.
type admissions struct {
	mu      sync.Mutex
	q       []detectJob
	pending map[string]int
	held    int
	perCam  int
	cap     int
	wait    chan struct{}
}

func newAdmissions() *admissions {
	return &admissions{
		pending: make(map[string]int),
		perCam:  detectQPerCam,
		cap:     detectQCap,
		wait:    make(chan struct{}, 1),
	}
}

func (a *admissions) signal() {
	select {
	case a.wait <- struct{}{}:
	default:
	}
}

func (a *admissions) jobIndex(saveClip bool, cam string, anyCam bool) int {
	for i := range a.q {
		if a.q[i].saveClip != saveClip {
			continue
		}
		if anyCam || a.q[i].ev.Cam == cam {
			return i
		}
	}
	return -1
}

func (a *admissions) takeAt(i int) detectJob {
	job := a.q[i]
	copy(a.q[i:], a.q[i+1:])
	a.q[len(a.q)-1] = detectJob{}
	a.q = a.q[:len(a.q)-1]
	return job
}

func (a *admissions) queued() int {
	return len(a.q) + a.held
}

func (a *admissions) hold(cam string, saveClip bool) (admitHold, bool) {
	if a == nil {
		return admitHold{}, true
	}
	h := admitHold{cam: cam, saveClip: saveClip}
	a.mu.Lock()
	defer a.mu.Unlock()
	n := a.pending[cam]
	qn := a.queued()
	idx := -1
	if saveClip {
		// One open per camera. A newer episode replaces the queued one.
		if i := a.jobIndex(true, cam, false); i >= 0 {
			h.kind = holdReplace
			idx = i
		} else if qn >= a.cap {
			if i := a.jobIndex(false, cam, false); i >= 0 {
				h.kind = holdReplace
				idx = i
			} else if i := a.jobIndex(false, cam, true); i >= 0 {
				h.kind = holdReplaceAny
				idx = i
			} else if i := a.jobIndex(true, cam, true); i >= 0 {
				h.kind = holdReplaceAny
				idx = i
			}
		}
	} else if n >= a.perCam || qn >= a.cap {
		i := a.jobIndex(false, cam, false)
		if i < 0 {
			return admitHold{}, false
		}
		h.kind = holdReplace
		idx = i
	}
	if idx < 0 {
		a.pending[cam]++
		h.kind = holdAppend
	} else {
		h.claimed = a.takeAt(idx)
	}
	a.held++
	return h, true
}

func (a *admissions) release(h admitHold) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch h.kind {
	case holdAppend:
		a.held--
		a.debitLocked(h.cam)
	case holdReplace, holdReplaceAny:
		a.held--
		a.q = append(a.q, h.claimed)
		a.signal()
	}
}

func (a *admissions) fill(h admitHold, ev DetectEvent) bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	job := detectJob{ev: ev, saveClip: h.saveClip}
	switch h.kind {
	case holdAppend, holdReplace:
		a.q = append(a.q, job)
		a.held--
		a.signal()
		return true
	case holdReplaceAny:
		a.debitLocked(h.claimed.ev.Cam)
		a.pending[h.cam]++
		a.q = append(a.q, job)
		a.held--
		a.signal()
		return true
	default:
		return false
	}
}

func (a *admissions) push(ev DetectEvent, saveClip bool) bool {
	if a == nil {
		return true
	}
	h, ok := a.hold(ev.Cam, saveClip)
	if !ok {
		return false
	}
	if !a.fill(h, ev) {
		a.release(h)
		return false
	}
	return true
}

func (a *admissions) recv(ctx context.Context) (DetectEvent, bool, bool) {
	if a == nil {
		<-ctx.Done()
		return DetectEvent{}, false, false
	}
	for {
		if ev, opened, ok := a.pull(); ok {
			return ev, opened, true
		}
		select {
		case <-ctx.Done():
			return DetectEvent{}, false, false
		case <-a.wait:
		}
	}
}

func (a *admissions) pull() (DetectEvent, bool, bool) {
	if a == nil {
		return DetectEvent{}, false, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.q) == 0 {
		return DetectEvent{}, false, false
	}
	job := a.q[0]
	a.q[0] = detectJob{}
	a.q = a.q[1:]
	a.debitLocked(job.ev.Cam)
	return job.ev, job.saveClip, true
}

func (a *admissions) debitLocked(cam string) {
	n := a.pending[cam] - 1
	if n <= 0 {
		delete(a.pending, cam)
	} else {
		a.pending[cam] = n
	}
}
