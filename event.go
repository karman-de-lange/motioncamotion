package main

import "time"

type DetectEvent struct {
	Cam      string
	At       time.Time
	Segments [][]byte
	SubRGB   []byte // optional 640×640 RGB from sub at trigger time (stage1)
	SAD      float64
	Label    string
}

func eventMB(ev DetectEvent) float64 {
	n := 0
	for _, s := range ev.Segments {
		n += len(s)
	}
	return float64(n) / (1024 * 1024)
}
