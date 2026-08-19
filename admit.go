package main

import "sync"

const admitCPUMax = 70.0

type cpuPercent interface {
	Percent() float64
}

type camSlot struct {
	latest *DetectEvent
	opened bool
}

type admissions struct {
	cpu    cpuPercent
	cpuMax float64
	wake   chan struct{}

	mu   sync.Mutex
	cams map[string]*camSlot
}

func newAdmissions(cpu cpuPercent) *admissions {
	return &admissions{
		cpu:    cpu,
		cpuMax: admitCPUMax,
		wake:   make(chan struct{}, 1),
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
	return a.cpu != nil && a.cpu.Percent() > a.cpuMax
}

func (a *admissions) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// mark overwrites this camera's latest sample. opened sticks until pull.
func (a *admissions) mark(cam string, ev DetectEvent, opened bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	s := a.slot(cam)
	s.latest = &ev
	if opened {
		s.opened = true
	}
	a.mu.Unlock()
	a.signal()
}

// pull takes one latest sample. When CPU is high, only a cam that just opened.
func (a *admissions) pull() (ev DetectEvent, opened bool, ok bool) {
	if a == nil {
		return DetectEvent{}, false, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ev, opened, ok = a.takeOpened(); ok {
		return ev, opened, true
	}
	if a.loadHigh() {
		return DetectEvent{}, false, false
	}
	return a.takeAny()
}

func (a *admissions) takeOpened() (DetectEvent, bool, bool) {
	for _, s := range a.cams {
		if s.latest != nil && s.opened {
			return a.take(s)
		}
	}
	return DetectEvent{}, false, false
}

func (a *admissions) takeAny() (DetectEvent, bool, bool) {
	for _, s := range a.cams {
		if s.latest != nil {
			return a.take(s)
		}
	}
	return DetectEvent{}, false, false
}

func (a *admissions) take(s *camSlot) (DetectEvent, bool, bool) {
	ev := *s.latest
	opened := s.opened
	s.latest = nil
	s.opened = false
	return ev, opened, true
}
