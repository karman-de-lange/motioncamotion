package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
)

// DetBox is one detection in model-input pixel space (same size as rgb).
type DetBox struct {
	Class   string  `json:"class"`
	Score   float64 `json:"score"`
	X1      float64 `json:"x1"`
	Y1      float64 `json:"y1"`
	X2      float64 `json:"x2"`
	Y2      float64 `json:"y2"`
	ClassID int     `json:"class_id"`
}

func mapDetsLetterboxToStill(dets []DetBox, inferW, inferH, stillW, stillH int) []DetBox {
	if inferW <= 0 || inferH <= 0 || stillW <= 0 || stillH <= 0 || len(dets) == 0 {
		return dets
	}
	scaledW := inferW
	scaledH := stillH * inferW / stillW
	if scaledH > inferH {
		scaledH = inferH
		scaledW = stillW * inferH / stillH
	}
	padX := float64(inferW-scaledW) / 2
	padY := float64(inferH-scaledH) / 2
	cw := float64(scaledW)
	ch := float64(scaledH)
	if cw < 1 {
		cw = 1
	}
	if ch < 1 {
		ch = 1
	}
	out := make([]DetBox, len(dets))
	for i, d := range dets {
		d.X1 = (d.X1 - padX) / cw * float64(stillW)
		d.X2 = (d.X2 - padX) / cw * float64(stillW)
		d.Y1 = (d.Y1 - padY) / ch * float64(stillH)
		d.Y2 = (d.Y2 - padY) / ch * float64(stillH)
		out[i] = d
	}
	return out
}

func mapThumbRectToStill(x, y, w, h, stillW, stillH int) (x1, y1, x2, y2 float64) {
	if stillW <= 0 || stillH <= 0 || w <= 0 || h <= 0 {
		return 0, 0, 0, 0
	}
	x1 = float64(x) / float64(thumbW) * float64(stillW)
	y1 = float64(y) / float64(thumbH) * float64(stillH)
	x2 = float64(x+w) / float64(thumbW) * float64(stillW)
	y2 = float64(y+h) / float64(thumbH) * float64(stillH)
	return x1, y1, x2, y2
}

func motionBlobDet(ev DetectEvent, stillW, stillH int) []DetBox {
	if ev.BlobW <= 0 || ev.BlobH <= 0 {
		return nil
	}
	x1, y1, x2, y2 := mapThumbRectToStill(ev.BlobX, ev.BlobY, ev.BlobW, ev.BlobH, stillW, stillH)
	return []DetBox{{Class: "motion", Score: ev.SAD / 100, X1: x1, Y1: y1, X2: x2, Y2: y2}}
}

// AnnotateRGB draws axis-aligned boxes + labels onto an RGB24 buffer and returns JPEG.
func AnnotateRGB(rgb []byte, width, height int, dets []DetBox, quality int) ([]byte, error) {
	if width <= 0 || height <= 0 || len(rgb) < width*height*3 {
		return nil, fmt.Errorf("bad rgb")
	}
	if quality <= 0 {
		quality = 85
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 3
			o := img.PixOffset(x, y)
			img.Pix[o+0] = rgb[i+0]
			img.Pix[o+1] = rgb[i+1]
			img.Pix[o+2] = rgb[i+2]
			img.Pix[o+3] = 255
		}
	}
	palette := []color.RGBA{
		{0, 255, 80, 255},
		{255, 80, 80, 255},
		{80, 160, 255, 255},
		{255, 220, 40, 255},
	}
	motionCol := color.RGBA{255, 210, 40, 255}
	for i, d := range dets {
		col := palette[i%len(palette)]
		if d.Class == "motion" {
			col = motionCol
		}
		x1 := clampInt(int(d.X1), 0, width-1)
		y1 := clampInt(int(d.Y1), 0, height-1)
		x2 := clampInt(int(d.X2), 0, width-1)
		y2 := clampInt(int(d.Y2), 0, height-1)
		if x2 < x1 {
			x1, x2 = x2, x1
		}
		if y2 < y1 {
			y1, y2 = y2, y1
		}
		drawRect(img, x1, y1, x2, y2, col, 2)
		label := fmt.Sprintf("%s %.2f", d.Class, d.Score)
		drawLabel(img, x1, maxInt(0, y1-12), label, col)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func drawRect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA, thickness int) {
	for t := 0; t < thickness; t++ {
		hline(img, x1, x2, y1+t, c)
		hline(img, x1, x2, y2-t, c)
		vline(img, y1, y2, x1+t, c)
		vline(img, y1, y2, x2-t, c)
	}
}

func hline(img *image.RGBA, x1, x2, y int, c color.RGBA) {
	if y < 0 || y >= img.Bounds().Dy() {
		return
	}
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		if x >= 0 && x < img.Bounds().Dx() {
			img.SetRGBA(x, y, c)
		}
	}
}

func vline(img *image.RGBA, y1, y2, x int, c color.RGBA) {
	if x < 0 || x >= img.Bounds().Dx() {
		return
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		if y >= 0 && y < img.Bounds().Dy() {
			img.SetRGBA(x, y, c)
		}
	}
}

// Minimal 5x7 bitmap font for labels (ASCII subset).
func drawLabel(img *image.RGBA, x, y int, text string, c color.RGBA) {
	// Background bar
	w := len(text)*6 + 4
	h := 12
	bg := image.NewUniform(color.RGBA{0, 0, 0, 180})
	draw.Draw(img, image.Rect(x, y, x+w, y+h), bg, image.Point{}, draw.Over)
	cx := x + 2
	for _, ch := range text {
		drawChar(img, cx, y+2, ch, c)
		cx += 6
	}
}

func drawChar(img *image.RGBA, x, y int, ch rune, c color.RGBA) {
	// Very small font: use filled pixels for digits/letters via simple patterns.
	// Fallback: 3x5 block for any char so labels are visible even if coarse.
	glyph := fontGlyph(ch)
	for row := 0; row < 7; row++ {
		bits := glyph[row]
		for col := 0; col < 5; col++ {
			if bits&(1<<uint(4-col)) != 0 {
				img.SetRGBA(x+col, y+row, c)
			}
		}
	}
}

func fontGlyph(ch rune) [7]byte {
	// 5-bit rows, MSB left. Covers digits, a-z, space, dot.
	switch ch {
	case '0':
		return [7]byte{0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E}
	case '1':
		return [7]byte{0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E}
	case '2':
		return [7]byte{0x0E, 0x11, 0x01, 0x06, 0x08, 0x10, 0x1F}
	case '3':
		return [7]byte{0x0E, 0x11, 0x01, 0x06, 0x01, 0x11, 0x0E}
	case '4':
		return [7]byte{0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02}
	case '5':
		return [7]byte{0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E}
	case '6':
		return [7]byte{0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E}
	case '7':
		return [7]byte{0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08}
	case '8':
		return [7]byte{0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E}
	case '9':
		return [7]byte{0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C}
	case '.':
		return [7]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x0C, 0x0C}
	case ' ':
		return [7]byte{}
	case 'p':
		return [7]byte{0x00, 0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10}
	case 'e':
		return [7]byte{0x00, 0x0E, 0x11, 0x1F, 0x10, 0x11, 0x0E}
	case 'r':
		return [7]byte{0x00, 0x16, 0x19, 0x10, 0x10, 0x10, 0x10}
	case 's':
		return [7]byte{0x00, 0x0F, 0x10, 0x0E, 0x01, 0x11, 0x0E}
	case 'o':
		return [7]byte{0x00, 0x0E, 0x11, 0x11, 0x11, 0x11, 0x0E}
	case 'n':
		return [7]byte{0x00, 0x1E, 0x11, 0x11, 0x11, 0x11, 0x11}
	case 'c':
		return [7]byte{0x00, 0x0E, 0x11, 0x10, 0x10, 0x11, 0x0E}
	case 'a':
		return [7]byte{0x00, 0x0E, 0x01, 0x0F, 0x11, 0x11, 0x0F}
	case 't':
		return [7]byte{0x08, 0x08, 0x1E, 0x08, 0x08, 0x09, 0x06}
	default:
		// Generic block for unknown glyphs.
		return [7]byte{0x1F, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1F}
	}
}
