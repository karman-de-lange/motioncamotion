//go:build darwin

package main

// macOS VideoToolbox backend placeholder.
//
// Default `go build` on Darwin selects this file (not VPL).
// Universal core is shared; only FrameDecoder is platform-specific.
// Not implemented yet — init fails cleanly until VT decode is wired.
// See docs/UNIVERSAL_CORE.md.

func newFrameDecoder(codec int) (FrameDecoder, int) {
	_ = codec
	// Distinct from NVDEC stub (-100) for log triage.
	return nil, -101
}
