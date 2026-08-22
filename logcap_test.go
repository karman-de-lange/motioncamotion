package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapLogFile_keepsTailUnderMax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("line-aaaa-bbbb-cccc\n")
	}
	raw := []byte(b.String())
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := capLogFile(path, 500, 200); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) > 500 {
		t.Fatalf("size %d still over max 500", len(got))
	}
	if len(got) == 0 {
		t.Fatal("kept nothing")
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("tail should end on a line, got %q", got)
	}
}

func TestCapLogFile_noopWhenSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := capLogFile(path, 100, 50); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi\n" {
		t.Fatalf("rewrote small file: %q", got)
	}
}
