// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package transition

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

const pixFmtYUV420P = 0 // AV_PIX_FMT_YUV420P

// solidFrame allocates a w×h yuv420p frame with every sample of each plane set
// to the given value.
func solidFrame(t *testing.T, w, h int, yv, uv, vv byte) *av.Frame {
	t.Helper()
	f, err := av.NewVideoFrame(w, h, pixFmtYUV420P)
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	for plane, val := range []byte{yv, uv, vv} {
		p, ls := f.Plane(plane), f.Linesize(plane)
		for y := 0; y < f.PlaneHeight(plane); y++ {
			for x := 0; x < f.PlaneWidth(plane); x++ {
				p[y*ls+x] = val
			}
		}
	}
	return f
}

func TestRegistry(t *testing.T) {
	if _, ok := Lookup("fade"); !ok {
		t.Fatal(`Lookup("fade") missing`)
	}
	if _, ok := Lookup("definitely-not-a-transition"); ok {
		t.Fatal("Lookup of unknown name unexpectedly succeeded")
	}
	have := map[string]bool{}
	for _, n := range Names() {
		have[n] = true
	}
	for _, n := range []string{"wipeleft", "wiperight", "slideleft", "slideright", "circleopen", "circleclose", "radial", "vuslice"} {
		if !have[n] {
			t.Errorf("Names() missing %q", n)
		}
	}
}

func TestHelpers(t *testing.T) {
	// mix(a,b,m) = a*m + b*(1-m): m=1 → a, m=0 → b.
	if got := mix(10, 20, 1); got != 10 {
		t.Errorf("mix(10,20,1) = %v, want 10", got)
	}
	if got := mix(10, 20, 0); got != 20 {
		t.Errorf("mix(10,20,0) = %v, want 20", got)
	}
	if got := mix(10, 20, 0.5); got != 15 {
		t.Errorf("mix(10,20,0.5) = %v, want 15", got)
	}
	if got := smoothstep(0, 1, -1); got != 0 {
		t.Errorf("smoothstep below edge0 = %v, want 0", got)
	}
	if got := smoothstep(0, 1, 2); got != 1 {
		t.Errorf("smoothstep above edge1 = %v, want 1", got)
	}
	if got := smoothstep(0, 1, 0.5); got != 0.5 {
		t.Errorf("smoothstep midpoint = %v, want 0.5", got)
	}
	if got := fract(3.25); got < 0.2499 || got > 0.2501 {
		t.Errorf("fract(3.25) = %v, want ~0.25", got)
	}
	if got := clip8(300); got != 255 {
		t.Errorf("clip8(300) = %d, want 255", got)
	}
	if got := clip8(-5); got != 0 {
		t.Errorf("clip8(-5) = %d, want 0", got)
	}
}

// fade endpoints and midpoint hold exactly (progress 1 → a, 0 → b, 0.5 → mean).
func TestFade(t *testing.T) {
	a := solidFrame(t, 8, 8, 100, 110, 120)
	defer a.Close()
	b := solidFrame(t, 8, 8, 200, 130, 140)
	defer b.Close()
	out, err := av.NewVideoFrame(8, 8, pixFmtYUV420P)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	fade, _ := Lookup("fade")

	cases := []struct {
		p       float64
		y, u, v byte
	}{
		{1.0, 100, 110, 120}, // fully a
		{0.0, 200, 130, 140}, // fully b
		{0.5, 150, 120, 130}, // mean
	}
	for _, c := range cases {
		fade(out, a, b, c.p)
		if g := out.Plane(0)[0]; g != c.y {
			t.Errorf("p=%.1f Y = %d, want %d", c.p, g, c.y)
		}
		if g := out.Plane(1)[0]; g != c.u {
			t.Errorf("p=%.1f U = %d, want %d", c.p, g, c.u)
		}
		if g := out.Plane(2)[0]; g != c.v {
			t.Errorf("p=%.1f V = %d, want %d", c.p, g, c.v)
		}
	}
}

// wipeleft selects a or b across a vertical boundary at z = int(w*progress):
// x > z → b, else a.
func TestWipeleftBoundary(t *testing.T) {
	a := solidFrame(t, 8, 4, 100, 128, 128)
	defer a.Close()
	b := solidFrame(t, 8, 4, 200, 128, 128)
	defer b.Close()
	out, err := av.NewVideoFrame(8, 4, pixFmtYUV420P)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	wl, _ := Lookup("wipeleft")

	wl(out, a, b, 0.5) // z = int(8*0.5) = 4
	row := out.Plane(0)
	for x := 0; x <= 4; x++ {
		if row[x] != 100 {
			t.Errorf("x=%d Y=%d, want a (100)", x, row[x])
		}
	}
	for x := 5; x < 8; x++ {
		if row[x] != 200 {
			t.Errorf("x=%d Y=%d, want b (200)", x, row[x])
		}
	}
}

// Every registered transition must write every sample of the valid region of
// every plane — a guard against a transition leaving pixels uninitialized.
// Detected by running each transition into two buffers seeded with different
// values: a written sample is identical across runs (transitions are
// deterministic), an unwritten one keeps its seed and differs.
func TestAllTransitionsFillOutput(t *testing.T) {
	a := solidFrame(t, 16, 16, 80, 90, 100)
	defer a.Close()
	b := solidFrame(t, 16, 16, 180, 150, 160)
	defer b.Close()

	render := func(name string, seed byte, p float64) *av.Frame {
		out, err := av.NewVideoFrame(16, 16, pixFmtYUV420P)
		if err != nil {
			t.Fatal(err)
		}
		for plane := 0; plane < out.NumPlanes(); plane++ {
			buf := out.Plane(plane)
			for i := range buf {
				buf[i] = seed
			}
		}
		fn, _ := Lookup(name)
		fn(out, a, b, p)
		return out
	}

	for _, name := range Names() {
		for _, p := range []float64{1.0, 0.5, 0.0} {
			o1 := render(name, 0x00, p)
			o2 := render(name, 0xFF, p)
			for plane := 0; plane < o1.NumPlanes(); plane++ {
				w, h := o1.PlaneWidth(plane), o1.PlaneHeight(plane)
				l1, l2 := o1.Linesize(plane), o2.Linesize(plane)
				b1, b2 := o1.Plane(plane), o2.Plane(plane)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						if b1[y*l1+x] != b2[y*l2+x] {
							t.Fatalf("%s p=%.1f: plane %d (%d,%d) left unwritten", name, p, plane, x, y)
						}
					}
				}
			}
			o1.Close()
			o2.Close()
		}
	}
}
