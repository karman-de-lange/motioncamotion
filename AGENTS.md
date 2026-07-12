# AGENTS.md

Rules for working in this project. Follow strictly.

## Scope

- Do **only** what was asked. Nothing more.
- Implement the **minimum** change that satisfies the request.
- If the request is ambiguous, ask — do not guess and add extras.

## No workarounds (unless user explicitly approves)

- **Do not** paper over a real blocker (wrong OS, missing oneVPL/OpenVINO, can’t build) by faking the app. State the blocker; fix the real path or stop and ask.
- **Do not** invent parallel “looksee” / motion / RTSP paths outside the real binary.
- Workarounds, fallbacks, and “just in case” logic require **explicit user approval** first. Silence is not approval.

### ffmpeg / go2rtc

- **ffmpeg is fine for debugging only** (ad-hoc shell: probe a stream, one-shot frame, check UDP vs TCP). Never commit it into the product path.
- **Never** use ffmpeg or go2rtc **in the app**, in libraries the app imports, in `cmd/*` harnesses that are part of the stack, in runtime helpers, or in any code that ships as “how the system works.”
- Production decode path is **only** the project’s VPL/Go stack unless the user changes that rule.

## Runtime truth

- Looksee, motion, and YOLO are features of the **real app** (`go build` / `./motioncamotion`), not side scripts that pretend to be the app.
- That binary needs **Linux + Intel oneVPL (`libmfx-gen`/VAAPI) + OpenVINO**. It does not build or run as designed on macOS without that stack.
- If the environment cannot run the real app, say so. Do not replace it with a fake app.

## Do not

- Add fallbacks, workarounds, or "just in case" logic unless explicitly requested.
- Fix, refactor, or improve things that were not asked for.
- Add defensive code (extra copies, buffer pools, drain goroutines, error handling beyond basics) unless requested.
- Explore tangents (e.g. alternate configs, sub-streams, extra benchmarks) unless asked.
- Delete or change unrelated code.
- Add features "for later" or "while we're here."

## Do

- Match existing code style when editing.
- Say clearly when a request would require something extra (e.g. buffer reuse risk) — let the user decide.
- Measure or test only what the user asked to verify.
- When blocked: name the missing dependency/platform and what would fix it on the real path.

## Examples

**Asked:** "Move to channel, nothing else."
**Do:** RTP callback → channel. Stop.
**Don't:** copy per packet, drain goroutine, motion removal beyond what's needed, TUI changes, benchmarks.

**Asked:** "What does stream receiver use?"
**Do:** One measurement, one number.
**Don't:** Sub-stream comparison, multiple benchmark files, profiling rabbit hole.