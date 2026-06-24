// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

package processors

import (
	"context"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

const rawFixture = "../raw/testdata/sample.dng"

func TestRawDecodeInitAndStreamInfo(t *testing.T) {
	r := &RawDecode{}
	if err := r.Init(map[string]any{"input": rawFixture}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer r.Close()

	si, err := r.OutputStreamInfo()
	if err != nil {
		t.Fatalf("OutputStreamInfo: %v", err)
	}
	if si.Type != av.MediaTypeVideo {
		t.Errorf("Type = %v, want video", si.Type)
	}
	if si.PixFmt != av.PixFmtRGBA() {
		t.Errorf("PixFmt = %d, want RGBA %d", si.PixFmt, av.PixFmtRGBA())
	}
	if si.Width <= 0 || si.Height <= 0 || si.Width <= si.Height {
		t.Errorf("dims = %dx%d, want landscape positive", si.Width, si.Height)
	}
	if got := r.OutputFrameCount(); got != 1 {
		t.Errorf("OutputFrameCount = %d, want 1", got)
	}
}

func TestRawDecodeRunEmitsOneFrame(t *testing.T) {
	r := &RawDecode{}
	if err := r.Init(map[string]any{"input": rawFixture}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer r.Close()

	var frames []*av.Frame
	send := func(f *av.Frame) error { frames = append(frames, f); return nil }
	if err := r.Run(context.Background(), send); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("emitted %d frames, want 1", len(frames))
	}
	f := frames[0]
	defer f.Close() // send succeeded → ownership transferred to us

	if f.Width() != r.w || f.Height() != r.h {
		t.Errorf("frame %dx%d, want %dx%d", f.Width(), f.Height(), r.w, r.h)
	}

	// The develop must not be a flat/black frame: some pixel differs from the first.
	plane := f.Plane(0)
	if len(plane) < 8 {
		t.Fatalf("empty plane")
	}
	uniform := true
	for i := 4; i+4 <= len(plane); i += 4 {
		if plane[i] != plane[0] || plane[i+1] != plane[1] || plane[i+2] != plane[2] {
			uniform = false
			break
		}
	}
	if uniform {
		t.Error("emitted frame is uniform — develop produced a flat frame")
	}
}

func TestRawDecodeInitErrors(t *testing.T) {
	if err := (&RawDecode{}).Init(map[string]any{}); err == nil {
		t.Error("Init without input should error")
	}
	if err := (&RawDecode{}).Init(map[string]any{"input": "photo.jpg"}); err == nil {
		t.Error("Init with non-RAW input should error")
	}
}
