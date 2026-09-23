package main

import (
	"bytes"
	"context"
	"os"
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

func assertAdmitIdle(t *testing.T, a *admissions) {
	t.Helper()
	if a.held != 0 {
		t.Fatalf("held=%d want 0", a.held)
	}
	if len(a.pending) != 0 {
		t.Fatalf("pending=%v want empty", a.pending)
	}
}

func TestFireMotion_emptyRingDoesNotLatchEpisode(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	w.fireMotion(4, 0, 0, 0, 0)
	if w.episode {
		t.Fatal("empty ring latched episode")
	}
	if _, _, ok := w.adm.pull(); ok {
		t.Fatal("marked without segments")
	}
	assertAdmitIdle(t, w.adm)
	w.ring.push(fakeHEVCKeyframe())
	w.fireMotion(4, 0, 0, 0, 0)
	ev, opened, ok := w.adm.pull()
	if !ok {
		t.Fatal("expected mark after ring has AUs")
	}
	if !opened || ev.SAD != 4 {
		t.Fatalf("first successful mark must be open sad=%v opened=%v", ev.SAD, opened)
	}
}

func TestEnqueueDetect_emptyRingNoMark(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	if w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("empty ring reported mark")
	}
	if _, _, ok := w.adm.pull(); ok {
		t.Fatal("marked without segments")
	}
	assertAdmitIdle(t, w.adm)
	w.adm.perCam = 1
	w.ring.push(fakeHEVCKeyframe())
	if !w.enqueueDetect("motion", 2, false, 0, 0, 0, 0) {
		t.Fatal("extra after released empty-ring hold")
	}
	ev, opened, ok := w.adm.pull()
	if !ok || ev.SAD != 2 || opened {
		t.Fatalf("want extra sad=2 got sad=%v opened=%v ok=%v", ev.SAD, opened, ok)
	}
}

func TestEnqueueDetect_queuesMultiple(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	w.ring.push(fakeHEVCKeyframe())
	if !w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("first")
	}
	if !w.enqueueDetect("motion", 2, false, 0, 0, 0, 0) {
		t.Fatal("second")
	}
	ev, opened, ok := w.adm.pull()
	if !ok || ev.SAD != 1 || !opened {
		t.Fatalf("want first open sad=1 got sad=%v opened=%v ok=%v", ev.SAD, opened, ok)
	}
	ev, opened, ok = w.adm.pull()
	if !ok || ev.SAD != 2 || opened {
		t.Fatalf("want second sad=2 got sad=%v opened=%v ok=%v", ev.SAD, opened, ok)
	}
}

func TestEnqueueDetect_overwriteKeepsLatest(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	w.adm.perCam = 1
	w.ring.push(fakeHEVCKeyframe())
	if !w.enqueueDetect("motion", 1, false, 0, 0, 0, 0) {
		t.Fatal("first extra")
	}
	if !w.enqueueDetect("motion", 2, false, 0, 0, 0, 0) {
		t.Fatal("full extra must replace, not skip")
	}
	ev, opened, ok := w.adm.pull()
	if !ok || ev.SAD != 2 || opened {
		t.Fatalf("want latest extra sad=2 got sad=%v opened=%v ok=%v", ev.SAD, opened, ok)
	}
	if _, _, ok := w.adm.pull(); ok {
		t.Fatal("expected single extra after overwrite")
	}
}

func TestEnqueueDetect_openNeverSkipped(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	w.adm.perCam = 1
	w.adm.cap = 1
	w.ring.push(fakeHEVCKeyframe())
	if !w.enqueueDetect("motion", 1, false, 0, 0, 0, 0) {
		t.Fatal("extra")
	}
	if !w.enqueueDetect("motion", 2, true, 0, 0, 0, 0) {
		t.Fatal("open must not skip when per-cam and global cap are full")
	}
	found := false
	for i := 0; i < 4; i++ {
		ev, opened, ok := w.adm.pull()
		if !ok {
			break
		}
		if opened && ev.SAD == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("open job missing")
	}
}

func TestEnqueueDetect_skipDoesNotSnapshot(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	w.adm.perCam = 1
	w.ring.push(fakeHEVCKeyframe())
	if !w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("open")
	}
	n := 0
	w.ring.onSnapshot = func() { n++ }
	if w.enqueueDetect("motion", 2, false, 0, 0, 0, 0) {
		t.Fatal("extra with only an open slot should skip")
	}
	if n != 0 {
		t.Fatal("snapshot on drop path")
	}
}

func TestEnqueueDetect_copiesGOPNotTimeWindow(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	idr := fakeHEVCKeyframe()
	p := fakePFrame()
	w.ring.push(idr)
	w.ring.push(p)
	w.ring.push(p)
	w.ring.push(idr)
	w.ring.push(p)
	if !w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("enqueue")
	}
	ev, _, ok := w.adm.pull()
	if !ok {
		t.Fatal("missing job")
	}
	if len(ev.Segments) != 2 {
		t.Fatalf("want GOP 2 aus, got %d (10s dump?)", len(ev.Segments))
	}
	if !bytes.Equal(ev.Segments[0], idr) || !bytes.Equal(ev.Segments[1], p) {
		t.Fatal("job is not last keyframe→newest")
	}
}

func TestEnqueueDetect_noKeyframeNoMark(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	w.ring.push(fakePFrame())
	if w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("P-only ring reported mark")
	}
	if _, _, ok := w.adm.pull(); ok {
		t.Fatal("marked without keyframe")
	}
	assertAdmitIdle(t, w.adm)
}

func TestApplyMotion_updatesGates(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c"}, 3, 1024, 2, 6, 3, 6, nil)
	w.ApplyMotion(camMotionSettings{
		Name: "c", Threshold: 0.08, MotionMinBlob: 20,
		MotionMinCompact: 0.4, MotionSustainFrames: 4,
	})
	got := w.MotionSettings()
	if got.Threshold < 0.079 || got.Threshold > 0.081 || got.MotionMinBlob != 20 ||
		got.MotionMinCompact != 0.4 || got.MotionSustainFrames != 4 {
		t.Fatalf("settings %+v", got)
	}
}

func TestApplyMotion_rebuildsIgnoreMask(t *testing.T) {
	w := NewCameraWorker(Camera{Name: "c"}, 3, 1024, 2, 6, 3, 6, nil)
	if w.ignoreMask() != nil {
		t.Fatal("no masks at start")
	}
	w.ApplyMotion(camMotionSettings{
		Name: "c", Threshold: 0.03, MotionMinBlob: 6,
		MotionMinCompact: 0, MotionSustainFrames: 2,
		MotionMasks: []MotionMask{{X: 0, Y: 0, W: 0.5, H: 0.5}},
	})
	ign := w.ignoreMask()
	if ign == nil || !ign[0] {
		t.Fatal("top-left should be ignored")
	}
	if ign[thumbSize-1] {
		t.Fatal("bottom-right should still count")
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

func TestStreamWith_clearsEpisode(t *testing.T) {
	w := &CameraWorker{ring: newRingBuffer(64 * 1024), episode: true}
	done := make(chan struct{})
	go func() {
		w.streamWith(context.Background(),
			func(context.Context) {},
			func(context.Context) {},
		)
		close(done)
	}()
	waitDone(t, done, "streamWith blocked")
	if w.episode {
		t.Fatal("episode still set after stream session start")
	}
}

func TestWriteAlertClip_preThenPost(t *testing.T) {
	oldPre, oldPost := clipPreDur, clipPostDur
	clipPreDur = 40 * time.Millisecond
	clipPostDur = 40 * time.Millisecond
	defer func() {
		clipPreDur, clipPostDur = oldPre, oldPost
	}()

	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	pre := append(fakeHEVCKeyframe(), []byte("PREDATA")...)
	post := append(fakeHEVCKeyframe(), []byte("POSTDATA")...)
	w.ring.push(pre)
	at := time.Now()
	rel := "alerts/20260829/prepost.h265"
	abs, err := store.AbsMedia(rel)
	if err != nil {
		t.Fatal(err)
	}
	preDone := make(chan struct{})
	release := make(chan struct{})
	w.afterClipPre = func() {
		close(preDone)
		<-release
	}
	done := make(chan struct{})
	go func() {
		w.writeAlertClip(store, rel, at)
		close(done)
	}()
	waitDone(t, preDone, "pre-roll not written")
	first, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(first, []byte("PREDATA")) {
		t.Fatal("missing pre-roll in first write")
	}
	if bytes.Contains(first, []byte("POSTDATA")) {
		t.Fatal("post-roll present before wait")
	}
	w.ring.push(post)
	close(release)
	waitDone(t, done, "writeAlertClip did not finish")
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("PREDATA")) {
		t.Fatal("missing pre-roll")
	}
	if !bytes.Contains(data, []byte("POSTDATA")) {
		t.Fatal("missing post-roll")
	}
}

func TestWriteAlertClip_reconnectKeepsPreRoll(t *testing.T) {
	oldPre, oldPost := clipPreDur, clipPostDur
	clipPreDur = 40 * time.Millisecond
	clipPostDur = 40 * time.Millisecond
	defer func() {
		clipPreDur, clipPostDur = oldPre, oldPost
	}()

	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	pre := append(fakeHEVCKeyframe(), []byte("PREDATA")...)
	w.ring.push(pre)
	at := time.Now()
	rel := "alerts/20260829/keep.h265"
	abs, err := store.AbsMedia(rel)
	if err != nil {
		t.Fatal(err)
	}
	preDone := make(chan struct{})
	release := make(chan struct{})
	w.afterClipPre = func() {
		close(preDone)
		<-release
	}
	done := make(chan struct{})
	go func() {
		w.writeAlertClip(store, rel, at)
		close(done)
	}()
	waitDone(t, preDone, "pre-roll not written")
	w.ring.clear()
	w.ring.push(append(fakeHEVCKeyframe(), []byte("NEWDATA")...))
	close(release)
	waitDone(t, done, "writeAlertClip did not finish")
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("PREDATA")) {
		t.Fatal("pre-roll replaced after reconnect")
	}
	if bytes.Contains(data, []byte("NEWDATA")) {
		t.Fatal("post-reconnect dump overwrote pre-roll")
	}
}

func TestEnqueueDetect_openCapturesClip(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	oldPost := clipPostDur
	clipPostDur = 0
	defer func() { clipPostDur = oldPost }()
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	clipper := &alertClipper{store: store}
	clipper.register(w)
	pre := append(fakeHEVCKeyframe(), []byte("PREDATA")...)
	w.ring.push(pre)
	preDone := make(chan struct{})
	w.afterClipPre = func() { close(preDone) }
	if !w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("open")
	}
	ev, opened, ok := w.adm.pull()
	if !ok || !opened {
		t.Fatal("missing open job")
	}
	waitDone(t, preDone, "open did not start clip")
	abs, err := store.AbsMedia(motionClipRel(ev))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("PREDATA")) {
		t.Fatal("clip missing fire-time ring")
	}
}

func TestEnqueueDetect_secondOpenDropsFirstHold(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	oldPost := clipPostDur
	clipPostDur = 0
	defer func() { clipPostDur = oldPost }()
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	clipper := &alertClipper{store: store}
	clipper.register(w)
	w.ring.push(fakeHEVCKeyframe())
	firstDone := make(chan struct{})
	w.afterClipPre = func() { close(firstDone) }
	if !w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("first")
	}
	firstEv := w.adm.q[0].ev
	waitDone(t, firstDone, "first clip")
	secondDone := make(chan struct{})
	w.afterClipPre = func() { close(secondDone) }
	if !w.enqueueDetect("motion", 1, true, 0, 0, 0, 0) {
		t.Fatal("second")
	}
	secondEv := w.adm.q[0].ev
	waitDone(t, secondDone, "second clip")
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.clipHold) != 1 || len(w.adm.q) != 1 {
		t.Fatalf("holds=%d queued=%d", len(store.clipHold), len(w.adm.q))
	}
	if _, ok := store.clipHold[motionClipRel(firstEv)]; ok {
		t.Fatal("first hold kept")
	}
	if store.clipHold[motionClipRel(secondEv)] != 1 {
		t.Fatalf("second hold %d", store.clipHold[motionClipRel(secondEv)])
	}
}

func TestEnqueueDetect_extraDoesNotCaptureClip(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	oldPost := clipPostDur
	clipPostDur = 0
	defer func() { clipPostDur = oldPost }()
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	w.adm = newAdmissions()
	clipper := &alertClipper{store: store}
	clipper.register(w)
	w.ring.push(fakeHEVCKeyframe())
	preDone := make(chan struct{})
	w.afterClipPre = func() { close(preDone) }
	if !w.enqueueDetect("motion", 1, false, 0, 0, 0, 0) {
		t.Fatal("extra")
	}
	select {
	case <-preDone:
		t.Fatal("extra enqueue started clip")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWriteAlertClip_lateDoesNotDumpLiveRing(t *testing.T) {
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, nil)
	late := append(fakeHEVCKeyframe(), []byte("LATEDATA")...)
	w.ring.push(late)
	rel := "motion/20260907/late.h265"
	abs, err := store.AbsMedia(rel)
	if err != nil {
		t.Fatal(err)
	}
	w.writeAlertClip(store, rel, time.Now().Add(-8*time.Minute))
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatal("late capture dumped live ring")
	}
}

func TestWriteAlertClip_emptyRingNoLog(t *testing.T) {
	oldPost := clipPostDur
	clipPostDur = 0
	defer func() { clipPostDur = oldPost }()
	logQ := make(chan string, 4)
	w := NewCameraWorker(Camera{Name: "c1"}, 3, 64*1024, 1, 1, 3, 3, logQ)
	store, err := newEventStore(t.TempDir(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	w.writeAlertClip(store, "alerts/20260829/empty.h265", time.Now())
	select {
	case msg := <-logQ:
		t.Fatalf("logged %q on empty ring", msg)
	default:
	}
}
