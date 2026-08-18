package main

// thumbRing holds recent motion thumbs (Y planes) for temporal averaging.
// Pure Go — same on Mac/Linux/CUDA after FrameDecoder produces a thumb.
type thumbRing struct {
	slots [][]byte // each len == thumbSize
	cap   int
	head  int // next write index
	len   int
}

func newThumbRing(capacity int) *thumbRing {
	if capacity < 2 {
		capacity = 2
	}
	slots := make([][]byte, capacity)
	for i := range slots {
		slots[i] = make([]byte, thumbSize)
	}
	return &thumbRing{slots: slots, cap: capacity}
}

func (r *thumbRing) clear() {
	r.head = 0
	r.len = 0
}

func (r *thumbRing) Len() int { return r.len }

// push copies thumb into the ring (thumb must be len thumbSize).
func (r *thumbRing) push(thumb []byte) {
	if len(thumb) < thumbSize {
		return
	}
	copy(r.slots[r.head], thumb[:thumbSize])
	r.head = (r.head + 1) % r.cap
	if r.len < r.cap {
		r.len++
	}
}

// at returns the thumb age frames ago (0 = newest). ok=false if not enough history.
func (r *thumbRing) at(age int) ([]byte, bool) {
	if age < 0 || age >= r.len {
		return nil, false
	}
	// newest is at head-1
	idx := r.head - 1 - age
	if idx < 0 {
		idx += r.cap
	}
	return r.slots[idx], true
}

// meanAge writes average of [startAge, startAge+n) into dst (newest age=0).
// Returns false if not enough samples.
func (r *thumbRing) meanAge(dst []byte, startAge, n int) bool {
	if n < 1 || len(dst) < thumbSize || startAge < 0 || startAge+n > r.len {
		return false
	}
	var sum [thumbSize]uint32
	for a := startAge; a < startAge+n; a++ {
		t, ok := r.at(a)
		if !ok {
			return false
		}
		for i := 0; i < thumbSize; i++ {
			sum[i] += uint32(t[i])
		}
	}
	div := uint32(n)
	for i := 0; i < thumbSize; i++ {
		dst[i] = byte(sum[i] / div)
	}
	return true
}

// motionScoreTemporal averages the newest avgN thumbs vs the avgN thumbs
// starting lag frames ago, then runs SAD+blob (same gates as before).
// ready=false until the ring has lag+avgN samples.
func motionScoreTemporal(r *thumbRing, avgN, lag int, ignore []bool, recentBuf, olderBuf []byte) (sad float64, blob, nChanged int, ready bool) {
	if avgN < 1 {
		avgN = 1
	}
	if lag < avgN {
		// need non-overlapping windows: older ends before recent starts
		lag = avgN
	}
	need := lag + avgN
	if r.Len() < need {
		return 0, 0, 0, false
	}
	if !r.meanAge(recentBuf, 0, avgN) {
		return 0, 0, 0, false
	}
	if !r.meanAge(olderBuf, lag, avgN) {
		return 0, 0, 0, false
	}
	sad, changed := sadMotion(olderBuf, recentBuf, ignore)
	blob = maxBlob(changed, thumbW, thumbH)
	for _, c := range changed {
		if c {
			nChanged++
		}
	}
	return sad, blob, nChanged, true
}
