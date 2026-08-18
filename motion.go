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

// maxBlob returns the largest 4-connected region of changed cells.
func maxBlob(changed []bool, w, h int) int {
	if len(changed) != w*h {
		return 0
	}
	seen := make([]bool, len(changed))
	best := 0
	var stack []int
	for i := range changed {
		if !changed[i] || seen[i] {
			continue
		}
		stack = stack[:0]
		stack = append(stack, i)
		seen[i] = true
		size := 0
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			x, y := p%w, p/w
			for _, n := range [4]int{p - 1, p + 1, p - w, p + w} {
				if n < 0 || n >= len(changed) || seen[n] || !changed[n] {
					continue
				}
				nx, ny := n%w, n/w
				if abs(nx-x)+abs(ny-y) != 1 {
					continue
				}
				seen[n] = true
				stack = append(stack, n)
			}
		}
		if size > best {
			best = size
		}
	}
	return best
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
