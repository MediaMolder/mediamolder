// Copyright (C) 2003-2025 x264 project
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from x264/common/pixel.c:
//   x264_pixel_satd_4x4, x264_pixel_satd_8x4, x264_pixel_satd_8x8.
//
// The 8×8 SATD is computed as two 8×4 SATDs (top and bottom half-rows),
// each of which is two independent 4×4 SATDs (left and right columns).
// This decomposition is numerically equivalent to the packed-SIMD approach
// used by x264's C reference (satd_8x4 packs two 4×4 half-rows into 32-bit
// integers, but the arithmetic is separable, so the results are identical).

package imgmath

// SATD8x8 returns the 8×8 Sum of Absolute Hadamard-Transformed Differences
// between two 8×8 pixel blocks.
//
//   - fenc[i*fencStride : i*fencStride+8] is row i of the source block.
//   - ref[i*refStride  : i*refStride+8]  is row i of the reference block.
//
// Mirrors x264_pixel_satd_8x8 (common/pixel.c):
//
//	satd_8x8 = satd_8x4(top 4 rows) + satd_8x4(bottom 4 rows)
func SATD8x8(fenc []byte, fencStride int, ref []byte, refStride int) int32 {
	top := satd8x4(fenc, fencStride, ref, refStride)
	bot := satd8x4(fenc[4*fencStride:], fencStride, ref[4*refStride:], refStride)
	return top + bot
}

// satd8x4 computes the 8×4 SATD as the sum of two side-by-side 4×4 SATDs.
//
// Mirrors x264_pixel_satd_8x4 (common/pixel.c). The x264 C reference packs
// the left and right 4-column differences into the low and high 16-bit halves
// of 32-bit integers and processes them together; the two halves are
// mathematically independent (the Hadamard is linear and separable), so
// satd_8x4 == satd_4x4(left cols) + satd_4x4(right cols).
func satd8x4(fenc []byte, fencStride int, ref []byte, refStride int) int32 {
	return satd4x4(fenc, fencStride, ref, refStride) +
		satd4x4(fenc[4:], fencStride, ref[4:], refStride)
}

// satd4x4 computes the 4×4 Sum of Absolute Hadamard-Transformed Differences.
//
// Algorithm (mirrors x264_pixel_satd_4x4, common/pixel.c):
//  1. Build a 4×4 difference matrix D[i][j] = fenc[i*fencStride+j] −
//     ref[i*refStride+j].
//  2. Apply the 4-point Hadamard butterfly row-wise (HADAMARD4 macro).
//  3. Apply the 4-point Hadamard butterfly column-wise.
//  4. Return ½ · Σ |H[i][j]|  (½ normalisation matches x264_pixel_satd_4x4).
//
// HADAMARD4(d0,d1,d2,d3, s0,s1,s2,s3) expands to:
//
//	t0=s0+s1; t1=s0-s1; t2=s2+s3; t3=s2-s3
//	d0=t0+t2; d2=t0-t2; d1=t1+t3; d3=t1-t3
func satd4x4(fenc []byte, fencStride int, ref []byte, refStride int) int32 {
	var tmp [4][4]int32
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			tmp[i][j] = int32(fenc[i*fencStride+j]) - int32(ref[i*refStride+j])
		}
	}

	// Row-wise Hadamard butterfly.
	for i := 0; i < 4; i++ {
		s0, s1, s2, s3 := tmp[i][0], tmp[i][1], tmp[i][2], tmp[i][3]
		t0, t1, t2, t3 := s0+s1, s0-s1, s2+s3, s2-s3
		tmp[i][0] = t0 + t2
		tmp[i][1] = t1 + t3
		tmp[i][2] = t0 - t2
		tmp[i][3] = t1 - t3
	}

	// Column-wise Hadamard butterfly + absolute-value accumulation.
	var sum int32
	for j := 0; j < 4; j++ {
		s0, s1, s2, s3 := tmp[0][j], tmp[1][j], tmp[2][j], tmp[3][j]
		t0, t1, t2, t3 := s0+s1, s0-s1, s2+s3, s2-s3
		sum += iabs(t0+t2) + iabs(t1+t3) + iabs(t0-t2) + iabs(t1-t3)
	}

	return sum >> 1 // ½ normalisation (matches x264_pixel_satd_4x4 >> 1)
}

func iabs(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
