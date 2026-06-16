// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Full-resolution measurement frames for dissolve edge refinement.
//
// InitFullres builds the SAME LowresFrame structure the lookahead uses, but
// from the full-resolution luma WITHOUT downsampling: plane 0 is a padded
// copy of the source and planes 1–3 are the half-pel averages. Everything
// downstream — the 8×8 MB grid, diamond ME, MV/cost caches, 9-mode intra,
// LowresFrameCost/LowresFrameCostReverse — works unchanged; a full-res frame
// simply carries 4× the macroblocks, which is exactly the 4× sample gain
// that makes low-alpha blend frames visible (the half-res downsampling
// filter attenuates the faint incoming-scene ghost most strongly in the high
// frequencies where it is most distinctive).
package imgmath

import "fmt"

// InitFullres creates a populated measurement frame from a full-resolution
// 8-bit luma plane at NATIVE resolution. Same contract as InitLowres minus
// the downsample. Memory: four padded planes (~4× the source plane).
func InitFullres(src []byte, srcW, srcH, srcStride int) (*LowresFrame, error) {
	if srcStride < srcW {
		return nil, fmt.Errorf("InitFullres: srcStride %d < srcW %d", srcStride, srcW)
	}
	if len(src) < srcH*srcStride {
		return nil, fmt.Errorf("InitFullres: src len %d too small for %dx%d stride %d",
			len(src), srcW, srcH, srcStride)
	}
	if srcW < 2 || srcH < 2 {
		return nil, fmt.Errorf("InitFullres: source too small (%dx%d)", srcW, srcH)
	}

	f, err := NewLowresFrame(srcW, srcH)
	if err != nil {
		return nil, err
	}
	f.Stride = srcW + 2*LowresPadH
	totalRows := srcH + 2*LowresPadV
	for p := 0; p < 4; p++ {
		f.planeBuf[p] = make([]byte, f.Stride*totalRows)
	}

	// Pad the source with a duplicate last column and row so the half-pel
	// filters can read x+1 / y+1 at the edges without special cases (the
	// same trick InitLowres uses).
	padSrcStride := srcW + 1
	padSrc := make([]byte, (srcH+1)*padSrcStride)
	for y := 0; y < srcH; y++ {
		row := src[y*srcStride : y*srcStride+srcW]
		dst := padSrc[y*padSrcStride:]
		copy(dst, row)
		dst[srcW] = dst[srcW-1]
	}
	copy(padSrc[srcH*padSrcStride:], padSrc[(srcH-1)*padSrcStride:srcH*padSrcStride])

	// Plane 0: source copy. Planes 1–3: bilinear half-pel variants
	// (H = avg(x, x+1), V = avg(y, y+1), HV = avg of the 2×2 quad — the
	// same accumulation order as the lowres FILTER macro, applied at native
	// resolution).
	for y := 0; y < srcH; y++ {
		row0 := padSrc[y*padSrcStride:]
		row1 := padSrc[(y+1)*padSrcStride:]
		off := f.planeOffset(0, y)
		dst0 := f.planeBuf[0][off:]
		dsth := f.planeBuf[1][off:]
		dstv := f.planeBuf[2][off:]
		dstc := f.planeBuf[3][off:]
		for x := 0; x < srcW; x++ {
			a, b := row0[x], row0[x+1]
			c, d := row1[x], row1[x+1]
			dst0[x] = a
			dsth[x] = byte((uint16(a) + uint16(b) + 1) >> 1)
			dstv[x] = byte((uint16(a) + uint16(c) + 1) >> 1)
			dstc[x] = lrFilter(a, c, b, d)
		}
	}

	for p := 0; p < 4; p++ {
		expandBorder(f.planeBuf[p], f.Stride, srcW, srcH, LowresPadH, LowresPadV)
	}

	var lumaSum uint64
	for y := 0; y < srcH; y++ {
		off := f.planeOffset(0, y)
		for _, v := range f.planeBuf[0][off : off+srcW] {
			lumaSum += uint64(v)
		}
	}
	f.AvgLuma = float32(lumaSum) / float32(srcW*srcH)

	return f, nil
}

// PlaneSAD returns the mean absolute difference between the active integer-
// pel planes of two frames of identical dimensions — the alignment
// fingerprint for windowed re-decode (a re-decoded frame downsampled with
// the same lowres filter should match the retained lowres plane almost
// exactly; a mismatch means the decode window is misaligned).
func (f *LowresFrame) PlaneSAD(o *LowresFrame) (float64, error) {
	if f.Width != o.Width || f.Height != o.Height {
		return 0, fmt.Errorf("PlaneSAD: dimension mismatch %dx%d vs %dx%d",
			f.Width, f.Height, o.Width, o.Height)
	}
	var sum uint64
	for y := 0; y < f.Height; y++ {
		fo := f.planeOffset(0, y)
		oo := o.planeOffset(0, y)
		fr := f.planeBuf[0][fo : fo+f.Width]
		or := o.planeBuf[0][oo : oo+o.Width]
		for x := range fr {
			d := int(fr[x]) - int(or[x])
			if d < 0 {
				d = -d
			}
			sum += uint64(d)
		}
	}
	return float64(sum) / float64(f.Width*f.Height), nil
}
