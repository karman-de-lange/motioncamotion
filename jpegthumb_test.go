package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureJPEGThumb_shrinks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "full.jpg")
	im := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			im.Set(x, y, color.RGBA{80, 90, 100, 255})
		}
	}
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, im, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	dst, err := ensureJPEGThumb(src, 160)
	if err != nil {
		t.Fatal(err)
	}
	r, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds().Dx() != 160 || got.Bounds().Dy() != 90 {
		t.Fatalf("size %dx%d want 160x90", got.Bounds().Dx(), got.Bounds().Dy())
	}
	dst2, err := ensureJPEGThumb(src, 160)
	if err != nil || dst2 != dst {
		t.Fatalf("cache %s %v", dst2, err)
	}
}

func TestEnsureJPEGThumb_concurrentMiss(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "full.jpg")
	im := image.NewRGBA(image.Rect(0, 0, 320, 180))
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, im, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	errc := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := ensureJPEGThumb(src, jpegListThumbW)
			errc <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
}
