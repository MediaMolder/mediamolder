// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

package raw

import "testing"

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
