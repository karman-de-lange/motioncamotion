package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExportPruneDemo(t *testing.T) {
	root := os.Getenv("SCRATCH_PRUNE")
	if root == "" {
		t.Skip("no SCRATCH_PRUNE")
	}
	_ = os.RemoveAll(root)
	s, err := newEventStore(root, 1, 0) // 1 day age
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "motion", "oldday")
	_ = os.MkdirAll(dir, 0o755)
	old := filepath.Join(dir, "old.h265")
	recent := filepath.Join(dir, "recent.h265")
	_ = os.WriteFile(old, make([]byte, 200), 0o644)
	_ = os.WriteFile(recent, make([]byte, 200), 0o644)
	past := time.Now().Add(-10 * 24 * time.Hour)
	_ = os.Chtimes(old, past, past)
	list := func(path string) string {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return fmt.Sprint(names)
	}
	_ = os.WriteFile(filepath.Join(root, "..", "prune_before.txt"), []byte(list(dir)+"\n"), 0o644)
	n, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "..", "prune_after.txt"), []byte(fmt.Sprintf("removed=%d files=%s\n", n, list(dir))), 0o644)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old should be gone")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatal("recent should remain")
	}
}
