package main

import (
	"math"
	"sort"
)

const (
	thumbW     = 128
	thumbH     = 128
	thumbSize  = thumbW * thumbH
	noiseFloor = 12
)

// MotionMask is a normalized rectangle [0,1] over the sub frame (ignored for SAD).
type MotionMask struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// buildIgnoreMask returns per-thumb-cell true = ignore (OSD etc).
func buildIgnoreMask(masks []MotionMask) []bool {
	if len(masks) == 0 {
		return nil
	}
	ignore := make([]bool, thumbSize)
	for _, m := range masks {
		x0 := int(m.X * float64(thumbW))
		y0 := int(m.Y * float64(thumbH))
		x1 := int((m.X + m.W) * float64(thumbW))
		y1 := int((m.Y + m.H) * float64(thumbH))
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 > thumbW {
			x1 = thumbW
		}
		if y1 > thumbH {
			y1 = thumbH
		}
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				ignore[y*thumbW+x] = true
			}
		}
	}
	return ignore
}

// sadMotion compares thumbs; ignore cells are skipped. Returns % of active cells
// changed and the changed bitmap (for blob sizing).
func sadMotion(prev, curr []byte, ignore []bool) (pct float64, changed []bool) {
	if len(prev) != len(curr) || len(prev) == 0 {
		return 0, nil
	}
	changed = make([]bool, len(prev))
	active := 0
	nChanged := 0
	for i := range prev {
		if ignore != nil && ignore[i] {
			continue
		}
		active++
		d := int(curr[i]) - int(prev[i])
		if d < 0 {
			d = -d
		}
		if d > noiseFloor {
			changed[i] = true
			nChanged++
		}
	}
	if active == 0 {
		return 0, changed
	}
	return float64(nChanged) / float64(active) * 100, changed
}

func compactness(blob, nChanged int) float64 {
	if nChanged <= 0 {
		return 0
	}
	return float64(blob) / float64(nChanged)
}

const (
	noiseWin    = 48
	noiseNeed   = 16
	autoCompact = 0.25
)

// quietBlob tracks this camera's typical noise blob (foliage, not people).
type quietBlob struct {
	v    [noiseWin]int
	work [noiseWin]int
	n, i int
}

func blobLooksQuiet(blob, nCh int) bool {
	if blob < 16 {
		return true
	}
	return nCh >= 16 && compactness(blob, nCh) < autoCompact
}

func (q *quietBlob) push(blob int) {
	if blob < 0 {
		return
	}
	q.v[q.i] = blob
	q.i = (q.i + 1) % noiseWin
	if q.n < noiseWin {
		q.n++
	}
}

func (q *quietBlob) ready() bool { return q.n >= noiseNeed }

func (q *quietBlob) median() int {
	if q.n < 1 {
		return 0
	}
	copy(q.work[:q.n], q.v[:q.n])
	sort.Ints(q.work[:q.n])
	return q.work[q.n/2]
}

func (q *quietBlob) need(minBlob int) int {
	n := q.median() * 2
	if minBlob > n {
		n = minBlob
	}
	if n < 16 {
		n = 16
	}
	return n
}

func motionHit(sad float64, blob, nCh, minBlob, needBlob int, minCompact float64) bool {
	if blob < minBlob {
		return false
	}
	if minCompact > 0 && nCh >= 16 && compactness(blob, nCh) < minCompact {
		return false
	}
	if needBlob > 0 && blob < needBlob {
		return false
	}
	return true
}

func maxBlob(changed []bool, w, h int) int {
	n, _, _, _, _ := maxBlobBounds(changed, w, h)
	return n
}

// maxBlobBounds is the largest 4-connected region and its inclusive cell box.
func maxBlobBounds(changed []bool, w, h int) (size, x0, y0, x1, y1 int) {
	if len(changed) != w*h {
		return 0, 0, 0, 0, 0
	}
	seen := make([]bool, len(changed))
	var stack []int
	for i := range changed {
		if !changed[i] || seen[i] {
			continue
		}
		stack = stack[:0]
		stack = append(stack, i)
		seen[i] = true
		n, minx, miny, maxx, maxy := 0, w, h, -1, -1
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			n++
			x, y := p%w, p/w
			if x < minx {
				minx = x
			}
			if y < miny {
				miny = y
			}
			if x > maxx {
				maxx = x
			}
			if y > maxy {
				maxy = y
			}
			for _, nb := range [4]int{p - 1, p + 1, p - w, p + w} {
				if nb < 0 || nb >= len(changed) || seen[nb] || !changed[nb] {
					continue
				}
				nx, ny := nb%w, nb/w
				if abs(nx-x)+abs(ny-y) != 1 {
					continue
				}
				seen[nb] = true
				stack = append(stack, nb)
			}
		}
		if n > size {
			size, x0, y0, x1, y1 = n, minx, miny, maxx, maxy
		}
	}
	return size, x0, y0, x1, y1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func pHash(gray []byte) uint64 {
	const n = thumbW
	if len(gray) < n*n {
		return 0
	}
	coeffs := make([]float64, 0, 64)
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			if u == 0 && v == 0 {
				continue
			}
			var sum float64
			for x := 0; x < n; x++ {
				for y := 0; y < n; y++ {
					pixel := float64(gray[y*n+x])
					sum += pixel *
						math.Cos((math.Pi*(2*float64(x)+1)*float64(u))/(2*float64(n))) *
						math.Cos((math.Pi*(2*float64(y)+1)*float64(v))/(2*float64(n)))
				}
			}
			coeffs = append(coeffs, sum)
		}
	}
	sorted := append([]float64(nil), coeffs...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]

	var hash uint64
	for i, c := range coeffs {
		if c > median {
			hash |= 1 << uint(i)
		}
	}
	return hash
}

func hamming(a, b uint64) int {
	x := a ^ b
	n := 0
	for x != 0 {
		n += int(x & 1)
		x >>= 1
	}
	return n
}
