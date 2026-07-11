package main

/*
#cgo LDFLAGS: -L/usr/lib -L/usr/lib/x86_64-linux-gnu -Wl,-rpath,/usr/lib:/usr/lib/x86_64-linux-gnu -lopenvino_c -lopenvino -lopenvino_onnx_frontend -Wl,-Bdynamic -ltbb

#include <stdlib.h>
#include "ov_infer.h"
*/
import "C"

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const inferSize = 640

type detBox struct {
	x1, y1, x2, y2 float64
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func letterboxRGB(src []byte, srcW, srcH, dstW, dstH int) []byte {
	dst := make([]byte, dstW*dstH*3)
	for i := 0; i < len(dst); i += 3 {
		dst[i] = 114
		dst[i+1] = 114
		dst[i+2] = 114
	}

	scaledW := dstW
	scaledH := srcH * dstW / srcW
	if scaledH > dstH {
		scaledH = dstH
		scaledW = srcW * dstH / srcH
	}
	padX := (dstW - scaledW) / 2
	padY := (dstH - scaledH) / 2

	for dy := 0; dy < scaledH; dy++ {
		sy := dy * srcH / scaledH
		for dx := 0; dx < scaledW; dx++ {
			sx := dx * srcW / scaledW
			si := (sy*srcW + sx) * 3
			di := ((padY+dy)*dstW + (padX + dx)) * 3
			dst[di] = src[si]
			dst[di+1] = src[si+1]
			dst[di+2] = src[si+2]
		}
	}
	return dst
}

func setPixel(rgb []byte, w, h, x, y int, r, g, b uint8) {
	if x < 0 || y < 0 || x >= w || y >= h {
		return
	}
	i := (y*w + x) * 3
	rgb[i] = r
	rgb[i+1] = g
	rgb[i+2] = b
}

func drawBox(rgb []byte, w, h int, box detBox, r, g, b uint8, thick int) {
	x1 := clamp(int(box.x1), 0, w-1)
	y1 := clamp(int(box.y1), 0, h-1)
	x2 := clamp(int(box.x2), 0, w-1)
	y2 := clamp(int(box.y2), 0, h-1)
	if x2 < x1 {
		x1, x2 = x2, x1
	}
	if y2 < y1 {
		y1, y2 = y2, y1
	}
	for t := 0; t < thick; t++ {
		for x := x1; x <= x2; x++ {
			setPixel(rgb, w, h, x, y1+t, r, g, b)
			setPixel(rgb, w, h, x, y2-t, r, g, b)
		}
		for y := y1; y <= y2; y++ {
			setPixel(rgb, w, h, x1+t, y, r, g, b)
			setPixel(rgb, w, h, x2-t, y, r, g, b)
		}
	}
}

func boxColor(name string) (uint8, uint8, uint8) {
	switch name {
	case "car":
		return 255, 64, 0
	case "person":
		return 0, 255, 0
	case "cat":
		return 0, 128, 255
	default:
		return 255, 255, 0
	}
}

func rgbToImage(rgb []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			img.SetRGBA(x, y, color.RGBA{R: rgb[i], G: rgb[i+1], B: rgb[i+2], A: 255})
		}
	}
	return img
}

func imageToRGB(img *image.RGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rgb := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*w + x) * 3
			rgb[i] = uint8(r >> 8)
			rgb[i+1] = uint8(g >> 8)
			rgb[i+2] = uint8(bl >> 8)
		}
	}
	return rgb
}

func drawLabel(img *image.RGBA, x, y int, text string, fg, bg color.RGBA) {
	pad := 2
	width := len(text)*7 + pad*2
	height := 13 + pad*2
	rect := image.Rect(x, y, x+width, y+height)
	draw.Draw(img, rect, &image.Uniform{C: bg}, image.Point{}, draw.Src)
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(fg),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x+pad, y+pad+12),
	}
	d.DrawString(text)
}

func saveRGBJPEG(path string, rgb []byte, w, h int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	img := rgbToImage(rgb, w, h)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
}

func main() {
	out := flag.String("o", "", "write annotated JPEG here")
	conf := flag.Float64("conf", 0.10, "confidence threshold for pass box")
	inW := flag.Int("w", 0, "input frame width (0 = infer from size or 640)")
	inH := flag.Int("h", 0, "input frame height (0 = infer from size or 640)")
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s [-o out.jpg] [-w 1920 -h 1088] <model.onnx> <frame.rgb> [device]\n", os.Args[0])
		os.Exit(1)
	}
	model := args[0]
	path := args[1]
	device := "CPU"
	if len(args) > 2 {
		device = args[2]
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}

	srcW, srcH := *inW, *inH
	if srcW <= 0 || srcH <= 0 {
		if len(raw) == inferSize*inferSize*3 {
			srcW, srcH = inferSize, inferSize
		} else if len(raw)%(1920*3) == 0 {
			srcW, srcH = 1920, len(raw)/(1920*3)
		} else {
			fmt.Fprintf(os.Stderr, "unknown frame size %d bytes; set -w and -h\n", len(raw))
			os.Exit(1)
		}
	}
	want := srcW * srcH * 3
	if len(raw) != want {
		fmt.Fprintf(os.Stderr, "want %d bytes rgb24 %dx%d, got %d\n", want, srcW, srcH, len(raw))
		os.Exit(1)
	}

	rgb := letterboxRGB(raw, srcW, srcH, inferSize, inferSize)
	fmt.Printf("letterbox %dx%d -> %dx%d\n", srcW, srcH, inferSize, inferSize)

	cModel := C.CString(model)
	cDevice := C.CString(device)
	defer C.free(unsafe.Pointer(cModel))
	defer C.free(unsafe.Pointer(cDevice))

	var inf *C.OVInfer
	if rc := C.ov_infer_new(&inf, cModel, cDevice, C.int(inferSize), C.int(inferSize)); rc != 0 {
		fmt.Fprintf(os.Stderr, "ov_infer_new failed: %d\n", int(rc))
		os.Exit(1)
	}
	defer C.ov_infer_free(inf)

	var person, car, cat C.float
	if rc := C.ov_infer_class_scores(inf,
		(*C.uint8_t)(unsafe.Pointer(&rgb[0])),
		&person, &car, &cat); rc != 0 {
		fmt.Fprintf(os.Stderr, "ov_infer_class_scores failed: %d\n", int(rc))
		os.Exit(1)
	}

	fmt.Printf("device=%s person=%.3f car=%.3f cat=%.3f\n",
		C.GoString(C.ov_infer_device(inf)), float64(person), float64(car), float64(cat))

	type det struct {
		id    int
		name  string
		score float64
		box   detBox
	}
	var dets []det
	var score, x1, y1, x2, y2 C.float
	for _, cls := range []struct {
		id   int
		name string
	}{
		{0, "person"},
		{2, "car"},
		{15, "cat"},
	} {
		if rc := C.ov_infer_best_for_class(inf,
			(*C.uint8_t)(unsafe.Pointer(&rgb[0])),
			C.int(cls.id), &score, &x1, &y1, &x2, &y2); rc == 0 && score > 0.01 {
			d := det{
				id:    cls.id,
				name:  cls.name,
				score: float64(score),
				box:   detBox{float64(x1), float64(y1), float64(x2), float64(y2)},
			}
			dets = append(dets, d)
			fmt.Printf("  %s box score=%.3f (%.0f,%.0f)-(%.0f,%.0f)\n",
				cls.name, d.score, d.box.x1, d.box.y1, d.box.x2, d.box.y2)
		}
	}

	var cMs C.float
	var cPass C.int
	var cScore C.float
	var cClass C.int
	var cX1, cY1, cX2, cY2 C.float
	if rc := C.ov_infer_run(inf,
		(*C.uint8_t)(unsafe.Pointer(&rgb[0])),
		C.float(*conf),
		&cMs, &cPass, &cScore, &cClass,
		&cX1, &cY1, &cX2, &cY2); rc != 0 {
		fmt.Fprintf(os.Stderr, "ov_infer_run conf=%.2f failed: %d\n", *conf, int(rc))
	} else {
		fmt.Printf("conf=%.2f pass=%v class=%d score=%.3f infer=%.0fms\n",
			*conf, cPass != 0, int(cClass), float64(cScore), float64(cMs))
	}

	if *out != "" {
		img := rgbToImage(rgb, inferSize, inferSize)
		for _, d := range dets {
			r, g, b := boxColor(d.name)
			thick := 2
			if d.score >= *conf {
				thick = 4
			}
			outRGB := imageToRGB(img)
			drawBox(outRGB, inferSize, inferSize, d.box, r, g, b, thick)
			img = rgbToImage(outRGB, inferSize, inferSize)

			lx := clamp(int(d.box.x1), 0, inferSize-1)
			ly := clamp(int(d.box.y1)-16, 0, inferSize-1)
			label := fmt.Sprintf("%s %.2f", d.name, d.score)
			br, bg, bb := boxColor(d.name)
			drawLabel(img, lx, ly, label, color.RGBA{255, 255, 255, 255}, color.RGBA{br, bg, bb, 255})
		}
		if err := saveRGBJPEG(*out, imageToRGB(img), inferSize, inferSize); err != nil {
			fmt.Fprintf(os.Stderr, "jpeg save: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("saved %s\n", *out)
	}
}