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
	return f
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
