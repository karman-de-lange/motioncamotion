package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EventRecord is one persisted motion or YOLO-pass alert (JSONL index line).
type EventRecord struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // "motion" | "alert"
	Cam       string    `json:"cam"`
	At        time.Time `json:"at"`
	Label     string    `json:"label"` // motion|fake
	SAD       float64   `json:"sad"`
	Pass      bool      `json:"pass"`
	ClipRel   string    `json:"clip,omitempty"`  // relative annex-B .h265
	VideoRel  string    `json:"video,omitempty"` // legacy; no longer written (old ffmpeg mp4)
	StillRel  string    `json:"still,omitempty"` // relative jpeg
	Dets      []DetBox  `json:"dets,omitempty"`
	MainScore float64   `json:"main_score,omitempty"`
	MainClass string    `json:"main_class,omitempty"`
}

// EventStore writes media + append-only index; serves lists for the web UI.
type EventStore struct {
	mu       sync.Mutex
	root     string
	index    string
	maxAge   time.Duration // 0 = no age prune
	maxBytes int64         // 0 = no size prune
	// live recent for SSE / UI (newest last)
	recent  []EventRecord
	recentN int
	subs    map[chan EventRecord]struct{}
}

func newEventStore(root string, maxAgeDays int, maxMB int) (*EventStore, error) {
	if root == "" {
		return nil, fmt.Errorf("empty store root")
	}
	if err := os.MkdirAll(filepath.Join(root, "motion"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "alerts"), 0o755); err != nil {
		return nil, err
	}
	s := &EventStore{
		root:     root,
		index:    filepath.Join(root, "events.jsonl"),
		maxAge:   time.Duration(maxAgeDays) * 24 * time.Hour,
		maxBytes: int64(maxMB) << 20,
		recentN:  2000,
		subs:     make(map[chan EventRecord]struct{}),
	}
	if maxAgeDays <= 0 {
		s.maxAge = 0
	}
	if maxMB <= 0 {
		s.maxBytes = 0
	}
	_ = s.loadRecent()
	return s, nil
}

func (s *EventStore) Root() string { return s.root }

func (s *EventStore) loadRecent() error {
	f, err := os.Open(s.index)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	var all []EventRecord
	sc := bufio.NewScanner(f)
	// large lines OK for dets
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec EventRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		all = append(all, rec)
	}
	if len(all) > s.recentN {
		all = all[len(all)-s.recentN:]
	}
	s.recent = all
	return sc.Err()
}

func (s *EventStore) appendIndex(rec EventRecord) error {
	f, err := os.OpenFile(s.index, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := f.Write(b); err != nil {
		return err
	}
	s.recent = append(s.recent, rec)
	if len(s.recent) > s.recentN {
		s.recent = s.recent[len(s.recent)-s.recentN:]
	}
	s.broadcast(rec)
	return nil
}

func (s *EventStore) broadcast(rec EventRecord) {
	for ch := range s.subs {
		select {
		case ch <- rec:
		default:
		}
	}
}

// Subscribe returns a channel of live events; caller must Unsubscribe.
func (s *EventStore) Subscribe() chan EventRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan EventRecord, 16)
	s.subs[ch] = struct{}{}
	return ch
}

func (s *EventStore) Unsubscribe(ch chan EventRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, ch)
	close(ch)
}

func (s *EventStore) List(limit int, typ string) []EventRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.recent) {
		limit = len(s.recent)
	}
	// newest first
	out := make([]EventRecord, 0, limit)
	for i := len(s.recent) - 1; i >= 0 && len(out) < limit; i-- {
		r := s.recent[i]
		if typ != "" && r.Type != typ {
			continue
		}
		out = append(out, r)
	}
	return out
}

// eventQuery filters a jsonl scan. Before is a newest-first cursor (strictly older).
// Since is inclusive; Until is exclusive.
type eventQuery struct {
	Type   string
	Cam    string
	Class  string
	Before time.Time
	Since  time.Time
	Until  time.Time
	Limit  int
}

type eventPage struct {
	Events  []EventRecord `json:"events"`
	Total   int           `json:"total"`
	HasMore bool          `json:"has_more"`
}

func eventMatches(r EventRecord, q eventQuery) bool {
	if q.Type != "" && r.Type != q.Type {
		return false
	}
	if q.Cam != "" && r.Cam != q.Cam {
		return false
	}
	if !q.Since.IsZero() && r.At.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && !r.At.Before(q.Until) {
		return false
	}
	if q.Class != "" {
		want := strings.ToLower(q.Class)
		ok := strings.ToLower(r.MainClass) == want
		if !ok {
			for _, d := range r.Dets {
				if strings.ToLower(d.Class) == want {
					ok = true
					break
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// ListPage newest-first page from events.jsonl (not the RAM recent window).
// Shares s.mu with prune/rewriteIndexExisting so a rewrite cannot tear the scan.
func (s *EventStore) ListPage(limit int, typ string, before time.Time) []EventRecord {
	return s.queryEvents(eventQuery{Limit: limit, Type: typ, Before: before}).Events
}

func (s *EventStore) queryEvents(q eventQuery) eventPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q.Limit <= 0 {
		q.Limit = 100
	}
	page := eventPage{Events: []EventRecord{}}
	f, err := os.Open(s.index)
	if err != nil {
		return page
	}
	defer f.Close()
	var matched []EventRecord
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec EventRecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if !eventMatches(rec, q) {
			continue
		}
		matched = append(matched, rec)
	}
	page.Total = len(matched)
	for i := len(matched) - 1; i >= 0; i-- {
		r := matched[i]
		if !q.Before.IsZero() && !r.At.Before(q.Before) {
			continue
		}
		if len(page.Events) == q.Limit {
			page.HasMore = true
			break
		}
		page.Events = append(page.Events, r)
	}
	return page
}

// SaveMotion writes h265 clip (+ optional still jpeg + browser MP4) and indexes a motion event.
func (s *EventStore) SaveMotion(ev DetectEvent, stillJPEG []byte) (EventRecord, error) {
	id := fmt.Sprintf("%s_%s_%d", ev.Cam, ev.At.Format("20060102_150405"), ev.At.UnixNano()%1e6)
	day := ev.At.Format("20060102")
	dir := filepath.Join(s.root, "motion", day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return EventRecord{}, err
	}
	clipName := id + ".h265"
	clipPath := filepath.Join(dir, clipName)
	data := buildClipBitstream(ev.Segments)
	if len(data) == 0 {
		return EventRecord{}, fmt.Errorf("empty clip")
	}
	if err := os.WriteFile(clipPath, data, 0o644); err != nil {
		return EventRecord{}, err
	}
	rec := EventRecord{
		ID:      id,
		Type:    "motion",
		Cam:     ev.Cam,
		At:      ev.At,
		Label:   ev.Label,
		SAD:     ev.SAD,
		Pass:    false,
		ClipRel: filepath.ToSlash(filepath.Join("motion", day, clipName)),
	}
	if len(stillJPEG) > 0 {
		stillName := id + ".jpg"
		stillPath := filepath.Join(dir, stillName)
		if err := os.WriteFile(stillPath, stillJPEG, 0o644); err == nil {
			rec.StillRel = filepath.ToSlash(filepath.Join("motion", day, stillName))
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendIndex(rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// SaveAlert writes annotated still + metadata for a YOLO pass.
// clipRel is the matching motion .h265.
func (s *EventStore) SaveAlert(ev DetectEvent, stillJPEG []byte, dets []DetBox, topClass string, topScore float64, clipRel string) (EventRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("%s_%s_%d", ev.Cam, ev.At.Format("20060102_150405"), ev.At.UnixNano()%1e6)
	day := ev.At.Format("20060102")
	dir := filepath.Join(s.root, "alerts", day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return EventRecord{}, err
	}
	stillName := id + ".jpg"
	stillPath := filepath.Join(dir, stillName)
	if err := os.WriteFile(stillPath, stillJPEG, 0o644); err != nil {
		return EventRecord{}, err
	}
	// sidecar meta
	metaName := id + ".json"
	rec := EventRecord{
		ID:        id,
		Type:      "alert",
		Cam:       ev.Cam,
		At:        ev.At,
		Label:     ev.Label,
		SAD:       ev.SAD,
		Pass:      true,
		StillRel:  filepath.ToSlash(filepath.Join("alerts", day, stillName)),
		ClipRel:   clipRel,
		Dets:      dets,
		MainScore: topScore,
		MainClass: topClass,
	}
	mb, _ := json.MarshalIndent(rec, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, metaName), mb, 0o644)
	if err := s.appendIndex(rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// AbsMedia resolves a relative media path under root (no ..).
func (s *EventStore) AbsMedia(rel string) (string, error) {
	rel = filepath.Clean("/" + rel)
	rel = strings.TrimPrefix(rel, "/")
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("bad path")
	}
	abs := filepath.Join(s.root, rel)
	if !strings.HasPrefix(abs, filepath.Clean(s.root)+string(os.PathSeparator)) && abs != filepath.Clean(s.root) {
		return "", fmt.Errorf("path escape")
	}
	return abs, nil
}

// Prune removes old/oversized media under motion/ and alerts/; rewrites index.
// Returns number of files removed.
func (s *EventStore) Prune() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(time.Now())
}

// pruneLocked is testable with a fixed now.
func (s *EventStore) pruneLocked(now time.Time) (int, error) {
	type fileInfo struct {
		path string
		mod  time.Time
		size int64
	}
	var files []fileInfo
	for _, sub := range []string{"motion", "alerts"} {
		_ = filepath.Walk(filepath.Join(s.root, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			files = append(files, fileInfo{path: path, mod: info.ModTime(), size: info.Size()})
			return nil
		})
	}
	removed := 0
	keep := make(map[string]bool)
	// Age prune
	if s.maxAge > 0 {
		cutoff := now.Add(-s.maxAge)
		for _, f := range files {
			if f.mod.Before(cutoff) {
				_ = os.Remove(f.path)
				removed++
			} else {
				keep[f.path] = true
			}
		}
	} else {
		for _, f := range files {
			keep[f.path] = true
		}
	}
	// Rebuild file list after age prune
	files = files[:0]
	var total int64
	for _, sub := range []string{"motion", "alerts"} {
		_ = filepath.Walk(filepath.Join(s.root, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			files = append(files, fileInfo{path: path, mod: info.ModTime(), size: info.Size()})
			total += info.Size()
			return nil
		})
	}
	if s.maxBytes > 0 && total > s.maxBytes {
		var motion, alerts []fileInfo
		root := filepath.Clean(s.root)
		for _, f := range files {
			rel, err := filepath.Rel(root, f.path)
			if err != nil {
				motion = append(motion, f)
				continue
			}
			if strings.HasPrefix(filepath.ToSlash(rel), "alerts/") {
				alerts = append(alerts, f)
			} else {
				motion = append(motion, f)
			}
		}
		// Drop oldest motion first so restart prune cannot wipe the alert list.
		sort.Slice(motion, func(i, j int) bool { return motion[i].mod.Before(motion[j].mod) })
		for _, f := range motion {
			if total <= s.maxBytes {
				break
			}
			if err := os.Remove(f.path); err == nil {
				total -= f.size
				removed++
				delete(keep, f.path)
			}
		}
		if total > s.maxBytes {
			sort.Slice(alerts, func(i, j int) bool { return alerts[i].mod.Before(alerts[j].mod) })
			for _, f := range alerts {
				if total <= s.maxBytes {
					break
				}
				if err := os.Remove(f.path); err == nil {
					total -= f.size
					removed++
					delete(keep, f.path)
				}
			}
		}
	}
	// Filter index to existing media when possible
	_ = s.rewriteIndexExisting()
	return removed, nil
}

func (s *EventStore) rewriteIndexExisting() error {
	f, err := os.Open(s.index)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var kept []EventRecord
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		var rec EventRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		// Keep if any referenced media file still exists.
		ok := false
		for _, rel := range []string{rec.ClipRel, rec.VideoRel, rec.StillRel} {
			if rel == "" {
				continue
			}
			p := filepath.Join(s.root, filepath.FromSlash(rel))
			if _, err := os.Stat(p); err == nil {
				ok = true
				break
			}
		}
		if ok {
			kept = append(kept, rec)
		}
	}
	f.Close()
	tmp := s.index + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, rec := range kept {
		b, _ := json.Marshal(rec)
		_, _ = out.Write(append(b, '\n'))
	}
	out.Close()
	if err := os.Rename(tmp, s.index); err != nil {
		return err
	}
	if len(kept) > s.recentN {
		kept = kept[len(kept)-s.recentN:]
	}
	s.recent = kept
	return nil
}

// SelectPruneCandidates is pure helper used by tests: given files with mtime/size,
// return paths that should be deleted under age + size policy.
func SelectPruneCandidates(now time.Time, maxAge time.Duration, maxBytes int64, paths []string, mods []time.Time, sizes []int64) []string {
	type fi struct {
		path string
		mod  time.Time
		size int64
	}
	var files []fi
	for i := range paths {
		files = append(files, fi{paths[i], mods[i], sizes[i]})
	}
	var del []string
	alive := make([]fi, 0, len(files))
	if maxAge > 0 {
		cutoff := now.Add(-maxAge)
		for _, f := range files {
			if f.mod.Before(cutoff) {
				del = append(del, f.path)
			} else {
				alive = append(alive, f)
			}
		}
	} else {
		alive = files
	}
	var total int64
	for _, f := range alive {
		total += f.size
	}
	if maxBytes > 0 && total > maxBytes {
		sort.Slice(alive, func(i, j int) bool { return alive[i].mod.Before(alive[j].mod) })
		for _, f := range alive {
			if total <= maxBytes {
				break
			}
			del = append(del, f.path)
			total -= f.size
		}
	}
	return del
}
