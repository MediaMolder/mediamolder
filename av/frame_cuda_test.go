// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

func TestCUDAFrameRejectsSoftwareFrame(t *testing.T) {
	f, err := NewVideoFrame(16, 16, PixFmtByName("nv12"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, ok := f.CUDAFrame(); ok {
		t.Fatal("software frame reported a CUDA surface")
	}
	if out, err := f.AllocHWFrameLike(); err == nil {
		out.Close()
		t.Fatal("software frame unexpectedly allocated a hardware surface")
	}
}

func TestCUDAFrame(t *testing.T) {
	device := RequireHWDevice(t, HWDeviceCUDA)
	const width, height = 192, 128
	format := PixFmtByName("nv12")
	pool, err := NewHWFramesContext(device, width, height, format, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	f, err := AllocHWFrame(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.SetPTS(42)
	f.p.width, f.p.height = width-4, height-2
	f.SetCrop(0, 2, 0, 4)

	view, ok := f.CUDAFrame()
	if !ok || view.Context == 0 || view.PixFmt != format || view.NumPlanes != 2 {
		t.Fatalf("unexpected NV12 CUDA view: %+v, ok = %v", view, ok)
	}
	for i, plane := range view.Planes[:view.NumPlanes] {
		if plane.DevicePtr == 0 || plane.Pitch < width || plane.Pitch != f.Linesize(i) {
			t.Fatalf("invalid CUDA plane %d: %+v", i, plane)
		}
		if f.Plane(i) != nil {
			t.Fatalf("Plane(%d) exposed CUDA memory as a host slice", i)
		}
	}

	// Only the source frame's own reference to the pool is needed.
	pool.Close()
	out, err := f.AllocHWFrameLike()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	outView, ok := out.CUDAFrame()
	if !ok || outView.Context != view.Context || outView.PixFmt != view.PixFmt ||
		outView.Planes[0].DevicePtr == view.Planes[0].DevicePtr {
		t.Fatalf("output did not allocate a new surface in the same CUDA context: %+v", outView)
	}
	if out.PTS() != 42 || out.Width() != f.Width() || out.Height() != f.Height() ||
		out.VisibleWidth() != f.VisibleWidth() || out.VisibleHeight() != f.VisibleHeight() {
		t.Fatal("output did not preserve frame dimensions, crop, and PTS")
	}
	f.Unref()
	if _, ok := f.CUDAFrame(); ok {
		t.Fatal("unreferenced frame reported a CUDA surface")
	}
	if _, ok := out.CUDAFrame(); !ok {
		t.Fatal("output lost its CUDA surface after the source was unreferred")
	}
}
