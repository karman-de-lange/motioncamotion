package main

import (
	"context"
	"testing"
	"time"
)

const testBound = 300 * time.Millisecond

func waitDone(t *testing.T, done <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(testBound):
		t.Fatal(msg)
	}
}

type closeWaitStub struct {
	closed chan struct{}
	nClose int
}

func newCloseWaitStub() *closeWaitStub {
	return &closeWaitStub{closed: make(chan struct{})}
}

func (s *closeWaitStub) close() {
	s.nClose++
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
}

func (s *closeWaitStub) wait(ctx context.Context) {
	go func() {
		<-ctx.Done()
		s.close()
	}()
	<-s.closed
}

func TestApplyMotion_updatesGates(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c"}, 3, 1024, 2, 6, 3, 6, 3*time.Second, nil, nil)
	w.ApplyMotion(camMotionSettings{
		Name: "c", Threshold: 0.08, MotionMinBlob: 20,
		MotionMinCompact: 0.4, MotionCooldownSec: 12, MotionSustainFrames: 4,
	})
	got := w.MotionSettings()
	if got.Threshold < 0.079 || got.Threshold > 0.081 || got.MotionMinBlob != 20 ||
		got.MotionMinCompact != 0.4 || got.MotionCooldownSec != 12 || got.MotionSustainFrames != 4 {
		t.Fatalf("settings %+v", got)
	}
}

func TestClearLooksee_previewUnavailable(t *testing.T) {
	w := &CameraWorker{
		status:        "connected",
		latestSubs:    [][]byte{[]byte("old-au")},
		lastSubAUAt:   time.Now(),
		previewJPEG:   []byte("old-jpeg"),
		previewJPEGAt: time.Now(),
	}
	if w.subAUStale(time.Second) {
		t.Fatal("fixture must not already be stale")
	}
	w.clearLooksee()
	if !w.lastSubAUAt.IsZero() {
		t.Fatal("lastSubAUAt not zeroed")
	}
	if !w.subAUStale(time.Second) {
		t.Fatal("expected stale after clear")
	}
	if aus := w.copyLatestSubs(); len(aus) != 0 {
		t.Fatalf("latestSubs still present: %d", len(aus))
	}
	if jpg := w.cachedPreviewJPEG(); jpg != nil {
		t.Fatal("preview JPEG still cached")
	}
	if _, err := w.previewJPEGBytes(); err == nil {
		t.Fatal("preview should fail after clear")
	}
}

func TestSubAUStale(t *testing.T) {
	w := &CameraWorker{}
	if !w.subAUStale(time.Second) {
		t.Fatal("zero lastSubAUAt should be stale")
	}
	w.setLatestSubAU([]byte("au"))
	if w.subAUStale(time.Second) {
		t.Fatal("fresh AU should not be stale")
	}
	w.lastSubAUAt = time.Now().Add(-2 * time.Second)
	if !w.subAUStale(time.Second) {
		t.Fatal("AU older than maxAge should be stale")
	}
	w.clearLooksee()
	if !w.subAUStale(time.Second) {
		t.Fatal("cleared looksee should be stale")
	}
}

func TestPreviewJPEGBytes_leftoverCacheWithoutAUs(t *testing.T) {
	w := &CameraWorker{
		previewJPEG:   []byte("jpeg"),
		previewJPEGAt: time.Now(),
	}
	jpg, err := w.previewJPEGBytes()
	if err != nil {
		t.Fatalf("leftover cache should serve: %v", err)
	}
	if string(jpg) != "jpeg" {
		t.Fatalf("got %q", jpg)
	}
}

func TestLooksee_setAfterClear(t *testing.T) {
	w := &CameraWorker{
		latestSubs:    [][]byte{[]byte("old-au")},
		lastSubAUAt:   time.Now(),
		previewJPEG:   []byte("old-jpeg"),
		previewJPEGAt: time.Now(),
	}
	w.clearLooksee()
	w.setLatestSubAU([]byte("new-au"))
	if w.subAUStale(time.Second) {
		t.Fatal("new AU should not be stale")
	}
	if aus := w.copyLatestSubs(); len(aus) != 1 {
		t.Fatalf("want 1 AU after set, got %d", len(aus))
	}
}

func TestStorePreviewJPEG_rejectsOldGen(t *testing.T) {
	w := &CameraWorker{
		latestSubs:    [][]byte{[]byte("old-au")},
		lastSubAUAt:   time.Now(),
		previewJPEG:   []byte("old-jpeg"),
		previewJPEGAt: time.Now().Add(-time.Second),
	}
	_, gen := w.snapshotLooksee()
	w.clearLooksee()
	if w.storePreviewJPEGAtGen([]byte("reseed"), gen) {
		t.Fatal("must not store JPEG from pre-clear generation")
	}
	if !w.storePreviewJPEGAtGen([]byte("new-session"), w.lookseeGen) {
		t.Fatal("new generation should store")
	}
	if jpg := w.cachedPreviewJPEGAtGen(gen); jpg != nil {
		t.Fatal("must not fallback to pre-clear generation")
	}
	if jpg := w.cachedPreviewJPEG(); string(jpg) != "new-session" {
		t.Fatalf("new-session cache=%q", jpg)
	}
}

func TestPreviewJPEGBytes_clearDuringDecodeDoesNotReseed(t *testing.T) {
	w := &CameraWorker{
		latestSubs:    [][]byte{[]byte("old-au")},
		lastSubAUAt:   time.Now(),
		previewJPEG:   []byte("old-jpeg"),
		previewJPEGAt: time.Now().Add(-time.Second),
	}
	w.afterLookseeCopy = func() { w.clearLooksee() }
	_, err := w.previewJPEGBytes()
	if err == nil {
		t.Fatal("expected error after overlapping clear")
	}
	if jpg := w.cachedPreviewJPEG(); jpg != nil {
		t.Fatal("cleared cache was reseeded")
	}
}

func TestRunSub_unparseableURLClearsLooksee(t *testing.T) {
	w := &CameraWorker{
		cfg:           Camera{Sub: ":"},
		latestSubs:    [][]byte{[]byte("old-au")},
		lastSubAUAt:   time.Now(),
		previewJPEG:   []byte("old-jpeg"),
		previewJPEGAt: time.Now(),
	}
	w.runSub(context.Background())
	if !w.lastSubAUAt.IsZero() {
		t.Fatal("runSub should clear looksee on early return")
	}
	if aus := w.copyLatestSubs(); len(aus) != 0 {
		t.Fatal("runSub should drop AU ring")
	}
	if jpg := w.cachedPreviewJPEG(); jpg != nil {
		t.Fatal("runSub should drop JPEG cache")
	}
}

func TestStreamWith_subExitClosesMain(t *testing.T) {
	w := &CameraWorker{status: "reconnecting"}
	main := newCloseWaitStub()
	subStarted := make(chan struct{})
	done := make(chan struct{})
	go func() {
		w.streamWith(context.Background(),
			func(c context.Context) { main.wait(c) },
			func(context.Context) { close(subStarted) },
		)
		close(done)
	}()
	waitDone(t, subStarted, "sub never started")
	waitDone(t, done, "streamWith stayed up after sub exited")
	select {
	case <-main.closed:
	default:
		t.Fatal("main Close path did not run")
	}
	if st := w.Status(); st != "reconnecting" {
		t.Fatalf("status=%q, want reconnecting", st)
	}
}

func TestStreamWith_mainExitClosesSub(t *testing.T) {
	w := &CameraWorker{status: "reconnecting"}
	sub := newCloseWaitStub()
	mainStarted := make(chan struct{})
	done := make(chan struct{})
	go func() {
		w.streamWith(context.Background(),
			func(context.Context) { close(mainStarted) },
			func(c context.Context) { sub.wait(c) },
		)
		close(done)
	}()
	waitDone(t, mainStarted, "main never started")
	waitDone(t, done, "streamWith stayed up after main exited")
	select {
	case <-sub.closed:
	default:
		t.Fatal("sub Close path did not run")
	}
	if st := w.Status(); st != "reconnecting" {
		t.Fatalf("status=%q, want reconnecting", st)
	}
}

func TestStreamWith_statusReconnectingWhilePeerTearsDown(t *testing.T) {
	w := &CameraWorker{status: "connected"}
	release := make(chan struct{})
	sawCancel := make(chan struct{})
	done := make(chan struct{})
	go func() {
		w.streamWith(context.Background(),
			func(c context.Context) {
				<-c.Done()
				close(sawCancel)
				<-release
			},
			func(context.Context) {},
		)
		close(done)
	}()
	waitDone(t, sawCancel, "main never saw cancel")
	if st := w.Status(); st != "reconnecting" {
		t.Fatalf("status=%q while survivor still tearing down", st)
	}
	close(release)
	waitDone(t, done, "streamWith did not return")
}

func TestStreamWith_parentCancel(t *testing.T) {
	w := &CameraWorker{status: "reconnecting"}
	ctx, cancel := context.WithCancel(context.Background())
	mainSaw := make(chan struct{})
	subSaw := make(chan struct{})
	done := make(chan struct{})
	go func() {
		w.streamWith(ctx,
			func(c context.Context) { <-c.Done(); close(mainSaw) },
			func(c context.Context) { <-c.Done(); close(subSaw) },
		)
		close(done)
	}()
	cancel()
	waitDone(t, mainSaw, "main did not see parent cancel")
	waitDone(t, subSaw, "sub did not see parent cancel")
	waitDone(t, done, "streamWith did not return on parent cancel")
}

func TestStreamWith_bothExitImmediately(t *testing.T) {
	w := &CameraWorker{status: "connected"}
	done := make(chan struct{})
	go func() {
		w.streamWith(context.Background(),
			func(context.Context) {},
			func(context.Context) {},
		)
		close(done)
	}()
	waitDone(t, done, "streamWith blocked when both peers returned")
	if st := w.Status(); st != "reconnecting" {
		t.Fatalf("status=%q, want reconnecting", st)
	}
}

func TestStreamWith_reenterAfterPeerExit(t *testing.T) {
	w := &CameraWorker{status: "reconnecting"}
	n := 0
	for i := 0; i < 2; i++ {
		main := newCloseWaitStub()
		done := make(chan struct{})
		go func() {
			w.streamWith(context.Background(),
				func(c context.Context) { main.wait(c) },
				func(context.Context) { n++ },
			)
			close(done)
		}()
		waitDone(t, done, "streamWith stayed up on reenter")
	}
	if n != 2 {
		t.Fatalf("sub sessions=%d, want 2", n)
	}
}

func TestStreamWith_clearsMainRing(t *testing.T) {
	w := &CameraWorker{ring: newRingBuffer(64 * 1024)}
	w.ring.push([]byte("old-session-au"))
	if len(w.ring.snapshot(time.Hour)) == 0 {
		t.Fatal("fixture ring should have a chunk")
	}
	done := make(chan struct{})
	go func() {
		w.streamWith(context.Background(),
			func(context.Context) {},
			func(context.Context) {},
		)
		close(done)
	}()
	waitDone(t, done, "streamWith blocked")
	if got := w.ring.snapshot(time.Hour); len(got) != 0 {
		t.Fatalf("ring still has %d chunks after streamWith", len(got))
	}
}
