package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/pion/rtp"
)

const (
	// Pre-trigger main-ring slice written on motion (wall-clock AU timestamps).
	snapshotDur = 10 * time.Second
	// Keep more than snapshot so the window is full when motion fires.
	ringKeepDur = 20 * time.Second
)

type CameraWorker struct {
	cfg           Camera
	sadCutoff     float64
	sustainFrames int
	minBlob       int
	minCompact    float64 // 0 = no compactness gate
	avgFrames     int     // temporal mean window (noise kill)
	lagFrames     int     // older window offset (slow human motion)
	cooldown      time.Duration
	ignore        []bool
	status        string

	mu               sync.Mutex
	ring             *ringBuffer
	lastMotionAt     time.Time
	subCodec         int    // CodecHEVC or CodecAVC
	subSPS           []byte // H.264 SDP params for preview / IDR
	subPPS           []byte
	latestSubs       [][]byte // recent sub annex-B AUs for on-demand web JPEG
	lastSubAUAt      time.Time
	lookseeGen       uint64
	previewJPEG      []byte
	previewJPEGAt    time.Time
	afterLookseeCopy func() // test hook: runs after snapshot, before decode
	adm              *admissions
	episode          bool

	detectQ chan<- DetectEvent
	logQ    chan<- string
}

// Keep enough AUs for H.264 looksee to always include an IDR (Imou ~15fps, GOP often 2–4s).
const previewAUKeep = 64

func NewCameraWorker(cam Camera, sadCutoff float64, maxBuf, sustainFrames, minBlob, avgFrames, lagFrames int, cooldown time.Duration, detectQ chan<- DetectEvent, logQ chan<- string) *CameraWorker {
	if sustainFrames < 1 {
		sustainFrames = 1
	}
	if minBlob < 1 {
		minBlob = 1
	}
	if avgFrames < 1 {
		avgFrames = 3
	}
	if lagFrames < avgFrames {
		lagFrames = avgFrames
	}
	if cam.Threshold > 0 {
		sadCutoff = cam.Threshold * 100
	}
	if cam.MotionSustainFrames > 0 {
		sustainFrames = cam.MotionSustainFrames
	}
	if cam.MotionMinBlob > 0 {
		minBlob = cam.MotionMinBlob
	}
	if cam.MotionCooldownSec > 0 {
		cooldown = time.Duration(cam.MotionCooldownSec) * time.Second
	} else if cam.MotionCooldownSec < 0 {
		cooldown = 0
	}
	return &CameraWorker{
		cfg:           cam,
		sadCutoff:     sadCutoff,
		sustainFrames: sustainFrames,
		minBlob:       minBlob,
		minCompact:    cam.MotionMinCompact,
		avgFrames:     avgFrames,
		lagFrames:     lagFrames,
		cooldown:      cooldown,
		ignore:        buildIgnoreMask(cam.MotionMasks),
		status:        "reconnecting",
		ring:          newRingBuffer(maxBuf),
		detectQ:       detectQ,
		logQ:          logQ,
	}
}

// tryMotionFire returns true if motion may enqueue (cooldown elapsed).
func (w *CameraWorker) tryMotionFire() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if w.cooldown > 0 && !w.lastMotionAt.IsZero() && now.Sub(w.lastMotionAt) < w.cooldown {
		return false
	}
	w.lastMotionAt = now
	return true
}

func (w *CameraWorker) setStatus(s string) {
	w.mu.Lock()
	w.status = s
	w.mu.Unlock()
}

func (w *CameraWorker) Status() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *CameraWorker) Name() string {
	return w.cfg.Name
}

type camMotionSettings struct {
	Name                string  `json:"name"`
	Threshold           float64 `json:"threshold"`
	MotionMinBlob       int     `json:"motion_min_blob"`
	MotionMinCompact    float64 `json:"motion_min_compact"`
	MotionCooldownSec   int     `json:"motion_cooldown_sec"`
	MotionSustainFrames int     `json:"motion_sustain_frames"`
}

func (w *CameraWorker) MotionSettings() camMotionSettings {
	w.mu.Lock()
	defer w.mu.Unlock()
	cd := int(w.cooldown / time.Second)
	return camMotionSettings{
		Name:                w.cfg.Name,
		Threshold:           w.sadCutoff / 100,
		MotionMinBlob:       w.minBlob,
		MotionMinCompact:    w.minCompact,
		MotionCooldownSec:   cd,
		MotionSustainFrames: w.sustainFrames,
	}
}

func (w *CameraWorker) ApplyMotion(s camMotionSettings) {
	if s.Threshold > 0 {
		w.sadCutoff = s.Threshold * 100
	}
	if s.MotionMinBlob > 0 {
		w.minBlob = s.MotionMinBlob
	}
	if s.MotionMinCompact >= 0 {
		w.minCompact = s.MotionMinCompact
	}
	if s.MotionCooldownSec >= 0 {
		w.mu.Lock()
		w.cooldown = time.Duration(s.MotionCooldownSec) * time.Second
		w.mu.Unlock()
	}
	if s.MotionSustainFrames > 0 {
		w.sustainFrames = s.MotionSustainFrames
	}
}

func (w *CameraWorker) setSubCodec(codec int) {
	w.mu.Lock()
	w.subCodec = codec
	w.mu.Unlock()
}

func (w *CameraWorker) SubCodec() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.subCodec
}

func (w *CameraWorker) setSubH264Params(sps, pps []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(sps) > 0 {
		w.subSPS = append([]byte(nil), sps...)
	}
	if len(pps) > 0 {
		w.subPPS = append([]byte(nil), pps...)
	}
}

func (w *CameraWorker) SubH264Params() (sps, pps []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.subSPS) > 0 {
		sps = append([]byte(nil), w.subSPS...)
	}
	if len(w.subPPS) > 0 {
		pps = append([]byte(nil), w.subPPS...)
	}
	return sps, pps
}

// setLatestSubAU appends a sub annex-B AU for web looksee (RAM only, short ring).
func (w *CameraWorker) setLatestSubAU(au []byte) {
	if len(au) == 0 {
		return
	}
	cp := make([]byte, len(au))
	copy(cp, au)
	w.mu.Lock()
	w.latestSubs = append(w.latestSubs, cp)
	if len(w.latestSubs) > previewAUKeep {
		w.latestSubs = w.latestSubs[len(w.latestSubs)-previewAUKeep:]
	}
	w.lastSubAUAt = time.Now()
	w.mu.Unlock()
}

// lastSubAUAt must be zeroed so the next watchdog grace is not computed from the previous session.
func (w *CameraWorker) clearLooksee() {
	w.mu.Lock()
	w.latestSubs = nil
	w.lastSubAUAt = time.Time{}
	w.lookseeGen++
	w.previewJPEG = nil
	w.previewJPEGAt = time.Time{}
	w.mu.Unlock()
}

// subAUStale reports whether no sub AU has arrived within maxAge (or never).
func (w *CameraWorker) subAUStale(maxAge time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lastSubAUAt.IsZero() {
		return true
	}
	return time.Since(w.lastSubAUAt) > maxAge
}

func (w *CameraWorker) copyLatestSubs() [][]byte {
	aus, _ := w.snapshotLooksee()
	return aus
}

func (w *CameraWorker) snapshotLooksee() ([][]byte, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	gen := w.lookseeGen
	if len(w.latestSubs) == 0 {
		return nil, gen
	}
	out := make([][]byte, len(w.latestSubs))
	for i, au := range w.latestSubs {
		cp := make([]byte, len(au))
		copy(cp, au)
		out[i] = cp
	}
	return out, gen
}

// copyLatestSubsN returns the last n sub AUs (n<=0 means all).
func (w *CameraWorker) copyLatestSubsN(n int) [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.latestSubs) == 0 {
		return nil
	}
	start := 0
	if n > 0 && len(w.latestSubs) > n {
		start = len(w.latestSubs) - n
	}
	out := make([][]byte, len(w.latestSubs)-start)
	for i, au := range w.latestSubs[start:] {
		cp := make([]byte, len(au))
		copy(cp, au)
		out[i] = cp
	}
	return out
}

// cachedPreviewJPEG returns any cached looksee JPEG (up to 60s).
func (w *CameraWorker) cachedPreviewJPEG() []byte {
	return w.cachedPreviewJPEGFresh(60 * time.Second)
}

// cachedPreviewJPEGFresh returns cache only if younger than ttl.
func (w *CameraWorker) cachedPreviewJPEGFresh(ttl time.Duration) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.previewJPEG) == 0 || time.Since(w.previewJPEGAt) > ttl {
		return nil
	}
	out := make([]byte, len(w.previewJPEG))
	copy(out, w.previewJPEG)
	return out
}

func (w *CameraWorker) storePreviewJPEGAtGen(jpg []byte, gen uint64) bool {
	// Always install a fresh slice so HTTP readers never see a half-written buffer.
	cp := make([]byte, len(jpg))
	copy(cp, jpg)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lookseeGen != gen {
		return false
	}
	w.previewJPEG = cp
	w.previewJPEGAt = time.Now()
	return true
}

func (w *CameraWorker) cachedPreviewJPEGAtGen(gen uint64) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lookseeGen != gen || len(w.previewJPEG) == 0 || time.Since(w.previewJPEGAt) > 60*time.Second {
		return nil
	}
	out := make([]byte, len(w.previewJPEG))
	copy(out, w.previewJPEG)
	return out
}

func annexB(parts ...[]byte) []byte {
	var b bytes.Buffer
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		b.Write([]byte{0, 0, 0, 1})
		b.Write(p)
	}
	return b.Bytes()
}

// rtspTCPClient forces interleaved TCP. UDP loses heavily on remote NVRs (e.g. 10.0.0.161).
func rtspTCPClient(u *base.URL) *gortsplib.Client {
	p := gortsplib.ProtocolTCP
	return &gortsplib.Client{Scheme: u.Scheme, Host: u.Host, Protocol: &p}
}

func (w *CameraWorker) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		w.setStatus("reconnecting")
		w.stream(ctx)
		if ctx.Err() != nil {
			return
		}

		w.setStatus("reconnecting")
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (w *CameraWorker) stream(ctx context.Context) {
	w.streamWith(ctx, w.runMain, w.runSub)
}

func (w *CameraWorker) streamWith(ctx context.Context, main, sub func(context.Context)) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if w.ring != nil {
		w.ring.clear()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		defer w.setStatus("reconnecting")
		main(streamCtx)
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		defer w.setStatus("reconnecting")
		sub(streamCtx)
	}()
	wg.Wait()
}

func (w *CameraWorker) runMain(ctx context.Context) {
	u, err := base.ParseURL(w.cfg.Main)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", w.cfg.Name, err)
		return
	}

	c := rtspTCPClient(u)
	if err := c.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", w.cfg.Name, err)
		return
	}
	defer c.Close()
	go func() {
		<-ctx.Done()
		c.Close()
	}()

	desc, _, err := c.Describe(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", w.cfg.Name, err)
		return
	}

	var forma *format.H265
	medi := desc.FindFormat(&forma)
	if medi == nil {
		fmt.Fprintf(os.Stderr, "%s main: no H265\n", w.cfg.Name)
		return
	}

	rtpDec, err := forma.CreateDecoder()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", w.cfg.Name, err)
		return
	}

	if _, err = c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", w.cfg.Name, err)
		return
	}

	trim := time.NewTicker(2 * time.Second)
	defer trim.Stop()

	c.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
		if ctx.Err() != nil {
			return
		}

		au, err := rtpDec.Decode(pkt)
		if err != nil {
			if errors.Is(err, rtph265.ErrMorePacketsNeeded) || errors.Is(err, rtph265.ErrNonStartingPacketAndNoPrevious) {
				return
			}
			return
		}

		// In-band VPS: push as-is. IRAP without VPS: inject SDP once (not every P-frame).
		if h265AUHasVPS(au) {
			w.ring.push(annexB(au...))
		} else if h265AUHasIRAP(au) {
			parts := [][]byte{forma.VPS, forma.SPS, forma.PPS}
			parts = append(parts, au...)
			w.ring.push(annexB(parts...))
		} else {
			w.ring.push(annexB(au...))
		}
	})

	if _, err = c.Play(nil); err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", w.cfg.Name, err)
		return
	}
	if ctx.Err() == nil {
		w.setStatus("connected")
	}

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()

	for {
		select {
		case <-trim.C:
			w.ring.trim(ringKeepDur)
		case <-done:
			return
		}
	}
}

func (w *CameraWorker) enqueueDetect(kind sampleKind, label string, sad float64, subRGB []byte) {
	segments := w.ring.snapshot(snapshotDur)
	if len(segments) == 0 {
		return
	}
	ev := DetectEvent{
		Cam:      w.cfg.Name,
		At:       time.Now(),
		Segments: segments,
		SubRGB:   subRGB,
		SAD:      sad,
		Label:    label,
	}
	ok := true
	if w.adm != nil {
		ok = w.adm.submit(w.cfg.Name, kind, ev)
	} else if w.detectQ != nil {
		w.detectQ <- ev
	}
	if !ok || w.logQ == nil {
		return
	}
	if sad >= 0 {
		w.logQ <- fmt.Sprintf("%s: %s sad=%.1f%% queued %d aus %.1fMB", ev.Cam, label, sad, len(ev.Segments), eventMB(ev))
	} else {
		w.logQ <- fmt.Sprintf("%s: %s queued %d aus %.1fMB", ev.Cam, label, len(ev.Segments), eventMB(ev))
	}
}

func (w *CameraWorker) FakeMotion() {
	w.enqueueDetect(sampleOpen, "fake", -1, nil)
}
