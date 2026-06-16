// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Per-frame psy AC ("visual") energy, the perceptual texture measure x264's
// psy-rd uses (encoder/rdo.c, x264_psy ac_energy):
//
//	energy(block) = SATD(block, zero) − SAD(block, zero)>>1
//
// SATD against a zero block is the (½-normalised) Hadamard transform of the
// raw pixels; its DC coefficient is half the pixel sum, which is exactly
// SAD-vs-zero>>1 for unsigned pixels — so the subtraction removes the DC
// term, leaving pure high-frequency (texture/detail) energy. Reference-free:
// one value per frame, no lag dimension.
//
// Why the scene detector wants it: blending two uncorrelated scenes
// attenuates AC energy quadratically (~alpha²+(1−alpha)² for the mix), so a
// dissolve carves a U-shaped dip into the per-frame energy spanning exactly
// [S, E−1], steepest at the edges and flat at the midpoint — complementary
// geometry to the inter/intra ratio ramps, whose gentlest part (the foot)
// sits at the edges.
package imgmath

var zeroBlock [8]byte

// sadVsZero8x8 returns the sum of the 64 pixels of an 8×8 block — the SAD
// against a zero reference for unsigned pixels.
func sadVsZero8x8(pix []byte, stride int) int32 {
	var sum int32
	for y := 0; y < 8; y++ {
		row := pix[y*stride : y*stride+8]
		for _, p := range row {
			sum += int32(p)
		}
	}
	return sum
}

// LowresFrameACEnergy returns the frame's psy AC energy: the sum over
// interior macroblocks (same crop as the frame cost aggregation) of
// SATD8x8(block, zero) − SAD(block, zero)>>1. The raw interior-MB sum is
// returned (same convention as the frame intra cost): normalise by MB count
// before comparing across resolutions. int64 because a large lowres plane's
// sum exceeds int32.
func LowresFrameACEnergy(f *LowresFrame) int64 {
	xLo, xHi := 0, f.MBW
	yLo, yHi := 0, f.MBH
	if f.MBW > 2 && f.MBH > 2 {
		xLo, xHi = 1, f.MBW-1
		yLo, yHi = 1, f.MBH-1
	}
	var sum int64
	for mby := yLo; mby < yHi; mby++ {
		for mbx := xLo; mbx < xHi; mbx++ {
			off := f.planeOffset(mbx*MBSize, mby*MBSize)
			blk := f.planeBuf[0][off:]
			satd := SATD8x8(blk, f.Stride, zeroBlock[:], 0)
			dc := sadVsZero8x8(blk, f.Stride) >> 1
			sum += int64(satd - dc)
		}
	}
	return sum
}
