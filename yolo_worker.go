package main

/*
#cgo CFLAGS: -I/home/karman/opt/openvino/runtime/include -I/usr/include
#cgo LDFLAGS: -L/home/karman/opt/openvino/runtime/lib/intel64 -L/home/karman/opt/openvino/runtime/3rdparty/tbb/lib -L/usr/lib -L/usr/lib/x86_64-linux-gnu -Wl,-rpath,/home/karman/opt/openvino/runtime/lib/intel64:/home/karman/opt/openvino/runtime/3rdparty/tbb/lib -lopenvino_c -lopenvino -lopenvino_onnx_frontend -Wl,-Bdynamic -ltbb -ldl

#include <stdlib.h>
#include "ov_infer.h"
*/
import "C"

import (
	"context"
	"fmt"
	"path/filepath"
	"unsafe"
)

type yoloConfig struct {
	MainConf float64
}

const smallDetH = inferSize / 8
const smallDetConf = 0.65
const stumpMaxAspect = 1.5

func stumpGeom(w, h float64) bool {
	return h < float64(smallDetH) && w > 0 && h < w*stumpMaxAspect
}

func filterDets(dets []DetBox, conf float64) []DetBox {
	out := dets[:0]
	for _, d := range dets {
		h := d.Y2 - d.Y1
		w := d.X2 - d.X1
		if h <= 0 || w <= 0 || d.Score < conf {
			continue
		}
		if stumpGeom(w, h) && d.Score < smallDetConf {
			continue
		}
		out = append(out, d)
	}
	return out
}

var cocoNames = map[int]string{
	0:  "person",
	2:  "car",
	5:  "bus",
	7:  "truck",
	14: "animal",
	15: "animal",
	16: "animal",
	17: "animal",
	18: "animal",
	19: "animal",
}

func newOVInfer(modelPath, device string) (*C.OVInfer, error) {
	abs, err := filepath.Abs(modelPath)
	if err != nil {
		return nil, err
	}
	cModel := C.CString(abs)
	defer C.free(unsafe.Pointer(cModel))
	cDevice := C.CString(device)
	defer C.free(unsafe.Pointer(cDevice))

	var inf *C.OVInfer
	if rc := C.ov_infer_new(&inf, cModel, cDevice, C.int(inferSize), C.int(inferSize)); rc != 0 {
		return nil, fmt.Errorf("ov_infer_new %d (model=%s device=%s)", int(rc), abs, device)
	}
	return inf, nil
}

func freeOVInfer(inf *C.OVInfer) {
	if inf != nil {
		C.ov_infer_free(inf)
	}
}

func runInfer(inf *C.OVInfer, rgb []byte, conf float64) (pass bool, ms, score float64, className string, err error) {
	if inf == nil || len(rgb) == 0 {
		return false, 0, 0, "", fmt.Errorf("infer not ready")
	}
	var cMs, cScore C.float
	var cPass, cClass C.int
	var cX1, cY1, cX2, cY2 C.float
	rc := C.ov_infer_run(inf,
		(*C.uint8_t)(unsafe.Pointer(&rgb[0])),
		C.float(conf),
		&cMs, &cPass, &cScore, &cClass,
		&cX1, &cY1, &cX2, &cY2)
	if rc != 0 {
		return false, 0, 0, "", fmt.Errorf("ov_infer_run %d", int(rc))
	}
	name := cocoNames[int(cClass)]
	return cPass != 0, float64(cMs), float64(cScore), name, nil
}

// runInferMulti returns all wanted-class detections at/above conf (sorted by score).
func runInferMulti(inf *C.OVInfer, rgb []byte, conf float64) (dets []DetBox, ms float64, err error) {
	if inf == nil || len(rgb) == 0 {
		return nil, 0, fmt.Errorf("infer not ready")
	}
	var cMs C.float
	var cN C.int
	var cDets [C.OV_MAX_DETS]C.OVDet
	rc := C.ov_infer_run_multi(inf,
		(*C.uint8_t)(unsafe.Pointer(&rgb[0])),
		C.float(conf),
		&cMs,
		&cDets[0],
		C.int(C.OV_MAX_DETS),
		&cN)
	if rc != 0 {
		return nil, 0, fmt.Errorf("ov_infer_run_multi %d", int(rc))
	}
	n := int(cN)
	if n < 0 {
		n = 0
	}
	if n > int(C.OV_MAX_DETS) {
		n = int(C.OV_MAX_DETS)
	}
	out := make([]DetBox, 0, n)
	for i := 0; i < n; i++ {
		d := cDets[i]
		name := cocoNames[int(d.class_id)]
		if name == "" {
			name = fmt.Sprintf("cls%d", int(d.class_id))
		}
		out = append(out, DetBox{
			Class:   name,
			Score:   float64(d.score),
			X1:      float64(d.x1),
			Y1:      float64(d.y1),
			X2:      float64(d.x2),
			Y2:      float64(d.y2),
			ClassID: int(d.class_id),
		})
	}
	return out, float64(cMs), nil
}

func rgbMean(rgb []byte) int {
	if len(rgb) == 0 {
		return 0
	}
	sum := 0
	for i := 0; i < len(rgb); i += 3 {
		sum += (int(rgb[i]) + int(rgb[i+1]) + int(rgb[i+2])) / 3
	}
	return sum / (len(rgb) / 3)
}

func runYOLOWorker(ctx context.Context, adm *admissions, logQ chan<- string, store *EventStore, inf *C.OVInfer, yc yoloConfig) {
	device := C.GoString(C.ov_infer_device(inf))
	logQ <- fmt.Sprintf("yolo ready device=%s main_conf=%.2f", device, yc.MainConf)
	occ := newOccupancy()

	for {
		ev, opened, ok := adm.recv(ctx)
		if !ok {
			return
		}
		processDetect(ev, opened, logQ, store, inf, yc, occ)
	}
}

func processDetect(ev DetectEvent, saveClip bool, logQ chan<- string, store *EventStore, inf *C.OVInfer, yc yoloConfig, occ *occupancy) {
	mainRGB, frames, ok := decodeRGBNearEnd(ev.Segments, inferSize, inferSize)
	stillRGB, stillW, stillH, _, stillOK := decodeRGBFitNearEnd(ev.Segments, stillMaxW, stillMaxH)
	var stillPlain []byte
	if stillOK {
		stillPlain, _ = encodePreviewJPEG(stillRGB, stillW, stillH)
	} else if ok {
		stillPlain, _ = encodePreviewJPEG(mainRGB, inferSize, inferSize)
	}

	var clipRel string
	if saveClip && store != nil {
		rec, err := store.SaveMotion(ev, stillPlain)
		if err != nil {
			logQ <- fmt.Sprintf("%s: motion save failed: %v", ev.Cam, err)
		} else {
			clipRel = rec.ClipRel
			logQ <- fmt.Sprintf("%s: motion saved %s", ev.Cam, rec.ID)
		}
	}

	if !ok {
		logQ <- fmt.Sprintf("%s: main decode failed aus=%d", ev.Cam, len(ev.Segments))
		return
	}

	dets, mainMS, err := runInferMulti(inf, mainRGB, yc.MainConf)
	if err != nil {
		logQ <- fmt.Sprintf("%s: infer error: %v", ev.Cam, err)
		return
	}
	dets = filterDets(dets, yc.MainConf)
	fresh := occ.consume(ev.Cam, dets, ev.At)
	mainPass := len(fresh) > 0
	topName := ""
	topScore := 0.0
	if mainPass {
		topName = fresh[0].Class
		topScore = fresh[0].Score
	} else if len(dets) > 0 {
		topName = dets[0].Class
		topScore = dets[0].Score
	}
	if !mainPass {
		if len(dets) > 0 {
			logQ <- fmt.Sprintf("%s: same %s %.2f (no new alert)", ev.Cam, topName, topScore)
		}
		return
	}
	logQ <- fmt.Sprintf("%s: main frames=%d infer=%.0fms pass=%v dets=%d fresh=%d score=%.2f %s",
		ev.Cam, frames, mainMS, mainPass, len(dets), len(fresh), topScore, topName)
	dets = fresh

	annRGB, annW, annH := mainRGB, inferSize, inferSize
	annDets := dets
	if stillOK {
		annRGB, annW, annH = stillRGB, stillW, stillH
		annDets = mapDetsLetterboxToStill(dets, inferSize, inferSize, stillW, stillH)
	}
	ann, err := AnnotateRGB(annRGB, annW, annH, annDets, 90)
	if err != nil {
		logQ <- fmt.Sprintf("%s: annotate failed: %v", ev.Cam, err)
		ann = stillPlain
	}
	if store != nil && len(ann) > 0 {
		arec, err := store.SaveAlert(ev, ann, dets, topName, topScore, clipRel)
		if err != nil {
			logQ <- fmt.Sprintf("%s: alert save failed: %v", ev.Cam, err)
		} else {
			logQ <- fmt.Sprintf("%s: alert saved %s dets=%d", ev.Cam, arec.ID, len(dets))
		}
	}
	logQ <- fmt.Sprintf("%s: stage done (detect %s %.2f)", ev.Cam, topName, topScore)
}
