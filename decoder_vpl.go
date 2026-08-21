//go:build !nvidia_nvdec && !darwin

package main

/*
#cgo CFLAGS: -I/usr/include/vpl
#cgo LDFLAGS: -lmfx-gen -lstdc++

#include <stdlib.h>
#include "vpl_decode.h"
*/
import "C"

import "unsafe"

// Intel oneVPL / VAAPI backend (default build). See docs/UNIVERSAL_CORE.md.

type vplDecoder struct {
	dec *C.VPLDecoder
	y   []byte
	uv  []byte
}

func newFrameDecoder(codec int) (FrameDecoder, int) {
	var d *C.VPLDecoder
	rc := int(C.vpl_decoder_new(&d, C.int(codec)))
	if rc != 0 || d == nil {
		return nil, rc
	}
	return &vplDecoder{dec: d}, 0
}

func (v *vplDecoder) Thumb(au []byte, yOut []byte) int {
	if v == nil || v.dec == nil || len(au) == 0 || len(yOut) == 0 {
		return -1
	}
	return int(C.vpl_decoder_thumb(v.dec,
		(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
		(*C.uint8_t)(unsafe.Pointer(&yOut[0])), C.int(len(yOut))))
}

func (v *vplDecoder) RGB(au []byte, rgbOut []byte, outW, outH int) int {
	if v == nil || v.dec == nil || len(au) == 0 || len(rgbOut) == 0 {
		return -1
	}
	y, uv, pitch, w, h, rc := v.decodeNV12(au)
	if rc != 0 {
		return rc
	}
	if !useChroma(uv, pitch, w, h) {
		// Dead UV must not be converted; BT.601 of U=V=0 is green.
		uv = nil
	}
	var uvp *C.uint8_t
	if len(uv) > 0 {
		uvp = (*C.uint8_t)(unsafe.Pointer(&uv[0]))
	}
	C.vpl_nv12_letterbox_rgb(
		(*C.uint8_t)(unsafe.Pointer(&y[0])),
		uvp,
		C.int(pitch), C.int(w), C.int(h),
		(*C.uint8_t)(unsafe.Pointer(&rgbOut[0])),
		C.int(outW), C.int(outH),
	)
	return 0
}

func (v *vplDecoder) decodeNV12(au []byte) (y, uv []byte, pitch, w, h, rc int) {
	if len(v.y) == 0 {
		v.y = make([]byte, 1920*1088)
		v.uv = make([]byte, 1920*544)
	}
	for i := 0; i < 2; i++ {
		var cw, ch C.int
		rc = int(C.vpl_decoder_nv12(v.dec,
			(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
			(*C.uint8_t)(unsafe.Pointer(&v.y[0])), C.int(len(v.y)),
			(*C.uint8_t)(unsafe.Pointer(&v.uv[0])), C.int(len(v.uv)),
			&cw, &ch))
		w, h = int(cw), int(ch)
		if rc != -2 {
			break
		}
		v.y = make([]byte, w*h)
		v.uv = make([]byte, w*((h+1)/2))
	}
	if rc != 0 {
		return nil, nil, 0, 0, 0, rc
	}
	yn := w * h
	uvn := w * ((h + 1) / 2)
	return v.y[:yn], v.uv[:uvn], w, w, h, 0
}

// useChroma is false when UV looks like zeros; BT.601 of U=V=0 is green.
func useChroma(uv []byte, pitch, srcW, srcH int) bool {
	return !uvPlaneDead(uv, pitch, srcW, srcH)
}

func letterboxInferRGB(y, uv []byte, pitch, srcW, srcH, dstW, dstH int) []byte {
	if !useChroma(uv, pitch, srcW, srcH) {
		uv = nil
	}
	return nv12LetterboxRGB(y, uv, pitch, srcW, srcH, dstW, dstH)
}

func (v *vplDecoder) RGBFit(au []byte, rgbOut []byte, maxW, maxH int) (outW, outH int, rc int) {
	if v == nil || v.dec == nil || len(au) == 0 || len(rgbOut) == 0 {
		return 0, 0, -1
	}
	var cW, cH C.int
	rc = int(C.vpl_decoder_rgb_fit(v.dec,
		(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
		(*C.uint8_t)(unsafe.Pointer(&rgbOut[0])), C.int(len(rgbOut)),
		C.int(maxW), C.int(maxH), &cW, &cH))
	return int(cW), int(cH), rc
}

func (v *vplDecoder) Close() {
	if v == nil || v.dec == nil {
		return
	}
	C.vpl_decoder_free(v.dec)
	v.dec = nil
	v.y = nil
	v.uv = nil
}

func uvPlaneDead(uv []byte, pitch, srcW, srcH int) bool {
	var uvp *C.uint8_t
	if len(uv) > 0 {
		uvp = (*C.uint8_t)(unsafe.Pointer(&uv[0]))
	}
	return C.vpl_uv_plane_dead(uvp, C.int(pitch), C.int(srcW), C.int(srcH)) != 0
}

func nv12LetterboxRGB(y, uv []byte, pitch, srcW, srcH, dstW, dstH int) []byte {
	rgb := make([]byte, dstW*dstH*3)
	var uvp *C.uint8_t
	if len(uv) > 0 {
		uvp = (*C.uint8_t)(unsafe.Pointer(&uv[0]))
	}
	C.vpl_nv12_letterbox_rgb(
		(*C.uint8_t)(unsafe.Pointer(&y[0])),
		uvp,
		C.int(pitch), C.int(srcW), C.int(srcH),
		(*C.uint8_t)(unsafe.Pointer(&rgb[0])),
		C.int(dstW), C.int(dstH),
	)
	return rgb
}
