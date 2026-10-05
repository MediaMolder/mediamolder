// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package raw

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"math/rand"
	"testing"
)

func TestScaledSizeRoundsLikeAConsumer(t *testing.T) {
	cases := []struct {
		w, h, max, nw, nh int
		shrink            bool
	}{
		{6032, 4032, 4096, 4096, 2738, true},  // a 24-megapixel sensor to a viewer's grid
		{4032, 6032, 4096, 2738, 4096, true},  // portrait
		{4000, 3000, 4096, 4000, 3000, false}, // within bounds: never enlarged
		{4096, 2738, 4096, 4096, 2738, false},
		{6032, 4032, 0, 6032, 4032, false}, // 0: full size
		{10, 1, 2, 2, 1, true},             // a row can never round to nothing
		{0, 0, 100, 0, 0, false},
	}
	for _, c := range cases {
		nw, nh, shrink := scaledSize(c.w, c.h, c.max)
		if nw != c.nw || nh != c.nh || shrink != c.shrink {
			t.Errorf("scaledSize(%d, %d, %d) = %d×%d %v, want %d×%d %v", c.w, c.h, c.max, nw, nh, shrink, c.nw, c.nh, c.shrink)
		}
	}
}

// The weights of one axis sum to one, cover every source sample when shrinking, and lie
// within the source.
func TestDistribWeighsEverySourceSampleOnce(t *testing.T) {
	for _, c := range []struct{ n, m int }{{4096, 6032}, {2738, 4032}, {3, 10}, {10, 10}, {10, 3}} {
		taps := distrib(c.n, c.m)
		covered := make([]bool, c.m)
		for i, tp := range taps {
			var sum float64
			for k, w := range tp.weights {
				sum += float64(w)
				j := tp.first + k
				if j < 0 || j >= c.m {
					t.Fatalf("%d←%d: index %d of tap %d is outside the source", c.n, c.m, j, i)
				}
				if w != 0 {
					covered[j] = true
				}
			}
			if math.Abs(sum-1) > 1e-5 {
				t.Fatalf("%d←%d: tap %d weighs %v", c.n, c.m, i, sum)
			}
		}
		if c.m > c.n {
			for j, ok := range covered {
				if !ok {
					t.Fatalf("%d←%d: source sample %d is weighed by no tap", c.n, c.m, j)
				}
			}
		}
	}
}

func sampleAt(img *image.NRGBA64, x, y int) (r, g, b, a uint16) {
	d := img.PixOffset(x, y)
	p := img.Pix[d : d+8]
	return binary.BigEndian.Uint16(p[0:2]), binary.BigEndian.Uint16(p[2:4]), binary.BigEndian.Uint16(p[4:6]), binary.BigEndian.Uint16(p[6:8])
}

// A flat field stays flat (every weight set sums to one), at every pixel, alpha opaque.
func TestScaleKeepsAFlatField(t *testing.T) {
	w, h := 301, 203
	src := make([]uint16, w*h*3)
	for i := 0; i < w*h; i++ {
		src[i*3], src[i*3+1], src[i*3+2] = 12345, 60000, 7
	}
	nw, nh, _ := scaledSize(w, h, 100)
	dst := scaleRGB16(src, w, h, nw, nh, 0)
	if b := dst.Bounds(); b.Dx() != nw || b.Dy() != nh {
		t.Fatalf("bounds %v, want %d×%d", b, nw, nh)
	}
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			if r, g, b, a := sampleAt(dst, x, y); r != 12345 || g != 60000 || b != 7 || a != 0xFFFF {
				t.Fatalf("(%d,%d) = %d %d %d %d", x, y, r, g, b, a)
			}
		}
	}
}

// A horizontal ramp shrinks to a ramp: monotone across the row, and away from the edges
// (where the clipped taps lean the weights inward, as any clamped resampler's do) each
// destination pixel is the ramp's value at its own centre.
func TestScaleKeepsARamp(t *testing.T) {
	w, h := 1000, 20
	src := make([]uint16, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint16(x * 65535 / (w - 1))
			i := (y*w + x) * 3
			src[i], src[i+1], src[i+2] = v, v, v
		}
	}
	nw, nh := 500, 10
	dst := scaleRGB16(src, w, h, nw, nh, 0)
	prev := -1
	for x := 0; x < nw; x++ {
		r, g, b, a := sampleAt(dst, x, 5)
		if g != r || b != r || a != 0xFFFF {
			t.Fatalf("x=%d: %d %d %d %d", x, r, g, b, a)
		}
		if int(r) < prev {
			t.Fatalf("not monotone at %d: %d after %d", x, r, prev)
		}
		prev = int(r)
		if x < 4 || x >= nw-4 {
			continue
		}
		// The ramp at the centre of this destination pixel: source index 2x + 0.5.
		want := (2*float64(x) + 0.5) * 65535 / float64(w-1)
		if d := math.Abs(float64(r) - want); d > 2 {
			t.Fatalf("x=%d: %d, want about %.1f", x, r, want)
		}
	}
}

// The result is the same with one worker and with many: a pixel's sum does not depend on
// which goroutine computed it.
func TestScaleIsTheSameAcrossWorkers(t *testing.T) {
	w, h := 613, 419
	rng := rand.New(rand.NewSource(1))
	src := make([]uint16, w*h*3)
	for i := range src {
		src[i] = uint16(rng.Intn(65536))
	}
	nw, nh, _ := scaledSize(w, h, 257)
	one := scaleRGB16(src, w, h, nw, nh, 1)
	many := scaleRGB16(src, w, h, nw, nh, 7)
	if !bytes.Equal(one.Pix, many.Pix) {
		t.Fatal("the workers changed the pixels")
	}
	more := scaleRGB16(src, w, h, nw, nh, 1000) // more workers than rows
	if !bytes.Equal(one.Pix, more.Pix) {
		t.Fatal("more workers than rows changed the pixels")
	}
}

// An overshooting lobe is clamped, never wrapped: a hard edge from black to full white.
func TestScaleClampsAnOvershoot(t *testing.T) {
	w, h := 64, 4
	src := make([]uint16, w*h*3)
	for y := 0; y < h; y++ {
		for x := w / 2; x < w; x++ {
			i := (y*w + x) * 3
			src[i], src[i+1], src[i+2] = 65535, 65535, 65535
		}
	}
	dst := scaleRGB16(src, w, h, 32, 2, 0)
	for x := 0; x < 32; x++ {
		r, _, _, _ := sampleAt(dst, x, 0)
		if x < 12 && r != 0 {
			t.Fatalf("x=%d: black side is %d", x, r)
		}
		if x > 19 && r != 65535 {
			t.Fatalf("x=%d: white side is %d", x, r)
		}
	}
}

// The full-size transcode is the per-sample loop it replaced: big-endian samples, opaque.
func TestTranscodeIsBigEndianAndOpaque(t *testing.T) {
	w, h := 5, 3
	src := make([]uint16, w*h*3)
	for i := range src {
		src[i] = uint16(i * 977)
	}
	out := transcodeRGB16(src, w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			r, g, b, a := sampleAt(out, x, y)
			if r != src[i] || g != src[i+1] || b != src[i+2] || a != 0xFFFF {
				t.Fatalf("(%d,%d) = %d %d %d %d, want %d %d %d", x, y, r, g, b, a, src[i], src[i+1], src[i+2])
			}
		}
	}
}

func BenchmarkScale24MP(b *testing.B) {
	w, h := 6032, 4032
	src := make([]uint16, w*h*3)
	for i := range src {
		src[i] = uint16(i)
	}
	nw, nh, _ := scaledSize(w, h, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleRGB16(src, w, h, nw, nh, 0)
	}
}
