// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package raw

import (
	"encoding/binary"
	"image"
	"math"
	"runtime"
	"sync"
)

// This file is the pure-Go, dependency-free downscale behind [DecodeDevelopScaled]: a
// separable Catmull-Rom resample of a packed 16-bit RGB buffer straight into the [Develop]
// raster, so a consumer that only ever shows a bounded master (a viewer's 4096-pixel grid)
// never materialises the full-sensor raster — 185 MB of NRGBA64 for a 24-megapixel sensor,
// and a second pass over it to shrink — and never pays for a per-pixel scaler.
//
// The filter is the one a consumer would otherwise apply with x/image/draw's CatmullRom:
// the cubic with a = -0.5, and, when shrinking, a support widened by the scale factor (so
// every source sample contributes and nothing aliases), taps clipped to the source and the
// weights renormalised at the edges. Both passes are split across the CPUs by rows: the
// horizontal pass over source rows, the vertical over destination rows. The result does not
// depend on the number of workers.

// scaledSize is the destination size for a longest edge of maxPixel: the same rounding a
// consumer uses, so a cached master keyed by its size stays the same size.
func scaledSize(w, h, maxPixel int) (int, int, bool) {
	long := w
	if h > w {
		long = h
	}
	if maxPixel <= 0 || long == 0 || maxPixel >= long {
		return w, h, false
	}
	f := float64(maxPixel) / float64(long)
	nw, nh := int(float64(w)*f+0.5), int(float64(h)*f+0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh, true
}

// catmullRom is the Catmull-Rom cubic (a = -0.5), support 2.
func catmullRom(t float64) float64 {
	if t < 0 {
		t = -t
	}
	switch {
	case t < 1:
		return (1.5*t-2.5)*t*t + 1
	case t < 2:
		return ((-0.5*t+2.5)*t-4)*t + 2
	}
	return 0
}

// tap is one destination index's contributing source range and weights.
type tap struct {
	first   int
	weights []float32
}

// distrib is the per-index weight table of one axis: a destination of n from a source of
// m. Shrinking widens the kernel by the scale so that every source sample is weighed;
// enlarging (not used by the develop, but correct) keeps the kernel as it is.
func distrib(n, m int) []tap {
	scale := float64(m) / float64(n)
	half, arg := 2.0, 1.0
	if scale > 1 {
		half *= scale
		arg = 1 / scale
	}
	out := make([]tap, n)
	for i := range out {
		c := (float64(i) + 0.5) * scale
		lo := int(math.Floor(c - half))
		hi := int(math.Ceil(c + half))
		if lo < 0 {
			lo = 0
		}
		if hi > m {
			hi = m
		}
		ws := make([]float64, 0, hi-lo)
		sum := 0.0
		for j := lo; j < hi; j++ {
			w := catmullRom((float64(j) + 0.5 - c) * arg)
			ws = append(ws, w)
			sum += w
		}
		t := tap{first: lo, weights: make([]float32, len(ws))}
		for k, w := range ws {
			t.weights[k] = float32(w / sum)
		}
		out[i] = t
	}
	return out
}

// scaleRGB16 resamples a packed RGB16 buffer (w×h, 3 samples per pixel, host order) to
// nw×nh and writes it into a new NRGBA64 (big-endian samples, alpha 0xFFFF). workers ≤ 0
// means one per CPU.
//
// The two passes run in chunks of destination rows: the window of source rows a chunk
// needs is shrunk horizontally into a float32 buffer the size of that window, then the
// chunk's rows are shrunk vertically from it. The windows of neighbouring chunks overlap by
// the kernel's support, a few rows, which are shrunk again; what is never built is a
// horizontal pass over the whole sensor (189 MB of float32 for a 24-megapixel sensor to
// 4096), so the develop's transient is the chunk's window. Within a chunk each pass is
// banded across the workers by rows.
func scaleRGB16(src []uint16, w, h, nw, nh, workers int) *image.NRGBA64 {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	xs := distrib(nw, w)
	ys := distrib(nh, h)
	dst := image.NewNRGBA64(image.Rect(0, 0, nw, nh))
	const chunk = 64
	var tmp []float32
	for y0 := 0; y0 < nh; y0 += chunk {
		y1 := y0 + chunk
		if y1 > nh {
			y1 = nh
		}
		// The source rows this chunk reads: the taps' firsts rise with the row.
		s0 := ys[y0].first
		s1 := ys[y1-1].first + len(ys[y1-1].weights)
		rows := s1 - s0
		if cap(tmp) < rows*nw*3 {
			tmp = make([]float32, rows*nw*3)
		}
		tmp = tmp[:rows*nw*3]
		// Pass one: the window's source rows, shrunk to nw, as float32 RGB.
		parallelRows(rows, workers, func(r0, r1 int) {
			for r := r0; r < r1; r++ {
				srow := src[(s0+r)*w*3 : (s0+r+1)*w*3]
				trow := tmp[r*nw*3 : (r+1)*nw*3]
				for x, t := range xs {
					var cr, cg, cb float32
					s := t.first * 3
					for _, wt := range t.weights {
						cr += wt * float32(srow[s])
						cg += wt * float32(srow[s+1])
						cb += wt * float32(srow[s+2])
						s += 3
					}
					trow[x*3], trow[x*3+1], trow[x*3+2] = cr, cg, cb
				}
			}
		})
		// Pass two: the chunk's destination rows from the shrunk window.
		parallelRows(y1-y0, workers, func(d0, d1 int) {
			for y := y0 + d0; y < y0+d1; y++ {
				t := ys[y]
				drow := dst.Pix[y*dst.Stride : y*dst.Stride+nw*8]
				for x := 0; x < nw; x++ {
					var cr, cg, cb float32
					s := (t.first-s0)*nw*3 + x*3
					for _, wt := range t.weights {
						cr += wt * tmp[s]
						cg += wt * tmp[s+1]
						cb += wt * tmp[s+2]
						s += nw * 3
					}
					d := x * 8
					binary.BigEndian.PutUint16(drow[d:d+2], ftou16(cr))
					binary.BigEndian.PutUint16(drow[d+2:d+4], ftou16(cg))
					binary.BigEndian.PutUint16(drow[d+4:d+6], ftou16(cb))
					binary.BigEndian.PutUint16(drow[d+6:d+8], 0xFFFF)
				}
			}
		})
	}
	return dst
}

// ftou16 rounds a sample to 16 bits, clamped: a Catmull-Rom lobe can overshoot an edge.
func ftou16(f float32) uint16 {
	i := int32(f + 0.5)
	if i < 0 {
		return 0
	}
	if i > 0xFFFF {
		return 0xFFFF
	}
	return uint16(i)
}

// parallelRows runs fn over [0, n) in contiguous bands, one goroutine each, and waits.
func parallelRows(n, workers int, fn func(y0, y1 int)) {
	if workers <= 1 || n <= 1 {
		fn(0, n)
		return
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		y0, y1 := n*i/workers, n*(i+1)/workers
		if y0 == y1 {
			continue
		}
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			fn(y0, y1)
		}(y0, y1)
	}
	wg.Wait()
}

// transcodeRGB16 is the full-size path: the packed buffer into an NRGBA64, row by row across
// the CPUs. (The result is the same as the per-sample loop it replaces.)
func transcodeRGB16(src []uint16, w, h int) *image.NRGBA64 {
	out := image.NewNRGBA64(image.Rect(0, 0, w, h))
	parallelRows(h, runtime.GOMAXPROCS(0), func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			srow := src[y*w*3 : (y+1)*w*3]
			drow := out.Pix[y*out.Stride : y*out.Stride+w*8]
			for x := 0; x < w; x++ {
				s, d := x*3, x*8
				binary.BigEndian.PutUint16(drow[d:d+2], srow[s])
				binary.BigEndian.PutUint16(drow[d+2:d+4], srow[s+1])
				binary.BigEndian.PutUint16(drow[d+4:d+6], srow[s+2])
				binary.BigEndian.PutUint16(drow[d+6:d+8], 0xFFFF)
			}
		}
	})
	return out
}
