// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// columnFrame returns a frame whose every sample in a row holds its own
// column index (mod 256 for 8-bit). Interleaved nv12 chroma holds c in U and
// 200-c in V. Packed rgb24 holds the column in R.
func columnFrame(t *testing.T, w, h int, fmtName string) *Frame {
	t.Helper()
	pf := PixFmtByName(fmtName)
	if pf < 0 {
		t.Fatalf("unknown pix fmt %q", fmtName)
	}
	f, err := NewVideoFrame(w, h, pf)
	if err != nil {
		t.Fatalf("NewVideoFrame(%s): %v", fmtName, err)
	}
	for p := 0; p < f.NumPlanes(); p++ {
		data, ls := f.Plane(p), f.Linesize(p)
		for row := 0; row < f.PlaneHeight(p); row++ {
			line := data[row*ls:]
			switch {
			case fmtName == "yuv420p10le":
				for c := 0; c < f.PlaneWidth(p); c++ {
					binary.LittleEndian.PutUint16(line[2*c:], uint16(c))
				}
			case fmtName == "nv12" && p == 1:
				for c := 0; c < w/2; c++ {
					line[2*c], line[2*c+1] = byte(c), byte(200-c)
				}
			case fmtName == "rgb24":
				for c := 0; c < w; c++ {
					line[3*c], line[3*c+1], line[3*c+2] = byte(c), 1, 2
				}
			default:
				for c := 0; c < f.PlaneWidth(p); c++ {
					line[c] = byte(c)
				}
			}
		}
	}
	f.SetPTS(4242)
	// The last luma sample of each row holds the row index, so a vertical
	// crop can be checked. The crops above do not include that column.
	stampRow(f, fmtName)
	return f
}

func stampRow(f *Frame, fmtName string) {
	ls := f.Linesize(0)
	data := f.Plane(0)
	w := f.Width()
	bpp := 1
	switch fmtName {
	case "yuv420p10le":
		bpp = 2
	case "rgb24":
		bpp = 3
	}
	for row := 0; row < f.Height(); row++ {
		off := row*ls + (w-1)*bpp
		if fmtName == "yuv420p10le" {
			binary.LittleEndian.PutUint16(data[off:], uint16(row))
			continue
		}
		data[off] = byte(row)
	}
}

func TestCropColumns_PlaneOffsets(t *testing.T) {
	const left, width = 92, 136
	for _, tc := range []struct {
		fmt   string
		check func(t *testing.T, out *Frame, row int)
	}{
		{"yuv420p", func(t *testing.T, out *Frame, row int) {
			y := out.Plane(0)[row*out.Linesize(0):]
			if y[0] != left || y[width-1] != left+width-1 {
				t.Errorf("row %d luma = %d..%d, want %d..%d", row, y[0], y[width-1], left, left+width-1)
			}
			if row < out.PlaneHeight(1) {
				for _, p := range []int{1, 2} {
					c := out.Plane(p)[row*out.Linesize(p):]
					if c[0] != left/2 || c[width/2-1] != (left+width)/2-1 {
						t.Errorf("row %d plane %d = %d..%d, want %d..%d", row, p, c[0], c[width/2-1], left/2, (left+width)/2-1)
					}
				}
			}
		}},
		{"yuv420p10le", func(t *testing.T, out *Frame, row int) {
			y := out.Plane(0)[row*out.Linesize(0):]
			if a, b := binary.LittleEndian.Uint16(y), binary.LittleEndian.Uint16(y[2*(width-1):]); a != left || b != left+width-1 {
				t.Errorf("row %d luma = %d..%d, want %d..%d", row, a, b, left, left+width-1)
			}
			if row < out.PlaneHeight(1) {
				c := out.Plane(1)[row*out.Linesize(1):]
				if a := binary.LittleEndian.Uint16(c); a != left/2 {
					t.Errorf("row %d chroma = %d, want %d", row, a, left/2)
				}
			}
		}},
		{"nv12", func(t *testing.T, out *Frame, row int) {
			y := out.Plane(0)[row*out.Linesize(0):]
			if y[0] != left {
				t.Errorf("row %d luma = %d, want %d", row, y[0], left)
			}
			if row < out.PlaneHeight(1) {
				uv := out.Plane(1)[row*out.Linesize(1):]
				if uv[0] != left/2 || uv[1] != 200-left/2 || uv[width-2] != (left+width)/2-1 {
					t.Errorf("row %d uv = %d,%d..%d, want %d,%d..%d", row, uv[0], uv[1], uv[width-2], left/2, 200-left/2, (left+width)/2-1)
				}
			}
		}},
		{"rgb24", func(t *testing.T, out *Frame, row int) {
			px := out.Plane(0)[row*out.Linesize(0):]
			if px[0] != left || px[1] != 1 || px[2] != 2 || px[3*(width-1)] != left+width-1 {
				t.Errorf("row %d rgb = %v..%d, want [%d 1 2]..%d", row, px[:3], px[3*(width-1)], left, left+width-1)
			}
		}},
	} {
		t.Run(tc.fmt, func(t *testing.T) {
			src := columnFrame(t, 320, 240, tc.fmt)
			defer src.Close()
			out, err := src.CropColumns(left, width)
			if err != nil {
				t.Fatalf("CropColumns: %v", err)
			}
			defer out.Close()
			if out.Width() != width || out.Height() != 240 || out.PixFmt() != src.PixFmt() {
				t.Fatalf("out = %dx%d %s, want %dx240 %s", out.Width(), out.Height(), PixFmtName(out.PixFmt()), width, tc.fmt)
			}
			if out.PTS() != 4242 {
				t.Errorf("PTS = %d, want 4242", out.PTS())
			}
			for _, row := range []int{0, 119, 239} {
				tc.check(t, out, row)
			}
		})
	}
}

func TestCropColumns_CopiesSideData(t *testing.T) {
	src := columnFrame(t, 64, 16, "yuv420p")
	defer src.Close()
	payload := append(bytes.Repeat([]byte{0xab}, 16), "hello"...)
	if err := src.AddSEIUnregisteredSideData(payload); err != nil {
		t.Fatalf("AddSEIUnregisteredSideData: %v", err)
	}
	out, err := src.CropColumns(8, 32)
	if err != nil {
		t.Fatalf("CropColumns: %v", err)
	}
	defer out.Close()
	got := out.SEIUnregisteredSideData()
	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatalf("side data = %x, want %x", got, payload)
	}
}

func TestCropColumns_RejectsOutOfRange(t *testing.T) {
	src := columnFrame(t, 64, 16, "yuv420p")
	defer src.Close()
	for _, w := range [][2]int{{-2, 8}, {0, 0}, {60, 8}, {0, 66}} {
		if out, err := src.CropColumns(w[0], w[1]); err == nil {
			out.Close()
			t.Errorf("CropColumns(%d, %d) succeeded, want error", w[0], w[1])
		}
	}
}

// A frame that still carries crop fields (a decoder with apply_cropping off
// leaves the conformance window there) is cut in visible coordinates and the
// result carries no crop fields of its own.
func TestCropColumns_FoldsPendingCrop(t *testing.T) {
	src := columnFrame(t, 64, 16, "yuv420p")
	defer src.Close()
	src.SetCrop(2, 0, 4, 8)
	if got := src.VisibleWidth(); got != 52 {
		t.Fatalf("VisibleWidth = %d, want 52", got)
	}
	out, err := src.CropColumns(10, 16)
	if err != nil {
		t.Fatalf("CropColumns: %v", err)
	}
	defer out.Close()
	if out.Width() != 16 || out.Height() != 14 || out.VisibleWidth() != 16 {
		t.Fatalf("out %dx%d visible %d, want 16x14 visible 16", out.Width(), out.Height(), out.VisibleWidth())
	}
	if got := out.Plane(0)[0]; got != 14 {
		t.Errorf("luma column 0 = %d, want 14 (4 folded + 10)", got)
	}
	if got := out.Plane(1)[0]; got != 7 {
		t.Errorf("chroma column 0 = %d, want 7", got)
	}
	if o, err := src.CropColumns(40, 16); err == nil {
		o.Close()
		t.Error("window past the visible width succeeded, want error")
	}
}

func TestVisibleWidth(t *testing.T) {
	if got := (*Frame)(nil).VisibleWidth(); got != 0 {
		t.Errorf("nil VisibleWidth = %d", got)
	}
	(*Frame)(nil).SetCrop(1, 1, 1, 1)
	f := columnFrame(t, 64, 16, "yuv420p")
	defer f.Close()
	if got := f.VisibleWidth(); got != 64 {
		t.Errorf("VisibleWidth = %d, want 64", got)
	}
	f.SetCrop(1, 2, 3, 4)
	if top, bottom, left, right := f.Crop(); top != 1 || bottom != 2 || left != 3 || right != 4 {
		t.Errorf("Crop = %d %d %d %d", top, bottom, left, right)
	}
	if got := f.VisibleWidth(); got != 57 {
		t.Errorf("VisibleWidth = %d, want 57", got)
	}
	if got := f.VisibleHeight(); got != 13 {
		t.Errorf("VisibleHeight = %d, want 13", got)
	}
	if got := (*Frame)(nil).VisibleHeight(); got != 0 {
		t.Errorf("nil VisibleHeight = %d", got)
	}
}

func TestLog2ChromaW(t *testing.T) {
	for name, want := range map[string]int{"yuv420p": 1, "yuv422p": 1, "yuv444p": 0, "yuv411p": 2, "nv12": 1, "rgb24": 0} {
		f, err := NewVideoFrame(16, 16, PixFmtByName(name))
		if err != nil {
			t.Fatalf("NewVideoFrame(%s): %v", name, err)
		}
		if got := f.Log2ChromaW(); got != want {
			t.Errorf("%s: Log2ChromaW = %d, want %d", name, got, want)
		}
		f.Close()
	}
	if PixFmtByName("no_such_format") != -1 {
		t.Error("PixFmtByName(unknown) != -1")
	}
}

func TestCropRect_PlaneOffsets(t *testing.T) {
	const x, y, w, h = 92, 40, 136, 80
	for _, fmtName := range []string{"yuv420p", "yuv420p10le", "nv12", "rgb24"} {
		t.Run(fmtName, func(t *testing.T) {
			src := columnFrame(t, 320, 240, fmtName)
			defer src.Close()
			out, err := src.CropRect(x, y, w, h)
			if err != nil {
				t.Fatalf("CropRect: %v", err)
			}
			defer out.Close()
			if out.Width() != w || out.Height() != h || out.PixFmt() != src.PixFmt() {
				t.Fatalf("out = %dx%d, want %dx%d", out.Width(), out.Height(), w, h)
			}
			if out.PTS() != 4242 {
				t.Errorf("PTS = %d, want 4242", out.PTS())
			}
			if top, bottom, left, right := out.Crop(); top != 0 || bottom != 0 || left != 0 || right != 0 {
				t.Errorf("crop fields = %d %d %d %d, want clear", top, bottom, left, right)
			}
			checkCropOrigin(t, out, fmtName, x, y)
			// The row stamp lives in the source's last column, which this
			// window does not include. A second crop of that column checks y.
			edge, err := src.CropRect(320-16, y, 16, h)
			if err != nil {
				t.Fatalf("edge crop: %v", err)
			}
			defer edge.Close()
			if got := rowStamp(edge, fmtName, 15, 0); got != y {
				t.Errorf("row stamp = %d, want %d", got, y)
			}
		})
	}
}

func checkCropOrigin(t *testing.T, out *Frame, fmtName string, x, y int) {
	t.Helper()
	switch fmtName {
	case "yuv420p10le":
		pix := binary.LittleEndian.Uint16(out.Plane(0))
		if pix != uint16(x) {
			t.Errorf("luma origin = %d, want column %d", pix, x)
		}
	case "rgb24":
		px := out.Plane(0)
		if px[0] != byte(x) || px[1] != 1 || px[2] != 2 {
			t.Errorf("rgb origin = %v, want [%d 1 2]", px[:3], x)
		}
		if px[1] == byte(y) && y != 1 {
			t.Errorf("rgb G was overwritten with the row")
		}
	default:
		if got := out.Plane(0)[0]; got != byte(x) {
			t.Errorf("luma origin = %d, want column %d", got, x)
		}
	}
	_ = y
}

func rowStamp(f *Frame, fmtName string, col, row int) int {
	ls := f.Linesize(0)
	data := f.Plane(0)
	bpp := 1
	switch fmtName {
	case "yuv420p10le":
		bpp = 2
	case "rgb24":
		bpp = 3
	}
	off := row*ls + col*bpp
	if fmtName == "yuv420p10le" {
		return int(binary.LittleEndian.Uint16(data[off:]))
	}
	return int(data[off])
}

func TestCropRect_FoldsPendingCrop(t *testing.T) {
	src := columnFrame(t, 64, 32, "yuv420p")
	defer src.Close()
	src.SetCrop(4, 2, 6, 8)
	out, err := src.CropRect(10, 6, 16, 8)
	if err != nil {
		t.Fatalf("CropRect: %v", err)
	}
	defer out.Close()
	if out.Width() != 16 || out.Height() != 8 {
		t.Fatalf("out = %dx%d, want 16x8", out.Width(), out.Height())
	}
	if got := out.Plane(0)[0]; got != 16 {
		t.Errorf("luma origin = %d, want 16 (crop_left 6 + x 10)", got)
	}
	// Stamp source column 16 with the row so the vertical fold is visible.
	// Visible (10, 6) is source (16, 10) once crop_left 6 and crop_top 4
	// are folded.
	ls := src.Linesize(0)
	for row := 0; row < src.Height(); row++ {
		src.Plane(0)[row*ls+16] = byte(row)
	}
	folded, err := src.CropRect(10, 6, 16, 8)
	if err != nil {
		t.Fatalf("CropRect after stamp: %v", err)
	}
	defer folded.Close()
	if got := folded.Plane(0)[0]; got != 10 {
		t.Errorf("folded row = %d, want source row 10", got)
	}
}

func TestCropWindow_OwnsReference(t *testing.T) {
	src := columnFrame(t, 64, 16, "yuv420p")
	src.SetPTS(7)
	before := src.Plane(0)[0]
	win, err := src.CropWindow(8, 2, 16, 8)
	if err != nil {
		src.Close()
		t.Fatalf("CropWindow: %v", err)
	}
	if src.Width() != 64 || src.Height() != 16 || src.Plane(0)[0] != before {
		t.Errorf("source changed: %dx%d luma0 %d", src.Width(), src.Height(), src.Plane(0)[0])
	}
	if top, _, left, _ := src.Crop(); top != 0 || left != 0 {
		t.Errorf("source crop = %d,%d", top, left)
	}
	src.Close()
	if win.Width() != 16 || win.Height() != 8 || win.PTS() != 7 {
		t.Fatalf("window = %dx%d pts %d, want 16x8 pts 7", win.Width(), win.Height(), win.PTS())
	}
	if got := win.Plane(0)[0]; got != 8 {
		t.Errorf("window luma = %d, want 8", got)
	}
	if top, bottom, left, right := win.Crop(); top|bottom|left|right != 0 {
		t.Errorf("window crop fields = %d %d %d %d", top, bottom, left, right)
	}
	win.Close()
}

func TestCropWindow_FoldsPendingCrop(t *testing.T) {
	src := columnFrame(t, 64, 16, "yuv420p")
	defer src.Close()
	src.SetCrop(0, 0, 8, 0)
	win, err := src.CropWindow(4, 0, 16, 16)
	if err != nil {
		t.Fatalf("CropWindow: %v", err)
	}
	defer win.Close()
	if got := win.Plane(0)[0]; got != 12 {
		t.Errorf("window luma = %d, want 12 (pending 8 + x 4)", got)
	}
	if _, _, left, _ := src.Crop(); left != 8 {
		t.Errorf("source crop_left = %d, want 8", left)
	}
	rect, err := src.CropRect(4, 0, 16, 16)
	if err != nil {
		t.Fatalf("CropRect: %v", err)
	}
	defer rect.Close()
	if rect.Plane(0)[0] != win.Plane(0)[0] {
		t.Errorf("CropRect luma %d, CropWindow luma %d", rect.Plane(0)[0], win.Plane(0)[0])
	}
}

func TestCropWindow_RejectsHardware(t *testing.T) {
	pf := PixFmtByName("cuda")
	if pf < 0 {
		pf = PixFmtByName("videotoolbox")
	}
	if pf < 0 {
		t.Skip("no hardware pixel format in this build")
	}
	f, err := NewVideoFrame(16, 16, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer f.Close()
	f.setTestPixFmt(pf)
	if !f.Hardware() {
		t.Fatal("Hardware() = false")
	}
	if out, err := f.CropWindow(0, 0, 8, 8); err == nil {
		out.Close()
		t.Fatal("CropWindow succeeded on a hardware format")
	}
	if out, err := f.CropRect(0, 0, 8, 8); err == nil {
		out.Close()
		t.Fatal("CropRect succeeded on a hardware format")
	}
}
