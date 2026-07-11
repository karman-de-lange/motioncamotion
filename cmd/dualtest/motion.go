package main

import (
	"math"
	"sort"
)

const (
	thumbSize    = 32 * 32
	noiseFloor   = 12
	sadThreshold = 3.0  // percent changed pixels
	hashThreshold = 8   // hamming bits
)

func sadPct(prev, curr []byte) float64 {
	if len(prev) != len(curr) || len(prev) == 0 {
		return 0
	}
	changed := 0
	for i := range prev {
		d := int(curr[i]) - int(prev[i])
		if d < 0 {
			d = -d
		}
		if d > noiseFloor {
			changed++
		}
	}
	return float64(changed) / float64(len(prev)) * 100
}

func pHash(gray []byte) uint64 {
	const n = 32
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