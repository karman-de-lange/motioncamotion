package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/image/draw"
)

const jpegListThumbW = 192

var jpegThumbGate = make(chan struct{}, 2)

func jpegThumbPath(src string, maxW int) string {
	return src + ".t" + strconv.Itoa(maxW) + ".jpg"
}

func jpegThumbFresh(dst string, srcMod time.Time) bool {
	di, err := os.Stat(dst)
	return err == nil && !di.ModTime().Before(srcMod) && di.Size() > 0
}

func ensureJPEGThumb(src string, maxW int) (string, error) {
	if maxW < 32 {
		maxW = 32
	}
	if maxW > 640 {
		maxW = 640
	}
	dst := jpegThumbPath(src, maxW)
	si, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if jpegThumbFresh(dst, si.ModTime()) {
		return dst, nil
	}
	jpegThumbGate <- struct{}{}
	defer func() { <-jpegThumbGate }()
	if jpegThumbFresh(dst, si.ModTime()) {
		return dst, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	im, err := jpeg.Decode(f)
	f.Close()
	if err != nil {
		return "", err
	}
	b := im.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return "", fmt.Errorf("empty jpeg")
	}
	nw := maxW
	if sw < nw {
		nw = sw
	}
	nh := sh * nw / sw
	if nh < 1 {
		nh = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.ApproxBiLinear.Scale(out, out.Bounds(), im, b, draw.Src, nil)
	w, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		return "", err
	}
	tmp := w.Name()
	err = jpeg.Encode(w, out, &jpeg.Options{Quality: 70})
	w.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dst, nil
}
