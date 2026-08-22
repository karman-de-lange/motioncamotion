package main

import (
	"sync"
	"time"
)

type ringChunk struct {
	at   time.Time
	data []byte
}

type ringBuffer struct {
	mu         sync.Mutex
	chunks     []ringChunk
	total      int
	maxBytes   int
	onSnapshot func()
}

func newRingBuffer(maxBytes int) *ringBuffer {
	return &ringBuffer{maxBytes: maxBytes}
}

func (r *ringBuffer) push(data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.chunks = append(r.chunks, ringChunk{at: time.Now(), data: cp})
	r.total += len(cp)
	for r.total > r.maxBytes && len(r.chunks) > 0 {
		r.total -= len(r.chunks[0].data)
		r.chunks = r.chunks[1:]
	}
}

func (r *ringBuffer) clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.chunks = nil
	r.total = 0
	r.mu.Unlock()
}

func (r *ringBuffer) trim(keep time.Duration) {
	cutoff := time.Now().Add(-keep)

	r.mu.Lock()
	defer r.mu.Unlock()

	for len(r.chunks) > 0 && r.chunks[0].at.Before(cutoff) {
		r.total -= len(r.chunks[0].data)
		r.chunks = r.chunks[1:]
	}
}

func (r *ringBuffer) snapshot(keep time.Duration) [][]byte {
	if r.onSnapshot != nil {
		r.onSnapshot()
	}
	cutoff := time.Now().Add(-keep)

	r.mu.Lock()
	defer r.mu.Unlock()

	var out [][]byte
	for _, c := range r.chunks {
		if c.at.Before(cutoff) {
			continue
		}
		cp := make([]byte, len(c.data))
		copy(cp, c.data)
		out = append(out, cp)
	}
	return out
}
