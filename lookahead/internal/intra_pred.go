// Copyright (C) 2003-2025 x264 project
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from x264/common/predict.c (predict_8x8c_*_c) and
// x264/common/pixel.c (intra_satd_x3_8x8c).
//
// The lookahead calls h->pixf.intra_mbcmp_x3_8x8c which evaluates the same
// three intra modes (DC, H, V) as intra_satd_x3_8x8c_c and returns the SATD
// against the source block for each mode.

package imgmath

// IntraSATD3_8x8c evaluates three 8×8 chroma-block intra-prediction modes
// (DC, H, V) and returns their SATD costs against the source block.
//
//   - src[i*srcStride : i*srcStride+8] is row i of the source 8×8 block.
//   - topRow[0..7]  are the 8 pixels immediately above the block (y−1 row).
//   - leftCol[0..7] are the 8 pixels immediately left of the block (x−1 col),
//     where leftCol[y] is the pixel at the same height as src row y.
//
// When a neighbour is unavailable (border macroblock), the caller should pass
// slices filled with 128, matching x264's predict_8x8c_dc_128_c fallback.
//
// Returns (dcSATD, hSATD, vSATD). The minimum of the three is used as the
// per-MB intra cost in the lookahead (slicetype_mb_cost, slicetype.c).
//
// Mirrors intra_satd_x3_8x8c_c defined by INTRA_MBCMP in pixel.c, using
// predict_8x8c_dc_c, predict_8x8c_h_c, and predict_8x8c_v_c from predict.c.
func IntraSATD3_8x8c(
	src []byte, srcStride int,
	topRow [8]byte,
	leftCol [8]byte,
) (dcSATD, hSATD, vSATD int32) {
	var pred [64]byte

	predict8x8cDC(topRow, leftCol, &pred)
	dcSATD = SATD8x8(src, srcStride, pred[:], 8)

	predict8x8cH(leftCol, &pred)
	hSATD = SATD8x8(src, srcStride, pred[:], 8)

	predict8x8cV(topRow, &pred)
	vSATD = SATD8x8(src, srcStride, pred[:], 8)

	return dcSATD, hSATD, vSATD
}

// predict8x8cDC fills pred with the 8×8 chroma DC intra prediction.
//
// The 8×8 block is divided into four 4×4 quadrants with independent DC values:
//
//	┌────────┬────────┐
//	│  dc0   │  dc1   │  dc0 = (sum(top[0..3]) + sum(left[0..3]) + 4) >> 3
//	│ rows   │ rows   │  dc1 = (sum(top[4..7])                   + 2) >> 2
//	│ 0−3    │ 0−3    │  dc2 = (sum(left[4..7])                  + 2) >> 2
//	├────────┼────────┤  dc3 = (sum(top[4..7]) + sum(left[4..7]) + 4) >> 3
//	│  dc2   │  dc3   │
//	│ rows   │ rows   │
//	│ 4−7    │ 4−7    │
//	└────────┴────────┘
//
// Mirrors x264_predict_8x8c_dc_c in x264/common/predict.c.
func predict8x8cDC(top [8]byte, left [8]byte, pred *[64]byte) {
	var s0, s1, s2, s3 uint32
	for i := 0; i < 4; i++ {
		s0 += uint32(top[i])
		s1 += uint32(top[i+4])
		s2 += uint32(left[i])
		s3 += uint32(left[i+4])
	}
	dc0 := byte((s0 + s2 + 4) >> 3)
	dc1 := byte((s1 + 2) >> 2)
	dc2 := byte((s3 + 2) >> 2)
	dc3 := byte((s1 + s3 + 4) >> 3)

	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			pred[y*8+x] = dc0
			pred[y*8+x+4] = dc1
		}
	}
	for y := 4; y < 8; y++ {
		for x := 0; x < 4; x++ {
			pred[y*8+x] = dc2
			pred[y*8+x+4] = dc3
		}
	}
}

// predict8x8cH fills each row of pred with the corresponding left-column
// neighbour pixel value (horizontal intra prediction).
//
// Mirrors x264_predict_8x8c_h_c in x264/common/predict.c.
func predict8x8cH(left [8]byte, pred *[64]byte) {
	for y := 0; y < 8; y++ {
		v := left[y]
		for x := 0; x < 8; x++ {
			pred[y*8+x] = v
		}
	}
}

// predict8x8cV fills every row of pred by copying the top-row neighbours.
//
// Mirrors x264_predict_8x8c_v_c in x264/common/predict.c.
func predict8x8cV(top [8]byte, pred *[64]byte) {
	for y := 0; y < 8; y++ {
		copy(pred[y*8:y*8+8], top[:])
	}
}

// imin3 returns the minimum of three int32 values.
func imin3(a, b, c int32) int32 {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func clip8(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

func f1(a, b byte) byte    { return (a + b + 1) >> 1 }
func f2(a, b, c byte) byte { return (a + 2*b + c + 2) >> 2 }

// predict8x8cP mirrors x264_predict_8x8c_p_c (planar for 8x8c).
func predict8x8cP(top [8]byte, left [8]byte, tl byte, pred []byte) {
	var H, V int
	for i := 0; i < 4; i++ {
		t1 := int(top[4+i])
		t2idx := 2 - i
		var t2 int
		if t2idx < 0 {
			t2 = int(tl)
		} else {
			t2 = int(top[t2idx])
		}
		H += (i + 1) * (t1 - t2)
		l1 := int(left[4+i])
		l2idx := 2 - i
		var l2 int
		if l2idx < 0 {
			l2 = int(tl)
		} else {
			l2 = int(left[l2idx])
		}
		V += (i + 1) * (l1 - l2)
	}
	a := 16 * (int(left[7]) + int(top[7]))
	b := (17*H + 16) >> 5
	c := (17*V + 16) >> 5
	i00 := a - 3*b - 3*c + 16
	for y := 0; y < 8; y++ {
		pix := i00
		for x := 0; x < 8; x++ {
			pred[y*8+x] = clip8(pix >> 5)
			pix += b
		}
		i00 += c
	}
}

// predict8x8Filter mirrors predict_8x8_filter_c for ALL_NEIGHBORS.
func predict8x8Filter(top [16]byte, left [8]byte, lt byte, hasTR bool, edge *[36]byte) {
	// LEFT part
	edge[15] = (top[0] + 2*lt + left[0] + 2) >> 2
	edge[14] = (lt + 2*left[0] + left[1] + 2) >> 2
	edge[13] = (left[0] + 2*left[1] + left[2] + 2) >> 2
	edge[12] = (left[1] + 2*left[2] + left[3] + 2) >> 2
	edge[11] = (left[2] + 2*left[3] + left[4] + 2) >> 2
	edge[10] = (left[3] + 2*left[4] + left[5] + 2) >> 2
	edge[9] = (left[4] + 2*left[5] + left[6] + 2) >> 2
	edge[7] = (left[6] + 3*left[7] + 2) >> 2
	edge[6] = edge[7]
	// TOP part
	edge[16] = (lt + 2*top[0] + top[1] + 2) >> 2
	edge[17] = (top[0] + 2*top[1] + top[2] + 2) >> 2
	edge[18] = (top[1] + 2*top[2] + top[3] + 2) >> 2
	edge[19] = (top[2] + 2*top[3] + top[4] + 2) >> 2
	edge[20] = (top[3] + 2*top[4] + top[5] + 2) >> 2
	edge[21] = (top[4] + 2*top[5] + top[6] + 2) >> 2
	edge[22] = (top[5] + 2*top[6] + top[7] + 2) >> 2
	t7 := top[7]
	t8 := t7
	if hasTR {
		t8 = top[8]
	}
	edge[23] = (top[6] + 2*t7 + t8 + 2) >> 2
	if hasTR {
		edge[24] = (top[7] + 2*top[8] + top[9] + 2) >> 2
		edge[25] = (top[8] + 2*top[9] + top[10] + 2) >> 2
		edge[26] = (top[9] + 2*top[10] + top[11] + 2) >> 2
		edge[27] = (top[10] + 2*top[11] + top[12] + 2) >> 2
		edge[28] = (top[11] + 2*top[12] + top[13] + 2) >> 2
		edge[29] = (top[12] + 2*top[13] + top[14] + 2) >> 2
		edge[30] = (top[13] + 2*top[14] + top[15] + 2) >> 2
		edge[31] = (top[14] + 3*top[15] + 2) >> 2
		edge[32] = edge[31]
	} else {
		for k := 24; k <= 32; k++ {
			edge[k] = t7
		}
	}
}

// full8x8IntraSATD returns min SATD over DC/H/V + P + DDL/DDR/VR/HD/VL/HU
// (matches x264 lookahead intra when subpel_refine > 1, which is always).
func full8x8IntraSATD(f *LowresFrame, mbx, mby int, src []byte, stride int, baseTop [8]byte, baseLeft [8]byte) int32 {
	dc, hh, vv := IntraSATD3_8x8c(src, stride, baseTop, baseLeft)
	minS := imin3(dc, hh, vv)
	// P
	tl := byte(128)
	if mby > 0 && mbx > 0 {
		tl = f.planeBuf[0][f.planeOffset(mbx*MBSize-1, mby*MBSize-1)]
	}
	var p [64]byte
	predict8x8cP(baseTop, baseLeft, tl, p[:])
	if ps := SATD8x8(src, stride, p[:], 8); ps < minS {
		minS = ps
	}
	// 8x8 angled: build topExt[16]
	var topExt [16]byte
	copy(topExt[:8], baseTop[:])
	hasTR := false
	if mby > 0 && mbx+1 < f.MBW {
		trOff := f.planeOffset((mbx+1)*MBSize, mby*MBSize-1)
		copy(topExt[8:], f.planeBuf[0][trOff:trOff+8])
		hasTR = true
	} else if mby > 0 {
		for k := 8; k < 16; k++ {
			topExt[k] = baseTop[7]
		}
	}
	var edge [36]byte
	predict8x8Filter(topExt, baseLeft, tl, hasTR, &edge)
	for _, pr := range []func(*[36]byte, []byte){
		predict8x8DDL, predict8x8DDR, predict8x8VR, predict8x8HD, predict8x8VL, predict8x8HU,
	} {
		var pd [64]byte
		pr(&edge, pd[:])
		if s := SATD8x8(src, stride, pd[:], 8); s < minS {
			minS = s
		}
	}
	return minS
}

func predict8x8DDL(edge *[36]byte, pred []byte) {
	var t [16]byte
	for i := 0; i < 16; i++ {
		t[i] = edge[16+i]
	}
	pred[0] = f2(t[0], t[1], t[2])
	pred[1] = f2(t[1], t[2], t[3])
	pred[8] = pred[1]
	pred[2] = f2(t[2], t[3], t[4])
	pred[9] = pred[2]
	pred[16] = pred[9]
	pred[3] = f2(t[3], t[4], t[5])
	pred[10] = pred[3]
	pred[17] = pred[10]
	pred[24] = pred[17]
	pred[4] = f2(t[4], t[5], t[6])
	pred[11] = pred[4]
	pred[18] = pred[11]
	pred[25] = pred[18]
	pred[32] = pred[25]
	pred[5] = f2(t[5], t[6], t[7])
	pred[12] = pred[5]
	pred[19] = pred[12]
	pred[26] = pred[19]
	pred[33] = pred[26]
	pred[40] = pred[33]
	pred[6] = f2(t[6], t[7], t[8])
	pred[13] = pred[6]
	pred[20] = pred[13]
	pred[27] = pred[20]
	pred[34] = pred[27]
	pred[41] = pred[34]
	pred[48] = pred[41]
	pred[7] = f2(t[7], t[8], t[9])
	pred[14] = pred[7]
	pred[21] = pred[14]
	pred[28] = pred[21]
	pred[35] = pred[28]
	pred[42] = pred[35]
	pred[49] = pred[42]
	pred[56] = pred[49]
	pred[15] = f2(t[8], t[9], t[10])
	pred[22] = pred[15]
	pred[29] = pred[22]
	pred[36] = pred[29]
	pred[43] = pred[36]
	pred[50] = pred[43]
	pred[57] = pred[50]
	pred[23] = f2(t[9], t[10], t[11])
	pred[30] = pred[23]
	pred[37] = pred[30]
	pred[44] = pred[37]
	pred[51] = pred[44]
	pred[58] = pred[51]
	pred[31] = f2(t[10], t[11], t[12])
	pred[38] = pred[31]
	pred[45] = pred[38]
	pred[52] = pred[45]
	pred[59] = pred[52]
	pred[39] = f2(t[11], t[12], t[13])
	pred[46] = pred[39]
	pred[53] = pred[46]
	pred[60] = pred[53]
	pred[47] = f2(t[12], t[13], t[14])
	pred[54] = pred[47]
	pred[61] = pred[54]
	pred[55] = f2(t[13], t[14], t[15])
	pred[62] = pred[55]
	pred[63] = f2(t[14], t[15], t[15])
}

func predict8x8DDR(edge *[36]byte, pred []byte) {
	var t [8]byte
	for i := 0; i < 8; i++ {
		t[i] = edge[16+i]
	}
	var l [8]byte
	for i := 0; i < 8; i++ {
		l[i] = edge[14-i]
	}
	lt := edge[15]
	pred[7*8+0] = f2(l[7], l[6], l[5])
	pred[6*8+0] = f2(l[6], l[5], l[4])
	pred[7*8+1] = pred[6*8+0]
	pred[5*8+0] = f2(l[5], l[4], l[3])
	pred[6*8+1] = pred[5*8+0]
	pred[7*8+2] = pred[6*8+1]
	pred[4*8+0] = f2(l[4], l[3], l[2])
	pred[5*8+1] = pred[4*8+0]
	pred[6*8+2] = pred[5*8+1]
	pred[7*8+3] = pred[6*8+2]
	pred[3*8+0] = f2(l[3], l[2], l[1])
	pred[4*8+1] = pred[3*8+0]
	pred[5*8+2] = pred[4*8+1]
	pred[6*8+3] = pred[5*8+2]
	pred[7*8+4] = pred[6*8+3]
	pred[2*8+0] = f2(l[2], l[1], l[0])
	pred[3*8+1] = pred[2*8+0]
	pred[4*8+2] = pred[3*8+1]
	pred[5*8+3] = pred[4*8+2]
	pred[6*8+4] = pred[5*8+3]
	pred[7*8+5] = pred[6*8+4]
	pred[1*8+0] = f2(l[1], l[0], lt)
	pred[2*8+1] = pred[1*8+0]
	pred[3*8+2] = pred[2*8+1]
	pred[4*8+3] = pred[3*8+2]
	pred[5*8+4] = pred[4*8+3]
	pred[6*8+5] = pred[5*8+4]
	pred[7*8+6] = pred[6*8+5]
	pred[0*8+0] = f2(l[0], lt, t[0])
	pred[1*8+1] = pred[0*8+0]
	pred[2*8+2] = pred[1*8+1]
	pred[3*8+3] = pred[2*8+2]
	pred[4*8+4] = pred[3*8+3]
	pred[5*8+5] = pred[4*8+4]
	pred[6*8+6] = pred[5*8+5]
	pred[7*8+7] = pred[6*8+6]
	pred[1*8+0] = f2(lt, t[0], t[1])
	pred[2*8+1] = pred[1*8+0]
	pred[3*8+2] = pred[2*8+1]
	pred[4*8+3] = pred[3*8+2]
	pred[5*8+4] = pred[4*8+3]
	pred[6*8+5] = pred[5*8+4]
	pred[7*8+6] = pred[6*8+5]
	pred[2*8+0] = f2(t[0], t[1], t[2])
	pred[3*8+1] = pred[2*8+0]
	pred[4*8+2] = pred[3*8+1]
	pred[5*8+3] = pred[4*8+2]
	pred[6*8+4] = pred[5*8+3]
	pred[7*8+5] = pred[6*8+4]
	pred[3*8+0] = f2(t[1], t[2], t[3])
	pred[4*8+1] = pred[3*8+0]
	pred[5*8+2] = pred[4*8+1]
	pred[6*8+3] = pred[5*8+2]
	pred[7*8+4] = pred[6*8+3]
	pred[4*8+0] = f2(t[2], t[3], t[4])
	pred[5*8+1] = pred[4*8+0]
	pred[6*8+2] = pred[5*8+1]
	pred[7*8+3] = pred[6*8+2]
	pred[5*8+0] = f2(t[3], t[4], t[5])
	pred[6*8+1] = pred[5*8+0]
	pred[7*8+2] = pred[6*8+1]
	pred[6*8+0] = f2(t[4], t[5], t[6])
	pred[7*8+1] = pred[6*8+0]
	pred[7*8+0] = f2(t[5], t[6], t[7])
}

func predict8x8VR(edge *[36]byte, pred []byte) {
	var t [8]byte
	for i := 0; i < 8; i++ {
		t[i] = edge[16+i]
	}
	var l [8]byte
	for i := 0; i < 8; i++ {
		l[i] = edge[14-i]
	}
	lt := edge[15]
	pred[6*8+0] = f2(l[5], l[4], l[3])
	pred[7*8+0] = f2(l[6], l[5], l[4])
	pred[4*8+0] = f2(l[3], l[2], l[1])
	pred[6*8+1] = pred[4*8+0]
	pred[5*8+0] = f2(l[4], l[3], l[2])
	pred[7*8+1] = pred[5*8+0]
	pred[2*8+0] = f2(l[1], l[0], lt)
	pred[4*8+1] = pred[2*8+0]
	pred[6*8+2] = pred[4*8+1]
	pred[3*8+0] = f2(l[2], l[1], l[0])
	pred[5*8+1] = pred[3*8+0]
	pred[7*8+2] = pred[5*8+1]
	pred[1*8+0] = f2(l[0], lt, t[0])
	pred[3*8+1] = pred[1*8+0]
	pred[5*8+2] = pred[3*8+1]
	pred[7*8+3] = pred[5*8+2]
	pred[0*8+0] = f1(lt, t[0])
	pred[2*8+1] = pred[0*8+0]
	pred[4*8+2] = pred[2*8+1]
	pred[6*8+3] = pred[4*8+2]
	pred[1*8+1] = f2(lt, t[0], t[1])
	pred[3*8+2] = pred[1*8+1]
	pred[5*8+3] = pred[3*8+2]
	pred[7*8+4] = pred[5*8+3]
	pred[0*8+1] = f1(t[0], t[1])
	pred[2*8+2] = pred[0*8+1]
	pred[4*8+3] = pred[2*8+2]
	pred[6*8+4] = pred[4*8+3]
	pred[1*8+2] = f2(t[0], t[1], t[2])
	pred[3*8+3] = pred[1*8+2]
	pred[5*8+4] = pred[3*8+3]
	pred[7*8+5] = pred[5*8+4]
	pred[0*8+2] = f1(t[1], t[2])
	pred[2*8+3] = pred[0*8+2]
	pred[4*8+4] = pred[2*8+3]
	pred[6*8+5] = pred[4*8+4]
	pred[1*8+3] = f2(t[1], t[2], t[3])
	pred[3*8+4] = pred[1*8+3]
	pred[5*8+5] = pred[3*8+4]
	pred[7*8+6] = pred[5*8+5]
	pred[0*8+3] = f1(t[2], t[3])
	pred[2*8+4] = pred[0*8+3]
	pred[4*8+5] = pred[2*8+4]
	pred[6*8+6] = pred[4*8+5]
	pred[1*8+4] = f2(t[2], t[3], t[4])
	pred[3*8+5] = pred[1*8+4]
	pred[5*8+6] = pred[3*8+5]
	pred[7*8+7] = pred[5*8+6]
	pred[0*8+4] = f1(t[3], t[4])
	pred[2*8+5] = pred[0*8+4]
	pred[4*8+6] = pred[2*8+5]
	pred[6*8+7] = pred[4*8+6]
	pred[1*8+5] = f2(t[3], t[4], t[5])
	pred[3*8+6] = pred[1*8+5]
	pred[5*8+7] = pred[3*8+6]
	pred[0*8+5] = f1(t[4], t[5])
	pred[2*8+6] = pred[0*8+5]
	pred[4*8+7] = pred[2*8+6]
	pred[1*8+6] = f2(t[4], t[5], t[6])
	pred[3*8+7] = pred[1*8+6]
	pred[0*8+6] = f1(t[5], t[6])
	pred[2*8+7] = pred[0*8+6]
	pred[1*8+7] = f2(t[5], t[6], t[7])
	pred[0*8+7] = f1(t[6], t[7])
}

func predict8x8HD(edge *[36]byte, pred []byte) {
	var t [8]byte
	for i := 0; i < 8; i++ {
		t[i] = edge[16+i]
	}
	var l [8]byte
	for i := 0; i < 8; i++ {
		l[i] = edge[14-i]
	}
	lt := edge[15]
	pred[7*8+0] = f1(l[6], l[7])
	pred[7*8+1] = f2(l[5], l[6], l[7])
	pred[6*8+0] = f1(l[5], l[6])
	pred[6*8+1] = f2(l[4], l[5], l[6])
	pred[7*8+2] = pred[6*8+1]
	pred[5*8+0] = f1(l[4], l[5])
	pred[6*8+2] = pred[5*8+0]
	pred[5*8+1] = f2(l[3], l[4], l[5])
	pred[6*8+3] = pred[5*8+1]
	pred[7*8+4] = pred[6*8+3]
	pred[4*8+0] = f1(l[3], l[4])
	pred[5*8+2] = pred[4*8+0]
	pred[6*8+4] = pred[5*8+2]
	pred[4*8+1] = f2(l[2], l[3], l[4])
	pred[5*8+3] = pred[4*8+1]
	pred[6*8+5] = pred[5*8+3]
	pred[7*8+6] = pred[6*8+5]
	pred[3*8+0] = f1(l[2], l[3])
	pred[4*8+2] = pred[3*8+0]
	pred[5*8+4] = pred[4*8+2]
	pred[6*8+6] = pred[5*8+4]
	pred[3*8+1] = f2(l[1], l[2], l[3])
	pred[4*8+3] = pred[3*8+1]
	pred[5*8+5] = pred[4*8+3]
	pred[6*8+7] = pred[5*8+5]
	pred[7*8+5] = pred[6*8+7]
	pred[2*8+0] = f1(l[1], l[2])
	pred[3*8+2] = pred[2*8+0]
	pred[4*8+4] = pred[3*8+2]
	pred[5*8+6] = pred[4*8+4]
	pred[2*8+1] = f2(l[0], l[1], l[2])
	pred[3*8+3] = pred[2*8+1]
	pred[4*8+5] = pred[3*8+3]
	pred[5*8+7] = pred[4*8+5]
	pred[6*8+5] = pred[5*8+7]
	pred[1*8+0] = f1(l[0], l[1])
	pred[2*8+2] = pred[1*8+0]
	pred[3*8+4] = pred[2*8+2]
	pred[4*8+6] = pred[3*8+4]
	pred[1*8+1] = f2(lt, l[0], l[1])
	pred[2*8+3] = pred[1*8+1]
	pred[3*8+5] = pred[2*8+3]
	pred[4*8+7] = pred[3*8+5]
	pred[5*8+5] = pred[4*8+7]
	pred[0*8+0] = f1(lt, l[0])
	pred[1*8+2] = pred[0*8+0]
	pred[2*8+4] = pred[1*8+2]
	pred[3*8+6] = pred[2*8+4]
	pred[0*8+1] = f2(l[0], lt, t[0])
	pred[1*8+3] = pred[0*8+1]
	pred[2*8+5] = pred[1*8+3]
	pred[3*8+7] = pred[2*8+5]
	pred[4*8+5] = pred[3*8+7]
	pred[0*8+2] = f2(t[1], t[0], lt)
	pred[1*8+4] = pred[0*8+2]
	pred[2*8+6] = pred[1*8+4]
	pred[3*8+5] = pred[2*8+6]
	pred[0*8+3] = f2(t[2], t[1], t[0])
	pred[1*8+5] = pred[0*8+3]
	pred[2*8+7] = pred[1*8+5]
	pred[3*8+4] = pred[2*8+7]
	pred[0*8+4] = f2(t[3], t[2], t[1])
	pred[1*8+6] = pred[0*8+4]
	pred[2*8+5] = pred[1*8+6]
	pred[0*8+5] = f2(t[4], t[3], t[2])
	pred[1*8+7] = pred[0*8+5]
	pred[2*8+4] = pred[1*8+7]
	pred[0*8+6] = f2(t[5], t[4], t[3])
	pred[1*8+5] = pred[0*8+6]
	pred[0*8+7] = f2(t[6], t[5], t[4])
	pred[1*8+4] = pred[0*8+7]
}

func predict8x8VL(edge *[36]byte, pred []byte) {
	var t [16]byte
	for i := 0; i < 16; i++ {
		t[i] = edge[16+i]
	}
	pred[0] = f1(t[0], t[1])
	pred[1] = f2(t[0], t[1], t[2])
	pred[2] = f1(t[1], t[2])
	pred[3] = f2(t[1], t[2], t[3])
	pred[4] = f1(t[2], t[3])
	pred[5] = f2(t[2], t[3], t[4])
	pred[6] = f1(t[3], t[4])
	pred[7] = f2(t[3], t[4], t[5])
	pred[8] = f1(t[1], t[2])
	pred[0+1*8] = pred[8]
	pred[9] = f2(t[1], t[2], t[3])
	pred[1+1*8] = pred[9]
	pred[10] = f1(t[2], t[3])
	pred[2+1*8] = pred[10]
	pred[11] = f2(t[2], t[3], t[4])
	pred[3+1*8] = pred[11]
	pred[12] = f1(t[3], t[4])
	pred[4+1*8] = pred[12]
	pred[13] = f2(t[3], t[4], t[5])
	pred[5+1*8] = pred[13]
	pred[14] = f1(t[4], t[5])
	pred[6+1*8] = pred[14]
	pred[15] = f2(t[4], t[5], t[6])
	pred[7+1*8] = pred[15]
	pred[0+2*8] = f1(t[2], t[3])
	pred[1+2*8] = f2(t[2], t[3], t[4])
	pred[2+2*8] = f1(t[3], t[4])
	pred[3+2*8] = f2(t[3], t[4], t[5])
	pred[4+2*8] = f1(t[4], t[5])
	pred[5+2*8] = f2(t[4], t[5], t[6])
	pred[6+2*8] = f1(t[5], t[6])
	pred[7+2*8] = f2(t[5], t[6], t[7])
	pred[0+3*8] = f1(t[3], t[4])
	pred[1+3*8] = f2(t[3], t[4], t[5])
	pred[2+3*8] = f1(t[4], t[5])
	pred[3+3*8] = f2(t[4], t[5], t[6])
	pred[4+3*8] = f1(t[5], t[6])
	pred[5+3*8] = f2(t[5], t[6], t[7])
	pred[6+3*8] = f1(t[6], t[7])
	pred[7+3*8] = f2(t[6], t[7], t[8])
	pred[0+4*8] = f1(t[4], t[5])
	pred[1+4*8] = f2(t[4], t[5], t[6])
	pred[2+4*8] = f1(t[5], t[6])
	pred[3+4*8] = f2(t[5], t[6], t[7])
	pred[4+4*8] = f1(t[6], t[7])
	pred[5+4*8] = f2(t[6], t[7], t[8])
	pred[6+4*8] = f1(t[7], t[8])
	pred[7+4*8] = f2(t[7], t[8], t[9])
	pred[0+5*8] = f1(t[5], t[6])
	pred[1+5*8] = f2(t[5], t[6], t[7])
	pred[2+5*8] = f1(t[6], t[7])
	pred[3+5*8] = f2(t[6], t[7], t[8])
	pred[4+5*8] = f1(t[7], t[8])
	pred[5+5*8] = f2(t[7], t[8], t[9])
	pred[6+5*8] = f1(t[8], t[9])
	pred[7+5*8] = f2(t[8], t[9], t[10])
	pred[0+6*8] = f1(t[6], t[7])
	pred[1+6*8] = f2(t[6], t[7], t[8])
	pred[2+6*8] = f1(t[7], t[8])
	pred[3+6*8] = f2(t[7], t[8], t[9])
	pred[4+6*8] = f1(t[8], t[9])
	pred[5+6*8] = f2(t[8], t[9], t[10])
	pred[6+6*8] = f1(t[9], t[10])
	pred[7+6*8] = f2(t[9], t[10], t[11])
	pred[0+7*8] = f1(t[7], t[8])
	pred[1+7*8] = f2(t[7], t[8], t[9])
	pred[2+7*8] = f1(t[8], t[9])
	pred[3+7*8] = f2(t[8], t[9], t[10])
	pred[4+7*8] = f1(t[9], t[10])
	pred[5+7*8] = f2(t[9], t[10], t[11])
	pred[6+7*8] = f1(t[10], t[11])
	pred[7+7*8] = f2(t[10], t[11], t[12])
}

func predict8x8HU(edge *[36]byte, pred []byte) {
	var l [8]byte
	for i := 0; i < 8; i++ {
		l[i] = edge[14-i]
	}
	pred[0] = f1(l[0], l[1])
	pred[1] = f2(l[0], l[1], l[2])
	pred[2] = f1(l[1], l[2])
	pred[8] = pred[2]
	pred[3] = f2(l[1], l[2], l[3])
	pred[9] = pred[3]
	pred[4] = f1(l[2], l[3])
	pred[10] = pred[4]
	pred[16] = pred[10]
	pred[5] = f2(l[2], l[3], l[4])
	pred[11] = pred[5]
	pred[17] = pred[11]
	pred[6] = f1(l[3], l[4])
	pred[12] = pred[6]
	pred[18] = pred[12]
	pred[24] = pred[18]
	pred[7] = f2(l[3], l[4], l[5])
	pred[13] = pred[7]
	pred[19] = pred[13]
	pred[25] = pred[19]
	pred[14] = f1(l[4], l[5])
	pred[20] = pred[14]
	pred[26] = pred[20]
	pred[32] = pred[26]
	pred[15] = f2(l[4], l[5], l[6])
	pred[21] = pred[15]
	pred[27] = pred[21]
	pred[33] = pred[27]
	pred[22] = f1(l[5], l[6])
	pred[28] = pred[22]
	pred[34] = pred[28]
	pred[40] = pred[34]
	pred[23] = f2(l[5], l[6], l[7])
	pred[29] = pred[23]
	pred[35] = pred[29]
	pred[41] = pred[35]
	pred[30] = f1(l[6], l[7])
	pred[36] = pred[30]
	pred[42] = pred[36]
	pred[48] = pred[42]
	pred[31] = f2(l[6], l[7], l[7])
	pred[37] = pred[31]
	pred[43] = pred[37]
	pred[49] = pred[43]
	pred[38] = f1(l[7], l[7])
	pred[44] = pred[38]
	pred[50] = pred[44]
	pred[56] = pred[50]
	pred[39] = f1(l[7], l[7])
	pred[45] = pred[39]
	pred[51] = pred[45]
	pred[57] = pred[51]
	pred[46] = f1(l[7], l[7])
	pred[52] = pred[46]
	pred[58] = pred[52]
	pred[47] = f1(l[7], l[7])
	pred[53] = pred[47]
	pred[59] = pred[53]
	pred[54] = f1(l[7], l[7])
	pred[60] = pred[54]
	pred[55] = f1(l[7], l[7])
	pred[61] = pred[55]
	pred[62] = f1(l[7], l[7])
	pred[63] = f1(l[7], l[7])
}
