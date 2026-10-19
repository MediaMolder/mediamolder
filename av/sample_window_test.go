// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

func lumaAt(f *Frame, x, y int) int {
	p, ls := f.Plane(0), f.Linesize(0)
	return int(p[y*ls+x])
}

func setLuma(f *Frame, x, y int, v byte) {
	p, ls := f.Plane(0), f.Linesize(0)
	p[y*ls+x] = v
}

func TestFillBlack_LimitedAndFullRange(t *testing.T) {
	f, err := NewVideoFrame(4, 4, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	setLuma(f, 0, 0, 200)
	if err := f.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(f, 0, 0); got != 16 {
		t.Fatalf("unspecified luma = %d, want 16", got)
	}
	u := f.Plane(1)
	if int(u[0]) != 128 {
		t.Fatalf("chroma = %d, want 128", u[0])
	}

	f.setTestColor(2, 0, 0, 0, 0) // AVCOL_RANGE_JPEG
	if err := f.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(f, 1, 1); got != 0 {
		t.Fatalf("full-range luma = %d, want 0", got)
	}
	if int(f.Plane(1)[0]) != 128 {
		t.Fatalf("full-range chroma = %d, want 128", f.Plane(1)[0])
	}
}

func TestResampleWindow_IntegerCopyAndQuarterShift(t *testing.T) {
	src, err := NewVideoFrame(16, 8, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			setLuma(src, x, y, byte(x+10*y))
		}
	}
	setLuma(src, 10, 0, 0)
	setLuma(src, 11, 0, 100)
	setLuma(src, 12, 0, 0)

	dst, err := NewVideoFrame(4, 4, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if err := ResampleWindow(dst, src, 2, 0, 4, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(dst, 0, 0); got != 2 {
		t.Fatalf("integer copy luma = %d, want 2", got)
	}
	// Output (3,1) is source (2+3, 0+1) = 5 + 10.
	if got := lumaAt(dst, 3, 1); got != 15 {
		t.Fatalf("integer copy (3,1) = %d, want 15", got)
	}

	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if err := ResampleWindow(dst, src, 10.25, 0, 4, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	// 0.75*0 + 0.25*100 = 25, then 0.75*100 + 0.25*0 = 75.
	if got := lumaAt(dst, 0, 0); got != 25 {
		t.Fatalf("x=10.25 pixel 0 = %d, want 25", got)
	}
	if got := lumaAt(dst, 1, 0); got != 75 {
		t.Fatalf("x=10.25 pixel 1 = %d, want 75", got)
	}
}

func TestResampleWindow_OutsideIsBlack(t *testing.T) {
	src, err := NewVideoFrame(8, 4, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			setLuma(src, x, y, byte(40+x))
		}
	}
	dst, err := NewVideoFrame(8, 4, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	// Window starts 4 pixels left of the picture. 1:1, so the left half is black.
	if err := ResampleWindow(dst, src, -4, 0, 8, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(dst, 0, 0); got != 16 {
		t.Fatalf("hung-off luma = %d, want 16", got)
	}
	if got := lumaAt(dst, 4, 1); got != 40 {
		t.Fatalf("first visible luma = %d, want 40", got)
	}
	if got := lumaAt(dst, 7, 1); got != 43 {
		t.Fatalf("last luma = %d, want 43", got)
	}
}

func TestResampleWindow_AreaAveragesAndLanczosHoldsFlat(t *testing.T) {
	src, err := NewVideoFrame(4, 4, PixFmtByName("yuv444p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	setLuma(src, 0, 0, 0)
	setLuma(src, 1, 0, 40)
	setLuma(src, 0, 1, 80)
	setLuma(src, 1, 1, 120)
	dst, err := NewVideoFrame(2, 2, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if err := ResampleWindow(dst, src, 0, 0, 4, 4, "area"); err != nil {
		t.Fatal(err)
	}
	// Pixel (0,0) covers source [0,2) × [0,2): mean of 0, 40, 80, 120.
	if got := lumaAt(dst, 0, 0); got != 60 {
		t.Fatalf("area = %d, want 60", got)
	}

	flat, err := NewVideoFrame(8, 8, PixFmtByName("yuv444p"))
	if err != nil {
		t.Fatal(err)
	}
	defer flat.Close()
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			setLuma(flat, x, y, 80)
		}
	}
	out, err := NewVideoFrame(4, 4, flat.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := out.CopyPropsFrom(flat); err != nil {
		t.Fatal(err)
	}
	if err := out.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if err := ResampleWindow(out, flat, 1, 1, 4, 4, "lanczos"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(out, 2, 2); got != 80 {
		t.Fatalf("lanczos flat = %d, want 80", got)
	}
}

func TestResampleWindow_SplineBlursAnInteger(t *testing.T) {
	src, err := NewVideoFrame(8, 8, PixFmtByName("yuv444p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			setLuma(src, x, y, 0)
		}
	}
	for y := 0; y < 8; y++ {
		setLuma(src, 2, y, 100)
	}
	dst, err := NewVideoFrame(4, 4, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	// Window y=2 keeps the B-spline support inside the picture. Output
	// pixel 2 sits on source column 2. Centre weight 4/6 and each
	// neighbour 1/6 turns 100 into 67. Catmull-Rom would copy 100.
	if err := ResampleWindow(dst, src, 0, 2, 4, 4, "spline"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(dst, 2, 0); got != 67 {
		t.Fatalf("spline = %d, want 67", got)
	}
}

func TestResampleWindow_DownscaleStretchesKernel(t *testing.T) {
	src, err := NewVideoFrame(4, 4, PixFmtByName("yuv444p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := byte(0)
			if x == 1 || x == 3 {
				v = 100
			}
			setLuma(src, x, y, v)
		}
	}
	dst, err := NewVideoFrame(1, 1, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	// 4→1 bilinear. The stretched tent covers all four source pixels plus
	// the black taps beside them: 166/4 rounds to 42. An unstretched
	// tent would average only the middle pair and land on 50.
	if err := ResampleWindow(dst, src, 0, 1, 4, 1, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(dst, 0, 0); got != 42 {
		t.Fatalf("downscale = %d, want 42", got)
	}
}

func TestResampleWindow_ChromaFollowsLumaEdge(t *testing.T) {
	src, err := NewVideoFrame(8, 8, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			setLuma(src, x, y, 200)
		}
	}
	u, uls := src.Plane(1), src.Linesize(1)
	for y := 0; y < src.PlaneHeight(1); y++ {
		for x := 0; x < src.PlaneWidth(1); x++ {
			u[y*uls+x] = 40
		}
	}
	dst, err := NewVideoFrame(8, 8, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	// y=-1, 1:1. Luma row 1 is inside. Chroma row 0 covers luma rows 0
	// and 1, so it is filtered: half neutral black, half 40.
	if err := ResampleWindow(dst, src, 0, -1, 8, 8, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(dst, 0, 0); got != 16 {
		t.Fatalf("luma row 0 = %d, want 16", got)
	}
	if got := lumaAt(dst, 0, 1); got != 200 {
		t.Fatalf("luma row 1 = %d, want 200", got)
	}
	if got := int(dst.Plane(1)[0]); got != 84 {
		t.Fatalf("chroma row 0 = %d, want 84", got)
	}
}

func TestResampleWindow_CentreUsesPixelExtent(t *testing.T) {
	src, err := NewVideoFrame(8, 4, PixFmtByName("yuv444p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			setLuma(src, x, y, 0)
		}
		setLuma(src, 0, y, 100)
	}
	dst, err := NewVideoFrame(8, 4, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	// x=-0.3. Output column 0 has its centre at continuous 0.2, inside
	// the first pixel: 0.7*100 + 0.3*black.
	if err := ResampleWindow(dst, src, -0.3, 0, 8, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(dst, 0, 0); got != 75 {
		t.Fatalf("inside edge = %d, want 75", got)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	// x=0.6. Output column 7 has its centre past the last pixel.
	if err := ResampleWindow(dst, src, 0.6, 0, 8, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := lumaAt(dst, 7, 0); got != 16 {
		t.Fatalf("outside edge = %d, want 16", got)
	}
}

func TestResampleWindow_RGB565RoundTrip(t *testing.T) {
	fmt := PixFmtByName("rgb565le")
	if fmt < 0 {
		t.Fatal("no rgb565le")
	}
	f, err := NewVideoFrame(4, 4, fmt)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	put16(f, 0, 0, 0xFFFF)
	if err := f.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if got := get16(f, 0, 0); got != 0 {
		t.Fatalf("rgb565 black = %#x, want 0", got)
	}
	src, err := NewVideoFrame(4, 4, fmt)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			put16(src, x, y, 0xF800)
		}
	}
	dst, err := NewVideoFrame(4, 4, fmt)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if err := ResampleWindow(dst, src, 0, 0, 4, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := get16(dst, 1, 1); got != 0xF800 {
		t.Fatalf("rgb565 red = %#x, want 0xf800", got)
	}

	be := PixFmtByName("rgb565be")
	if be < 0 {
		t.Fatal("no rgb565be")
	}
	bf, err := NewVideoFrame(4, 4, be)
	if err != nil {
		t.Fatal(err)
	}
	defer bf.Close()
	putBE(bf, 0, 0, 0xFFFF)
	if err := bf.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if got := getBE(bf, 0, 0); got != 0 {
		t.Fatalf("rgb565be black = %#x, want 0", got)
	}
	bsrc, err := NewVideoFrame(4, 4, be)
	if err != nil {
		t.Fatal(err)
	}
	defer bsrc.Close()
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			putBE(bsrc, x, y, 0xF821)
		}
	}
	bdst, err := NewVideoFrame(4, 4, be)
	if err != nil {
		t.Fatal(err)
	}
	defer bdst.Close()
	if err := bdst.CopyPropsFrom(bsrc); err != nil {
		t.Fatal(err)
	}
	if err := bdst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if err := ResampleWindow(bdst, bsrc, 0, 0, 4, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	// R=31, G=1, B=1. All three rgb565be components have to land.
	if got := getBE(bdst, 1, 1); got != 0xF821 {
		t.Fatalf("rgb565be = %#x, want 0xf821", got)
	}
}

func TestResampleWindow_Packed10KeepsNeighbours(t *testing.T) {
	fmt := PixFmtByName("x2rgb10le")
	if fmt < 0 {
		t.Fatal("no x2rgb10le")
	}
	// B in bits 0-9, G in 10-19, R in 20-29. B is shift 0 and is stored
	// in the same bytes as the low bits of G.
	const pix = 0x00100401
	src, err := NewVideoFrame(4, 4, fmt)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			put32(src, x, y, pix)
		}
	}
	dst, err := NewVideoFrame(4, 4, fmt)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if err := ResampleWindow(dst, src, 0, 0, 4, 4, "bilinear"); err != nil {
		t.Fatal(err)
	}
	if got := get32(dst, 1, 1) & 0x3FFFFFFF; got != pix {
		t.Fatalf("x2rgb10le = %#x, want %#x", got, pix)
	}
}

func TestResampleWindow_TenBitQuarterShift(t *testing.T) {
	fmt := PixFmtByName("yuv420p10le")
	if fmt < 0 {
		t.Fatal("no yuv420p10le")
	}
	src, err := NewVideoFrame(8, 4, fmt)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	put10(src, 0, 0, 100)
	put10(src, 1, 0, 300)
	dst, err := NewVideoFrame(2, 2, fmt)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	// Poison the high bits. FillBlack must clear them: a 10-bit reader
	// consumes the whole 16-bit word, and the buffer is not zeroed.
	put10(dst, 0, 0, 0xFFFF)
	if err := dst.FillBlack(); err != nil {
		t.Fatal(err)
	}
	if luma10(dst, 0, 0) != 64 {
		t.Fatalf("10-bit black = %d, want 64", luma10(dst, 0, 0))
	}
	if err := ResampleWindow(dst, src, 0.25, 0, 2, 2, "bilinear"); err != nil {
		t.Fatal(err)
	}
	// 0.75*100 + 0.25*300 = 150.
	if got := luma10(dst, 0, 0); got != 150 {
		t.Fatalf("10-bit blend = %d, want 150", got)
	}
}

func put32(f *Frame, x, y int, v uint32) {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*4
	p[off] = byte(v)
	p[off+1] = byte(v >> 8)
	p[off+2] = byte(v >> 16)
	p[off+3] = byte(v >> 24)
}

func get32(f *Frame, x, y int) int {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*4
	return int(p[off]) | int(p[off+1])<<8 | int(p[off+2])<<16 | int(p[off+3])<<24
}

func putBE(f *Frame, x, y int, v uint16) {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*2
	p[off] = byte(v >> 8)
	p[off+1] = byte(v)
}

func getBE(f *Frame, x, y int) int {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*2
	return int(p[off])<<8 | int(p[off+1])
}

func put16(f *Frame, x, y int, v uint16) {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*2
	p[off] = byte(v)
	p[off+1] = byte(v >> 8)
}

func get16(f *Frame, x, y int) int {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*2
	return int(p[off]) | int(p[off+1])<<8
}

func put10(f *Frame, x, y int, v uint16) {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*2
	p[off] = byte(v)
	p[off+1] = byte(v >> 8)
}

func luma10(f *Frame, x, y int) int {
	p, ls := f.Plane(0), f.Linesize(0)
	off := y*ls + x*2
	return int(p[off]) | int(p[off+1])<<8
}

// A 320×180 frame is large enough that the separable filter splits rows
// across threads. The second Apply reuses the store at a new origin, which
// fails if the weight table from the first call is left in place.
func TestResampleWindow_ThreadedRowsAndStoreReuse(t *testing.T) {
	const w, h = 320, 180
	src, err := NewVideoFrame(w, h, PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.FillBlack(); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := byte(0)
			if x%2 == 1 {
				v = 100
			}
			setLuma(src, x, y, v)
		}
	}
	dst, err := NewVideoFrame(w, h, src.PixFmt())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.CopyPropsFrom(src); err != nil {
		t.Fatal(err)
	}
	poisonPlane(dst, 0xFF)
	if !SampleCentresInside(w, h, w, h, 0.25, 0, float64(w), float64(h)) {
		t.Fatal("quarter shift of the full picture should cover every centre")
	}
	store, err := NewResampleStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Apply(dst, src, 0.25, 0, w, h, "bilinear"); err != nil {
		t.Fatal(err)
	}
	// sx = i + 0.25. Even source columns are 0 and odd columns are 100,
	// so pixel 10 is 25 and pixel 11 is 75. The last centre mixes column
	// 319 (100) with the black sample past the edge (16), which is 79.
	for _, y := range []int{0, 90, h - 1} {
		if got := lumaAt(dst, 10, y); got != 25 {
			t.Fatalf("row %d col 10 = %d, want 25", y, got)
		}
		if got := lumaAt(dst, 11, y); got != 75 {
			t.Fatalf("row %d col 11 = %d, want 75", y, got)
		}
		if got := lumaAt(dst, w-1, y); got != 79 {
			t.Fatalf("row %d last = %d, want 79", y, got)
		}
	}
	u, uls := dst.Plane(1), dst.Linesize(1)
	if int(u[0]) != 128 || int(u[(h/2-1)*uls+w/2-1]) != 128 {
		t.Fatalf("chroma = %d %d, want 128", u[0], u[(h/2-1)*uls+w/2-1])
	}
	if err := store.Apply(dst, src, 1.25, 0, w, h, "bilinear"); err != nil {
		t.Fatal(err)
	}
	// sx = i + 1.25. Pixel 10 reads column 11 and pixel 11 reads column 12.
	if got := lumaAt(dst, 10, 0); got != 75 {
		t.Fatalf("reused store col 10 = %d, want 75", got)
	}
	if got := lumaAt(dst, 11, 90); got != 25 {
		t.Fatalf("reused store col 11 = %d, want 25", got)
	}
	store.Close()
	if err := store.Apply(dst, src, 0.25, 0, w, h, "bilinear"); err == nil {
		t.Fatal("Apply after Close succeeded")
	}
}

func BenchmarkResampleWindow(b *testing.B) {
	const w, h = 320, 180
	src, err := NewVideoFrame(w, h, PixFmtByName("yuv420p"))
	if err != nil {
		b.Fatal(err)
	}
	defer src.Close()
	dst, err := NewVideoFrame(w, h, src.PixFmt())
	if err != nil {
		b.Fatal(err)
	}
	defer dst.Close()
	if err := src.FillBlack(); err != nil {
		b.Fatal(err)
	}
	store, err := NewResampleStore()
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.Apply(dst, src, 0.25, 0.25, float64(w), float64(h), "bicubic"); err != nil {
			b.Fatal(err)
		}
	}
}

func poisonPlane(f *Frame, v byte) {
	for p := 0; p < f.NumPlanes(); p++ {
		data, ls := f.Plane(p), f.Linesize(p)
		pw, ph := f.PlaneWidth(p), f.PlaneHeight(p)
		for y := 0; y < ph; y++ {
			row := data[y*ls : y*ls+pw]
			for i := range row {
				row[i] = v
			}
		}
	}
}
