package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type Config struct {
	Threshold           float64  `json:"threshold"`
	BufferMB            int      `json:"buffer_mb"`
	MotionSustainFrames int      `json:"motion_sustain_frames"` // consecutive SAD hits; default 3
	MotionCooldownSec   int      `json:"motion_cooldown_sec"`   // per-cam silence after fire; default 3
	MotionMinBlob       int      `json:"motion_min_blob"`       // min connected changed cells; default 8
	MotionAvgFrames     int      `json:"motion_avg_frames"`     // mean last N thumbs (noise); default 3
	MotionLagFrames     int      `json:"motion_lag_frames"`     // older window start (slow motion); default 8
	Model               string   `json:"model"`
	Device              string   `json:"device"`
	SubConf             float64  `json:"sub_conf"`
	MainConf            float64  `json:"main_conf"`
	ForceStage2         bool     `json:"force_stage2"`
	FakeMotionSec       int      `json:"fake_motion_sec"`
	FakeMotionMinSec    int      `json:"fake_motion_min_sec"`
	FakeMotionMaxSec    int      `json:"fake_motion_max_sec"`
	Headless            bool     `json:"headless"`
	PreviewAddr         string   `json:"preview_addr"` // e.g. ":8088"; empty = off
	ClipDir             string   `json:"clip_dir"`
	ClipCams            []string `json:"clip_cams"`
	Cameras             []Camera `json:"cameras"`
}

type Camera struct {
	Name        string       `json:"name"`
	Main        string       `json:"main"`
	Sub         string       `json:"sub"`
	MotionMasks []MotionMask `json:"motion_masks"` // normalized 0–1 rects ignored by SAD (OSD etc.)
}

type cpuSampler struct {
	mu      sync.Mutex
	lastCPU uint64
	lastAt  time.Time
}

func (s *cpuSampler) Percent() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open("/proc/self/stat")
	if err != nil {
		return 0
	}
	defer f.Close()

	line, err := io.ReadAll(f)
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(line))
	if len(fields) < 15 {
		return 0
	}

	utime, _ := strconv.ParseUint(fields[13], 10, 64)
	stime, _ := strconv.ParseUint(fields[14], 10, 64)
	total := utime + stime

	now := time.Now()
	if s.lastAt.IsZero() {
		s.lastCPU = total
		s.lastAt = now
		return 0
	}

	elapsed := now.Sub(s.lastAt).Seconds()
	if elapsed <= 0 {
		return 0
	}

	pct := (float64(total-s.lastCPU) / 100.0) / elapsed * 100
	s.lastCPU = total
	s.lastAt = now
	return pct
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 0.03
	}
	if cfg.BufferMB <= 0 {
		cfg.BufferMB = 200
	}
	if cfg.MotionSustainFrames <= 0 {
		cfg.MotionSustainFrames = 3
	}
	// 0 / omitted → 3s; negative → off.
	if cfg.MotionCooldownSec == 0 {
		cfg.MotionCooldownSec = 3
	} else if cfg.MotionCooldownSec < 0 {
		cfg.MotionCooldownSec = 0
	}
	if cfg.MotionMinBlob <= 0 {
		cfg.MotionMinBlob = 8
	}
	if cfg.MotionAvgFrames <= 0 {
		cfg.MotionAvgFrames = 3
	}
	if cfg.MotionLagFrames <= 0 {
		cfg.MotionLagFrames = 8
	}
	if cfg.Model == "" {
		cfg.Model = "models/yolov8n.onnx"
	}
	if cfg.Device == "" {
		cfg.Device = "GPU"
	}
	if cfg.SubConf <= 0 {
		cfg.SubConf = 0.5
	}
	if cfg.MainConf <= 0 {
		cfg.MainConf = 0.65
	}
	if cfg.PreviewAddr == "" {
		cfg.PreviewAddr = ":8088"
	}
	return cfg, nil
}

func main() {
	cfgPath := "cameras.json"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if len(cfg.Cameras) == 0 {
		fmt.Fprintln(os.Stderr, "no cameras")
		os.Exit(1)
	}

	sadCutoff := cfg.Threshold * 100
	maxBuf := cfg.BufferMB << 20
	cooldown := time.Duration(cfg.MotionCooldownSec) * time.Second

	inf, err := newOVInfer(cfg.Model, cfg.Device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo init: %v\n", err)
		os.Exit(1)
	}
	defer freeOVInfer(inf)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	detectQ := make(chan DetectEvent, 64)
	logQ := make(chan string, 256)

	var clips *clipSaver
	if cfg.ClipDir != "" && len(cfg.ClipCams) > 0 {
		clips = newClipSaver(cfg.ClipDir, cfg.ClipCams)
	}
	yc := yoloConfig{
		SubConf:     cfg.SubConf,
		MainConf:    cfg.MainConf,
		ForceStage2: cfg.ForceStage2,
	}
	go runYOLOWorker(ctx, detectQ, logQ, clips, inf, yc)

	workers := make([]*CameraWorker, len(cfg.Cameras))
	for i, cam := range cfg.Cameras {
		workers[i] = NewCameraWorker(cam, sadCutoff, maxBuf, cfg.MotionSustainFrames, cfg.MotionMinBlob, cfg.MotionAvgFrames, cfg.MotionLagFrames, cooldown, detectQ, logQ)
		go workers[i].Run(ctx)
	}

	if cfg.PreviewAddr != "" && cfg.PreviewAddr != "off" {
		go runPreviewServer(ctx, cfg.PreviewAddr, workers, logQ)
	}

	if cfg.FakeMotionMinSec > 0 && cfg.FakeMotionMaxSec >= cfg.FakeMotionMinSec {
		go runFakeMotionRandom(ctx, workers, cfg.FakeMotionMinSec, cfg.FakeMotionMaxSec)
	} else if cfg.FakeMotionSec > 0 {
		go runFakeMotion(ctx, workers, time.Duration(cfg.FakeMotionSec)*time.Second)
	}

	if cfg.Headless {
		runHeadless(ctx, cancel, logQ)
		return
	}

	app := tview.NewApplication()
	cameraView := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	cpuView := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	logView := tview.NewTextView().SetDynamicColors(true).SetWrap(false).SetScrollable(true)

	cameraView.SetBorder(true).SetTitle(" Cameras ")
	cpuView.SetBorder(true).SetTitle(" CPU ")
	logView.SetBorder(true).SetTitle(" Motion / YOLO ")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(cameraView, 0, 1, false).
		AddItem(cpuView, 3, 0, false).
		AddItem(logView, 0, 3, true)

	app.SetRoot(layout, true)
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' || event.Rune() == 'Q' {
			cancel()
			app.Stop()
			return nil
		}
		return event
	})

	var logMu sync.Mutex
	var logLines []string

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-logQ:
				logMu.Lock()
				logLines = append(logLines, time.Now().Format("15:04:05")+" "+msg)
				if len(logLines) > 200 {
					logLines = logLines[len(logLines)-200:]
				}
				text := strings.Join(logLines, "\n")
				logMu.Unlock()
				app.QueueUpdateDraw(func() {
					logView.SetText(text)
				})
			}
		}
	}()

	sampler := &cpuSampler{}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pct := sampler.Percent()
				lines := make([]string, len(workers))
				for i, w := range workers {
					lines[i] = fmt.Sprintf("%s: %s", w.cfg.Name, w.Status())
				}
				camText := strings.Join(lines, "\n")
				cpuText := fmt.Sprintf("CPU: %.1f%%", pct)
				app.QueueUpdateDraw(func() {
					cameraView.SetText(camText)
					cpuView.SetText(cpuText)
				})
			}
		}
	}()

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		os.Exit(1)
	}
}

func runFakeMotion(ctx context.Context, workers []*CameraWorker, interval time.Duration) {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			workers[rnd.Intn(len(workers))].FakeMotion()
		}
	}
}

func runFakeMotionRandom(ctx context.Context, workers []*CameraWorker, minSec, maxSec int) {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	span := maxSec - minSec + 1

	for {
		wait := time.Duration(minSec+rnd.Intn(span)) * time.Second
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			workers[rnd.Intn(len(workers))].FakeMotion()
		}
	}
}

func runHeadless(ctx context.Context, cancel context.CancelFunc, logQ <-chan string) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		signal.Stop(sig)
		cancel()
	}()

	startAt := time.Now()
	sampler := &cpuSampler{}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fmt.Printf("%s [%3.0fs] cpu=%.1f%%\n", time.Now().Format("15:04:05"), time.Since(startAt).Seconds(), sampler.Percent())
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("%s cpu avg: %.1f%%  elapsed: %.0fs\n", time.Now().Format("15:04:05"), sampler.Percent(), time.Since(startAt).Seconds())
			return
		case msg := <-logQ:
			fmt.Printf("%s %s\n", time.Now().Format("15:04:05"), msg)
		}
	}
}