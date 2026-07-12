package main

/*
#cgo CFLAGS: -I/usr/include/vpl
#cgo LDFLAGS: -lmfx-gen -lstdc++

#include <stdlib.h>
#include "vpl_decode.h"
*/
import "C"

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	previewMaxW = 640
	previewMaxH = 640
	jpegQuality = 70
)

// Serialize on-demand looksee decodes (one VPL session at a time).
var previewDecMu sync.Mutex

func encodePreviewJPEG(rgb []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 || len(rgb) < width*height*3 {
		return nil, fmt.Errorf("no frame")
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 3
			o := (y*width + x) * 4
			img.Pix[o+0] = rgb[i+0]
			img.Pix[o+1] = rgb[i+1]
			img.Pix[o+2] = rgb[i+2]
			img.Pix[o+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// annexBHasNALType scans annex-B for H.264 nal_unit_type (e.g. 5=IDR, 7=SPS).
func annexBHasNALType(b []byte, nalType byte) bool {
	for i := 0; i+4 < len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 {
			continue
		}
		off := 0
		if b[i+2] == 1 {
			off = i + 3
		} else if i+3 < len(b) && b[i+2] == 0 && b[i+3] == 1 {
			off = i + 4
		} else {
			continue
		}
		if off < len(b) && b[off]&0x1F == nalType {
			return true
		}
	}
	return false
}

// prepareLookseeAUs: HEVC ring is used as-is. H.264: start at first IDR in the
// ring (need a keyframe for a fresh decoder) and inject SDP SPS/PPS once only.
func prepareLookseeAUs(aus [][]byte, codec int, sps, pps []byte) [][]byte {
	if len(aus) == 0 {
		return aus
	}
	if codec != int(C.VPL_CODEC_AVC) {
		return aus
	}
	start := -1
	for i, au := range aus {
		if annexBHasNALType(au, 5) {
			start = i
			break
		}
	}
	if start < 0 {
		// No IDR in ring yet — still try from start with SPS/PPS (may fail until GOP).
		start = 0
	}
	out := make([][]byte, len(aus)-start)
	copy(out, aus[start:])
	if len(sps) > 0 && len(out) > 0 && !annexBHasNALType(out[0], 7) {
		head := annexB(sps, pps)
		first := make([]byte, 0, len(head)+len(out[0]))
		first = append(first, head...)
		first = append(first, out[0]...)
		out[0] = first
	}
	return out
}

// Looksee: feed recent sub AUs into a fresh VPL decoder → RGB → JPEG.
// codec selects HEVC vs AVC session; only AVC uses sps/pps injection above.
func decodeAUsToRGBFit(aus [][]byte, maxW, maxH, codec int) (rgb []byte, outW, outH int, err error) {
	if len(aus) == 0 {
		return nil, 0, 0, fmt.Errorf("no sub frame yet")
	}
	previewDecMu.Lock()
	defer previewDecMu.Unlock()

	var dec *C.VPLDecoder
	if rc := C.vpl_decoder_new(&dec, C.int(codec)); rc != 0 {
		return nil, 0, 0, fmt.Errorf("vpl_decoder_new %d", int(rc))
	}
	defer C.vpl_decoder_free(dec)

	buf := make([]byte, maxW*maxH*3)
	var lastRC int
	var cW, cH C.int
	for _, au := range aus {
		if len(au) == 0 {
			continue
		}
		rc := C.vpl_decoder_rgb_fit(dec,
			(*C.uint8_t)(unsafe.Pointer(&au[0])), C.size_t(len(au)),
			(*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)),
			C.int(maxW), C.int(maxH), &cW, &cH)
		lastRC = int(rc)
		if rc == 0 {
			w, h := int(cW), int(cH)
			out := make([]byte, w*h*3)
			copy(out, buf[:w*h*3])
			return out, w, h, nil
		}
	}
	if lastRC == 1 {
		return nil, 0, 0, fmt.Errorf("no frame yet")
	}
	return nil, 0, 0, fmt.Errorf("vpl_decoder_rgb_fit %d", lastRC)
}

func (w *CameraWorker) previewJPEGBytes() ([]byte, error) {
	if jpg := w.cachedPreviewJPEGFresh(2 * time.Second); jpg != nil {
		return jpg, nil
	}

	aus := w.copyLatestSubs()
	if len(aus) == 0 {
		return nil, fmt.Errorf("warming up — wait 1–2s")
	}
	codec := w.SubCodec()
	if codec != int(C.VPL_CODEC_AVC) {
		codec = int(C.VPL_CODEC_HEVC)
	}
	sps, pps := w.SubH264Params()
	aus = prepareLookseeAUs(aus, codec, sps, pps)

	rgb, pw, ph, err := decodeAUsToRGBFit(aus, previewMaxW, previewMaxH, codec)
	if err != nil {
		if stale := w.cachedPreviewJPEG(); stale != nil {
			return stale, nil
		}
		return nil, err
	}
	jpg, err := encodePreviewJPEG(rgb, pw, ph)
	if err != nil {
		return nil, err
	}
	w.storePreviewJPEG(jpg)
	return jpg, nil
}

func runPreviewServer(ctx context.Context, addr string, workers []*CameraWorker, logQ chan<- string) {
	if addr == "" {
		return
	}
	byName := make(map[string]*CameraWorker, len(workers))
	for _, w := range workers {
		byName[w.Name()] = w
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		var b strings.Builder
		b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8">
<title>motioncamotion looksee</title>
<style>
body{font-family:system-ui,sans-serif;background:#111;color:#ddd;margin:12px}
h1{font-size:1.1rem;font-weight:600}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(320px,1fr));gap:12px}
.card{background:#1a1a1a;border:1px solid #333;border-radius:8px;overflow:hidden}
.card h2{font-size:.85rem;margin:0;padding:8px 10px;background:#222;display:flex;justify-content:space-between}
.card h2 .st{color:#888;font-weight:400}
.card img{display:block;width:100%;height:auto;object-fit:contain;background:#000;min-height:80px}
.meta{font-size:.75rem;color:#888;padding:6px 10px}
a{color:#8cf}
</style></head><body>
<h1>motioncamotion looksee</h1>
<p class="meta">Each tile refreshes after load (no full-page reload)</p>
<div class="grid">`)
		for i, w := range workers {
			name := w.Name()
			st := w.Status()
			b.WriteString(`<div class="card"><h2><span>`)
			b.WriteString(name)
			b.WriteString(`</span><span class="st">`)
			b.WriteString(st)
			b.WriteString(`</span></h2>`)
			b.WriteString(fmt.Sprintf(`<img data-cam="%s" data-delay="%d" alt="%s" src="">`, name, i*200, name))
			b.WriteString(`<div class="meta"><a href="/snap/`)
			b.WriteString(name)
			b.WriteString(`.jpg">jpeg</a> · <span data-age="`)
			b.WriteString(name)
			b.WriteString(`">…</span></div></div>`)
		}
		b.WriteString(`</div>
<script>
(function(){
  var OK_MS=2500, ERR_MS=1200;
  function tick(img){
    var cam=img.getAttribute('data-cam'); if(!cam) return;
    var age=document.querySelector('[data-age="'+cam+'"]');
    var probe=new Image();
    probe.onload=function(){ img.src=probe.src; if(age) age.textContent='ok'; setTimeout(function(){tick(img);}, OK_MS); };
    probe.onerror=function(){ if(age) age.textContent='wait…'; setTimeout(function(){tick(img);}, ERR_MS); };
    probe.src='/snap/'+encodeURIComponent(cam)+'.jpg?t='+Date.now();
  }
  document.querySelectorAll('img[data-cam]').forEach(function(img){
    setTimeout(function(){ tick(img); }, parseInt(img.getAttribute('data-delay')||'0',10));
  });
})();
</script></body></html>`)
		_, _ = rw.Write([]byte(b.String()))
	})

	mux.HandleFunc("/snap/", func(rw http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/snap/")
		name = strings.TrimSuffix(name, ".jpg")
		name = strings.TrimSpace(name)
		w := byName[name]
		if w == nil {
			http.NotFound(rw, r)
			return
		}
		jpg, err := w.previewJPEGBytes()
		if err != nil {
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.Header().Set("Retry-After", "1")
			rw.WriteHeader(http.StatusServiceUnavailable)
			_, _ = rw.Write([]byte(err.Error()))
			return
		}
		rw.Header().Set("Content-Type", "image/jpeg")
		rw.Header().Set("Cache-Control", "no-store")
		_, _ = rw.Write(jpg)
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()

	logQ <- fmt.Sprintf("preview http://0.0.0.0%s/", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logQ <- fmt.Sprintf("preview server: %v", err)
	}
}
