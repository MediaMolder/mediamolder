// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Progressive lag narrowing for dissolve edge precision.
//
// The plateau detector measures a blend with reference distances LONGER than
// the blend (k > D): those saturate cleanly, but their run geometry localizes
// the edges only to within a few frames. This stage then re-measures the two
// edges with progressively SHORTER distances. The edge geometry is exact and
// k-independent:
//
//   - the forward ratio at distance k (predict j from past j−k) first rises at
//     j = S, the first blended frame, for every k — before S both frames are
//     pure old-scene content;
//   - the reverse ratio at distance k (predict j from future j+k) is last
//     elevated at j = E−1 and returns to the new-scene floor at exactly j = E,
//     the first pure new frame, for every k;
//   - the forward run stays elevated until the reference slides out of the
//     blend at j = E+k−1.
//
// What narrowing buys is CONTRAST. At small k the within-scene floors are low
// even on busy content (1–3 frame prediction works through motion), while the
// per-step blend mismatch ~k/D is still large for short blends. At each ladder
// level the local floors and the elevated level are measured from the data and
// the edges are re-located by threshold crossings; the ladder stops when the
// contrast at the next level sinks toward the noise floor (long gradual
// blends), leaving the previous — wider-lag — estimate in place.
package lookahead

import (
	"sort"

	imgmath "github.com/MediaMolder/MediaMolder/lookahead/internal"
)

// edgeLadder is the descending set of narrow prediction distances used to
// progressively localize dissolve edges. It must scale with blend duration:
// a distance k resolves an edge only when the blend fraction it spans (~k/D)
// clears the content-noise floor, so mid-length blends need the mid rungs
// (k ≈ D/3) — measured on real footage, lag 15 localizes a 45-frame blend's
// end to ±1 where lag ≤ 5 has no contrast at all. Each level only runs while
// it is strictly below the current duration estimate; levels without contrast
// are skipped (not fatal — contrast is non-monotonic across scales: high
// floors at large k, vanishing elevation at small k, a sweet spot near D/3).
// Long blends are handled by the direct reverse end measurement instead
// (reverseEndFoot).
var edgeLadder = [...]int{15, 10, 5, 3, 2, 1}

const (
	// edgeMinContrast rejects a ladder level whose floor→elevated contrast is
	// too small to threshold reliably. A linear blend of duration D produces a
	// per-step mismatch of ~k/(D+1) at distance k, so this floor decides the
	// longest blend the lag-1 level can still refine (~1/0.06 ≈ 16 frames on
	// noiseless content; real SATD concavity extends it).
	edgeMinContrast = 0.06
	// edgeThetaFrac places the crossing threshold this fraction of the
	// contrast above the floor — low, so the detected crossing hugs the foot
	// of the rise rather than its midpoint.
	edgeThetaFrac = 0.30
	// edgeStartThetaFrac is the lower crossing fraction used only by the
	// rung START walk. The first blended frame carries ~1/(D+1) of the
	// contrast — for D=4 that is 0.2, lifted by SATD concavity to a measured
	// 27–33 % at the smallest rung across captures, straddling
	// edgeThetaFrac. The pre-blend side is pure old-scene floor, so the
	// start walk can afford the thinner margin; end walks keep the standard
	// fraction (their outside is the busier new scene).
	edgeStartThetaFrac = 0.22
	// rungStartBackBound caps how far before the INCOMING start estimate a
	// rung may move the start. The plateau/leading-edge start errs by ~±2
	// by contract, while the pre-blend floor is not guaranteed flat at every
	// rung: a local floor step (measured: six consecutive pre-blend frames
	// reading at the lowered start threshold on one rung) can masquerade as
	// blend and walk the start many frames early. A violating rung keeps the
	// current start; its end measurement still applies.
	rungStartBackBound = 2
	// edgeBridgeFrac: a sub-threshold frame inside a run is bridged as a dip
	// only when it stays this fraction of the contrast above the floor. A
	// frame AT the floor is the run's end, not a dip — without this gate the
	// run walk strings together floor-level noise on busy content and inflates
	// the run by tens of frames (measured: a 4-frame blend's forward trailing
	// run walked 6 frames into the new scene over three bridged noise dips).
	edgeBridgeFrac = 0.15
	// rungStartMaxD is the largest duration estimate whose START the ladder
	// rungs may move. The first blended frame carries alpha = 1/(D+1) of the
	// contrast, while the crossing threshold sits at edgeStartThetaFrac of
	// it, so frames with alpha below ~0.15 are invisible to every rung and
	// the rung start lands late — where the plateau consensus start (which
	// projects the ramp through the invisible head) is already accurate.
	// Bias is one-sided late, so the earliest rung start wins. 10 rather
	// than a smaller bound because a true 7-frame blend whose plateau end
	// overshot presents as a 10-frame estimate, and its start needs the
	// rungs (measured: the plateau start was 2 early and the k=3 rung saw
	// the true start frame at 64 % of contrast).
	rungStartMaxD = 10
	// footRetractSigma: reverseEndFoot may pull the end EARLIER than the
	// incoming estimate only when its predicted crossing jitter (floor noise
	// over ramp slope, in frames) is at most this. Extension needs no gate:
	// every end estimator's failure mode on noisy floors is early (a noise dip
	// truncates a run or crosses a level first), so a later foot is correcting
	// that bias while an earlier one on a flat noisy ramp is likely chasing
	// noise (measured: on ~100-frame blends the per-frame ramp slope drops
	// below the floor noise and the foot undershot an exact plateau end by 13).
	footRetractSigma = 3.0
)

// computeReverse fills m.RevRatio at reverse distance k for frames [lo, hi]
// (predict j from the FUTURE reference j+k) and returns the column index.
// Reverse measurements use the per-frame reverse MV/cost caches
// (imgmath.LowresFrameCostReverse), fully separate from the forward ones, so
// any reverse distance up to MaxLag is valid regardless of the forward
// schedule.
func computeReverse(m *CostMatrix, lowres []*imgmath.LowresFrame, lo, hi, k int) int {
	col := m.ensureRevCol(k)
	if k < 1 || k > imgmath.MaxLag {
		return col
	}
	nLow := len(lowres)
	for j := max(0, lo); j <= hi && j < m.N; j++ {
		rj := j + k
		if rj >= nLow || lowres[j] == nil || lowres[rj] == nil {
			continue
		}
		in := float64(m.IntraCost[j])
		if in <= 0 {
			continue
		}
		cost, _ := imgmath.LowresFrameCostReverse(lowres[j], lowres[rj], k-1)
		r := float64(cost) / in
		if r > 1 {
			r = 1
		}
		m.RevRatio[j][col] = float32(r)
	}
	return col
}

// reverseEndFoot measures a dissolve's end DIRECTLY using a reverse distance
// slightly wider than the blend — the tool for long blends, where the narrow
// edge-ladder distances have no per-step contrast (~k/D vanishes).
//
// With kRev > D, the reverse ratio (predict j from the future reference
// j+kRev, which is pure new-scene content past the whole blend) saturates on
// [E−kRev, S−1] and then falls across the blend as the current frame's
// old-scene component shrinks, landing on the new-scene floor at exactly
// j = E. This is the mirror of the forward LEADING ramp whose foot makes the
// start measurement accurate: the foot sits directly on the edge, with no −k
// offset to extrapolate through. The foot is recovered with the same
// two-crossing projection rampFeet uses (50%/20% of the ramp height against a
// local post-E baseline), which is exact for linear blends and lands within a
// few frames on concave real-footage ramps.
//
// Returns (foot, sigma, true) on success — sigma is the predicted crossing
// jitter in frames (floor noise over measured ramp slope), the caller's
// confidence gate for accepting a retraction. Returns (eApprox, 0, false)
// when the geometry cannot be measured (no saturated plateau before the
// blend, too few future frames, or the result drifts implausibly far from
// the estimate).
func reverseEndFoot(m *CostMatrix, lowres []*imgmath.LowresFrame, S, E, kRev int) (int, float64, bool) {
	D := E - S
	if D < 2 || kRev <= D || kRev > imgmath.MaxLag {
		return E, 0, false
	}
	baseWin := min(40, max(8, kRev/2))
	lo := max(0, S-8)
	hi := min(m.N-1, E+4+baseWin)
	col := computeReverse(m, lowres, lo, hi, kRev)
	rev := func(j int) (float64, bool) {
		if j < lo || j > hi || j >= len(m.RevRatio) || col >= len(m.RevRatio[j]) {
			return 0, false
		}
		v := float64(m.RevRatio[j][col])
		if v <= 0 { // a computed cost always carries ME penalties (> 0)
			return 0, false
		}
		return v, true
	}

	// Plateau top: the last saturated frame before the ramp. The saturated run
	// is [E−kRev, S−1]; scan back from just inside the blend.
	top := -1
	for j := min(hi, S+D/4); j >= lo; j-- {
		if v, ok := rev(j); ok && v >= defaultSaturation {
			top = j
			break
		}
	}
	if top < 0 {
		return E, 0, false
	}
	satLvl, ok1 := percentileOver(rev, max(lo, top-4), top, 0.75)
	// The baseline is the level the ramp lands ON — its CENTER, not its low
	// tail. With a low percentile the 20% crossing level sits below the floor
	// itself, the crossing waits for a deep noise dip past the true foot, and
	// the projection systematically overshoots (measured +4..+8 on noisy
	// floors); on clean floors the median and the low tail coincide.
	base, ok2 := percentileOver(rev, E+3, hi, 0.50)
	if !ok1 || !ok2 || satLvl-base < 2*edgeMinContrast {
		return E, 0, false
	}

	// Two-crossing projection down the falling ramp (mirrors footScan): find
	// the first frames at/below the 50% and 20% levels of the ramp height and
	// extrapolate the line through them to the baseline.
	va := base + 0.5*(satLvl-base)
	vb := base + 0.2*(satLvl-base)
	ja, jb := -1, -1
	for j := top + 1; j <= hi; j++ {
		v, ok := rev(j)
		if !ok {
			break
		}
		if ja < 0 && v <= va {
			ja = j
		}
		if v <= vb {
			jb = j
			break
		}
	}
	if ja < 0 || jb < 0 {
		return E, 0, false
	}
	g := jb - ja
	foot := jb + int(float64(g)*(0.2/0.3)+0.5)
	// Drift bound: the estimate's end error is at most ~half the blend.
	if foot <= S || absInt(foot-E) > max(8, D/2) {
		return E, 0, false
	}
	// Predicted crossing jitter: floor noise (robust sigma from the IQR) over
	// the measured per-frame ramp slope. On a clean linear ramp this is ~0; on
	// a ~100-frame blend whose ramp height is a fraction of the floor noise it
	// reaches tens of frames — the regime where single-frame crossings carry
	// no end information and the plateau estimate must stand.
	q1, okq1 := percentileOver(rev, E+3, hi, 0.25)
	q3, okq3 := percentileOver(rev, E+3, hi, 0.75)
	sigma := 0.0
	if okq1 && okq3 {
		slope := (satLvl - base) / float64(max(1, jb-top))
		if slope > 0 {
			sigma = (q3 - q1) / 1.35 / slope
		}
	}
	return foot, sigma, true
}

// percentileOver collects f over [lo, hi] and returns the p-quantile.
func percentileOver(f func(int) (float64, bool), lo, hi int, p float64) (float64, bool) {
	var v []float64
	for j := lo; j <= hi; j++ {
		if x, ok := f(j); ok {
			v = append(v, x)
		}
	}
	if len(v) == 0 {
		return 0, false
	}
	return percentile(v, p), true
}

// findAnchor scans from `from` in direction dir (±1) up to limit (inclusive)
// for the first frame at or above th that has an elevated neighbour on either
// side (a single-frame motion spike has both neighbours below, so it cannot
// anchor; a frame on the run's boundary has its interior neighbour elevated).
func findAnchor(f func(int) (float64, bool), from, dir, limit int, th float64) (int, bool) {
	for j := from; (dir > 0 && j <= limit) || (dir < 0 && j >= limit); j += dir {
		v0, k0 := f(j)
		if !k0 || v0 < th {
			continue
		}
		vp, kp := f(j - 1)
		vn, kn := f(j + 1)
		if (kp && vp >= th) || (kn && vn >= th) {
			return j, true
		}
	}
	return 0, false
}

// walkRun walks from an in-run anchor in direction dir while the signal stays
// at or above th, bridging single-frame dips, and returns the last elevated
// frame — the edge of the contiguous run CONTAINING the anchor. Walking
// outward from inside the run (rather than scanning inward from the window
// edge) is what keeps a neighbouring transition's run, even inside the search
// window, from capturing the edge. A dip is bridged only while it stays above
// bridgeFloor: a frame that has dropped to the out-of-run floor is the run's
// end, and treating it as a dip lets the walk string together floor-level
// noise on busy content and inflate the run far past the true edge.
func walkRun(f func(int) (float64, bool), anchor, dir, limit int, th, bridgeFloor float64) int {
	last := anchor
	j := anchor + dir
	for (dir > 0 && j <= limit) || (dir < 0 && j >= limit) {
		v, ok := f(j)
		if !ok {
			break
		}
		if v >= th {
			last = j
			j += dir
			continue
		}
		// Single-frame dip: bridge it only if it stays off the floor and the
		// run resumes immediately.
		v2, ok2 := f(j + dir)
		if v >= bridgeFloor && ok2 && v2 >= th && ((dir > 0 && j+dir <= limit) || (dir < 0 && j+dir >= limit)) {
			last = j + dir
			j += 2 * dir
			continue
		}
		break
	}
	return last
}

// refineDissolveEdges progressively narrows the prediction distance around the
// two edges of a detected dissolve and returns refined (StartFrame, EndFrame)
// in the SceneTransition convention (last unblended frame before the blend,
// first unblended frame after it). All measurements are stored in the matrix
// (forward via Refine into Ratio, reverse into RevRatio), so they appear in
// the cost-matrix CSV.
func refineDissolveEdges(s *LookaheadScanner, m *CostMatrix, lowres []*imgmath.LowresFrame, startFrame, endFrame int) (int, int, refineInfo) {
	S := startFrame + 1 // first blended frame
	E := endFrame       // first pure new-scene frame
	inS := S            // incoming start: anchor for rungStartBackBound
	if len(lowres) == 0 || E-S < 1 {
		return startFrame, endFrame, refineInfo{}
	}
	// Drift bound: narrowing adjusts edges, it must not wander to a
	// neighbouring event.
	origLo := S - (E - S) - 10
	origHi := E + (E - S) + 10
	// All ladder levels anchor from the ORIGINAL bounds' midpoint, which lies
	// inside the true blend by the rough-bounds contract (start within a few
	// frames, end overshooting by at most ~half the duration). Anchoring from
	// the running estimates instead lets one corrupted level poison the next:
	// at a large k two adjacent blends' runs can physically touch (E+k−1
	// reaches the next start), the merged-run walk lands E on the neighbour's
	// edge, and every later level would then anchor inside the neighbour's
	// run. With a fixed in-blend anchor the small-k levels, where the runs are
	// separated, recover the true edges regardless of what larger k reported.
	// The ladder runs in up to two passes. Within a pass, all scale gates use
	// the duration estimate FROZEN at the pass start, for the same reason as
	// the anchor: a corrupted level (e.g. a large rung fusing with a
	// neighbouring blend) must not inflate the running duration, freeze the
	// start, and gate off the very rungs that recover the true edges. But the
	// incoming estimate itself can be off (a short blend whose plateau end
	// overshot can look "long"), so when a pass materially shrinks the bounds,
	// a second pass re-gates at the refined scale — letting the small rungs
	// run and recover short-blend exactness.
	rungStart := -1 // earliest rung-resolved start across both passes
	footDone := false
	for pass := 0; pass < 2; pass++ {
		origD := E - S
		origMid := (S + E) / 2

		for _, k := range edgeLadder {
			D := origD
			if D < 2 || k >= D || k*4 < D {
				// A rung is only valid in D/4 ≤ k < D. Above D the plateau
				// consensus already owns the measurement. Below D/4 the per-step
				// blend fraction (~k/D) is too small relative to the elevation's
				// variation along the blend: the p75-based threshold then sits
				// above the run's tail (the alpha-difference is concave-compressed
				// near the ends), and on a long blend the small-k flicker around
				// the threshold lets the anchor walk carve a tiny false interval
				// out of the blend's middle — measured: lag 15 localizes a
				// 45-frame blend's end to ±1 but corrupts a 90-frame one, and
				// lag ≤ 5 collapsed a 90-frame blend to a 3-frame sliver. Blends
				// with no qualifying rung keep the plateau bounds; their end is
				// measured directly by reverseEndFoot below.
				continue
			}
			win := min(D, 16) + 6

			// Forward ratios at distance k across both edges (the start rise at S
			// and the end of the elevated run at E+k−1).
			fLo := max(k, S-win-6)
			fHi := min(m.N-1, E+k+win)
			_ = s.Refine(m, lowres, fLo, fHi, []int{k})
			fcol := sort.SearchInts(m.Lags, k)
			if fcol >= len(m.Lags) || m.Lags[fcol] != k {
				break
			}
			fwd := func(j int) (float64, bool) {
				if j < fLo || j > fHi || j < 0 || j >= len(m.Ratio) || fcol >= len(m.Ratio[j]) {
					return 0, false
				}
				return float64(m.Ratio[j][fcol]), true
			}

			// Reverse ratios at distance k around the end edge.
			rLo := max(0, E-win)
			rHi := min(m.N-1, E+win)
			rcol := computeReverse(m, lowres, rLo, rHi, k)
			rev := func(j int) (float64, bool) {
				if j < rLo || j > rHi || j < 0 || j >= len(m.RevRatio) || rcol >= len(m.RevRatio[j]) {
					return 0, false
				}
				return float64(m.RevRatio[j][rcol]), true
			}

			// Local levels measured from the data: floors just outside each edge
			// (robust p25 — tolerates a partially contaminated window when the
			// incoming estimate is off), elevated levels measured around the
			// MIDPOINT anchor rather than windows keyed off the running S/E —
			// those are the quantities being refined and can sit entirely
			// outside the true blend when the incoming estimate overshoots,
			// turning the "elevated" level into post-edge noise and the
			// threshold into a noise gate (measured: a 4-frame blend's reverse
			// elevation window landed wholly past the blend and the walk
			// chased new-scene noise 3 frames out).
			elevLo := max(0, origMid-(k+2))
			elevHi := min(m.N-1, origMid+(k+2))
			floorF, ok1 := percentileOver(fwd, fLo, S-2, 0.25)
			elevF, ok2 := percentileOver(fwd, max(fLo, elevLo), min(fHi, elevHi), 0.75)
			floorR, ok3 := percentileOver(rev, E+1, rHi, 0.25)
			elevR, ok4 := percentileOver(rev, max(rLo, elevLo), min(rHi, min(elevHi, E-1)), 0.75)
			if !ok1 || !ok2 || !ok3 || !ok4 {
				// An empty/unmeasurable window (e.g. a dissolve within ~k frames of
				// the clip start) is k-dependent in the opposite direction of the
				// ladder — a smaller k may still be measurable — so skip the level
				// rather than aborting the ladder.
				continue
			}
			cF := elevF - floorF
			cR := elevR - floorR
			if cF < edgeMinContrast && cR < edgeMinContrast {
				// No usable contrast in either direction at this distance. Skip the
				// level, not the ladder: contrast is non-monotonic across scales
				// (floors are high at large k, elevation vanishes at small k), so a
				// dead level says nothing about the next one.
				continue
			}
			fwdOK := cF >= edgeMinContrast
			revOK := cR >= edgeMinContrast
			thF := floorF + edgeThetaFrac*cF
			thR := floorR + edgeThetaFrac*cR

			// Each edge is located by anchoring INSIDE this blend's elevated run
			// (searching inward from the current estimate, so the first run met is
			// this blend's, not a neighbour's) and then walking OUTWARD to the
			// run's own edge. Scanning inward from the window edge instead would
			// latch onto any neighbouring transition whose run intersects the
			// window and fuse adjacent dissolves.

			// Start: anchor going backward from the in-blend midpoint (the forward
			// run [S, E+k−1] contains it), walk back to the run's first frame.
			// The backward walk uses the LOWER start threshold: the first
			// blended frame's elevation is only ~alpha(S) = 1/(D+1) of the
			// contrast (concavity lifts it some), which straddles the standard
			// crossing fraction — measured on two captures, the D=4 first
			// frame read 27 % and 33 % of contrast at the smallest rung. The
			// pre-blend side is pure old-scene floor, so the lower threshold
			// costs no false-early margin in practice.
			newS := -1
			if fwdOK {
				if a, ok := findAnchor(fwd, min(origMid, fHi-1), -1, max(fLo, S-win), thF); ok {
					thS := floorF + edgeStartThetaFrac*cF
					newS = walkRun(fwd, a, -1, max(fLo, S-win), thS, floorF+edgeBridgeFrac*cF)
				}
			}

			// End, reverse: the run [S−k, E−1] is at the floor from E on; anchor
			// going backward from the midpoint (never reaching a later neighbour),
			// walk forward to the run's last frame, E = last + 1.
			eRev := -1
			if revOK {
				if a, ok := findAnchor(rev, min(origMid, rHi), -1, max(rLo+1, S-1-k), thR); ok {
					eRev = walkRun(rev, a, +1, min(rHi, E+win), thR, floorR+edgeBridgeFrac*cR) + 1
				}
			}

			// End, forward: the run stays elevated until the reference slides out
			// of the blend at j = E+k−1; anchor backward from the midpoint, walk
			// forward, E = last − k + 1.
			eFwd := -1
			if fwdOK {
				if a, ok := findAnchor(fwd, min(origMid, fHi-1), -1, max(fLo+1, S-win), thF); ok {
					eFwd = walkRun(fwd, a, +1, min(fHi, E+k+win), thF, floorF+edgeBridgeFrac*cF) - k + 1
				}
			}

			// A level that cannot resolve its edges is skipped, not fatal — the
			// next (smaller) distance may have the contrast this one lacked.
			// Rungs update the START only for short blends (see rungStartMaxD);
			// beyond that the plateau consensus start is more accurate than any
			// rung's threshold crossing. The END always benefits: the rung's
			// run-edge walk and the reverse foot below are the only end
			// measurements that do not inherit the forward trailing smear.
			if D > rungStartMaxD {
				newS = S
			}
			if newS > 0 && newS < inS-rungStartBackBound {
				// Contract bound: a start this far before the incoming
				// estimate is local floor structure masquerading as blend at
				// this rung, not a real edge. Keep the current start; the
				// rung's end measurement still applies.
				newS = S
			}
			if newS < 0 || (eRev < 0 && eFwd < 0) {
				continue
			}
			// Fuse the two end estimates when they corroborate each other.
			// When they diverge, trust the reverse: its falling ramp lands on
			// the new-scene floor at exactly E with no −k extrapolation, while
			// the forward trailing run ends in the NEW scene — whose forward
			// floor can sit above a threshold derived from the pre-blend
			// (old-scene) floor, letting the trailing walk run away (measured:
			// 20+ frames into a busier new scene, inflating the duration
			// estimate and mis-gating everything downstream).
			newE := eRev
			if eRev > 0 && eFwd > 0 && absInt(eRev-eFwd) <= 2 {
				newE = (eRev + eFwd + 1) / 2
			} else if eRev < 0 {
				newE = eFwd
			}
			if newE-newS < 1 || newS < origLo || newE > origHi {
				continue
			}
			// The rung-start bias is one-sided late (the low-alpha head of the
			// blend is invisible below the crossing threshold), so among the
			// rungs that did resolve the start, the earliest wins — a later
			// rung must not drag an exact larger-rung start later.
			if newS != S {
				if rungStart < 0 || newS < rungStart {
					rungStart = newS
				} else {
					newS = rungStart
				}
			}
			S, E = newS, newE
		}

		// Long blends: the ladder distances above have no per-step contrast
		// (~k/D vanishes), so the end largely keeps the plateau estimate. Measure
		// it directly instead with a reverse distance slightly wider than the
		// blend — the reverse falling ramp lands on the new-scene floor at exactly
		// E (see reverseEndFoot). The margin over the current duration estimate
		// covers the plateau end error feeding the kRev choice. The foot runs at
		// most once per refinement: a second-pass re-measurement at a collapsed
		// kRev margin re-derives the same edge from worse geometry and can only
		// add noise (measured: pass-2 feet moved exact pass-1 ends by +3).
		if D := origD; D > 16 && !footDone {
			kRev := D + max(4, D/8)
			if e, sigma, ok := reverseEndFoot(m, lowres, S, E, kRev); ok {
				// A later foot corrects the early-biased run/consensus ends and
				// is accepted freely (within the foot's own drift bound). An
				// earlier foot retracts the end into the blend — legitimate
				// when the incoming estimate overshot, but on a long noisy
				// ramp it is indistinguishable from noise-chasing, so it must
				// also clear the confidence gate.
				if e >= E-2 || sigma <= footRetractSigma {
					E = e
					footDone = true
				}
			}
		}

		refD := E - S
		if rungStart < 0 && refD <= rungStartMaxD && origD > rungStartMaxD {
			// The start was frozen at the stale scale but the refined duration
			// now qualifies for rung starts — re-pass to resolve it, even when
			// the end barely moved.
			continue
		}
		if footDone && refD > 16 {
			// The foot owns a long blend's end: the rung run-edge walk loses
			// the blend's low-alpha tail below the crossing threshold (the
			// mirror of the invisible head, ~0.2·D frames) and a re-pass would
			// only drag the end back early. Starts are plateau-owned here.
			break
		}
		if absInt(refD-origD) <= 2 {
			break // converged: a second pass would re-derive the same edges
		}
	}

	// Final stage: the psy AC-energy dip fit (see energy.go). It integrates
	// all ~D in-blend samples, so it is the only estimator whose edge error
	// keeps shrinking on long blends — and the first frame-level START
	// estimator for them (rung starts are gated to D ≤ rungStartMaxD; the
	// plateau consensus start is ±3–5 there). It adopts an edge only when
	// the fitted dip is strong and that edge's own profile-likelihood jitter
	// is within energyEdgeMaxJitter; otherwise the bounds pass through.
	var info refineInfo
	if eS, eE, snr, ok := energyEdgeRefine(m, S, E); true {
		info.DipSNR = snr
		info.EnergyUsed = ok
		if ok && eS >= origLo && eE <= origHi {
			S, E = eS, eE
		}
	}

	// Channel-mean step fit (see meanstep.go): the means are exactly linear
	// in the blend, so this is the zero-shape-bias witness for short and
	// mid-length blends; its visibility gate skips the drift-dominated long
	// blends the energy dip owns.
	if mS, mE, ok := meanStepRefine(m, S, E); ok && mS >= origLo && mE <= origHi {
		S, E = mS, mE
		info.MeanStepUsed = true
	}

	return S - 1, E, info
}

// refineInfo reports what the signal-fit stages of refineDissolveEdges
// measured — observability for the staged progress log.
type refineInfo struct {
	DipSNR       float64 // fitted energy-dip depth over flank noise (0 = not measured)
	EnergyUsed   bool    // an energy-fit edge was adopted
	MeanStepUsed bool    // a channel-mean step-fit edge was adopted
}
