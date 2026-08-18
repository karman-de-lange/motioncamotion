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
	SubConf     float64
	MainConf    float64
	ForceStage2 bool
}

var cocoNames = map[int]string{
	0:  "person",
	2:  "car",
	5:  "bus",
	7:  "truck",
	14: "bird",
	15: "cat",
	16: "dog",
	17: "horse",
	18: "sheep",
	19: "cow",
	20: "elephant",
	21: "bear",
	22: "zebra",
	23: "giraffe",
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

func runYOLOWorker(ctx context.Context, detectQ <-chan DetectEvent, logQ chan<- string, store *EventStore, inf *C.OVInfer, yc yoloConfig) {
	device := C.GoString(C.ov_infer_device(inf))
	logQ <- fmt.Sprintf("yolo ready device=%s sub_conf=%.2f main_conf=%.2f", device, yc.SubConf, yc.MainConf)
	occ := newOccupancy()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-detectQ:
			func() {
				defer ev.finish()
				logQ <- fmt.Sprintf("%s: yolo start %s %d aus %.1fMB", ev.Cam, ev.Label, len(ev.Segments), eventMB(ev))

				// Stage 1: sub RGB when available.
				if len(ev.SubRGB) > 0 {
					subPass, subMS, subScore, subName, err := runInfer(inf, ev.SubRGB, yc.SubConf)
					if err != nil {
						logQ <- fmt.Sprintf("%s: stage1 error: %v", ev.Cam, err)
						// still try stage2 / save motion
					} else {
						logQ <- fmt.Sprintf("%s: stage1 sub infer=%.0fms pass=%v score=%.2f %s",
							ev.Cam, subMS, subPass, subScore, subName)
						if !subPass && !yc.ForceStage2 {
							// Persist motion clip even on sub reject.
							if store != nil {
								if rec, err := store.SaveMotion(ev, nil); err != nil {
									logQ <- fmt.Sprintf("%s: motion save failed: %v", ev.Cam, err)
								} else {
									logQ <- fmt.Sprintf("%s: motion saved %s (sub reject)", ev.Cam, rec.ID)
								}
							}
							logQ <- fmt.Sprintf("%s: stage done (sub reject)", ev.Cam)
							return
						}
					}
				}

				// Stage 2: last decoded main-ring frame (not brightest).
				mainRGB, frames, ok := decodeRGBNearEnd(ev.Segments, inferSize, inferSize)
				var stillPlain []byte
				if ok {
					stillPlain, _ = encodePreviewJPEG(mainRGB, inferSize, inferSize)
				}

				// Always save motion clip (even if decode/YOLO fail).
				var clipRel string
				if store != nil {
					rec, err := store.SaveMotion(ev, stillPlain)
					if err != nil {
						logQ <- fmt.Sprintf("%s: motion save failed: %v", ev.Cam, err)
					} else {
						clipRel = rec.ClipRel
						logQ <- fmt.Sprintf("%s: motion saved %s", ev.Cam, rec.ID)
					}
				}

				if !ok {
					logQ <- fmt.Sprintf("%s: stage2 main decode failed aus=%d", ev.Cam, len(ev.Segments))
					return
				}

				dets, mainMS, err := runInferMulti(inf, mainRGB, yc.MainConf)
				if err != nil {
					logQ <- fmt.Sprintf("%s: stage2 error: %v", ev.Cam, err)
					return
				}
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
				logQ <- fmt.Sprintf("%s: stage2 main frames=%d infer=%.0fms pass=%v dets=%d fresh=%d score=%.2f %s",
					ev.Cam, frames, mainMS, mainPass, len(dets), len(fresh), topScore, topName)

				if !mainPass {
					logQ <- fmt.Sprintf("%s: stage done (main reject)", ev.Cam)
					return
				}
				dets = fresh

				// Annotated multi-box still for alerts.
				ann, err := AnnotateRGB(mainRGB, inferSize, inferSize, dets, 90)
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
			}()
		}
	}
}
