// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"bytes"
	"testing"
)

func fillLuma(f *Frame, fn func(x, y int) byte) {
	ls := f.Linesize(0)
	p := f.Plane(0)
	for y := 0; y < f.Height(); y++ {
		for x := 0; x < f.Width(); x++ {
			p[y*ls+x] = fn(x, y)
		}
	}
}

func planeBytes(f *Frame) []byte {
	var buf bytes.Buffer
	for i := 0; i < f.NumPlanes(); i++ {
		ls := f.Linesize(i)
		row := f.Plane(i)
		for y := 0; y < f.PlaneHeight(i); y++ {
			buf.Write(row[y*ls : y*ls+f.PlaneWidth(i)])
		}
	}
	return buf.Bytes()
}

func TestScaler_SizeFormatAndColor(t *testing.T) {
	src, err := NewVideoFrame(32, 24, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer src.Close()
	fillLuma(src, func(x, y int) byte { return byte(x + y) })
	src.SetPTS(99)
	src.setTestColor(2, 1, 1, 1, 1)
	s, err := NewScaler(ScalerOptions{Width: 16, Height: 8, PixFmt: PixFmtByName("yuv420p"), Flags: "neighbor"})
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer s.Close()
	out, err := s.Scale(src)
	if err != nil {
		t.Fatalf("Scale: %v", err)
	}
	defer out.Close()
	if out.Width() != 16 || out.Height() != 8 || out.PixFmt() != src.PixFmt() {
		t.Fatalf("out = %dx%d fmt %d", out.Width(), out.Height(), out.PixFmt())
	}
	if out.PTS() != 99 {
		t.Errorf("PTS = %d, want 99", out.PTS())
	}
	if rng, prim, trc, spc, loc := out.testColor(); rng != 2 || prim != 1 || trc != 1 || spc != 1 || loc != 1 {
		t.Errorf("color = %d %d %d %d %d, want 2 1 1 1 1", rng, prim, trc, spc, loc)
	}
	if top, bottom, left, right := out.Crop(); top|bottom|left|right != 0 {
		t.Errorf("crop fields set on scaled frame")
	}
	if s.ReinitCount() != 1 {
		t.Errorf("reinits = %d, want 1", s.ReinitCount())
	}
	again, err := s.Scale(src)
	if err != nil {
		t.Fatalf("second Scale: %v", err)
	}
	again.Close()
	if s.ReinitCount() != 1 {
		t.Errorf("reinits after a repeat = %d, want 1", s.ReinitCount())
	}
}

func TestScaler_FullRangeStaysFullRange(t *testing.T) {
	src, err := NewVideoFrame(8, 8, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer src.Close()
	fillLuma(src, func(int, int) byte { return 255 })
	src.setTestColor(2, 1, 1, 6, 1) // JPEG range, bt709-ish primaries/trc, bt709 matrix
	s, err := NewScaler(ScalerOptions{Width: 4, Height: 4, PixFmt: -1, Flags: "neighbor", BitExact: true})
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer s.Close()
	out, err := s.Scale(src)
	if err != nil {
		t.Fatalf("Scale: %v", err)
	}
	defer out.Close()
	if rng, _, _, _, _ := out.testColor(); rng != 2 {
		t.Errorf("color_range = %d, want 2 (full)", rng)
	}
	if got := out.Plane(0)[0]; got != 255 {
		t.Errorf("luma = %d, want 255 (full range was not converted to limited)", got)
	}
}

func TestScaler_SourceSizeChange(t *testing.T) {
	s, err := NewScaler(ScalerOptions{Width: 8, Height: 8, PixFmt: -1, Flags: "bilinear"})
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer s.Close()
	for _, wh := range [][2]int{{16, 16}, {32, 24}, {16, 16}} {
		src, err := NewVideoFrame(wh[0], wh[1], PixFmtByName("yuv420p"))
		if err != nil {
			t.Fatalf("NewVideoFrame: %v", err)
		}
		out, err := s.Scale(src)
		src.Close()
		if err != nil {
			t.Fatalf("Scale %v: %v", wh, err)
		}
		if out.Width() != 8 || out.Height() != 8 {
			t.Errorf("out = %dx%d", out.Width(), out.Height())
		}
		out.Close()
	}
	if s.ReinitCount() != 3 {
		t.Errorf("reinits = %d, want 3 (two sizes, then back)", s.ReinitCount())
	}
}

func TestScaler_InterlacedError(t *testing.T) {
	src, err := NewVideoFrame(16, 16, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer src.Close()
	src.SetFieldFlags(true, true)
	s, err := NewScaler(ScalerOptions{Width: 8, Height: 8, PixFmt: -1, Flags: "bicubic"})
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer s.Close()
	if out, err := s.Scale(src); err == nil {
		out.Close()
		t.Fatal("Scale of an interlaced frame succeeded")
	}
}

func TestScaler_BitExactIdentical(t *testing.T) {
	src, err := NewVideoFrame(32, 32, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer src.Close()
	fillLuma(src, func(x, y int) byte { return byte((x*3 + y*5) & 255) })
	opt := ScalerOptions{Width: 18, Height: 14, PixFmt: -1, Flags: "bicubic", BitExact: true}
	a, err := NewScaler(opt)
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer a.Close()
	b, err := NewScaler(opt)
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer b.Close()
	oa, err := a.Scale(src)
	if err != nil {
		t.Fatalf("Scale a: %v", err)
	}
	defer oa.Close()
	ob, err := b.Scale(src)
	if err != nil {
		t.Fatalf("Scale b: %v", err)
	}
	defer ob.Close()
	if !bytes.Equal(planeBytes(oa), planeBytes(ob)) {
		t.Fatal("two bitexact scalers disagreed")
	}
}

func TestScaler_CropWindowMatchesCropRect(t *testing.T) {
	src, err := NewVideoFrame(64, 48, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer src.Close()
	fillLuma(src, func(x, y int) byte { return byte((x*3 + y*5) & 255) })
	src.setTestColor(2, 1, 1, 1, 1)
	win, err := src.CropWindow(8, 4, 32, 24)
	if err != nil {
		t.Fatalf("CropWindow: %v", err)
	}
	defer win.Close()
	rect, err := src.CropRect(8, 4, 32, 24)
	if err != nil {
		t.Fatalf("CropRect: %v", err)
	}
	defer rect.Close()
	opt := ScalerOptions{Width: 16, Height: 12, PixFmt: -1, Flags: "bicubic", BitExact: true}
	sw, err := NewScaler(opt)
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer sw.Close()
	sr, err := NewScaler(opt)
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	defer sr.Close()
	ow, err := sw.Scale(win)
	if err != nil {
		t.Fatalf("scale window: %v", err)
	}
	defer ow.Close()
	or, err := sr.Scale(rect)
	if err != nil {
		t.Fatalf("scale rect: %v", err)
	}
	defer or.Close()
	if !bytes.Equal(planeBytes(ow), planeBytes(or)) {
		t.Fatal("scaling a CropWindow disagreed with scaling the CropRect copy")
	}
	if rng, _, _, _, _ := ow.testColor(); rng != 2 {
		t.Errorf("window scale color_range = %d, want 2", rng)
	}
}

func TestScaler_UnknownFlags(t *testing.T) {
	if _, err := NewScaler(ScalerOptions{Flags: "sinc"}); err == nil {
		t.Fatal("unknown flags succeeded")
	}
}
