package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type clipSaver struct {
	dir  string
	cams map[string]bool
}

func newClipSaver(dir string, cams []string) *clipSaver {
	m := make(map[string]bool, len(cams))
	for _, c := range cams {
		m[c] = true
	}
	return &clipSaver{dir: dir, cams: m}
}

func (s *clipSaver) want(cam string) bool {
	return s != nil && s.cams[cam]
}

func (s *clipSaver) save(ev DetectEvent) (string, error) {
	if !s.want(ev.Cam) || len(ev.Segments) == 0 {
		return "", nil
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}

	stamp := ev.At.Format("20060102_150405")
	path := filepath.Join(s.dir, fmt.Sprintf("%s_%s.h265", ev.Cam, stamp))

	data := buildClipBitstream(ev.Segments)
	if len(data) == 0 {
		return "", fmt.Errorf("empty clip")
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}

	return path, nil
}