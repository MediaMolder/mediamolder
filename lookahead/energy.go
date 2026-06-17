// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Dissolve edge refinement from per-frame psy AC energy.
//
// Blending two uncorrelated scenes attenuates high-frequency energy
// quadratically in the mix (~alpha² + (1−alpha)²), so a dissolve carves a
// U-shaped dip into the per-frame energy spanning exactly [S, E−1]: flat at
// the old-scene level until S, minimum at the alpha = 0.5 midpoint (measured
// 11–20 % below the flank chord on validation content), back at the
// new-scene level from E on. Two properties make this the complementary
// signal to the inter/intra ratio ramps:
//
//   - the slope is MAXIMAL at the edges and zero at the midpoint — the ratio
//     ramps have their gentlest part (the foot) at the edges;
//   - the signal is reference-free, so a model fit can integrate ALL ~D
//     in-blend samples. Edge error shrinks ~1/√D, which is what makes long
//     blends tractable: at D ≥ 90 the ratio ramps' per-frame slope sinks
//     below the floor noise and no threshold-crossing estimator works.
package lookahead

import "math"

const (
	// energyMinD: blends at or below this duration estimate skip energy
	// refinement — they are rung/foot-owned and already frame-exact, and a
	// few-frame dip carries too few samples for the fit to beat them.
	energyMinD = 16
	// energyMinDipSigma: the fitted dip depth must clear this multiple of
	// the robust flank noise. With ~D in-blend samples the depth estimate's
	// own noise is ~sigma/√(0.53·D), so 3 sigma is a ≥15-sigma statistical
	// bar for D ≥ 30 — noise-fit bumps cannot pass.
	energyMinDipSigma = 3.0
	// energyMinDipFrac: the dip must also be at least this fraction of the
	// chord level. Dissolves between near-identical textures have physically
	// ill-defined energy edges; measured synthetic dips are 11–20 %.
	energyMinDipFrac = 0.04
	// energyEdgeMaxJitter: an edge is adopted only when its 2-sigma
	// profile-likelihood interval half-width is at most this many frames —
	// the same philosophy as footRetractSigma: a frame-level estimator is
	// trusted only when its own predicted jitter beats the estimate it
	// replaces.
	energyEdgeMaxJitter = 3
	// energyFadeLumaLo/Hi: in-blend mean luma outside this band means the
	// "dissolve" passes through near-black/white — energy collapses for
	// luma reasons there and the dip model does not apply (the luma fade
	// path owns those events).
	energyFadeLumaLo = 0.10 * 255
	energyFadeLumaHi = 0.90 * 255
)

// energyEdgeRefine fits a chord-referenced dip model to the per-frame AC
// energy around a detected dissolve and returns refined bounds. S0/E0 and the
// returned S/E use the internal convention (S = first blended frame, E =
// first pure new frame). snr is the fitted dip depth over the flank noise
// (reported even when not ok, for observability); ok is true when at least
// one edge was adopted.
//
// Model: base(j) is a LINE fit through both flank windows — the chord. It
// absorbs differing scene energies (EA ≠ EB) and slow drift, and is the only
// admissible reference under the no-baseline rule: absolute energy levels
// mean nothing across content. The dip is delta·b(j) with b = 4·a·(1−a),
// a = (j−s+1)/(e−s+1) clamped to [0,1] — zero exactly outside [s, e−1],
// maximal mid-blend, depth a free parameter (the SATD response is much
// shallower than the analytic square law, so the depth must be measured, not
// assumed). The (s, e) grid search pays sum-of-squares over a FIXED window
// including the flanks, so a candidate edge outside the true blend predicts
// a dip on flat flank frames and is penalised — that is what localizes the
// edges.
func energyEdgeRefine(m *CostMatrix, S0, E0 int) (int, int, float64, bool) {
	S, E := S0, E0
	D0 := E0 - S0
	n := min(m.N, len(m.Energy))
	if D0 <= energyMinD || n == 0 {
		return S, E, 0, false
	}
	for j := max(0, S0); j < E0 && j < len(m.AvgLuma); j++ {
		if m.AvgLuma[j] < energyFadeLumaLo || m.AvgLuma[j] > energyFadeLumaHi {
			return S, E, 0, false
		}
	}

	// The whole fit — window, flank baseline, noise, grid — is derived from
	// the current center estimate. When the first fit lands on a grid
	// boundary or moves an edge materially, the incoming estimate erred more
	// than the drift reach assumed AND the flank windows keyed off it were
	// partially in-blend, tilting the chord; one full re-fit centered on the
	// first result re-derives clean flanks (the same no-stale-keying lesson
	// the rung windows taught).
	fit := energyFitOnce(m, n, S0, E0)
	if !fit.valid {
		return S, E, fit.snr(), false
	}
	if fit.atBoundary || absInt(fit.bestS-S0) > 2 || absInt(fit.bestE-E0) > 2 {
		if f2 := energyFitOnce(m, n, fit.bestS, fit.bestE); f2.valid {
			fit = f2
		}
	}

	snr := fit.snr()
	if fit.delta < energyMinDipSigma*fit.sigma ||
		fit.delta < energyMinDipFrac*math.Abs(fit.mid) {
		return S, E, snr, false
	}

	// Per-edge 2-sigma profile-likelihood intervals: the set of candidate
	// edges whose best cost (minimised over the other edge) stays within
	// 4·sigma² of the optimum. A wide interval means the data cannot pin
	// that edge — keep the incoming estimate for it.
	lim := fit.cMin + 4*fit.sigma*fit.sigma
	jS := profileHalfWidth(fit.cost, fit.bestS-fit.sLo, lim, true)
	jE := profileHalfWidth(fit.cost, fit.bestE-fit.eLo, lim, false)
	ok := false
	if jS <= energyEdgeMaxJitter && absInt(fit.bestS-S0) > 1 {
		S = fit.bestS
		ok = true
	}
	if jE <= energyEdgeMaxJitter && absInt(fit.bestE-E0) > 1 {
		E = fit.bestE
		ok = true
	}
	if E-S < 2 {
		return S0, E0, snr, false
	}
	return S, E, snr, ok
}

// energyFit is one full dip-model fit around a center estimate.
type energyFit struct {
	valid        bool
	bestS, bestE int
	delta        float64 // fitted dip depth
	sigma        float64 // robust flank noise
	mid          float64 // chord level at the blend midpoint
	cMin         float64
	cost         [][]float64 // SSE grid for the profile intervals
	sLo, eLo     int         // grid origins
	atBoundary   bool        // optimum sits on a grid edge
}

func (f energyFit) snr() float64 {
	if f.sigma <= 0 {
		return 0
	}
	return f.delta / f.sigma
}

func energyFitOnce(m *CostMatrix, n, S0, E0 int) energyFit {
	D0 := E0 - S0
	wf := max(12, min(40, D0/2))
	drift := max(6, D0/4)
	wLo := max(0, S0-wf)
	wHi := min(n-1, E0+wf)

	// Chord baseline: a line fit through both flank windows. It absorbs
	// differing scene energies and slow drift; absolute levels are never
	// referenced (no-baseline rule).
	var fx, fy []float64
	for j := wLo; j <= S0-3; j++ {
		fx = append(fx, float64(j))
		fy = append(fy, float64(m.Energy[j]))
	}
	nPre := len(fx)
	for j := E0 + 3; j <= wHi; j++ {
		fx = append(fx, float64(j))
		fy = append(fy, float64(m.Energy[j]))
	}
	if nPre < 6 || len(fx)-nPre < 6 {
		return energyFit{}
	}
	slope, intercept, _ := linReg(fx, fy)
	base := func(j int) float64 { return slope*float64(j) + intercept }

	// Robust flank noise from the residual IQR.
	res := make([]float64, len(fx))
	for i := range fx {
		res[i] = fy[i] - (slope*fx[i] + intercept)
	}
	sigma := (percentile(res, 0.75) - percentile(res, 0.25)) / 1.35
	mid := base((S0 + E0) / 2)
	sigma = math.Max(sigma, 1e-6*math.Abs(mid)+1e-9)

	// Residual below the chord, over the fixed evaluation window. Including
	// the flank frames is what localizes the edges: a candidate edge outside
	// the true blend predicts a dip on flat flank frames and pays SSE.
	r := make([]float64, wHi-wLo+1)
	for j := wLo; j <= wHi; j++ {
		r[j-wLo] = base(j) - float64(m.Energy[j])
	}

	// Grid search over candidate edges; closed-form least-squares depth per
	// candidate. The cost grid is kept for the per-edge profile intervals.
	sLo, sHi := max(wLo, S0-drift), S0+drift
	eLo, eHi := E0-drift, min(wHi+1, E0+drift)
	cost := make([][]float64, sHi-sLo+1)
	cMin := math.Inf(1)
	bestS, bestE, bestDelta := S0, E0, 0.0
	for s := sLo; s <= sHi; s++ {
		row := make([]float64, eHi-eLo+1)
		for i := range row {
			row[i] = math.Inf(1)
		}
		cost[s-sLo] = row
		for e := max(eLo, s+2); e <= eHi; e++ {
			var rb, bb float64
			inv := 1.0 / float64(e-s+1)
			for j := wLo; j <= wHi; j++ {
				a := float64(j-s+1) * inv
				if a < 0 {
					continue // b = 0 before the blend
				}
				if a > 1 {
					break // b = 0 from e on; no later j contributes
				}
				b := 4 * a * (1 - a)
				rb += r[j-wLo] * b
				bb += b * b
			}
			if bb <= 0 {
				continue
			}
			delta := math.Max(0, rb/bb)
			var c float64
			for j := wLo; j <= wHi; j++ {
				a := math.Min(1, math.Max(0, float64(j-s+1)*inv))
				d := r[j-wLo] - delta*4*a*(1-a)
				c += d * d
			}
			row[e-eLo] = c
			if c < cMin {
				cMin, bestS, bestE, bestDelta = c, s, e, delta
			}
		}
	}
	if math.IsInf(cMin, 1) {
		return energyFit{}
	}
	return energyFit{
		valid: true,
		bestS: bestS, bestE: bestE,
		delta: bestDelta, sigma: sigma, mid: mid,
		cMin: cMin, cost: cost, sLo: sLo, eLo: eLo,
		atBoundary: bestS == sLo || bestS == sHi || bestE == eLo || bestE == eHi,
	}
}

// profileHalfWidth returns the half-width (in grid steps) of the interval of
// row indices (overRows) or column indices whose profile cost — minimised
// over the other axis — is at most lim, measured around bestIdx.
func profileHalfWidth(cost [][]float64, bestIdx int, lim float64, overRows bool) int {
	nR := len(cost)
	if nR == 0 {
		return 0
	}
	nC := len(cost[0])
	size := nC
	if overRows {
		size = nR
	}
	lo, hi := bestIdx, bestIdx
	for i := 0; i < size; i++ {
		best := math.Inf(1)
		if overRows {
			for c := 0; c < nC; c++ {
				if cost[i][c] < best {
					best = cost[i][c]
				}
			}
		} else {
			for rI := 0; rI < nR; rI++ {
				if cost[rI][i] < best {
					best = cost[rI][i]
				}
			}
		}
		if best <= lim {
			if i < lo {
				lo = i
			}
			if i > hi {
				hi = i
			}
		}
	}
	return max(bestIdx-lo, hi-bestIdx)
}
