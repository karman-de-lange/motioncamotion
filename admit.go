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

// admissions is a bounded YOLO work queue (channel). Extra frames of the
// same camera are skipped when that camera already has detectQPerCam queued
// or the global channel is full — RTP never blocks.
type admissions struct {
	ch      chan detectJob
	mu      sync.Mutex
	pending map[string]int
	perCam  int
}

func newAdmissions() *admissions {
	return &admissions{
		ch:      make(chan detectJob, detectQCap),
		pending: make(map[string]int),
		perCam:  detectQPerCam,
	}
}

func (a *admissions) push(ev DetectEvent, saveClip bool) bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	if a.pending[ev.Cam] >= a.perCam {
		a.mu.Unlock()
		return false
	}
	a.pending[ev.Cam]++
	a.mu.Unlock()

	job := detectJob{ev: ev, saveClip: saveClip}
	select {
	case a.ch <- job:
		return true
	default:
		a.mu.Lock()
		a.pending[ev.Cam]--
		if a.pending[ev.Cam] <= 0 {
			delete(a.pending, ev.Cam)
		}
		a.mu.Unlock()
		return false
	}
}

func (a *admissions) recv(ctx context.Context) (DetectEvent, bool, bool) {
	if a == nil {
		<-ctx.Done()
		return DetectEvent{}, false, false
	}
	select {
	case <-ctx.Done():
		return DetectEvent{}, false, false
	case job := <-a.ch:
		a.debit(job.ev.Cam)
		return job.ev, job.saveClip, true
	}
}

func (a *admissions) pull() (DetectEvent, bool, bool) {
	if a == nil {
		return DetectEvent{}, false, false
	}
	select {
	case job := <-a.ch:
		a.debit(job.ev.Cam)
		return job.ev, job.saveClip, true
	default:
		return DetectEvent{}, false, false
	}
}

func (a *admissions) debit(cam string) {
	a.mu.Lock()
	n := a.pending[cam] - 1
	if n <= 0 {
		delete(a.pending, cam)
	} else {
		a.pending[cam] = n
	}
	a.mu.Unlock()
}
