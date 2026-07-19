// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build darwin

package av

import "testing"

// VideoToolbox encoders default to hardware-only. On a host with no usable hardware
// encode session (a headless/virtualized CI runner, or a machine whose HW encoder is
// absent or busy) avcodec_open2 then fails with "Cannot create compression session",
// which is what stalled the macOS CI leg. OpenEncoder now defaults allow_sw=1 for any
// *_videotoolbox codec so the open falls back to VideoToolbox's software encoder.
//
// This test verifies both halves on any macOS host:
//   - forcing the software path (require_sw=1) opens a compression session — proving the
//     fallback target the default now enables is actually viable in this libav build;
//   - opening with default options succeeds (hardware where present, software otherwise).
//
// It exercises the exact operation that failed in CI (the encoder open), independently of
// whether this particular host has a hardware encoder.
func TestVideoToolboxEncoderOpensViaSoftware(t *testing.T) {
	const codec = "h264_videotoolbox"
	if !FindEncoder(codec) {
		t.Skipf("%s not present in this libav build", codec)
	}

	base := EncoderOptions{
		CodecName: codec,
		Width:     64,
		Height:    64,
		PixFmt:    0, // AV_PIX_FMT_YUV420P
		FrameRate: [2]int{30, 1},
		TimeBase:  [2]int{1, 30},
	}

	// Force the software encoder — the same path allow_sw=1 lands on when no hardware
	// session is available. If this cannot open, allow_sw=1 would not help either.
	sw := base
	sw.ExtraOpts = map[string]string{"require_sw": "1"}
	enc, err := OpenEncoder(sw)
	if err != nil {
		t.Fatalf("software VideoToolbox open failed — fallback path not viable: %v", err)
	}
	enc.Close()

	// Default options: OpenEncoder injects allow_sw=1, so this must open on any host
	// (hardware when present, software otherwise) rather than hard-failing HW-only.
	enc, err = OpenEncoder(base)
	if err != nil {
		t.Fatalf("default VideoToolbox open failed (allow_sw fallback did not apply): %v", err)
	}
	enc.Close()
}
