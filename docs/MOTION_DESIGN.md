# Motion system design (thumb ring + temporal windows)

## Goals

- Detect **slow human motion** (walk, stand/wave) without outdoor thrash tests only.
- **Very low CPU** after decode (decode still owns cost).
- **Same logic** on Mac / Linux Intel / CUDA — only `FrameDecoder` is platform-specific.
- Kill single-frame noise (glitch, bird flash) via **time average**, not by coarsening the gate forever.

## Non-goals

- Decode redesign (VPL/NVDEC/VT stay backends).
- Full background models (MOG2) unless this fails.
- Main-stream involvement until after a DetectEvent.

## Architecture

```
sub RTSP → AU → FrameDecoder.Thumb → Y thumb (128×128)
                                      │
                                      ▼
                              thumbRing (pure Go, per cam)
                                      │
              mean(last A frames)  vs  mean(frames lag..lag+A-1)
                                      │
                              SAD % + blob + sustain + cooldown
                                      │
                                      ▼
                              DetectEvent → main ring snapshot → YOLO
```

| Layer | Where | Portable? |
|-------|--------|-----------|
| Pixels from AU | `FrameDecoder` | platform backend |
| Thumb ring + mean + SAD | Go | **yes** |
| Main AU ring | `ringBuffer` | yes (unchanged) |

## Thumb ring

- Circular buffer of recent **Y thumbs** only (not compressed AUs).
- Default capacity **32** (~0.5 MB at 128×128).
- On large time gap (`thumbGapRebase`): **clear** ring (avoid false 99% after stall).

## Temporal compare (replaces N vs N−1 only)

| Param | Default | Meaning @ ~10 fps |
|-------|---------|-------------------|
| `motion_avg_frames` (A) | **3** | Mean last 3 thumbs (~0.3 s) — kills 1-frame glitches |
| `motion_lag_frames` (L) | **8** | Older window starts ~0.8 s ago |
| Compare | mean(recent A) vs mean(older A at lag L) | “What changed over ~1 s” |

Humans move across that window; single-frame birds often don’t survive both averages + sustain.

## Gates (unchanged ideas, retunable)

1. SAD% ≥ `threshold * 100` (noise floor still 12 per cell before counting).
2. max blob ≥ `motion_min_blob`.
3. sustain consecutive **hits** (default 2).
4. per-cam cooldown.

## CPU budget (order of magnitude)

- Mean 3×128² ≈ 50k adds/cam/frame.
- SAD 128² ≈ 16k compares.
- At 10 fps × 2 cams: **negligible** vs NVDEC/VPL decode.

## Config

```json
"motion_avg_frames": 3,
"motion_lag_frames": 8,
"motion_sustain_frames": 2,
"motion_min_blob": 4,
"threshold": 0.015
```

## Why scrap pure N−1

Adjacent-frame SAD at 10 fps only sees **~100 ms** of motion. Distant person on `_3` stayed under 1.5% frame-to-frame even when empty→presence was clear. Lagged window matches that reality without dropping threshold into leaf-noise.
