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
	return int(C.vpl_decoder_rgb(v.dec,
		(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
		(*C.uint8_t)(unsafe.Pointer(&rgbOut[0])), C.int(len(rgbOut)),
		C.int(outW), C.int(outH)))
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
}
