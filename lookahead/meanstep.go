// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Dissolve edge refinement from per-frame channel means (Y/U/V).
//
// A pixel-wise blend makes every channel's frame mean EXACTLY linear in the
// mix: mean(f) = (1−alpha)·muA + alpha·muB. During a linear dissolve each of
// the luma and chroma means therefore traces a straight ramp from the old
// scene's level to the new one's, departing at exactly S and landing at
// exactly E — a piecewise-linear flat–ramp–flat model with NO free amplitude
// (the levels come from the flanks) and no shape error for linear blends.
// Two scenes are at least as likely to differ in colour as in brightness, so
// U and V are independent witnesses where luma steps are small.
//
// The signal's limit is scene-mean DRIFT: within-scene means wander with
// motion, and once the per-frame ramp increment |step|/(D+1) sinks to the
// frame-to-frame mean noise, the fit can trade ramp against drift freely and
// its edges carry no information (measured: errors of ±25..±55 frames on
// 90–120-frame blends whose fits still reported tight intervals). The
// visibility gate below excludes that regime structurally; long blends are
// owned by the AC-energy dip and the reverse foot.
package lookahead

import "math"

const (
	// meanStepChanSNR: a channel participates only when its fitted level
	// step clears this multiple of the robust flank noise.
	meanStepChanSNR = 3.0
	// meanStepMinStep: ... and only when the step is at least this many
	// 8-bit levels in absolute terms. Frame means are quantised through
	// per-pixel byte rounding, which biases a blended frame's mean away
	// from the exact linear model by up to ~0.15 levels (measured) in a
	// correlated, non-monotonic way — a step below ~5× that scale gives
	// the fit nothing but rounding artifacts to chase, with flank noise
	// floors so low on static content that the relative gates all pass.
	meanStepMinStep = 0.75
	// meanStepChanVis: ... and only when the per-frame ramp increment
	// |step|/(D+1) clears this multiple of the frame-to-frame mean noise.
	// This is the gate that excludes the drift-dominated long-blend regime:
	// measured on validation content, blends of 4–30 frames score 6–34 and
	// 45+ frames score ≤ 1.8 — the latter produce confidently-wrong fits.
	meanStepChanVis = 3.0
	// meanStepRescueMove / meanStepRescueJitter: a fitted edge replaces the
	// incoming one outright when it moves ≥ 3 frames and its 2σ
	// profile-likelihood half-width is ≤ 2 — the incoming bound is
	// statistically rejected, whatever estimator produced it. Overriding
	// another estimator this hard additionally needs corroboration: two
	// participating channels, or one at polish-grade visibility (measured:
	// a lone marginal-visibility U channel rescued a 30-frame blend's end
	// 3 frames the wrong way).
	meanStepRescueMove   = 3
	meanStepRescueJitter = 2
	// meanStepPolishVis: small moves below the rescue size are accepted
	// only at zero fitted jitter and per-frame visibility this high — and
	// inward ones (later start / earlier end) need a move of at least 2.
	// The mean signal under-sees the blend's low-alpha head and tail
	// (encoding quantises sub-noise mean increments away), biasing its OWN
	// edges up to ~1 frame inward: an inward ±1 is as likely that bias as
	// a correction, while an outward ±1 corrects the same inward bias in
	// the rung/threshold estimators, and an inward ≥2 exceeds the bias
	// scale (measured: adopted a 4-frame blend's exact start where the
	// rung was +1 late; rejected the +1-inward fit that would have broken
	// an exact 7-frame start).
	meanStepPolishVis = 10.0
)

// meanStepChan is one participating channel's flank model.
type meanStepChan struct {
	pre  [2]float64 // slope, intercept of the pre-blend flank line
	post [2]float64 // slope, intercept of the post-blend flank line
	sig  float64    // robust flank residual noise
}

// meanStepRefine fits the flat–ramp–flat mean model around a detected
// dissolve and returns refined bounds (S = first blended frame, E = first
// pure new frame, the refineDissolveEdges convention). ok is true when at
// least one edge was adopted. Channels with no usable step (or absent
// chroma — AvgU/AvgV are populated by the scene_change_mc processor, not by
// every scanner caller) drop out individually; with no participating
// channel the bounds pass through untouched.
func meanStepRefine(m *CostMatrix, S0, E0 int) (int, int, bool) {
	S, E := S0, E0
	fit, ok := meanStepFitOnce(m, S0, E0)
	if !ok {
		return S, E, false
	}
	if fit.atBoundary || absInt(fit.s-S0) > 2 || absInt(fit.e-E0) > 2 {
		if f2, ok2 := meanStepFitOnce(m, fit.s, fit.e); ok2 {
			fit = f2
		}
	}
	corroborated := fit.nChans >= 2 || fit.vis >= meanStepPolishVis
	adopt := func(move, jitter int, outward bool) bool {
		if absInt(move) >= meanStepRescueMove && jitter <= meanStepRescueJitter && corroborated {
			return true
		}
		if jitter != 0 || fit.vis < meanStepPolishVis {
			return false
		}
		if outward {
			return move != 0
		}
		return absInt(move) >= 2
	}
	changed := false
	if adopt(fit.s-S0, fit.jS, fit.s < S0) {
		S = fit.s
		changed = true
	}
	if adopt(fit.e-E0, fit.jE, fit.e > E0) {
		E = fit.e
		changed = true
	}
	if E-S < 2 {
		return S0, E0, false
	}
	return S, E, changed
}

type meanStepFit struct {
	s, e       int
	jS, jE     int     // 2σ profile-likelihood half-widths per edge
	vis        float64 // best participating channel's per-frame visibility
	nChans     int     // participating channels
	atBoundary bool
}

func meanStepFitOnce(m *CostMatrix, S0, E0 int) (meanStepFit, bool) {
	D0 := E0 - S0
	if D0 < 2 {
		return meanStepFit{}, false
	}
	n := m.N
	wf := max(12, min(40, D0/2))
	drift := max(6, D0/4)
	wLo := max(0, S0-wf)
	wHi := min(n-1, E0+wf)
	if S0-3 < wLo+5 || E0+3 > wHi-5 {
		return meanStepFit{}, false
	}

	channels := [][]float32{m.AvgLuma, m.AvgU, m.AvgV}
	var chans []meanStepChan
	var series [][]float64
	bestVis := 0.0
	mid := float64(S0+E0) / 2
	for _, ch := range channels {
		if len(ch) < n {
			continue // chroma absent for this scanner's source
		}
		var px, py, qx, qy []float64
		for j := wLo; j <= S0-3; j++ {
			px = append(px, float64(j))
			py = append(py, float64(ch[j]))
		}
		for j := E0 + 3; j <= wHi; j++ {
			qx = append(qx, float64(j))
			qy = append(qy, float64(ch[j]))
		}
		if len(px) < 6 || len(qx) < 6 {
			continue
		}
		bp, ap, _ := linReg(px, py)
		bq, aq, _ := linReg(qx, qy)
		// Robust flank residual noise (level errors) and frame-to-frame
		// noise (the high-passed scale the per-frame ramp increment must
		// clear — flank σ alone hides slow drift).
		var res, dif []float64
		for i := range px {
			res = append(res, py[i]-(bp*px[i]+ap))
			if i > 0 {
				dif = append(dif, math.Abs(py[i]-py[i-1]))
			}
		}
		for i := range qx {
			res = append(res, qy[i]-(bq*qx[i]+aq))
			if i > 0 {
				dif = append(dif, math.Abs(qy[i]-qy[i-1]))
			}
		}
		sig := math.Max((percentile(res, 0.75)-percentile(res, 0.25))/1.35, 1e-4)
		sigd := math.Max(percentile(dif, 0.5)/0.954, 1e-4)
		step := (bq*mid + aq) - (bp*mid + ap)
		vis := math.Abs(step) / float64(D0+1) / sigd
		if math.Abs(step) < meanStepMinStep ||
			math.Abs(step)/sig < meanStepChanSNR || vis < meanStepChanVis {
			continue
		}
		// Flank stationarity: a "flank" whose fitted line rises by a sizable
		// fraction of the step across its own window is not a stable scene
		// level — it is an ADJACENT transition inside the window (measured:
		// with a second blend 6 frames after the first, the post-flank line
		// fit through the neighbour's ramp dragged the end 5 frames into
		// it) or drift strong enough to corrupt the model either way.
		preRise := math.Abs(bp) * float64(len(px))
		postRise := math.Abs(bq) * float64(len(qx))
		if preRise > 0.5*math.Abs(step) || postRise > 0.5*math.Abs(step) {
			continue
		}
		if vis > bestVis {
			bestVis = vis
		}
		sv := make([]float64, wHi-wLo+1)
		for j := wLo; j <= wHi; j++ {
			sv[j-wLo] = float64(ch[j])
		}
		chans = append(chans, meanStepChan{pre: [2]float64{bp, ap}, post: [2]float64{bq, aq}, sig: sig})
		series = append(series, sv)
	}
	if len(chans) == 0 {
		return meanStepFit{}, false
	}

	// 2-parameter grid: no free amplitude — the model is fully determined
	// by the flank lines and the candidate edges, so the chi² surface is
	// sharply curved wherever the step is visible.
	sLo, sHi := max(wLo, S0-drift), S0+drift
	eLo, eHi := E0-drift, min(wHi, E0+drift)
	cost := make([][]float64, sHi-sLo+1)
	cMin := math.Inf(1)
	bestS, bestE := S0, E0
	for s := sLo; s <= sHi; s++ {
		row := make([]float64, eHi-eLo+1)
		for i := range row {
			row[i] = math.Inf(1)
		}
		cost[s-sLo] = row
		for e := max(eLo, s+2); e <= eHi; e++ {
			inv := 1.0 / float64(e-s+1)
			var chi float64
			for ci, c := range chans {
				sv := series[ci]
				for j := wLo; j <= wHi; j++ {
					a := math.Min(1, math.Max(0, float64(j-s+1)*inv))
					pred := (1-a)*(c.pre[0]*float64(j)+c.pre[1]) + a*(c.post[0]*float64(j)+c.post[1])
					d := (sv[j-wLo] - pred) / c.sig
					chi += d * d
				}
			}
			row[e-eLo] = chi
			if chi < cMin {
				cMin, bestS, bestE = chi, s, e
			}
		}
	}
	if math.IsInf(cMin, 1) {
		return meanStepFit{}, false
	}
	lim := cMin + 4 // Δchi² = 4 ⇒ 2σ in one parameter
	jS := profileHalfWidth(cost, bestS-sLo, lim, true)
	jE := profileHalfWidth(cost, bestE-eLo, lim, false)
	return meanStepFit{
		s: bestS, e: bestE, jS: jS, jE: jE, vis: bestVis, nChans: len(chans),
		atBoundary: bestS == sLo || bestS == sHi || bestE == eLo || bestE == eHi,
	}, true
}
