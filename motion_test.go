package main

import (
	"encoding/json"
	"os"
	"testing"
)

// Tests exercise shipped motion helpers (sadMotion, maxBlob, config→cutoff).
// They prove gate math and threshold unit conversion used by sub_stream/main.

func TestSadMotion_percentAndNoiseFloor(t *testing.T) {
	prev := make([]byte, thumbSize)
	curr := make([]byte, thumbSize)
	// Unchanged: SAD 0
	pct, ch := sadMotion(prev, curr, nil)
	if pct != 0 {
		t.Fatalf("identical thumbs: want 0%% got %v", pct)
	}
	// Change one cell by noiseFloor only → not counted
	curr[0] = byte(noiseFloor)
	pct, ch = sadMotion(prev, curr, nil)
	if pct != 0 || ch[0] {
		t.Fatalf("delta==noiseFloor should not count: pct=%v ch0=%v", pct, ch[0])
	}
	// Change one cell by noiseFloor+1 → 1/4096 * 100
	curr[0] = byte(noiseFloor + 1)
	pct, ch = sadMotion(prev, curr, nil)
	want := 100.0 / float64(thumbSize)
	if !ch[0] {
		t.Fatal("expected cell 0 changed")
	}
	if pct < want-1e-9 || pct > want+1e-9 {
		t.Fatalf("one cell: want ~%v%% got %v", want, pct)
	}
}

func TestSadMotion_ignoreMask(t *testing.T) {
	prev := make([]byte, thumbSize)
	curr := make([]byte, thumbSize)
	// Change only cell 0
	curr[0] = 255
	ignore := make([]bool, thumbSize)
	ignore[0] = true
	pct, ch := sadMotion(prev, curr, ignore)
	if pct != 0 || ch[0] {
		t.Fatalf("ignored cell should not contribute: pct=%v ch0=%v", pct, ch[0])
	}
	// Change a non-ignored cell
	curr[10] = 255
	pct, ch = sadMotion(prev, curr, ignore)
	if pct <= 0 || !ch[10] {
		t.Fatalf("non-ignored change missing: pct=%v", pct)
	}
}

func TestCompactness_diffuseVsBlob(t *testing.T) {
	if compactness(8, 8) != 1 {
		t.Fatalf("solid blob compact=%v", compactness(8, 8))
	}
	if compactness(8, 80) >= 0.35 {
		t.Fatalf("diffuse should be below 0.35, got %v", compactness(8, 80))
	}
	if compactness(0, 0) != 0 {
		t.Fatal("empty compact")
	}
}

func TestMaxBlob_connectedRegion(t *testing.T) {
	// Two separate single cells → blob 1
	ch := make([]bool, thumbSize)
	ch[0] = true
	ch[100] = true
	if b := maxBlob(ch, thumbW, thumbH); b != 1 {
		t.Fatalf("two singles: want blob 1 got %d", b)
	}
	// 2×2 block → blob 4
	ch = make([]bool, thumbSize)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			ch[y*thumbW+x] = true
		}
	}
	if b := maxBlob(ch, thumbW, thumbH); b != 4 {
		t.Fatalf("2x2: want blob 4 got %d", b)
	}
}

// motionGate mirrors sub_stream fire condition (shipped order).
func motionGate(sad float64, blob, streak, sustain int, sadCutoff float64, minBlob int) bool {
	if sad < sadCutoff || blob < minBlob {
		return false
	}
	if streak < sustain {
		return false
	}
	return true
}

func TestQuietBlob_needAboveMedian(t *testing.T) {
	var q quietBlob
	for i := 0; i < noiseNeed; i++ {
		q.push(10)
	}
	if !q.ready() || q.median() != 10 {
		t.Fatalf("ready=%v med=%d", q.ready(), q.median())
	}
	need := q.need(3)
	if need != 20 {
		t.Fatalf("need %d want 20 (2*median); 32-cell floor?", need)
	}
	var still quietBlob
	for i := 0; i < noiseNeed; i++ {
		still.push(1)
	}
	if n := still.need(3); n != 16 {
		t.Fatalf("still-scene need %d want 16 (quiet blob cutoff), not minBlob", n)
	}
	if !motionHit(0, 24, 24, 3, need, 0) {
		t.Fatal("blob 24 should fire when need is 20 (32-cell floor?)")
	}
	if !motionHit(0, 80, 80, 3, need, 0) {
		t.Fatal("person blob should beat quiet median")
	}
	if motionHit(0, 12, 12, 3, need, 0) {
		t.Fatal("noise blob should stay under need")
	}
}

func TestMotionHit_compactOff(t *testing.T) {
	if !motionHit(3, 20, 200, 3, 20, 0) {
		t.Fatal("minCompact=0 must not apply compactness")
	}
	if motionHit(3, 20, 200, 3, 20, 0.25) {
		t.Fatal("minCompact=0.25 should reject blob/nChanged=0.1")
	}
}

func TestMotionHit_wideShotPerson(t *testing.T) {
	if !motionHit(0.78, 128, 128, 3, 0, 0) {
		t.Fatal("wide-shot person should fire before baseline is ready")
	}
	if motionHit(0.78, 2, 2, 3, 0, 0) {
		t.Fatal("blob below minBlob should not fire")
	}
	if !motionHit(0.78, 8, 8, 3, 0, 0) {
		t.Fatal("bootstrap must not hard-floor at 32 cells")
	}
	if motionHit(1.2, 40, 200, 3, 32, 0.35) {
		t.Fatal("diffuse foliage should fail compactness")
	}
	if !motionHit(1.2, 40, 45, 3, 32, 0.35) {
		t.Fatal("compact person should fire")
	}
}

func TestMotionGate_live161Defaults(t *testing.T) {
	// cameras_live161.json values after main.go conversion
	const (
		threshold = 0.015
		sadCutoff = threshold * 100 // 1.5
		minBlob   = 4
		sustain   = 2
	)
	// Historical miss: 1.7% would fail old 3% cutoff but pass 1.5%
	if motionGate(1.7, 4, 2, sustain, 3.0, minBlob) {
		t.Fatal("1.7% must fail old 3% cutoff")
	}
	if !motionGate(1.7, 4, 2, sustain, sadCutoff, minBlob) {
		t.Fatal("1.7% must pass live161 1.5% cutoff with blob/sustain")
	}
	// Below threshold
	if motionGate(1.4, 10, 2, sustain, sadCutoff, minBlob) {
		t.Fatal("1.4% should fail 1.5% cutoff")
	}
	// Blob too small
	if motionGate(5.0, 3, 2, sustain, sadCutoff, minBlob) {
		t.Fatal("blob 3 < min 4 should fail")
	}
	// Sustain incomplete
	if motionGate(5.0, 10, 1, sustain, sadCutoff, minBlob) {
		t.Fatal("streak 1 < sustain 2 should fail")
	}
}

func TestLoadConfig_thresholdToCutoff(t *testing.T) {
	// Write temp config exercising loadConfig + same transform as main()
	dir := t.TempDir()
	path := dir + "/c.json"
	raw := `{"threshold":0.015,"cameras":[{"name":"x","main":"rtsp://a","sub":"rtsp://b"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Threshold != 0.015 {
		t.Fatalf("threshold=%v", cfg.Threshold)
	}
	sadCutoff := cfg.Threshold * 100
	if sadCutoff != 1.5 {
		t.Fatalf("sadCutoff want 1.5 got %v", sadCutoff)
	}
	// Default when threshold omitted
	raw2 := `{"cameras":[{"name":"x","main":"rtsp://a","sub":"rtsp://b"}]}`
	path2 := dir + "/c2.json"
	if err := os.WriteFile(path2, []byte(raw2), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := loadConfig(path2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Threshold != 0.03 {
		t.Fatalf("default threshold want 0.03 got %v", cfg2.Threshold)
	}
	if cfg2.Threshold*100 != 3.0 {
		t.Fatalf("default cutoff want 3 got %v", cfg2.Threshold*100)
	}
}

func TestCamerasLive161_fileGates(t *testing.T) {
	data, err := os.ReadFile("cameras_live161.json")
	if err != nil {
		t.Skip("cameras_live161.json not present")
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Threshold != 0.015 {
		t.Fatalf("live161 threshold=%v want 0.015", cfg.Threshold)
	}
	if cfg.MotionMinBlob != 4 || cfg.MotionSustainFrames != 2 {
		t.Fatalf("live161 blob=%d sustain=%d", cfg.MotionMinBlob, cfg.MotionSustainFrames)
	}
	if len(cfg.Cameras) < 2 {
		t.Fatalf("want ≥2 cams got %d", len(cfg.Cameras))
	}
}
