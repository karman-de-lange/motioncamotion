# motioncamotion

Minimal Go RTSP stack for 16 Hikvision main streams with GPU H.265 decode and motion detection. No ffmpeg/go2rtc in this path. Hikvision ONVIF motion events are not an option.

## Project layout

| Path | Purpose |
|------|---------|
| `main.go` | TUI app — gortsplib receive, per-camera `Payloads` channel, `Subscribe()`/`Unsubscribe()`, noop when no subscribers |
| `cameras.json` | Camera config (currently 4 cams on NVR `10.3.2.150`, channels 101/201/301/401) |
| `cmd/vaapitest/` | VAAPI/oneVPL decode + motion comparison harness (not wired into main app yet) |
| `AGENTS.md` | Agent rules — minimal changes only |

## Main app (current)

- Receives RTP via gortsplib, reassembles H.265 access units.
- **No decoder, no motion detection** in main app yet.
- RTP callback: if `subs == 0`, return immediately; else `w.Payloads <- pkt.Payload` (no copy, no drain goroutine).
- Run: `go run . cameras.json`

## vaapitest harness (current)

Pipeline: RTSP → RTP/H.265 AU → **GPU decode** (oneVPL `libmfx-gen`, VAAPI) → Map Y plane → **CPU downscale to 32×32** → two motion scorers side-by-side.

Build & run 60s test:

```bash
go build -o /tmp/vaapitest ./cmd/vaapitest
timeout 60 /tmp/vaapitest cameras.json
```

### Files

- `vpl_decode.c` — `MFXInitialize` + `MFX_ACCEL_MODE_VIA_VAAPI`, HEVC decode to video memory, `FrameInterface->Map`, nearest-neighbor downscale
- `motion.go` — SAD (% changed pixels) and pHash + Hamming
- `main.go` — 4 parallel cameras, SIGTERM shutdown, per-cam stats

### Motion thresholds (vaapitest)

- **SAD**: ≥ 3% of pixels changed, noise floor 12 (ignore diffs ≤ 12)
- **Hamming**: ≥ 8 bits on 64-bit pHash

### Thumb size

`THUMB_W` × `THUMB_H` = **32×32** (`vpl_decode.h`, `motion.go`). Likely too small for reliable person/cat detection; next test should try 64×64 or 128×72.

## Hardware / decode setup

- Intel iGPU, `/dev/dri/renderD128`, user in `video` group
- `vainfo`: `VAProfileHEVCMain` supported
- oneVPL **dispatcher** failed (`MFXCreateSession`); **direct `libmfx-gen`** works:
  - `MFXInitialize` + `MFX_ACCEL_MODE_VIA_VAAPI`
  - `MFXVideoDECODE_DecodeHeader` before `MFXVideoDECODE_Init`
- Installed for testing: `libva-dev`, `libmfx-gen1.2`
- `intel_gpu_top` not installed

## CPU measurements (Linux `top` = per-core %)

| Stage | 4 cams (approx) |
|-------|-----------------|
| Receive only, noop callback | ~5% per-core (~1.25% per cam) |
| Receive + channel (no subscribers) | ~5% |
| VAAPI decode, GPU-only frames (no readback) | ~8% per-core (~7.8% avg over 60s) |
| VAAPI decode + RAM copy (`OUT_SYSTEM_MEMORY`) | ~20% per-core (~19.9% avg over 60s) |
| Decode + Map Y + 32×32 downscale + SAD/pHash | ~41% per-core (60s run) |
| 16 cams linear guess (GPU-only decode) | ~4% per-core average on 8 cores |

**Key finding:** RAM copy was the killer. GPU-only decode dropped ~20% → ~8%. Map/readback for motion adds significant CPU (~41% with current path).

## VPP downscale (blocked)

Attempted oneVPL VPP resize (GPU in → system out, 32×32). `MFXVideoVPP_Query` returns `MFX_ERR_UNSUPPORTED` on this stack (`thumb error -17`).

**Workaround in use:** Map decoded GPU surface, CPU nearest-neighbor downscale to 32×32. Full Y-plane readback is the cost driver, not the small thumb size.

## Motion comparison — 60s, 4 cameras (2026-06-08)

Thresholds: `sad >= 3%` (noise>12), `hamming >= 8`.

| Camera | Frames | sad_triggers | hash_triggers | max_sad | max_hamming |
|--------|--------|--------------|---------------|---------|-------------|
| hik150_1 | 402 | 2 | 60 | 54.6% | 36 |
| hik150_2 | 398 | 1 | 5 | 99.4% | 26 |
| hik150_3 | 398 | 1 | 93 | 99.6% | 34 |
| hik150_4 | 398 | 1 | 177 | 99.0% | 36 |

**cpu avg: 41.5%**

### Feed context (important for interpreting results)

- **Clock OSD** on cameras — ticking digits cause small periodic change; Hamming picks this up more than SAD.
- **hik150_1** — broken cable, static Hikvision “no signal” default screen, very noisy analog grain but no real motion. Best false-positive test case.
- **hik150_2** — quietest (5 hash triggers); clock barely visible in 32×32 thumb or cleaner feed.
- **hik150_3/4** — noisier; Hamming fires constantly (93/177 triggers) on mostly static scenes.

### Conclusions so far

| Method | Analog noise | Static broken feed (hik150_1) | Clock OSD |
|--------|--------------|-------------------------------|-----------|
| **SAD + noise floor** | Good — 1–2 triggers/60s | Good — 2 triggers | Mostly ignores |
| **pHash + Hamming** | Bad — 5–177 triggers/60s | Bad — 60 triggers on static | Sensitive |

**SAD + noise floor is the better default for analog CCTV.** Hamming at threshold 8 is too sensitive; would need higher threshold (12–14), dHash instead of pHash, or OSD mask.

**Not yet tested:** real motion (person walking, cat running past). 32×32 thumb may miss small/fast movers; bump to 64×64 or 128×72 before staging motion tests. CPU cost dominated by Map, not thumb size.

## Known issues

- Per-RTP byte diff in early experiments was wrong (RTP fragments ≠ frames).
- Occasional `thumb error -95` on first frame per camera (logged once); decode continues.
- Main app has no VAAPI integration yet.
- No motion events / TUI log in main app (removed during CPU profiling).

## Next steps

1. **Motion validation** — walk through frame, cardboard cat, etc.; log `max_sad` / `max_hamming` not just trigger counts.
2. **Bump thumb size** — try 64×64 or 128×72 in `vpl_decode.h` + `motion.go`.
3. **Optional:** tune `hashThreshold` or try dHash if Hamming still wanted as secondary signal.
4. **Optional:** fix GPU VPP downscale to cut Map cost (currently unsupported on gen).
5. **Wire winner into main app** — only when motion approach is validated.

## Scale target

16 Hikvision cameras, main streams, low CPU. GPU-only decode path looks viable (~4% per-core average estimated for 16 cams on 8 cores). Motion readback path needs work (VPP or smarter sampling) before 16-cam deployment.