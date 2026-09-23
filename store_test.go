package main

import (
	"encoding/json"
	"fmt"
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
	if rec.Type != "motion" || rec.ClipRel == "" || rec.VideoRel != "" {
		t.Fatalf("bad rec: %+v", rec)
	}
	abs, err := s.AbsMedia(rec.ClipRel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatal("SaveMotion must not write clip")
	}
	if err := s.WriteClipAt(rec.ClipRel, ev.Segments); err != nil {
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

func TestEventStore_loadRecentSurvivesReopen(t *testing.T) {
	root := t.TempDir()
	s, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	ev := DetectEvent{Cam: "c1", At: time.Now(), Label: "motion", SAD: 4, Segments: [][]byte{fakeHEVCKeyframe()}}
	mrec, err := s.SaveMotion(ev, []byte{0xff, 0xd8, 0xff, 0xd9})
	if err != nil {
		t.Fatal(err)
	}
	arec, err := s.SaveAlert(ev, []byte{0xff, 0xd8, 0xff, 0xd9}, []DetBox{{Class: "person", Score: 0.9, ClassID: 0}}, "person", 0.9, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{mrec.ClipRel, arec.ClipRel} {
		p, err := s.AbsMedia(rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s clip written by save", rel)
		}
	}
	s2, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s2.List(10, "alert")); n != 1 {
		t.Fatalf("alerts after reopen: %d", n)
	}
	if n := len(s2.List(10, "motion")); n != 1 {
		t.Fatalf("motion after reopen: %d", n)
	}
}

func TestEventStore_pruneSizePrefersMotion(t *testing.T) {
	root := t.TempDir()
	s, err := newEventStore(root, 7, 1) // 1 MiB cap
	if err != nil {
		t.Fatal(err)
	}
	motDir := filepath.Join(root, "motion", "20260101")
	alDir := filepath.Join(root, "alerts", "20260101")
	_ = os.MkdirAll(motDir, 0o755)
	_ = os.MkdirAll(alDir, 0o755)
	mot := filepath.Join(motDir, "old.h265")
	al := filepath.Join(alDir, "keep.jpg")
	if err := os.WriteFile(mot, make([]byte, 700*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(al, make([]byte, 700*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(mot, old, old)
	n, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("expected a removal, got %d", n)
	}
	if _, err := os.Stat(mot); !os.IsNotExist(err) {
		t.Fatal("motion file should be pruned first")
	}
	if _, err := os.Stat(al); err != nil {
		t.Fatal("alert still should survive size prune")
	}
}

func TestEventStore_pruneSizeKeepsAlertMotionClip(t *testing.T) {
	root := t.TempDir()
	day := "20260101"
	motDir := filepath.Join(root, "motion", day)
	alDir := filepath.Join(root, "alerts", day)
	if err := os.MkdirAll(motDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(alDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(motDir, "plain.h265")
	alertClip := filepath.Join(motDir, "alertclip.h265")
	alertStill := filepath.Join(alDir, "keep.jpg")
	if err := os.WriteFile(plain, make([]byte, 700*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alertClip, make([]byte, 700*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alertStill, make([]byte, 50*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(plain, old, old)
	_ = os.Chtimes(alertClip, old.Add(time.Minute), old.Add(time.Minute))
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	writeJSONL(t, root, []EventRecord{
		{ID: "plain", Type: "motion", Cam: "c1", At: at, ClipRel: "motion/20260101/plain.h265"},
		{ID: "al", Type: "alert", Cam: "c1", At: at.Add(time.Minute), ClipRel: "motion/20260101/alertclip.h265", StillRel: "alerts/20260101/keep.jpg"},
	})
	s, err := newEventStore(root, 7, 1) // 1 MiB cap
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("expected a removal, got %d", n)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatal("unreferenced motion clip should be pruned first")
	}
	if _, err := os.Stat(alertClip); err != nil {
		t.Fatal("alert-referenced motion clip should survive size prune")
	}
	if _, err := os.Stat(alertStill); err != nil {
		t.Fatal("alert still should survive size prune")
	}
}

func TestEventStore_pruneKeepsHeldMotionClip(t *testing.T) {
	root := t.TempDir()
	day := "20260101"
	motDir := filepath.Join(root, "motion", day)
	if err := os.MkdirAll(motDir, 0o755); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(motDir, "held.h265")
	plain := filepath.Join(motDir, "plain.h265")
	body := make([]byte, 700*1024)
	if err := os.WriteFile(held, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, body, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(held, old, old)
	_ = os.Chtimes(plain, old.Add(time.Minute), old.Add(time.Minute))
	s, err := newEventStore(root, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	rel := "motion/20260101/held.h265"
	s.HoldClip(rel)
	if _, err := s.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(held); err != nil {
		t.Fatal("held motion clip should survive size prune")
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatal("unheld motion clip should be pruned")
	}
	s.ReleaseClip(rel)
	other := filepath.Join(motDir, "other.h265")
	if err := os.WriteFile(other, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(held); !os.IsNotExist(err) {
		t.Fatal("released clip should prune")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("newer motion clip should remain")
	}
}

func TestEventStore_pruneClearsMissingAlertClipRel(t *testing.T) {
	root := t.TempDir()
	day := "20260101"
	alDir := filepath.Join(root, "alerts", day)
	if err := os.MkdirAll(alDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alDir, "keep.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	writeJSONL(t, root, []EventRecord{
		{ID: "al", Type: "alert", Cam: "cam118", At: at, ClipRel: "motion/20260101/gone.h265", StillRel: "alerts/20260101/keep.jpg"},
	})
	s, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(); err != nil {
		t.Fatal(err)
	}
	page := s.queryEvents(eventQuery{Type: "alert", Limit: 10})
	if len(page.Events) != 1 {
		t.Fatalf("want alert kept: %+v", page)
	}
	if page.Events[0].ClipRel != "" {
		t.Fatalf("missing clip should be cleared: %s", page.Events[0].ClipRel)
	}
	if page.Events[0].StillRel != "alerts/20260101/keep.jpg" {
		t.Fatalf("still should remain: %s", page.Events[0].StillRel)
	}
}

func writeJSONL(t *testing.T, root string, recs []EventRecord) {
	t.Helper()
	path := filepath.Join(root, "events.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, rec := range recs {
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEventStore_ListPageReadsPastRecent(t *testing.T) {
	root := t.TempDir()
	oldAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	newAt := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	recs := []EventRecord{
		{ID: "old-alert", Type: "alert", Cam: "hik2", At: oldAt, MainClass: "person", Dets: []DetBox{{Class: "person", Score: 0.9}}},
	}
	base := oldAt.Add(time.Hour)
	for i := 0; i < 2500; i++ {
		recs = append(recs, EventRecord{
			ID:   fmt.Sprintf("m%d", i),
			Type: "motion",
			Cam:  "cam118",
			At:   base.Add(time.Duration(i) * time.Second),
		})
	}
	recs = append(recs, EventRecord{
		ID:        "new-alert",
		Type:      "alert",
		Cam:       "hik1",
		At:        newAt,
		MainClass: "car",
		Dets:      []DetBox{{Class: "car", Score: 0.8}, {Class: "person", Score: 0.7}},
	})
	writeJSONL(t, root, recs)

	s, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.List(500, "alert")); n != 1 || s.List(500, "alert")[0].ID != "new-alert" {
		t.Fatalf("RAM list should only see the newest alert, got %+v", s.List(500, "alert"))
	}

	page := s.queryEvents(eventQuery{Type: "alert", Limit: 100})
	if page.Total != 2 || len(page.Events) != 2 || page.HasMore {
		t.Fatalf("full alert page: %+v", page)
	}
	if page.Events[0].ID != "new-alert" || page.Events[1].ID != "old-alert" {
		t.Fatalf("newest first: %#v %#v", page.Events[0].ID, page.Events[1].ID)
	}

	first := s.queryEvents(eventQuery{Type: "alert", Limit: 1})
	if len(first.Events) != 1 || first.Events[0].ID != "new-alert" || !first.HasMore || first.Total != 2 {
		t.Fatalf("first page: %+v", first)
	}
	older := s.ListPage(1, "alert", first.Events[0].At)
	if len(older) != 1 || older[0].ID != "old-alert" {
		t.Fatalf("before cursor: %+v", older)
	}

	hik2 := s.queryEvents(eventQuery{Type: "alert", Cam: "hik2", Limit: 10})
	if hik2.Total != 1 || hik2.Events[0].ID != "old-alert" {
		t.Fatalf("cam filter: %+v", hik2)
	}
	cars := s.queryEvents(eventQuery{Type: "alert", Class: "car", Limit: 10})
	if cars.Total != 1 || cars.Events[0].ID != "new-alert" {
		t.Fatalf("class filter: %+v", cars)
	}
	person := s.queryEvents(eventQuery{Type: "alert", Class: "person", Limit: 10})
	if person.Total != 2 {
		t.Fatalf("person matches main or dets: %+v", person)
	}
	since := s.queryEvents(eventQuery{Type: "alert", Since: newAt.Add(-time.Minute), Limit: 10})
	if since.Total != 1 || since.Events[0].ID != "new-alert" {
		t.Fatalf("since filter: %+v", since)
	}
	until := s.queryEvents(eventQuery{Type: "alert", Until: newAt, Limit: 10})
	if until.Total != 1 || until.Events[0].ID != "old-alert" {
		t.Fatalf("until exclusive: %+v", until)
	}
}

func TestEventStore_VerdictFiltersAndPersists(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 9, 21, 17, 24, 58, 0, time.UTC)
	writeJSONL(t, root, []EventRecord{
		{ID: "hik3_post", Type: "alert", Cam: "hik3", At: at, MainClass: "person"},
		{ID: "hik3_person", Type: "alert", Cam: "hik3", At: at.Add(time.Minute), MainClass: "person"},
		{ID: "cam56_rain", Type: "alert", Cam: "cam56", At: at.Add(2 * time.Minute), MainClass: "animal"},
	})
	s, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SetVerdict("hik3_post", "false"); err != nil {
		t.Fatal(err)
	}
	if _, fn, rn, err := s.SetVerdict("hik3_person", "real"); err != nil {
		t.Fatal(err)
	} else if fn != 1 || rn != 1 {
		t.Fatalf("counts false=%d real=%d", fn, rn)
	}
	falses := s.queryEvents(eventQuery{Verdict: "false", Limit: 10})
	if falses.Total != 1 || falses.Events[0].ID != "hik3_post" || falses.Events[0].Verdict != "false" || falses.FalseN != 1 {
		t.Fatalf("false page: %+v", falses)
	}
	unmarked := s.queryEvents(eventQuery{Verdict: "none", Limit: 10})
	if unmarked.Total != 1 || unmarked.Events[0].ID != "cam56_rain" {
		t.Fatalf("unmarked: %+v", unmarked)
	}
	if _, _, _, err := s.SetVerdict("../etc", "false"); err == nil {
		t.Fatal("path id should be rejected")
	}
	if _, fn, rn, err := s.SetVerdict("hik3_post", ""); err != nil {
		t.Fatal(err)
	} else if fn != 0 || rn != 1 {
		t.Fatalf("cleared counts false=%d real=%d", fn, rn)
	}

	s2, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	real := s2.queryEvents(eventQuery{Verdict: "real", Limit: 10})
	if real.Total != 1 || real.Events[0].ID != "hik3_person" || real.RealN != 1 || real.FalseN != 0 {
		t.Fatalf("reloaded: %+v", real)
	}
}

func TestEventStore_SetVerdictWriteFailureRestores(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 9, 21, 17, 24, 58, 0, time.UTC)
	writeJSONL(t, root, []EventRecord{
		{ID: "hik3_post", Type: "alert", Cam: "hik3", At: at, MainClass: "person"},
	})
	s, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SetVerdict("hik3_post", "real"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "verdicts.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SetVerdict("hik3_post", "false"); err == nil {
		t.Fatal("write onto a directory should fail")
	}
	s.mu.Lock()
	got := s.verdicts["hik3_post"]
	s.mu.Unlock()
	if got != "real" {
		t.Fatalf("verdict %q after failed write", got)
	}
}

func TestEventStore_PruneDropsVerdict(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 9, 21, 17, 24, 58, 0, time.UTC)
	writeJSONL(t, root, []EventRecord{
		{ID: "gone", Type: "alert", Cam: "hik3", At: at, StillRel: "alerts/missing.jpg"},
		{ID: "stay", Type: "alert", Cam: "hik3", At: at, StillRel: "alerts/keep.jpg"},
	})
	if err := os.MkdirAll(filepath.Join(root, "alerts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "alerts", "keep.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SetVerdict("gone", "false"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SetVerdict("stay", "real"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, gone := s.verdicts["gone"]
	stay := s.verdicts["stay"]
	s.mu.Unlock()
	if gone {
		t.Fatal("verdict kept for pruned alert")
	}
	if stay != "real" {
		t.Fatalf("kept verdict %q", stay)
	}
	page := s.queryEvents(eventQuery{Type: "alert", Limit: 10})
	if page.FalseN != 0 || page.RealN != 1 {
		t.Fatalf("counts false=%d real=%d", page.FalseN, page.RealN)
	}
}

func TestParseQueryTime(t *testing.T) {
	if !parseQueryTime("").IsZero() {
		t.Fatal("empty")
	}
	got := parseQueryTime("2026-08-19T18:00:00.123456789Z")
	want := time.Date(2026, 8, 19, 18, 0, 0, 123456789, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("nano %v", got)
	}
	d := parseQueryTime("2026-08-19")
	if !d.Equal(time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date %v", d)
	}
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
	rec, err := s.SaveAlert(ev, jpg, dets, "person", 0.9, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Type != "alert" || len(rec.Dets) != 2 {
		t.Fatalf("want 2 dets: %+v", rec)
	}
	if rec.ClipRel != filepath.ToSlash(filepath.Join("alerts", ev.At.Format("20060102"), rec.ID+".h265")) {
		t.Fatalf("clip rel: %s", rec.ClipRel)
	}
	clipAbs, err := s.AbsMedia(rec.ClipRel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clipAbs); !os.IsNotExist(err) {
		t.Fatal("SaveAlert must not write clip")
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

func TestEventStore_WriteClipAt(t *testing.T) {
	s, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	rel := "alerts/20260829/x.h265"
	if err := s.WriteClipAt(rel, [][]byte{fakeHEVCKeyframe()}); err != nil {
		t.Fatal(err)
	}
	abs, err := s.AbsMedia(rel)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(abs); err != nil || st.Size() == 0 {
		t.Fatalf("clip missing: %v", err)
	}
}
