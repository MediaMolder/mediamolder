// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

const testPixFmtYUV420P = 0 // AV_PIX_FMT_YUV420P

func TestNewVideoFramePlaneGeometry(t *testing.T) {
	f, err := NewVideoFrame(6, 4, testPixFmtYUV420P)
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer f.Close()

	if f.NumPlanes() != 3 {
		t.Fatalf("NumPlanes = %d, want 3", f.NumPlanes())
	}
	if f.Width() != 6 || f.Height() != 4 {
		t.Fatalf("dims = %dx%d, want 6x4", f.Width(), f.Height())
	}
	// Luma is full size; chroma is halved (rounded up).
	if f.PlaneWidth(0) != 6 || f.PlaneHeight(0) != 4 {
		t.Fatalf("luma plane = %dx%d, want 6x4", f.PlaneWidth(0), f.PlaneHeight(0))
	}
	for _, p := range []int{1, 2} {
		if f.PlaneWidth(p) != 3 || f.PlaneHeight(p) != 2 {
			t.Fatalf("chroma plane %d = %dx%d, want 3x2", p, f.PlaneWidth(p), f.PlaneHeight(p))
		}
	}

	for p := 0; p < f.NumPlanes(); p++ {
		if f.Linesize(p) < f.PlaneWidth(p) {
			t.Fatalf("plane %d linesize %d < width %d", p, f.Linesize(p), f.PlaneWidth(p))
		}
		plane := f.Plane(p)
		if want := f.Linesize(p) * f.PlaneHeight(p); len(plane) != want {
			t.Fatalf("plane %d len = %d, want %d", p, len(plane), want)
		}
		// The slice aliases the C buffer: a write must be visible on re-read.
		plane[0] = 42
		if got := f.Plane(p)[0]; got != 42 {
			t.Fatalf("plane %d not writable: re-read %d", p, got)
		}
	}
}

func TestPlaneNilForEmptyFrame(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer f.Close()
	// No buffers allocated yet.
	if got := f.Plane(0); got != nil {
		t.Fatalf("Plane(0) on buffer-less frame = %v, want nil", got)
	}
}
