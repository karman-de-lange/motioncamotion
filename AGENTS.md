# AGENTS.md

Rules for working in this project. Follow strictly.

## Scope

- Do **only** what was asked. Nothing more.
- Implement the **minimum** change that satisfies the request.
- If the request is ambiguous, ask — do not guess and add extras.

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

## Examples

**Asked:** "Move to channel, nothing else."
**Do:** RTP callback → channel. Stop.
**Don't:** copy per packet, drain goroutine, motion removal beyond what's needed, TUI changes, benchmarks.

**Asked:** "What does stream receiver use?"
**Do:** One measurement, one number.
**Don't:** Sub-stream comparison, multiple benchmark files, profiling rabbit hole.