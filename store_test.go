package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSelectPruneCandidates_AgeAndSize(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	paths := []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg"}
	mods := []time.Time{
		now.Add(-48 * time.Hour),
		now.Add(-2 * time.Hour),
		now.Add(-1 * time.Hour),
		now.Add(-30 * time.Minute),
	}
	sizes := []int64{100, 100, 100, 100}

	// Age 1 day → drop a
	del := SelectPruneCandidates(now, 24*time.Hour, 0, paths, mods, sizes)
	if len(del) != 1 || del[0] != "a.jpg" {
		t.Fatalf("age prune: %v", del)
	}

	// Size cap 250 bytes among all (no age): oldest first until under cap
	del = SelectPruneCandidates(now, 0, 250, paths, mods, sizes)
	// total 400 → remove oldest until <=250 → remove a and b (200 left) wait 400-100-100=200
	if len(del) < 2 {
		t.Fatalf("size prune expected >=2, got %v", del)
	}
	if del[0] != "a.jpg" {
		t.Fatalf("should remove oldest first: %v", del)
	}
}

func TestEventStore_SaveMotionAndPrune(t *testing.T) {
	root := t.TempDir()
	s, err := newEventStore(root, 1, 1) // 1 day, 1 MB
	if err != nil {
		t.Fatal(err)
	}
	ev := DetectEvent{
		Cam:      "testcam",
		At:       time.Now(),
		Label:    "fake",
		SAD:      -1,
		Segments: [][]byte{fakeHEVCKeyframe()},
	}
	rec, err := s.SaveMotion(ev, []byte{0xff, 0xd8, 0xff, 0xd9}) // minimal jpeg-ish
	if err != nil {
		t.Fatal(err)
	}
	if rec.Type != "motion" || rec.ClipRel == "" {
		t.Fatalf("bad rec: %+v", rec)
	}
	abs, err := s.AbsMedia(rec.ClipRel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatal(err)
	}
	list := s.List(10, "motion")
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}

	// Create an old dummy file and prune by age
	oldDir := filepath.Join(root, "motion", "20000101")
	_ = os.MkdirAll(oldDir, 0o755)
	oldPath := filepath.Join(oldDir, "old.h265")
	_ = os.WriteFile(oldPath, make([]byte, 100), 0o644)
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = os.Chtimes(oldPath, past, past)

	n, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("expected prune removed >=1, got %d", n)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old file should be gone")
	}
	// recent clip still there
	if _, err := os.Stat(abs); err != nil {
		t.Fatal("recent clip removed incorrectly")
	}
}

// Minimal annex-B-ish payload so buildClipBitstream returns non-empty.
func fakeHEVCKeyframe() []byte {
	// start code + fake IDR nal type 19 ((19<<1)=38)
	return []byte{0, 0, 0, 1, 38, 0, 1, 2, 3, 4, 5, 6, 7, 8}
}

func TestEventStore_SaveAlertMultiBox(t *testing.T) {
	root := t.TempDir()
	s, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	const w, h = 64, 64
	rgb := make([]byte, w*h*3)
	dets := []DetBox{
		{Class: "person", Score: 0.9, X1: 4, Y1: 4, X2: 20, Y2: 30, ClassID: 0},
		{Class: "car", Score: 0.8, X1: 30, Y1: 10, X2: 60, Y2: 50, ClassID: 2},
	}
	jpg, err := AnnotateRGB(rgb, w, h, dets, 90)
	if err != nil {
		t.Fatal(err)
	}
	ev := DetectEvent{Cam: "c1", At: time.Now(), Label: "motion", SAD: 12}
	rec, err := s.SaveAlert(ev, jpg, dets, "person", 0.9, "motion/x.h265", "motion/x.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Type != "alert" || len(rec.Dets) != 2 {
		t.Fatalf("want 2 dets: %+v", rec)
	}
	list := s.List(10, "alert")
	if len(list) != 1 || list[0].Dets[1].Class != "car" {
		t.Fatalf("list: %+v", list)
	}
	abs, _ := s.AbsMedia(rec.StillRel)
	if st, err := os.Stat(abs); err != nil || st.Size() < 50 {
		t.Fatalf("still missing or tiny")
	}
}
