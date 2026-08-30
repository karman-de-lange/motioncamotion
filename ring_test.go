package main

import (
	"bytes"
	"testing"
	"time"
)

func fakePFrame() []byte {
	return []byte{0, 0, 0, 1, 2}
}

func fakeH264IDR() []byte {
	return []byte{0, 0, 0, 1, 0x65, 0, 1, 2}
}

func TestRingSnapshotGOP_lastKeyframeToNewest(t *testing.T) {
	idr := fakeHEVCKeyframe()
	p := fakePFrame()
	r := newRingBuffer(64 * 1024)
	r.push(idr)
	r.push(p)
	r.push(p)
	r.push(idr)
	r.push(p)
	got := r.snapshotGOP()
	if len(got) != 2 {
		t.Fatalf("want last GOP 2, got %d (time-window dump?)", len(got))
	}
	if !bytes.Equal(got[0], idr) || !bytes.Equal(got[1], p) {
		t.Fatal("snapshot is not last keyframe→newest")
	}
	if all := r.snapshot(time.Hour); len(all) != 5 {
		t.Fatalf("ring size %d want 5", len(all))
	}
}

func TestRingSnapshotGOP_emptyAndNoKeyframe(t *testing.T) {
	r := newRingBuffer(1024)
	if got := r.snapshotGOP(); len(got) != 0 {
		t.Fatalf("empty ring GOP %d", len(got))
	}
	r.push(fakePFrame())
	r.push([]byte("not-annexb"))
	if got := r.snapshotGOP(); len(got) != 0 {
		t.Fatalf("P-only GOP %d want drop", len(got))
	}
}

func TestRingSnapshotGOP_cra(t *testing.T) {
	cra := []byte{0, 0, 0, 1, 32, 0, 1, 2}
	p := fakePFrame()
	r := newRingBuffer(1024)
	r.push(p)
	r.push(cra)
	r.push(p)
	got := r.snapshotGOP()
	if len(got) != 2 {
		t.Fatalf("want 2 from CRA, got %d", len(got))
	}
	if !bytes.Equal(got[0], cra) {
		t.Fatal("GOP did not start at CRA")
	}
}

func TestRingSnapshotGOP_h264IDR(t *testing.T) {
	idr := fakeH264IDR()
	p := fakePFrame()
	r := newRingBuffer(1024)
	r.push(p)
	r.push(idr)
	r.push(p)
	got := r.snapshotGOP()
	if len(got) != 2 {
		t.Fatalf("want 2 from H.264 IDR, got %d", len(got))
	}
	if !bytes.Equal(got[0], idr) {
		t.Fatal("GOP did not start at H.264 IDR")
	}
}
