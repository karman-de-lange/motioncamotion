# motioncamotion — status & notes

Snapshot as of **2026-06-12**. Reference doc for decisions and open questions.

## Verdict

**Worth continuing.** Not a pipe dream.

Goal: beat Frigate’s limitation — **motion on sub-stream, buffer main-stream, YOLO on demand, save only on valid detection**. Custom Go + oneVPL/VAAPI, no ffmpeg/go2rtc in the pipeline.

---

## What’s proven

| Piece | Result |
|-------|--------|
| Dual-stream (7 cams) | ~17–21% CPU steady |
| Sub motion (SAD + noise floor) | ~1 false trigger/cam/min on static; hik2 noisier (clock OSD) |
| Main ring buffer | **H.265 encoded** access units in RAM (~1 MB per 5s per cam at 1080p), **not** decoded frames |
| Motion → snapshot → `detectQ` → serial worker | Channel architecture works; FIFO backpressure OK |
| Main decode on event | ~34–42 ms per 5s burst; small CPU bump when triggered every few seconds |
| 7-cam bench (fake motion + clip save) | **16.9% CPU avg** (125s), no ffmpeg |
| **Two-stage YOLO harness (`stagetest`)** | SAD on sub → infer sub → infer main; stable on `hik1` |
| **OpenVINO infer in Go (no Python)** | Model loaded once at startup; ~170–195 ms/stage on CPU |

**Headline:** ~18% for 7 cams = receive + sub decode + motion + main buffering. Main decode + YOLO are **event-only**, one at a time.

---

## Architecture (as coded)

**Platform-universal rules** (primary detect, decode backends, vocabulary): see [`UNIVERSAL_CORE.md`](UNIVERSAL_CORE.md).

### Main app (`motioncamotion`)

```
Per camera:
  runMain()  → RTSP main → ring buffer (H.265 AUs, 10s window, 200MB cap)
  runSub()   → RTSP sub  → GPU decode → 32×32 thumb → SAD

On SAD >= threshold:
  ring.snapshot(5s) → DetectEvent → detectQ (global FIFO)
  runYOLOWorker (single goroutine): GPU decode segments → (YOLO stub) → optional clip save

Channels:
  detectQ  — written by any cam on motion/fake; read by one YOLO worker
  logQ     — TUI / headless log
```

`yolo_worker.go` still **decodes only** — inference not wired into main app yet.

**Buffer format:** compressed H.265 annex-B bitstream. Sub stream prepends VPS/SPS/PPS; main ring stores raw AUs only (same as production path).

### Stagetest harness (`cmd/stagetest/`)

Standalone two-stage pipeline (one cam):

```
RTSP main → ring buffer (5s snapshot, 10s trim)
RTSP sub  → live VPL decoder → RGB 640×640 + 32×32 thumb from RGB → SAD

On SAD or fake timer:
  stage1: cached sub RGB → OpenVINO YOLO (person/car/cat)
  stage2: main ring snapshot → fresh VPL decoder → RGB 640×640 → OpenVINO YOLO
```

Build & run:

```bash
go build -o stagetest ./cmd/stagetest/
./stagetest cameras_stage.json          # hik150_1 when 10.3.2.150 is up
./stagetest cameras_stage_hik1.json      # hik1 on 10.3.1.50 (working fallback)
```

**Infer backend:** `ov_infer.c` — OpenVINO C API via CGO. No Python subprocess. Session persists for process lifetime.

**Legacy:** `scripts/yolo_infer.py` remains in repo from first wiring test; `stagetest` no longer calls it.

---

## YOLO / inference (2026-06-12)

| Item | Detail |
|------|--------|
| Model | `models/yolov8n.onnx` (Ultralytics v8.4.0 asset) |
| Classes | person (0), car (2), cat (15) |
| Input | 640×640 RGB uint8 from VPL `vpl_decoder_rgb()` |
| Sub conf / main conf | 0.5 / 0.65 (configurable in JSON) |
| Runtime | OpenVINO 2025.4.0 (Intel apt repo on Debian 13) |
| Device config | `"device": "GPU"` in JSON — **currently falls back to CPU** |
| Plugins | Loaded from `/usr/lib/openvino-2025.4.0/` (apt does not auto-register) |

### Measured timings (`stagetest`, OpenVINO CPU, `hik1`)

| Step | Time |
|------|------|
| Sub infer (stage1) | ~170–195 ms |
| Main decode (stage2) | ~41–42 ms |
| Main infer (stage2) | ~178–191 ms |
| Full two-stage event | ~400–420 ms |

Compare to old Python path: ~500 ms **per stage** (process spawn + `InferenceSession` reload each call).

### GPU infer (not yet working)

- VPL decode uses **VAAPI** on Intel iGPU — works.
- OpenVINO `"GPU"` compile fails silently; wrapper retries **CPU**.
- Likely missing **Intel OpenCL** stack (`intel-opencl-icd` etc.); only generic `ocl-icd-libopencl1` installed.
- Fix is system packages, not app code — plugin `.so` files are present.

---

## Camera / network status (2026-06-12)

| Host | Status | Notes |
|------|--------|-------|
| `10.3.1.50` (hik1–3) | **Reachable** | stagetest OK on hik1; ping ~12 ms |
| `10.3.2.150` (hik150_1–4) | **Offline** | ping 100% loss; RTSP timeout — NVR/cam down again |

Credentials: `10.3.1.50` → `homeassistant` / `Karman1122334455`; `10.3.2.150` → `homeassistant` / `Karman112233`.

**Operational notes:**

- Stop Frigate before direct RTSP tests (stream lock / contention).
- Hammering `10.3.1.50` caused illegal-login lockout in earlier tests.
- Occasional RTP packet loss under load; early fake triggers sometimes hit **main decode failed** before ring has enough AUs (~70+ needed).

---

## System dependencies (inference)

Installed via Intel apt repo (`ubuntu24` line on Debian 13):

- `libopenvino-2025.4.0`
- `libopenvino-dev-2025.4.0`
- `libopenvino-intel-cpu-plugin-2025.4.0`
- `libopenvino-intel-gpu-plugin-2025.4.0`
- `libtbb-dev` (linker symlink for `-ltbb`)

Decode (unchanged): `libmfx-gen`, VAAPI / `iHD_drv_video.so`.

---

## What ffmpeg was doing (removed)

Only used in an early clip-save experiment: transcode buffer → H.264 mp4 for playback. That **re-encoded** and masked broken elementary streams. It is **not allowed** in this project.

Clips now save raw `.h265` (IDR-aligned, raw AUs, no SDP param prepend). Play with VLC/ffplay on Mac; QuickTime needs a proper muxer later if wanted.

---

## Remaining risks

1. **Real motion untested** — no walk-through (person/cardboard cat). Static scene scores 0.00 as expected.
2. **OSD / analog noise** — hik1/hik2 clock triggers; hik2 had 72 SAD triggers/60s in early tests. Need mask and/or debounce.
3. **YOLO not in main app** — works in `stagetest`; `runYOLOWorker` still stub (decode + optional clip only).
4. **Keyframe discipline** — ring stores raw AUs; snapshots must start at IDR for decode/playback (partially fixed in `hevc_clip.go`).
5. **NVR lockout** — hammering RTSP on 10.3.1.50 caused illegal-login lockout; stagger connections, don’t run Frigate + stress test together.
6. **16 real cams** — 7 looks fine; full dual-stream + YOLO bursts not measured on 16 physical cameras yet.
7. **OpenVINO on CPU** — ~400 ms/event serial cap ≈ ~2.5 two-stage events/s; GPU or lower-res model may be needed at scale.
8. **10.3.2.150 reliability** — frequent offline periods; keep `cameras_stage_hik1.json` for dev when NVR is down.

---

## Sensible next steps

1. Install Intel OpenCL → verify OpenVINO `device=GPU` and re-measure infer ms.
2. Walk-through on one cam — real person/car/cat; tune `sub_conf` / `main_conf`; set `force_stage2: false`.
3. Fold two-stage logic from `stagetest` into `runYOLOWorker` + extend `DetectEvent` with sub RGB.
4. Per-cam motion debounce (e.g. 2s cooldown) so noisy cams don’t flood `detectQ`.
5. Bump thumb 32×32 → 64×64 if walk-through misses movers.
6. `git init` — tarball backup exists; version control before main-app YOLO merge.

---

## Motion detection — problem statement

**Today:** full-frame SAD on 32×32 thumb. Any widespread change (wind in trees, shadows, clock OSD, analog grain) can trip threshold even when there is no person/car blob.

**Want:** motion concentrated in a **blob** (person, car), not diffuse screen-wide flicker.

Frigate-style masks on full resolution don’t map trivially to a 32×32 byte image. Need to ponder.

---

## Motion / mask — options to ponder

### A. Mask before downscale (preferred to explore)

1. Decode sub to full-res Y (or moderate size, e.g. 320×180).
2. Apply **polygon mask** in full/sub coordinates (same polygons as Frigate config — scale points to sub stream resolution).
3. **Zero out** (or ignore) masked pixels — OSD corners, sky, trees if polygon excludes them.
4. Downscale masked frame → 32×32 (or 64×64).
5. SAD only on unmasked thumb pixels (or entire thumb after mask zeroed).

Mask lives in **sub-stream pixel space**. Frigate masks are normalized 0–1; multiply by sub width/height.

### B. Mask after downscale (coarse)

1. Downscale full frame → 32×32.
2. Apply a **tiny bitmap mask** (e.g. 32×32 bitmask per cam, hand-tuned or auto-downsampled from Frigate polygon).

Simple but crude: a 10-pixel OSD corner on 1080p becomes ~1 pixel at 32×32; might be enough to kill clock triggers.

### C. Block / blob motion (not global SAD)

1. Downscale to thumb (32×32 or 64×64).
2. Threshold per-pixel change → **binary motion map**.
3. Cluster connected components (flood fill) or count changed cells in a grid.
4. Trigger only if one cluster exceeds **min area** and **max area** (person-sized blob), not if >50% of pixels changed (wind).

Handles “whole screen shimmer” vs “compact blob” without full-res mask if blob rules are tuned.

### D. Two-stage gate (implemented in stagetest)

1. Cheap SAD on 32×32 → “something changed”.
2. YOLO on sub RGB (~0.5 conf).
3. If pass (or `force_stage2` for testing) → YOLO on main buffer decode (~0.65 conf).

Keeps CPU low; defers expensive work. **Now proven in `stagetest`; not yet in main app.**

### E. Null motion blocks on frame before compare

Same as A/C combined:

- Either zero masked regions on Y plane before downscale, **or**
- After pixel-diff, **ignore** changed pixels that fall in masked thumb bits.

Equivalent if mask is aligned; pre-nulling is easier to reason about.

---

## Open questions

| Question | Notes |
|----------|-------|
| Sub vs main for mask coords? | Mask on **sub** (motion path). Main only for YOLO quality. |
| Thumb size final? | 32×32 cheap; 64×64 or 128×72 for blob detection worth A/B. |
| Frigate mask import? | `config.yml` has normalized polygons per cam — could parse into sub-pixel polygons. |
| Debounce vs blob? | Both: debounce reduces rate; blob/mask reduces false positives. |
| Wind in trees | Polygon exclude **or** blob min/max area **or** require cluster not touching frame edges. |
| OpenVINO GPU vs CPU at 16 cams? | Need GPU infer + serial worker throughput math before committing. |

---

## CPU reference (measured)

| Scenario | CPU avg (approx) |
|----------|------------------|
| Receive only (4 cams, noop) | ~5% |
| 4 sub motion decode | ~27% |
| 7 sub motion | ~41% → later ~21% (different paths) |
| 7 dual + buffer, no clips | ~19% |
| 7 dual + fake motion + h265 clips | **~17%** |
| 7 dual + fake + decode bursts + ffmpeg clips (old) | ~21% |
| 16 duplicated sub decode | ~115% |
| stagetest 1 cam + OpenVINO CPU infer (event bursts) | not profiled yet; infer is ~400 ms/event off hot path |

Main buffer receive is cheap. Sub decode + Map is the steady cost. Main decode + YOLO are burst-only.

---

## Config / bench files

| File | Purpose |
|------|---------|
| `cameras.json` | Production 7-cam main+sub |
| `cameras_stage.json` | stagetest: hik150_1, `force_stage2`, OpenVINO |
| `cameras_stage_hik1.json` | stagetest: hik1 on 10.3.1.50 (use when .150 offline) |
| `cameras_hik1_clip.json` | Single-cam clip test |
| `cameras_all_clip_bench.json` | 7-cam random fake motion + clips |
| `cameras_bench.json` | Headless CPU bench |
| `cameras_dual_7.json` | Dual-stream bench configs |
| `models/yolov8n.onnx` | YOLOv8n ONNX for infer |

Headless: `"headless": true`. Fake motion (main app): `"fake_motion_min_sec"` / `"fake_motion_max_sec"`. Stagetest fake: `"fake_motion_sec"`. Clips: `"clip_dir"`, `"clip_cams"`.

---

## Backup

`motioncamotion-backup-20260611.tar.gz` — source + configs; excludes `clips/` and built binary.

---

## Hard constraints (agents & humans)

- **No workarounds unless explicitly approved.** Do not fake the app when the real stack is missing.
- **ffmpeg:** OK for **debugging only** (ad-hoc stream probes). **Never** in the app, harnesses that are part of the stack, or any product/runtime path. Same for go2rtc. Production decode = platform `FrameDecoder` backend (default: Intel VPL); never ffmpeg.
- **Looksee / motion / YOLO** = the real `motioncamotion` binary only. Not substitute apps or snapshot proxies posing as looksee. Primary detect is universal (main-ring AUs → decode → YOLO); see `UNIVERSAL_CORE.md`.
- **Build/run target (production today):** Linux + Intel oneVPL (`libmfx-gen`, VAAPI) + OpenVINO. **NVDEC** (`-tags nvidia_nvdec`) is implemented for CUDA/WSL (RTX). Darwin VideoToolbox remains a stub. Do not fake looksee when decode init fails.
- If something does not build or cannot decode: fix or report the real dependency gap — do not paper over it.

---

## One-line summary

**Decode + motion + buffering viable for 7–16 cams; two-stage YOLO proven in `stagetest` with OpenVINO-in-Go (~400 ms/event on CPU). Next: GPU infer, walk-through validation, merge into main app.**