// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.

package imgmath

import "testing"

func TestEstimatedKernelSize(t *testing.T) {
	// 256×144 → sqrt(36864)=192 → round(4+1)=5
	if got := EstimatedKernelSize(256, 144); got != 5 {
		t.Errorf("256×144: got %d, want 5", got)
	}
	// 64×36 → sqrt(2304)=48 → round(4+48/192)=round(4.25)=4 (even → pad to 5)
	if got := EstimatedKernelSize(64, 36); got != 5 {
		t.Errorf("64×36: got %d, want 5", got)
	}
}

func TestCanny_Uniform(t *testing.T) {
	// Uniform image → no edges.
	w, h := 8, 8
	gray := make([]byte, w*h)
	for i := range gray {
		gray[i] = 128
	}
	edges := Canny(gray, w, h, 50, 150, false)
	for i, v := range edges {
		if v != 0 {
			t.Errorf("uniform: pixel %d = %d, want 0", i, v)
		}
	}
}

func TestCanny_StepEdge(t *testing.T) {
	// Vertical step edge (left half black, right half white) — interior
	// pixels (rows 1..h-2) on the boundary must be detected as edges.
	w, h := 10, 10
	gray := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= w/2 {
				gray[y*w+x] = 255
			}
		}
	}
	edges := Canny(gray, w, h, 10, 50, false)
	// Check that at least one interior pixel on the step column is an edge.
	foundEdge := false
	for y := 1; y < h-1; y++ {
		if edges[y*w+w/2] == 255 || edges[y*w+(w/2-1)] == 255 {
			foundEdge = true
			break
		}
	}
	if !foundEdge {
		t.Error("step edge: expected edges along the vertical boundary")
	}
}

func TestCanny_OutputLength(t *testing.T) {
	w, h := 5, 5
	gray := make([]byte, w*h)
	edges := Canny(gray, w, h, 10, 50, false)
	if len(edges) != w*h {
		t.Errorf("output length %d, want %d", len(edges), w*h)
	}
}

func TestDilate_SinglePixel(t *testing.T) {
	// A single 255 pixel in a 5×5 zero image, kernel=3 → 3×3 block of 255.
	w, h := 5, 5
	img := make([]byte, w*h)
	img[2*w+2] = 255 // centre
	out := Dilate(img, w, h, 3)
	for y := 1; y <= 3; y++ {
		for x := 1; x <= 3; x++ {
			if out[y*w+x] != 255 {
				t.Errorf("dilate: pixel (%d,%d) = %d, want 255", x, y, out[y*w+x])
			}
		}
	}
	// Corners of the image must remain 0.
	if out[0] != 0 || out[4] != 0 {
		t.Error("dilate: corners should be 0")
	}
}

func TestDilate_KernelOne(t *testing.T) {
	// kernelSize=1 → identity copy.
	w, h := 3, 3
	img := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}
	out := Dilate(img, w, h, 1)
	for i := range img {
		if out[i] != img[i] {
			t.Errorf("kernel=1: out[%d]=%d, want %d", i, out[i], img[i])
		}
	}
}
