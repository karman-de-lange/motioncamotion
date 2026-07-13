# Universal core vs platform backends

Design rules so we do **not** fork motion / detect / YOLO logic per OS or GPU.
Platforms only supply **decode** (and later **infer acceleration**). Everything else stays one shared pipeline.

---

## Vocabulary (what each element means)

Confusion usually comes from mixing **streams**, **triggers**, **decode**, and **detect**. They are different layers.

### Streams (camera sides)

| Term | Meaning |
|------|---------|
| **Sub stream** | Low-res RTSP path. Always on for motion + looksee thumbs. Cheap. |
| **Main / primary stream** | High-res RTSP path. Kept as **compressed** access units (AUs) in a ring. Not fully decoded continuously. |
| **AU (access unit)** | One encoded video frame (or IDR+config) as annex-B bytes (H.264 or H.265). Portable; no GPU involved yet. |

### Triggers (why work happens)

| Term | Meaning |
|------|---------|
| **Motion trigger** | Sub-stream thumbs change enough (SAD + masks + sustain + cooldown) → fire an event. |
| **Fake motion** | Timer/debug path that injects the same *kind* of event without real motion. Same downstream. |
| **Detect event** | The shared object on `detectQ`: “this camera might have something; here is a main-ring snapshot (AUs) + optional sub RGB.” |

**Rule:** How you *wake* the pipeline (motion vs fake vs future ONVIF) must **not** change how YOLO sees the primary. Triggers only produce a standard event.

### Decode (platform-specific)

| Term | Meaning |
|------|---------|
| **Decode** | AU bytes → pixels (Y thumb and/or RGB). This is the only step that *must* know Intel VPL / NVDEC / VideoToolbox / etc. |
| **Decoder backend** | One implementation of a small interface (`Thumb`, `RGB`, free). Chosen by **build tag** or startup probe — not by if/else inside motion or YOLO. |
| **Looksee** | HTTP UI that asks the **same** sub decode path for a JPEG. Not a second app. Not a second decode architecture. |

Decode does **not** decide policy (“is this a person?”). It only turns AUs into frames the core can use.

### Detect (universal)

| Term | Meaning |
|------|---------|
| **Stage 1 (sub detect)** | Optional cheap YOLO (or filter) on **sub** RGB already related to the motion window. |
| **Stage 2 / primary detect** | YOLO on **main/primary** frames decoded **from the ring snapshot** for that event. This is the authoritative “was it real?” check. |
| **YOLO / infer** | Runs on RGB tensors. Must see the **correct primary pixels** for stage 2 — not a different camera, not a stale wrong GOP, not a software mock that disagrees with production. |
| **Infer backend** | OpenVINO CPU/GPU, ORT CUDA, CoreML, etc. Swappable **under** the same `Infer(rgb) → boxes` API. Does not own stream selection. |

### Buffering & output

| Term | Meaning |
|------|---------|
| **Main ring** | Rolling window of **main AUs** (compressed). Portable. |
| **Snapshot** | Slice of main AUs around the trigger (e.g. ~5s). Handed to the detect worker. |
| **Clip save** | Optional write of those AUs (or re-mux) after a positive detect. |

---

## The one picture

```
                    PLATFORM                         UNIVERSAL CORE
                    --------                         --------------

  RTSP sub  ──► AUs ──► [Decoder backend] ──► Y thumb ──► motion / masks
                              │                              │
                              └──► RGB ──────────────────────┼──► looksee JPEG
                                                             │
                                                             ▼
                                                      DetectEvent
                                                      (cam id + main AU
                                                       snapshot [+ sub RGB])
                                                             │
  RTSP main ──► AUs ──► ring ───────────────────────────────┘
                                                             │
                                                             ▼
                                                      detect worker
                                                             │
                              ┌──────────────────────────────┤
                              ▼                              ▼
                    [Decoder backend]              (same event, same AUs)
                    main AUs → RGB                           │
                              │                              ▼
                              └──────────► [Infer backend] ► YOLO boxes
                                           stage1 sub / stage2 primary
                                                             │
                                                             ▼
                                                      clip / log / TUI
```

- **Left column (decode/infer backends):** may differ per Mac / Linux Intel / Linux CUDA.
- **Right column (core):** one implementation everywhere. No platform `#ifdef` inside motion scoring, event shape, or “which AUs are primary.”

---

## Design rules (normative)

### 1. Primary detect is universal

- **Primary detect** always means: take the **main-ring AU snapshot from the DetectEvent**, decode those AUs to RGB, run stage-2 YOLO.
- Platform code must **not** redefine primary as “whatever the GPU preview shows,” a different RTSP URL, or a sub-stream upscale.
- If stage 2 cannot get correct primary RGB, that is a **backend/decode failure**, not a reason to invent a second detect path.

### 2. Triggers do not care about decode backend

- Motion, fake motion, or any future trigger only enqueues a **standard DetectEvent**.
- Decode is not “triggered differently” per platform. The worker always: event → decode main AUs → infer.
- Build tags / runtime backend selection affect **which library** implements `Decode(AU)→pixels`, not the control flow of detect.

### 3. Decode is a dumb, replaceable backend

- Core talks only to a narrow API, e.g. thumb / RGB (and preferably thumb+RGB from **one** decode when both are needed).
- Core never imports VPL, CUDA, or VideoToolbox types.
- Switching Intel → CUDA → Mac = new backend package + build tag (or probe), **not** a fork of `runSub` / YOLO worker logic.

### 4. YOLO must see correct primary

For stage 2, “correct primary” means:

1. Same **camera** as the event.
2. AUs from that camera’s **main** ring snapshot (time window tied to the trigger).
3. Decode of those AUs yields RGB that matches the main stream resolution/aspect the model expects (letterbox/fit policy is **one** shared policy, implemented once; backends only provide scaled RGB or full frame for a shared scaler).
4. Keyframe/GOP rules are handled so decode actually produces a picture (IDR + config AUs), but that is decoder/session duty — still behind the same API.

If YOLO “works” on a fake JPEG path that is not that snapshot, it is **not** primary detect.

### 5. Sub vs main stay specialized by role, not by platform

| Role | Stream | When decoded |
|------|--------|----------------|
| Motion continuous | sub | always (thumbs) |
| Looksee | sub | on demand / cached from sub path |
| Stage 1 YOLO | sub RGB | on event (optional) |
| Stage 2 YOLO | **main** AUs from ring | on event only |
| Long buffer | main compressed | always (no full decode) |

Platforms do not swap these roles.

### 6. Infer backend ≠ decode backend

- You may decode with NVDEC and infer on OpenVINO CPU (valid on RTX + no Intel GPU).
- You may decode with VPL and infer on OpenVINO GPU (Intel box).
- Config should separate **decode backend** from **infer device** so we do not overload `"device": "GPU"`.

### 7. One product binary, many backends

- Looksee, motion, and YOLO remain features of **`motioncamotion`**, not side scripts.
- Missing backend on a machine = report gap or build with the right tag — do not add a parallel “almost looksee” app.
- ffmpeg remains debug-only; not a production decode backend unless the project explicitly changes that rule.

### 8. Shared types cross the boundary

Only portable data crosses core ↔ backend:

- `[]byte` AUs, codec id  
- `[]byte` Y thumb (fixed thumb size policy)  
- `[]byte` RGB + width/height  
- detect boxes (label, score, rect)

No VAAPI surfaces, no CUDA device pointers, no CVPixelBuffer in the universal layer.

---

## Build tags (what they are *for*)

Build tags (or an equivalent registry) answer only:

> “Which **implementation** of Decoder / Infer is linked into this binary?”

They do **not** answer:

- When motion fires  
- What a DetectEvent contains  
- Whether stage 2 uses main or sub  
- How looksee URLs work  

### Decode backends (as coded)

| File | Constraint | Role |
|------|------------|------|
| `decoder.go` | always | `FrameDecoder` interface, AU helpers, primary `decodeRGBBest` |
| `decoder_vpl.go` + `vpl_decode.c` | default Linux: `!nvidia_nvdec && !darwin` | Intel oneVPL/VAAPI (**production**) |
| `decoder_nvdec.go` + `nvdec_decode.c` | `nvidia_nvdec && !darwin` | **NVIDIA NVDEC** (CUDA / WSL RTX) |
| `decoder_darwin.go` | `darwin` | VideoToolbox stub (rc `-101`) |

```bash
# Linux Intel (default) — current production path
go build -o motioncamotion .

# Linux NVIDIA (WSL/RTX) — NVDEC + OpenVINO CPU typical
export LD_LIBRARY_PATH=$HOME/opt/openvino/runtime/lib/intel64:$HOME/opt/openvino/runtime/3rdparty/tbb/lib:/usr/lib/wsl/lib:$LD_LIBRARY_PATH
export OPENVINO_LIB_DIR=$HOME/opt/openvino/runtime/lib/intel64
go build -tags nvidia_nvdec -o motioncamotion .
./motioncamotion cameras_live161.json   # looksee :8088

# macOS — automatically selects decoder_darwin.go (stub until VT is implemented)
go build -o motioncamotion .
```

Stub init codes are intentional so logs distinguish missing backends (`-100` NVDEC, `-101` Darwin) from real VPL errors.

Default production target remains **Linux + Intel VPL + OpenVINO** until NVDEC/VT backends are implemented and accepted.

**Infer (YOLO)** is still OpenVINO via `yolo_worker.go` / `ov_infer.c` (not yet split by platform). Decode and infer stay independent: e.g. future NVDEC + OpenVINO CPU is valid.

---

## Mental model in one sentence

**Triggers and policy are universal; pixels come from a plug-in decoder; primary YOLO always means “YOLO on RGB from this event’s main-ring AUs.”**

If a change makes Mac/CUDA/Intel disagree on *that* sentence, it violates these rules.

---

## Out of scope for this doc

- Concrete NVDEC / VideoToolbox APIs  
- Choosing OpenVINO vs ORT CUDA for infer  
- Thumb size / SAD thresholds  

Those are implementation follow-ons; they must still obey the rules above.
