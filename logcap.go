package main

import (
	"bytes"
	"context"
	"os"
	"time"
)

const (
	logCapMax  = 8 << 20
	logCapKeep = 1 << 20
)

func stdoutPath() string {
	p, err := os.Readlink("/proc/self/fd/1")
	if err != nil {
		return ""
	}
	return p
}

func capLogFile(path string, max, keep int64) error {
	if path == "" || max <= 0 {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !fi.Mode().IsRegular() || fi.Size() <= max {
		return nil
	}
	if keep <= 0 || keep >= fi.Size() {
		return os.Truncate(path, 0)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, keep)
	if _, err := f.ReadAt(buf, fi.Size()-keep); err != nil {
		return err
	}
	if i := bytes.IndexByte(buf, '\n'); i >= 0 && i+1 < len(buf) {
		buf = buf[i+1:]
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err = f.Write(buf)
	return err
}

func runLogCap(ctx context.Context, path string) {
	if path == "" {
		return
	}
	_ = capLogFile(path, logCapMax, logCapKeep)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = capLogFile(path, logCapMax, logCapKeep)
		}
	}
}
