package main

import (
	"sync"
	"time"
)

const (
	occPersonTTL          = 5 * time.Second
	occGoneSamples        = 3
	occVehicleGoneSamples = 60
	occAnimalGoneSamples  = 60
	occIoU                = 0.40
	occExpandPx           = 48.0
)

type occTrack struct {
	classID  int
	x1, y1   float64
	x2, y2   float64
	lastSeen time.Time
	gone     int
	vehicle  bool
	animal   bool
}

type occupancy struct {
	mu   sync.Mutex
	cams map[string][]occTrack
}

func newOccupancy() *occupancy {
	return &occupancy{cams: make(map[string][]occTrack)}
}

func isVehicleClass(id int) bool {
	return id == 1 || id == 2 || id == 3 || id == 5 || id == 7
}

func isAnimalClass(id int) bool {
	return id >= 14 && id <= 19
}

func boxIoU(a, b occTrack) float64 {
	ix1 := a.x1
	if b.x1 > ix1 {
		ix1 = b.x1
	}
	iy1 := a.y1
	if b.y1 > iy1 {
		iy1 = b.y1
	}
	ix2 := a.x2
	if b.x2 < ix2 {
		ix2 = b.x2
	}
	iy2 := a.y2
	if b.y2 < iy2 {
		iy2 = b.y2
	}
	iw := ix2 - ix1
	ih := iy2 - iy1
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	aa := (a.x2 - a.x1) * (a.y2 - a.y1)
	ba := (b.x2 - b.x1) * (b.y2 - b.y1)
	u := aa + ba - inter
	if u <= 0 {
		return 0
	}
	return inter / u
}

func centerInExpanded(track occTrack, det occTrack) bool {
	w := track.x2 - track.x1
	h := track.y2 - track.y1
	mx := w * 0.5
	if mx > occExpandPx {
		mx = occExpandPx
	}
	my := h * 0.5
	if my > occExpandPx {
		my = occExpandPx
	}
	cx := (det.x1 + det.x2) / 2
	cy := (det.y1 + det.y2) / 2
	return cx >= track.x1-mx && cx <= track.x2+mx && cy >= track.y1-my && cy <= track.y2+my
}

func associate(track occTrack, det occTrack) bool {
	if track.classID != det.classID && !(track.vehicle && det.vehicle) && !(track.animal && det.animal) {
		return false
	}
	if boxIoU(track, det) >= occIoU {
		return true
	}
	if track.vehicle && det.vehicle {
		return centerInExpanded(track, det) || centerInExpanded(det, track)
	}
	if track.vehicle {
		return false
	}
	return centerInExpanded(track, det)
}

func detToTrack(d DetBox, now time.Time) occTrack {
	return occTrack{
		classID:  d.ClassID,
		x1:       d.X1,
		y1:       d.Y1,
		x2:       d.X2,
		y2:       d.Y2,
		lastSeen: now,
		vehicle:  isVehicleClass(d.ClassID),
		animal:   isAnimalClass(d.ClassID),
	}
}

// consume updates tracks for cam. Returns dets that are new objects (should alert).
func (o *occupancy) consume(cam string, dets []DetBox, now time.Time) []DetBox {
	o.mu.Lock()
	defer o.mu.Unlock()

	tracks := o.cams[cam]
	alive := tracks[:0]
	for _, tr := range tracks {
		if tr.vehicle || tr.animal {
			alive = append(alive, tr)
			continue
		}
		if now.Sub(tr.lastSeen) < occPersonTTL {
			alive = append(alive, tr)
		}
	}
	tracks = alive
	matched := make([]bool, len(tracks))
	var fresh []DetBox
	sawVehicle := false
	sawAnimal := false
	for _, d := range dets {
		if isVehicleClass(d.ClassID) {
			sawVehicle = true
		}
		if isAnimalClass(d.ClassID) {
			sawAnimal = true
		}
	}

	for _, d := range dets {
		dt := detToTrack(d, now)
		hit := -1
		for i, tr := range tracks {
			if matched[i] {
				continue
			}
			if associate(tr, dt) {
				hit = i
				break
			}
		}
		if hit >= 0 {
			tr := tracks[hit]
			tr.x1, tr.y1, tr.x2, tr.y2 = dt.x1, dt.y1, dt.x2, dt.y2
			tr.lastSeen = now
			tr.gone = 0
			tracks[hit] = tr
			matched[hit] = true
			continue
		}
		// Second YOLO class on the same parked bakkie (truck+car) is not a new object.
		if dt.vehicle {
			dup := false
			for _, tr := range tracks {
				if associate(tr, dt) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
		}
		tracks = append(tracks, dt)
		matched = append(matched, true)
		fresh = append(fresh, d)
	}

	kept := tracks[:0]
	for i, tr := range tracks {
		if matched[i] {
			kept = append(kept, tr)
			continue
		}
		if tr.vehicle {
			// Empty infer is not leave. Need many misses: YOLO often drops
			// one of two parked wrecks for tens of frames.
			if !sawVehicle {
				kept = append(kept, tr)
				continue
			}
			tr.gone++
			if tr.gone < occVehicleGoneSamples {
				kept = append(kept, tr)
			}
			continue
		}
		if tr.animal {
			// Empty infer is not leave. A sitting bird / stump false-positive
			// flickers around conf and last-GOP extras would re-alert every few seconds.
			if !sawAnimal {
				kept = append(kept, tr)
				continue
			}
			tr.gone++
			if tr.gone < occAnimalGoneSamples {
				kept = append(kept, tr)
			}
			continue
		}
		tr.gone++
		if tr.gone < occGoneSamples && now.Sub(tr.lastSeen) < occPersonTTL {
			kept = append(kept, tr)
		}
	}
	o.cams[cam] = kept
	return fresh
}
