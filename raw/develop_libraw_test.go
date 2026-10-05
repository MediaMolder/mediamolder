// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

package raw

import (
	"bytes"
	"testing"
)

// DecodeDevelop produces a 16-bit, linear, Rec.2020 master. The decode itself validates the
// buffer is w*h*3*2 bytes (6 bytes/pixel ⇒ genuinely 16-bit), so reaching a non-uniform NRGBA64
// proves the high-precision path end to end.
func TestDecodeDevelopFixture(t *testing.T) {
	d, err := DecodeDevelop("testdata/sample.dng")
	if err != nil {
		t.Fatalf("DecodeDevelop: %v", err)
	}
	if d.Image == nil {
		t.Fatal("nil image")
	}
	if d.ColorSpace != ColorRec2020 {
		t.Fatalf("colour space = %v, want Rec.2020", d.ColorSpace)
	}
	if d.Version != DevelopVersion {
		t.Fatalf("version = %q, want %q", d.Version, DevelopVersion)
	}
	if b := d.Image.Bounds(); b.Dx() <= 0 || b.Dy() <= 0 {
		t.Fatalf("empty bounds %v", b)
	}
	if IsUniform(d.Image) {
		t.Fatal("develop is uniform/black")
	}

	// Informational: count RGB samples carrying sub-8-bit detail (a nonzero low byte) — the mark
	// of real 16-bit precision rather than an 8-bit decode widened to 16-bit.
	px := d.Image.Pix
	subByte := 0
	for i := 0; i+8 <= len(px); i += 8 {
		if px[i+1] != 0 || px[i+3] != 0 || px[i+5] != 0 { // low bytes of R,G,B (big-endian)
			subByte++
		}
	}
	t.Logf("develop %v %s, %d/%d px carry sub-8-bit detail", d.Image.Bounds().Size(), d.ColorSpace, subByte, len(px)/8)
}

func TestDecodeDevelopNonRAW(t *testing.T) {
	if _, err := DecodeDevelop("testdata/sample.jpg"); err != ErrUnsupported {
		t.Errorf("DecodeDevelop(non-RAW) err = %v, want ErrUnsupported", err)
	}
}

// DecodeDevelopScaled shrinks the master from LibRaw's output: the longest edge is the bound,
// the aspect is kept, the colour space and version are the develop's, and a bound the sensor
// is already within returns the full size — never enlarged.
func TestDecodeDevelopScaledFixture(t *testing.T) {
	full, err := DecodeDevelop("testdata/sample.dng")
	if err != nil {
		t.Fatalf("DecodeDevelop: %v", err)
	}
	fb := full.Image.Bounds()
	long := fb.Dx()
	if fb.Dy() > long {
		long = fb.Dy()
	}
	bound := long / 2
	d, err := DecodeDevelopScaled("testdata/sample.dng", bound)
	if err != nil {
		t.Fatalf("DecodeDevelopScaled: %v", err)
	}
	nw, nh, shrink := scaledSize(fb.Dx(), fb.Dy(), bound)
	if !shrink {
		t.Fatalf("the fixture is %v: no shrink to %d", fb, bound)
	}
	if b := d.Image.Bounds(); b.Dx() != nw || b.Dy() != nh {
		t.Fatalf("scaled bounds %v, want %d×%d", b, nw, nh)
	}
	if d.ColorSpace != full.ColorSpace || d.Version != full.Version {
		t.Fatalf("scaled develop differs: %v %q vs %v %q", d.ColorSpace, d.Version, full.ColorSpace, full.Version)
	}
	if IsUniform(d.Image) {
		t.Fatal("scaled develop is uniform")
	}
	// The shrunk master is the full one, shrunk: the same filter over the same samples.
	src := make([]uint16, fb.Dx()*fb.Dy()*3)
	for y := 0; y < fb.Dy(); y++ {
		for x := 0; x < fb.Dx(); x++ {
			r, g, b, _ := sampleAt(full.Image, x, y)
			i := (y*fb.Dx() + x) * 3
			src[i], src[i+1], src[i+2] = r, g, b
		}
	}
	want := scaleRGB16(src, fb.Dx(), fb.Dy(), nw, nh, 1)
	if !bytes.Equal(want.Pix, d.Image.Pix) {
		t.Fatal("the scaled develop is not the full develop shrunk")
	}
	same, err := DecodeDevelopScaled("testdata/sample.dng", long)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(same.Image.Pix, full.Image.Pix) {
		t.Fatal("a bound the sensor is within changed the master")
	}
}
