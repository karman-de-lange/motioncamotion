package main

import "time"

type DetectEvent struct {
	Cam      string
	At       time.Time
	Segments [][]byte
	SAD      float64
	Label    string
	BlobX    int // thumb cells
	BlobY    int
	BlobW    int
	BlobH    int
}

func eventMB(ev DetectEvent) float64 {
	n := 0
	for _, s := range ev.Segments {
		n += len(s)
	}
	return float64(n) / (1024 * 1024)
}
