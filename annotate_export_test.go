package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportMultiBoxStill(t *testing.T) {
	dir := os.Getenv("SCRATCH_ALERT")
	if dir == "" {
		t.Skip("SCRATCH_ALERT not set")
	}
	const w, h = 320, 240
	rgb := make([]byte, w*h*3)
	for i := 0; i < len(rgb); i += 3 {
		rgb[i], rgb[i+1], rgb[i+2] = 30, 30, 40
	}
	dets := []DetBox{
		{Class: "person", Score: 0.92, X1: 40, Y1: 30, X2: 120, Y2: 200, ClassID: 0},
		{Class: "car", Score: 0.81, X1: 150, Y1: 80, X2: 300, Y2: 220, ClassID: 2},
	}
	jpg, err := AnnotateRGB(rgb, w, h, dets, 90)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "multi_boxes.jpg")
	if err := os.WriteFile(path, jpg, 0o644); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "meta.json")
	// minimal meta
	_ = os.WriteFile(meta, []byte(`{"dets":2,"classes":["person","car"]}`), 0o644)
	t.Log("wrote", path, len(jpg))
}
