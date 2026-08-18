package main

import (
	"sync"
	"time"
)

type sampleKind int

const (
	sampleOpen sampleKind = iota
	sampleHot
	sampleClose
)

const (
	admitCPUMax = 70.0
	admitQBusy  = 8
	admitHotGap = 500 * time.Millisecond
)

type cpuPercent interface {
	Percent() float64
}

type camSlot struct {
	inflight bool
	pending  *DetectEvent
	lastHot  time.Time
}

type admissions struct {
	q      chan DetectEvent
	cpu    cpuPercent
	cpuMax float64
	qBusy  int
	hotGap time.Duration

	mu   sync.Mutex
	cams map[string]*camSlot
}

func newAdmissions(q chan DetectEvent, cpu cpuPercent) *admissions {
	return &admissions{
		q:      q,
		cpu:    cpu,
		cpuMax: admitCPUMax,
		qBusy:  admitQBusy,
		hotGap: admitHotGap,
		cams:   make(map[string]*camSlot),
	}
}

func (a *admissions) slot(cam string) *camSlot {
	s := a.cams[cam]
	if s == nil {
		s = &camSlot{}
		a.cams[cam] = s
	}
	return s
}

func (a *admissions) loadHigh() bool {
	if a.cpu != nil && a.cpu.Percent() > a.cpuMax {
		return true
	}
	return len(a.q) >= a.qBusy
}

// submit admits a YOLO sample. Open is never dropped for CPU/queue (may replace
// a pending Hot/Close). Hot is capped at hotGap and dropped when load is high.
func (a *admissions) submit(cam string, kind sampleKind, ev DetectEvent) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	s := a.slot(cam)
	if kind == sampleHot && !s.lastHot.IsZero() && time.Since(s.lastHot) < a.hotGap {
		a.mu.Unlock()
		return false
	}
	if kind != sampleOpen && a.loadHigh() {
		a.mu.Unlock()
		return false
	}
	if kind == sampleHot {
		s.lastHot = time.Now()
	}
	ev.kind = kind
	if s.inflight {
		s.pending = &ev
		a.mu.Unlock()
		return true
	}
	s.inflight = true
	ev.done = func() { a.release(cam) }
	a.mu.Unlock()
	if a.enqueue(ev, kind == sampleOpen) {
		return true
	}
	a.release(cam)
	return false
}

func (a *admissions) enqueue(ev DetectEvent, block bool) bool {
	select {
	case a.q <- ev:
		return true
	default:
		if !block {
			return false
		}
		a.q <- ev
		return true
	}
}

func (a *admissions) release(cam string) {
	a.mu.Lock()
	s := a.cams[cam]
	if s == nil {
		a.mu.Unlock()
		return
	}
	p := s.pending
	s.pending = nil
	if p == nil {
		s.inflight = false
		a.mu.Unlock()
		return
	}
	p.done = func() { a.release(cam) }
	a.mu.Unlock()
	if !a.enqueue(*p, false) {
		a.mu.Lock()
		if a.cams[cam] != nil {
			a.cams[cam].inflight = false
		}
		a.mu.Unlock()
	}
}
