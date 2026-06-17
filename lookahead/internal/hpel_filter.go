// Copyright (C) 2003-2025 x264 project
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from x264/common/mc.c:
//   x264_frame_init_lowres, frame_init_lowres_core,
//   x264_frame_expand_border_lowres → plane_expand_border.

package imgmath

import "fmt"

// InitLowres creates a populated LowresFrame from a full-resolution 8-bit
// luma plane.
//
//   - src is a packed, row-major byte slice of size ≥ srcH × srcStride.
//   - srcW, srcH are the active pixel dimensions (width and height).
//   - srcStride is the byte stride of src (must be ≥ srcW).
//
// The output has dimensions (srcW/2) × (srcH/2) (truncated). Each of the
// four lowres planes is filled with filtered samples and its border is
// replicated for motion-estimation padding up to LowresPadH/LowresPadV.
//
// Mirrors x264_frame_init_lowres (common/mc.c).
func InitLowres(src []byte, srcW, srcH, srcStride int) (*LowresFrame, error) {
	if srcStride < srcW {
		return nil, fmt.Errorf("InitLowres: srcStride %d < srcW %d", srcStride, srcW)
	}
	if len(src) < srcH*srcStride {
		return nil, fmt.Errorf("InitLowres: src len %d too small for %dx%d stride %d",
			len(src), srcW, srcH, srcStride)
	}
	if srcW < 2 || srcH < 2 {
		return nil, fmt.Errorf("InitLowres: source too small (%dx%d)", srcW, srcH)
	}

	lrW := srcW / 2
	lrH := srcH / 2

	f, err := NewLowresFrame(lrW, lrH)
	if err != nil {
		return nil, err
	}
	f.Stride = lrW + 2*LowresPadH
	totalRows := lrH + 2*LowresPadV
	for p := 0; p < 4; p++ {
		f.planeBuf[p] = make([]byte, f.Stride*totalRows)
	}

	// x264 pre-pads the source with a duplicate last column and duplicate last
	// row so that frame_init_lowres_core can always access src[2x+2] and
	// src[2y+2] at the right and bottom edges without a special case.
	padSrcStride := srcW + 1
	padSrc := make([]byte, (srcH+1)*padSrcStride)
	for y := 0; y < srcH; y++ {
		row := src[y*srcStride : y*srcStride+srcW]
		dst := padSrc[y*padSrcStride:]
		copy(dst, row)
		dst[srcW] = dst[srcW-1] // duplicate last column
	}
	// Duplicate last row.
	lastSrc := padSrc[(srcH-1)*padSrcStride : srcH*padSrcStride]
	copy(padSrc[srcH*padSrcStride:], lastSrc)

	// frame_init_lowres_core: for each lowres position (x, y), read three
	// consecutive source rows (2y, 2y+1, 2y+2) and compute four
	// half-pixel-filtered samples using the FILTER macro.
	//
	//   dst0[x] = FILTER(row0[2x],   row1[2x],   row0[2x+1], row1[2x+1])
	//   dsth[x] = FILTER(row0[2x+1], row1[2x+1], row0[2x+2], row1[2x+2])
	//   dstv[x] = FILTER(row1[2x],   row2[2x],   row1[2x+1], row2[2x+1])
	//   dstc[x] = FILTER(row1[2x+1], row2[2x+1], row1[2x+2], row2[2x+2])
	//
	// Source: x264/common/mc.c, frame_init_lowres_core.
	for y := 0; y < lrH; y++ {
		row0 := padSrc[(2*y)*padSrcStride:]
		row1 := padSrc[(2*y+1)*padSrcStride:]
		row2 := padSrc[(2*y+2)*padSrcStride:]

		off := f.planeOffset(0, y)
		dst0 := f.planeBuf[0][off:]
		dsth := f.planeBuf[1][off:]
		dstv := f.planeBuf[2][off:]
		dstc := f.planeBuf[3][off:]

		for x := 0; x < lrW; x++ {
			x2 := 2 * x
			dst0[x] = lrFilter(row0[x2], row1[x2], row0[x2+1], row1[x2+1])
			dsth[x] = lrFilter(row0[x2+1], row1[x2+1], row0[x2+2], row1[x2+2])
			dstv[x] = lrFilter(row1[x2], row2[x2], row1[x2+1], row2[x2+1])
			dstc[x] = lrFilter(row1[x2+1], row2[x2+1], row1[x2+2], row2[x2+2])
		}
	}

	// Expand borders by replication so that ME can access any position in
	// [−LowresPadH, Width+LowresPadH) × [−LowresPadV, Height+LowresPadV)
	// without bounds checks.
	// Mirrors x264_frame_expand_border_lowres → plane_expand_border.
	for p := 0; p < 4; p++ {
		expandBorder(f.planeBuf[p], f.Stride, lrW, lrH, LowresPadH, LowresPadV)
	}

	// Compute average luma from the active region of planeBuf[0] (integer-pel
	// lowres luma).  This is cheap — one pass over the same pixels already
	// written above — and gives the fade detector a direct brightness signal.
	var lumaSum uint64
	for y := 0; y < lrH; y++ {
		off := f.planeOffset(0, y)
		for _, v := range f.planeBuf[0][off : off+lrW] {
			lumaSum += uint64(v)
		}
	}
	if lrW > 0 && lrH > 0 {
		f.AvgLuma = float32(lumaSum) / float32(lrW*lrH)
	}

	return f, nil
}

// lrFilter is the FILTER(a,b,c,d) macro from x264/common/mc.c
// (frame_init_lowres_core):
//
//	FILTER(a,b,c,d) = ( ((a+b+1)>>1) + ((c+d+1)>>1) + 1 ) >> 1
//
// It computes a two-level bilinear average, consistent with the asm
// implementations (which operate in the same accumulation order).
func lrFilter(a, b, c, d byte) byte {
	return byte((((uint16(a) + uint16(b) + 1) >> 1) + ((uint16(c) + uint16(d) + 1) >> 1) + 1) >> 1)
}

// expandBorder replicates the image border into the padding region of a single
// plane. The plane slice starts at the absolute top-left of the padded buffer
// (byte 0 = top-left padding corner). Active pixel (x, y) is at index
// (y+padV)*stride + (x+padH).
//
// Mirrors plane_expand_border in x264/common/frame.c.
func expandBorder(plane []byte, stride, width, height, padH, padV int) {
	// Step 1: horizontal padding for all active rows.
	for y := 0; y < height; y++ {
		rowBase := (y + padV) * stride
		left := plane[rowBase+padH]          // first active pixel
		right := plane[rowBase+padH+width-1] // last active pixel
		for x := 0; x < padH; x++ {
			plane[rowBase+x] = left
			plane[rowBase+padH+width+x] = right
		}
	}

	// Step 2: vertical padding — replicate the first and last active rows
	// (including the now-filled horizontal padding) upward and downward.
	firstRow := padV * stride
	lastRow := (height - 1 + padV) * stride
	for y := 0; y < padV; y++ {
		topDst := y * stride
		botDst := (height + padV + y) * stride
		copy(plane[topDst:topDst+stride], plane[firstRow:firstRow+stride])
		copy(plane[botDst:botDst+stride], plane[lastRow:lastRow+stride])
	}
}
