//go:build !nvidia_nvdec && !darwin

package main

import "testing"

func pix(rgb []byte, w, x, y int) (r, g, b byte) {
	i := (y*w + x) * 3
	return rgb[i], rgb[i+1], rgb[i+2]
}

func TestNV12LetterboxYExpand(t *testing.T) {
	// 2×2 studio black / white, 1:1 into 2×2. uv=nil → gray with Y expanded.
	black := []byte{16, 16, 16, 16}
	rgb := nv12LetterboxRGB(black, nil, 2, 2, 2, 2, 2)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			r, g, b := pix(rgb, 2, x, y)
			if r != 0 || g != 0 || b != 0 {
				t.Fatalf("Y=16 → want RGB 0,0,0 at %d,%d got %d,%d,%d", x, y, r, g, b)
			}
		}
	}

	white := []byte{235, 235, 235, 235}
	rgb = nv12LetterboxRGB(white, nil, 2, 2, 2, 2, 2)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			r, g, b := pix(rgb, 2, x, y)
			if r != 255 || g != 255 || b != 255 {
				t.Fatalf("Y=235 → want RGB 255,255,255 at %d,%d got %d,%d,%d", x, y, r, g, b)
			}
		}
	}
}

func TestNV12LetterboxPad114(t *testing.T) {
	// 4×2 → 4×4: pad_y=1, pad 114.
	y := []byte{
		235, 235, 235, 235,
		235, 235, 235, 235,
	}
	rgb := nv12LetterboxRGB(y, nil, 4, 4, 2, 4, 4)
	for x := 0; x < 4; x++ {
		r, g, b := pix(rgb, 4, x, 0)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("top pad want 114 got %d,%d,%d at x=%d", r, g, b, x)
		}
		r, g, b = pix(rgb, 4, x, 3)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("bottom pad want 114 got %d,%d,%d at x=%d", r, g, b, x)
		}
		r, g, b = pix(rgb, 4, x, 1)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content row1 want 255 got %d,%d,%d at x=%d", r, g, b, x)
		}
		r, g, b = pix(rgb, 4, x, 2)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content row2 want 255 got %d,%d,%d at x=%d", r, g, b, x)
		}
	}

	// 2×4 → 4×4: pad_x=1, pad 114.
	yp := []byte{
		235, 235,
		235, 235,
		235, 235,
		235, 235,
	}
	rgb = nv12LetterboxRGB(yp, nil, 2, 2, 4, 4, 4)
	for row := 0; row < 4; row++ {
		r, g, b := pix(rgb, 4, 0, row)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("left pad want 114 got %d,%d,%d at y=%d", r, g, b, row)
		}
		r, g, b = pix(rgb, 4, 3, row)
		if r != 114 || g != 114 || b != 114 {
			t.Fatalf("right pad want 114 got %d,%d,%d at y=%d", r, g, b, row)
		}
		r, g, b = pix(rgb, 4, 1, row)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content want 255 got %d,%d,%d at 1,%d", r, g, b, row)
		}
		r, g, b = pix(rgb, 4, 2, row)
		if r != 255 || g != 255 || b != 255 {
			t.Fatalf("content want 255 got %d,%d,%d at 2,%d", r, g, b, row)
		}
	}
}

func TestNV12LetterboxDownscale2x2(t *testing.T) {
	// 4×4 → 2×2; horizontal 16/235 pairs average to Y=125 → RGB 127.
	y := make([]byte, 16)
	for i := range y {
		if i%2 == 0 {
			y[i] = 16
		} else {
			y[i] = 235
		}
	}
	rgb := nv12LetterboxRGB(y, nil, 4, 4, 4, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != 127 || g != 127 || b != 127 {
				t.Fatalf("2×2 avg want 127 got %d,%d,%d at %d,%d", r, g, b, px, py)
			}
		}
	}
}

func TestNV12LetterboxColour(t *testing.T) {
	// BT.601 limited red: Y=81 U=90 V=240 → R=255 G=0 B=0.
	y := []byte{81, 81, 81, 81}
	uv := []byte{90, 240, 90, 240}
	rgb := nv12LetterboxRGB(y, uv, 2, 2, 2, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != 255 || g != 0 || b != 0 {
				t.Fatalf("red want 255,0,0 at %d,%d got %d,%d,%d", px, py, r, g, b)
			}
		}
	}

	y4 := make([]byte, 16)
	for i := range y4 {
		y4[i] = 81
	}
	uv4 := []byte{90, 240, 90, 240, 90, 240, 90, 240}
	rgb = nv12LetterboxRGB(y4, uv4, 4, 4, 4, 2, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 2; px++ {
			r, g, b := pix(rgb, 2, px, py)
			if r != 255 || g != 0 || b != 0 {
				t.Fatalf("downscale red want 255,0,0 at %d,%d got %d,%d,%d", px, py, r, g, b)
			}
		}
	}
}

func TestNV12LetterboxPitchAndOdd(t *testing.T) {
	// pitch=8, srcW=4: padding 0xFF must not leak into RGB.
	y := make([]byte, 8*2)
	for i := range y {
		y[i] = 255
	}
	for row := 0; row < 2; row++ {
		for x := 0; x < 4; x++ {
			y[row*8+x] = 16
		}
	}
	rgb := nv12LetterboxRGB(y, nil, 8, 4, 2, 4, 2)
	for py := 0; py < 2; py++ {
		for px := 0; px < 4; px++ {
			r, g, b := pix(rgb, 4, px, py)
			if r != 0 || g != 0 || b != 0 {
				t.Fatalf("padded pitch want 0 got %d,%d,%d at %d,%d", r, g, b, px, py)
			}
		}
	}

	// odd 3×3 1:1, Y=235 → 255, no read past pitch=3.
	odd := []byte{
		235, 235, 235,
		235, 235, 235,
		235, 235, 235,
	}
	rgb = nv12LetterboxRGB(odd, nil, 3, 3, 3, 3, 3)
	for py := 0; py < 3; py++ {
		for px := 0; px < 3; px++ {
			r, g, b := pix(rgb, 3, px, py)
			if r != 255 || g != 255 || b != 255 {
				t.Fatalf("odd want 255 got %d,%d,%d at %d,%d", r, g, b, px, py)
			}
		}
	}
}

func TestUVPlaneDead(t *testing.T) {
	if !uvPlaneDead(nil, 8, 8, 8) {
		t.Fatal("nil should be dead")
	}
	if !uvPlaneDead([]byte{1, 2}, 1, 8, 8) {
		t.Fatal("tiny pitch should be dead")
	}
	if !uvPlaneDead([]byte{1, 2, 3, 4}, 8, 1, 8) {
		t.Fatal("tiny width should be dead")
	}
	if !uvPlaneDead([]byte{1, 2, 3, 4}, 8, 8, 1) {
		t.Fatal("tiny height should be dead")
	}

	// 8×8 luma → 8×4 UV. All-zero crop is dead even if stride padding is live.
	uv := make([]byte, 16*4)
	for i := range uv {
		uv[i] = 0
	}
	for row := 0; row < 4; row++ {
		for col := 8; col < 16; col++ {
			uv[row*16+col] = 0xFF
		}
	}
	if !uvPlaneDead(uv, 16, 8, 8) {
		t.Fatal("zeros in crop should be dead (padding must not count)")
	}

	uv[0] = 90
	uv[1] = 240
	if uvPlaneDead(uv, 16, 8, 8) {
		t.Fatal("nonzero crop sample should be live")
	}
}
