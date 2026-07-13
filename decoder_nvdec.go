//go:build nvidia_nvdec && !darwin

package main

/*
#cgo CFLAGS: -I${SRCDIR}/third_party
#cgo LDFLAGS: -ldl

#include <stdlib.h>
#include "nvdec_decode.h"
*/
import "C"

import "unsafe"

// NVIDIA NVDEC backend (Linux + CUDA / WSL).
//
//	go build -tags nvidia_nvdec .
//
// See docs/UNIVERSAL_CORE.md.

type nvdecDecoder struct {
	dec *C.NVDECDecoder
}

func newFrameDecoder(codec int) (FrameDecoder, int) {
	var d *C.NVDECDecoder
	rc := int(C.nvdec_decoder_new(&d, C.int(codec)))
	if rc != 0 || d == nil {
		return nil, rc
	}
	return &nvdecDecoder{dec: d}, 0
}

func (v *nvdecDecoder) Thumb(au []byte, yOut []byte) int {
	if v == nil || v.dec == nil || len(au) == 0 || len(yOut) == 0 {
		return -1
	}
	return int(C.nvdec_decoder_thumb(v.dec,
		(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
		(*C.uint8_t)(unsafe.Pointer(&yOut[0])), C.int(len(yOut))))
}

func (v *nvdecDecoder) RGB(au []byte, rgbOut []byte, outW, outH int) int {
	if v == nil || v.dec == nil || len(au) == 0 || len(rgbOut) == 0 {
		return -1
	}
	return int(C.nvdec_decoder_rgb(v.dec,
		(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
		(*C.uint8_t)(unsafe.Pointer(&rgbOut[0])), C.int(len(rgbOut)),
		C.int(outW), C.int(outH)))
}

func (v *nvdecDecoder) RGBFit(au []byte, rgbOut []byte, maxW, maxH int) (outW, outH int, rc int) {
	if v == nil || v.dec == nil || len(au) == 0 || len(rgbOut) == 0 {
		return 0, 0, -1
	}
	var cW, cH C.int
	rc = int(C.nvdec_decoder_rgb_fit(v.dec,
		(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
		(*C.uint8_t)(unsafe.Pointer(&rgbOut[0])), C.int(len(rgbOut)),
		C.int(maxW), C.int(maxH), &cW, &cH))
	return int(cW), int(cH), rc
}

func (v *nvdecDecoder) Close() {
	if v == nil || v.dec == nil {
		return
	}
	C.nvdec_decoder_free(v.dec)
	v.dec = nil
}
