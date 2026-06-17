// Copyright (C) 2003-2025 x264 project
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from x264/encoder/me.c (x264_me_search_ref, DIA case) and
// x264/encoder/slicetype.c (slicetype_mb_cost, slicetype_frame_cost).

package imgmath

import "math"

// mvCostTab[d] = round( lambda * logs[d] ) with lambda=1 (LookaheadQP=12),
// logs[0]=0.718, logs[i]=2*log2(i+1)+1.718 for i>0. Matches x264 cost_mv
// table for the lookahead QP (see analyse.c:186 and tables.c:97).
var mvCostTab [65]int32

func init() {
	mvCostTab[0] = 1 // round(0.718)
	for i := 1; i < len(mvCostTab); i++ {
		logs := 2*math.Log2(float64(i+1)) + 1.718
		mvCostTab[i] = int32(logs + 0.5)
	}
}

func mvCostOne(d int) int32 {
	if d < 0 {
		d = -d
	}
	if d >= len(mvCostTab) {
		d = len(mvCostTab) - 1
	}
	return mvCostTab[d]
}

// mvCostLambda returns the MV cost for delta (dx,dy) from MVP, using the
// x264 logs table (lam=1). Mirrors p_cost_mv[dx-pmx] + p_cost_mv[dy-pmy].
func mvCostLambda(dx, dy int) int32 {
	return mvCostOne(dx) + mvCostOne(dy)
}

// medianInt returns the median of three integers.
func medianInt(a, b, c int) int {
	if a > b {
		a, b = b, a
	}
	if b > c {
		b = c
	}
	if a > b {
		_, b = b, a
	}
	return b
}

// computeSpatialMVP returns the spatial MV predictor for the macroblock at
// (mbx, mby) when scanning in reverse order (right-to-left, bottom-to-top).
//
// Candidate neighbours (already computed in reverse scan order):
//   - right:       (mbx+1, mby)
//   - below:       (mbx, mby+1)
//   - below-left:  (mbx-1, mby+1)
//   - below-right: (mbx+1, mby+1)
//
// If 0 candidates are available: MVP = (0,0).
// If 1 candidate: MVP = that candidate.
// If ≥ 2: component-wise median of the first three (uninit candidates
// contribute (0,0), matching x264's mvc array initialisation).
//
// Mirrors the MVC / x264_median_mv logic in slicetype_mb_cost (slicetype.c).
func computeSpatialMVP(f *LowresFrame, mvCache [][2]int16, mbx, mby int) (pmvx, pmvy int) {
	if mvCache == nil {
		return 0, 0
	}
	var mvc [3][2]int16 // up to 3 candidates; extras stay (0,0)
	n := 0
	mbw := f.MBW

	add := func(nbx, nby int) {
		if nbx < 0 || nbx >= mbw || nby < 0 || nby >= f.MBH {
			return
		}
		nb := nby*mbw + nbx
		if mvCache[nb][0] == uninitMVX {
			return
		}
		if n < 3 {
			mvc[n] = mvCache[nb]
			n++
		}
	}

	add(mbx+1, mby)   // right
	add(mbx, mby+1)   // below
	add(mbx-1, mby+1) // below-left
	add(mbx+1, mby+1) // below-right (4th; used if n<3 after first 3)

	switch n {
	case 0:
		return 0, 0
	case 1:
		return int(mvc[0][0]), int(mvc[0][1])
	default:
		// Component-wise median; mvc[2] is (0,0) when n == 2.
		return medianInt(int(mvc[0][0]), int(mvc[1][0]), int(mvc[2][0])),
			medianInt(int(mvc[0][1]), int(mvc[1][1]), int(mvc[2][1]))
	}
}

// DiamondME performs an integer-pixel diamond motion search for the 8×8
// macroblock at (mbx, mby) in fenc, using ref as the reference frame.
//
// (pmvx, pmvy) is the motion-vector predictor (MVP). The search starts from
// the MVP and all MV-bit costs are measured relative to it, matching x264's
// COST_MV macro which uses `p_cost_mv[(mx)-pmx] + p_cost_mv[(my)-pmy]`.
// Call with pmvx=pmvy=0 when no spatial predictor is available.
//
// Fast-skip optimisation: when the MVP is (0,0) and SATD@(0,0) < 64, ME is
// skipped and pure SATD is returned (mirrors the fast-skip in
// slicetype_mb_cost, slicetype.c).
//
// Post-search adjustments (mirrors slicetype_mb_cost after x264_me_search):
//   - Subtract LookaheadLambda (= p_cost_mv[0], the zero-delta cost).
//   - Add SkipBias when the best MV is non-zero.
//
// Returns (mvx, mvy, cost) in integer lowres pixels.
//
// Mirrors x264_me_search_ref, DIA case (me.c).
func DiamondME(fenc, ref *LowresFrame, mbx, mby, pmvx, pmvy int) (mvx, mvy int, cost int32) {
	// MV search bounds: keep the 8×8 reference window within the padded area.
	// Mirrors mv_min_spel / mv_max_spel in slicetype_mb_cost (slicetype.c).
	minX := -mbx*MBSize - LowresPadH
	maxX := ref.Width - (mbx+1)*MBSize + LowresPadH
	minY := -mby*MBSize - LowresPadV
	maxY := ref.Height - (mby+1)*MBSize + LowresPadV
	if minX < -MERange {
		minX = -MERange
	}
	if maxX > MERange {
		maxX = MERange
	}
	if minY < -MERange {
		minY = -MERange
	}
	if maxY > MERange {
		maxY = MERange
	}
	// Clamp MVP to search bounds.
	if pmvx < minX {
		pmvx = minX
	}
	if pmvx > maxX {
		pmvx = maxX
	}
	if pmvy < minY {
		pmvy = minY
	}
	if pmvy > maxY {
		pmvy = maxY
	}

	fOff := fenc.planeOffset(mbx*MBSize, mby*MBSize)
	fencBlock := fenc.planeBuf[0][fOff:]

	satdAt := func(rx, ry int) int32 {
		rOff := ref.planeOffset(mbx*MBSize+rx, mby*MBSize+ry)
		return SATD8x8(fencBlock, fenc.Stride, ref.planeBuf[0][rOff:], ref.Stride)
	}

	// Fast skip: when MVP == (0,0) and SATD@(0,0) < 64, skip ME entirely.
	// Returns pure SATD (no MV-bit adjustments), matching x264's behaviour
	// where `goto skip_motionest` bypasses the p_cost_mv subtraction and
	// SkipBias addition.
	// Mirrors: if( !M32(m[l].mvp) ) { cost = mbcmp; if(cost < 64) goto skip_motionest; }
	if pmvx == 0 && pmvy == 0 {
		zeroSATD := satdAt(0, 0)
		if zeroSATD < 64 {
			return 0, 0, zeroSATD
		}
	}

	// Initial cost at MVP: cost_mv[0]+cost_mv[0] (from x264 p_cost_mv table).
	bmx, bmy := pmvx, pmvy
	bcost := satdAt(bmx, bmy) + mvCostLambda(0, 0)

	// Diamond search: try the 4 neighbours of the current best position.
	// MV-bit costs are relative to the MVP (fixed throughout the search).
	// Mirrors X264_ME_DIA in x264_me_search_ref (encoder/me.c).
	dirs := [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}}
	for iter := 0; iter < MERange; iter++ {
		improved := false
		for _, d := range dirs {
			nx, ny := bmx+d[0], bmy+d[1]
			if nx < minX || nx > maxX || ny < minY || ny > maxY {
				continue
			}
			c := satdAt(nx, ny) + mvCostLambda(nx-pmvx, ny-pmvy)
			if c < bcost {
				bcost = c
				bmx, bmy = nx, ny
				improved = true
			}
		}
		if !improved {
			break
		}
	}

	// hpel refine at final int MV pos using prefiltered planes[0..3] (matches
	// subpel cost evaluation in x264 lowres with subpel_refine=2/4).
	bestSatd := satdAt(bmx, bmy)
	for ph := 1; ph < 4; ph++ {
		rOff := ref.planeOffset(mbx*MBSize+bmx, mby*MBSize+bmy)
		if s := SATD8x8(fencBlock, fenc.Stride, ref.planeBuf[ph][rOff:], ref.Stride); s < bestSatd {
			bestSatd = s
		}
	}
	bcost = bestSatd + mvCostLambda(bmx-pmvx, bmy-pmvy)

	// Post-search adjustments (slicetype_mb_cost, slicetype.c):
	//   1. Subtract p_cost_mv[0] (singular) , mirrors `m[l].cost -= a->p_cost_mv[0]`.
	bcost -= mvCostOne(0)
	//   2. Add SkipBias when the best MV (absolute) is non-zero (mirrors
	//      `if M32(m[l].mv) then m[l].cost += 5 * a->i_lambda`).
	if bmx != 0 || bmy != 0 {
		bcost += SkipBias
	}

	return bmx, bmy, bcost
}

// LowresMBInterCost returns the inter cost for a single 8×8 macroblock,
// caching the result in fenc's mvCache for the given lag.
//
// The spatial MV predictor is derived from already-computed neighbours (the
// scan runs bottom-to-top, right-to-left so right/below neighbours exist).
// Mirrors the inter section of slicetype_mb_cost (slicetype.c), including
// the BIT_DEPTH shift (>>0 for 8-bit) and LowresPenalty addition.
func LowresMBInterCost(fenc, ref *LowresFrame, mbx, mby, lag int) int32 {
	return lowresMBInterCostDir(fenc, ref, mbx, mby, lag, false)
}

// lowresMBInterCostDir is LowresMBInterCost parameterized by prediction
// direction: rev=false uses the forward caches (ref is lag+1 frames before
// cur), rev=true the reverse ones (ref is in cur's future). The two
// directions never share cache entries.
func lowresMBInterCostDir(fenc, ref *LowresFrame, mbx, mby, lag int, rev bool) int32 {
	mvCache, costCache := fenc.mvCacheDir(lag, rev)
	mbxy := mby*fenc.MBW + mbx
	if mvCache[mbxy][0] != uninitMVX {
		return costCache[mbxy]
	}

	pmvx, pmvy := computeSpatialMVP(fenc, mvCache, mbx, mby)
	bmx, bmy, rawCost := DiamondME(fenc, ref, mbx, mby, pmvx, pmvy)
	cost := rawCost + LowresPenalty // BIT_DEPTH=8 → no >>8 shift

	// Store the actual MV so that spatial neighbours can use it as a predictor.
	// The sentinel uninitMVX (0x7FFF) cannot appear here since MVs are clamped
	// to ±MERange=16.
	mvCache[mbxy] = [2]int16{int16(bmx), int16(bmy)}
	costCache[mbxy] = cost
	return cost
}

// LowresMBIntraCost computes (or returns cached) the intra cost for a single
// 8×8 macroblock:
//
//	min(SATD_DC, SATD_H, SATD_V) + IntraPenalty + LowresPenalty
//
// Mirrors the intra section of slicetype_mb_cost (slicetype.c).
func LowresMBIntraCost(f *LowresFrame, mbx, mby int) int32 {
	mbxy := mby*f.MBW + mbx
	if f.IntraCalc {
		return f.IntraCost[mbxy]
	}

	srcOff := f.planeOffset(mbx*MBSize, mby*MBSize)
	src := f.planeBuf[0][srcOff:]

	var top [8]byte
	var left [8]byte
	if mby > 0 {
		topOff := f.planeOffset(mbx*MBSize, mby*MBSize-1)
		copy(top[:], f.planeBuf[0][topOff:topOff+8])
	} else {
		for i := range top {
			top[i] = 128
		}
	}
	if mbx > 0 {
		for y := 0; y < 8; y++ {
			left[y] = f.planeBuf[0][f.planeOffset(mbx*MBSize-1, mby*MBSize+y)]
		}
	} else {
		for i := range left {
			left[i] = 128
		}
	}

	minSATD := full8x8IntraSATD(f, mbx, mby, src, f.Stride, top, left)
	cost := minSATD + IntraPenalty + LowresPenalty
	f.IntraCost[mbxy] = cost
	return cost
}

// LowresFrameCost computes the total P-frame cost and total intra cost for
// fenc relative to ref, summed over interior macroblocks only (matching
// x264's b_frame_score_mb condition in slicetype_mb_cost).
//
//   - pCost: sum over interior MBs of min(inter_cost, intra_cost).
//     Mirrors i_cost_est[lag][0] in slicetype_frame_cost.
//   - iCost: sum over interior MBs of intra_cost.
//     Mirrors i_cost_est[0][0].
//
// On the first call, intra costs for ALL MBs are computed and cached so that
// subsequent calls for different lags reuse the cached values.
//
// If lag < 0, ref is ignored and pCost == iCost (intra-only mode).
//
// Mirrors slicetype_frame_cost + the inner slicetype_mb_cost loop
// (x264/encoder/slicetype.c).
func LowresFrameCost(fenc, ref *LowresFrame, lag int) (pCost, iCost int32) {
	return lowresFrameCostDir(fenc, ref, lag, false)
}

// LowresFrameCostReverse is LowresFrameCost for backward-looking prediction:
// ref is a frame in fenc's FUTURE at distance lag+1. It uses the per-frame
// reverse MV/cost caches, fully separate from the forward ones — the caches
// are keyed by slot only, so sharing entries across directions would silently
// return the other direction's cost wherever the forward distance had already
// been computed for the same frame.
func LowresFrameCostReverse(fenc, ref *LowresFrame, lag int) (pCost, iCost int32) {
	return lowresFrameCostDir(fenc, ref, lag, true)
}

func lowresFrameCostDir(fenc, ref *LowresFrame, lag int, rev bool) (pCost, iCost int32) {
	// Compute and cache intra costs for all MBs on first call.
	if !fenc.IntraCalc {
		for mby := 0; mby < fenc.MBH; mby++ {
			for mbx := 0; mbx < fenc.MBW; mbx++ {
				LowresMBIntraCost(fenc, mbx, mby)
			}
		}
		fenc.IntraCalc = true
	}

	// Determine the interior-MB region. Only interior MBs contribute to the
	// frame score when the grid is large enough. Mirrors b_frame_score_mb.
	xLo, xHi := 0, fenc.MBW
	yLo, yHi := 0, fenc.MBH
	if fenc.MBW > 2 && fenc.MBH > 2 {
		xLo, xHi = 1, fenc.MBW-1
		yLo, yHi = 1, fenc.MBH-1
	}

	// Reverse-order scan (bottom-to-top, right-to-left) to match x264's
	// lookahead direction. For scene detection the scan order does not affect
	// the aggregate cost, but keeping it consistent with x264 is good practice.
	for mby := yHi - 1; mby >= yLo; mby-- {
		for mbx := xHi - 1; mbx >= xLo; mbx-- {
			icost := fenc.IntraCost[mby*fenc.MBW+mbx]
			iCost += icost

			mbCost := icost
			if lag >= 0 {
				interCost := lowresMBInterCostDir(fenc, ref, mbx, mby, lag, rev)
				if interCost < mbCost {
					mbCost = interCost
				}
			}
			pCost += mbCost
		}
	}
	return pCost, iCost
}
