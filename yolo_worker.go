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
	15: "cat",
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

func runYOLOWorker(ctx context.Context, detectQ <-chan DetectEvent, logQ chan<- string, clips *clipSaver, inf *C.OVInfer, yc yoloConfig) {
	device := C.GoString(C.ov_infer_device(inf))
	logQ <- fmt.Sprintf("yolo ready device=%s sub_conf=%.2f main_conf=%.2f", device, yc.SubConf, yc.MainConf)

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-detectQ:
			logQ <- fmt.Sprintf("%s: yolo start %s %d aus %.1fMB", ev.Cam, ev.Label, len(ev.Segments), eventMB(ev))

			// Stage 1: sub RGB when available.
			if len(ev.SubRGB) > 0 {
				subPass, subMS, subScore, subName, err := runInfer(inf, ev.SubRGB, yc.SubConf)
				if err != nil {
					logQ <- fmt.Sprintf("%s: stage1 error: %v", ev.Cam, err)
					continue
				}
				logQ <- fmt.Sprintf("%s: stage1 sub infer=%.0fms pass=%v score=%.2f %s",
					ev.Cam, subMS, subPass, subScore, subName)
				if !subPass && !yc.ForceStage2 {
					logQ <- fmt.Sprintf("%s: stage done (sub reject)", ev.Cam)
					continue
				}
			}

			// Stage 2: primary detect — decode main-ring AUs → YOLO (universal path).
			mainRGB, frames, ok := decodeRGBBest(ev.Segments, inferSize, inferSize)
			if !ok {
				logQ <- fmt.Sprintf("%s: stage2 main decode failed aus=%d", ev.Cam, len(ev.Segments))
				continue
			}
			mainPass, mainMS, mainScore, mainName, err := runInfer(inf, mainRGB, yc.MainConf)
			if err != nil {
				logQ <- fmt.Sprintf("%s: stage2 error: %v", ev.Cam, err)
				continue
			}
			logQ <- fmt.Sprintf("%s: stage2 main frames=%d infer=%.0fms pass=%v score=%.2f %s",
				ev.Cam, frames, mainMS, mainPass, mainScore, mainName)

			if !mainPass {
				logQ <- fmt.Sprintf("%s: stage done (main reject)", ev.Cam)
				continue
			}

			if clips != nil && clips.want(ev.Cam) {
				path, err := clips.save(ev)
				if err != nil {
					logQ <- fmt.Sprintf("%s: clip save failed: %v", ev.Cam, err)
				} else if path != "" {
					logQ <- fmt.Sprintf("%s: clip saved %s", ev.Cam, path)
				}
			}
			logQ <- fmt.Sprintf("%s: stage done (detect %s %.2f)", ev.Cam, mainName, mainScore)
		}
	}
}
