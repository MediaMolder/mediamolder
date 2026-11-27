// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import (
	"math"
	"sort"
)

// Short-transition detection: hard cuts, cut bursts and short-to-medium
// dissolves/fades, found from two per-frame signals that need no retained
// frames:
//
//   - the lag-1 inter/intra cost ratio r1 (motion-compensated: "could this
//     frame be predicted from the previous one?"), and
//   - a coarse thumbnail of the frame (grid cell means), in whose space a
//     pixel-wise blend of two scenes travels along a STRAIGHT LINE from the
//     old scene's point to the new one's, while camera motion does not.
//
// The ratio alone cannot tell a dissolve from a fast pan — both are runs of
// failed prediction — and a per-frame "the ratio is high" test fires on
// ordinary sports footage. Every rule here is therefore relative (a spike
// against its own neighbourhood, a run against its own flanks) and every
// gradual transition must pass the thumbnail linearity test. See
// docs/scene-detection.md ("How it works") for the full description.

// Thumbnail geometry. The grid is fixed so thresholds calibrated on it hold
// for any input resolution.
const (
	ThumbW = 32
	ThumbH = 18
)

const (
	// Hard cuts.
	cutMinRatio   = 0.75 // r1 at the cut frame
	cutMinIso     = 0.30 // r1 above every neighbour within ±cutIsoRadius
	cutIsoRadius  = 3
	cutOnsetRatio = 0.90 // one-sided (onset/offset) spikes need near-saturation
	cutThumbIso   = 2.5  // thumbnail jump vs its neighbours, one-sided cut

	// Candidate activity runs on r1.
	candFloorRadius = 30
	candDelta       = 0.12
	candPad         = 3

	// Linear (dissolve/fade) hypotheses.
	linMaxResid     = 0.20 // max off-line residual, relative to |B−A|
	linMinTravel    = 9.0  // rms thumbnail distance between the endpoints
	linMinSpeedGain = 1.5  // in-transition thumbnail speed vs the flanks
	linMinRatioGain = 0.14 // r1 inside (p75) above the flank median
	linMinCoverage  = 0.42 // participation ratio of the per-cell change
	linMidLo        = 0.12 // an intermediate frame has mix in (lo, 1−lo)
	linMinMids      = 2
	linMaxBackstep  = 0.06 // mix must not retreat by more than this
	linFlank        = 12   // flank frames on each side for the contrasts
	linDefaultMax   = 64   // longest blend searched when DissolveMaxLen unset
	linMaxSearch    = 240  // hard cap on the searched blend length

	// Pan rejection (global thumbnail shift).
	panMinRho    = 0.85 // residual after the best shift / unshifted residual
	panMinFlow   = 0.87 // same, for the first-order (sub-cell) fit
	panMaxShiftX = 16   // half-cells
	panMaxShiftY = 6
)

// shortScan carries the per-clip signals shared by the short-transition
// rules.
type shortScan struct {
	n     int
	r1    []float64
	th    [][]float32 // per-frame thumbnail, nil when unavailable
	tc    int         // thumbnail channels
	sp    []float64   // rms thumbnail step j−1 → j
	rhoOK []int8      // pan-test cache: 0 unknown, 1 not a pan, -1 pan
}

func newShortScan(m *CostMatrix) *shortScan {
	li := lagIndex(m.Lags, 1)
	if li < 0 || m.N < 2*cutIsoRadius+3 {
		return nil
	}
	s := &shortScan{n: m.N, r1: make([]float64, m.N)}
	for j := 0; j < m.N; j++ {
		if li < len(m.Ratio[j]) {
			v := float64(m.Ratio[j][li])
			if v > 1 {
				v = 1
			} else if v < 0 {
				v = 0
			}
			s.r1[j] = v
		}
	}
	if len(m.Thumb) >= m.N && m.ThumbC > 0 && len(m.Thumb[0]) == ThumbW*ThumbH*m.ThumbC {
		s.th = m.Thumb[:m.N]
		s.tc = m.ThumbC
		s.sp = make([]float64, m.N)
		for j := 1; j < m.N; j++ {
			s.sp[j] = math.Sqrt(sqDist(s.th[j], s.th[j-1]) / float64(len(s.th[j])))
		}
		s.rhoOK = make([]int8, m.N)
	}
	return s
}

func sqDist(a, b []float32) float64 {
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return sum
}

func maxOf(v []float64) float64 {
	m := math.Inf(-1)
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	return m
}

func medianOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return 0.5 * (s[len(s)/2-1] + s[len(s)/2])
}

// quantileOf returns the linearly interpolated q-quantile of v.
func quantileOf(v []float64, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	p := q * float64(len(s)-1)
	lo := int(p)
	if lo >= len(s)-1 {
		return s[len(s)-1]
	}
	f := p - float64(lo)
	return s[lo]*(1-f) + s[lo+1]*f
}

// notPan reports whether the thumbnail step j−1 → j is NOT explained by a
// global translation. Two estimators cover the speed range: a search over
// half-cell shifts (fast pans, several cells per frame) and a first-order
// gradient fit (slow pans, a fraction of a cell — typical of slow-motion
// replays, whose blur defeats block motion estimation long before the
// picture has moved a cell). When the better of the two still leaves at
// least panMinRho / panMinFlow of the uncompensated residual, no translation explains the
// change (a blend, a cut, a graphic); otherwise the camera moved.
func (s *shortScan) notPan(j int) bool {
	if s.th == nil || j < 1 || j >= s.n {
		return true
	}
	if s.rhoOK[j] != 0 {
		return s.rhoOK[j] > 0
	}
	ok := s.notFastPan(j) && thumbFlowRho(s.th[j], s.th[j-1], s.tc) >= panMinFlow
	if ok {
		s.rhoOK[j] = 1
	} else {
		s.rhoOK[j] = -1
	}
	return ok
}

// notFastPan is the shift-search half of notPan. Cut-like steps use it
// alone: the gradient fit assumes a small displacement of one picture and
// says nothing about a step between two different pictures.
func (s *shortScan) notFastPan(j int) bool {
	if s.th == nil || j < 1 || j >= s.n {
		return true
	}
	return thumbShiftRho(s.th[j], s.th[j-1], s.tc) >= panMinRho
}

// thumbShiftRho returns sqrt(best shifted mean-square difference / unshifted
// mean-square difference) between two thumbnails, searching half-cell shifts
// (each thumbnail is 2× nearest-neighbour upsampled) over ±panMaxShiftX ×
// ±panMaxShiftY with at least 40 % horizontal / 50 % vertical overlap.
func thumbShiftRho(a, b []float32, c int) float64 {
	const uw, uh = 2 * ThumbW, 2 * ThumbH
	at := func(t []float32, y, x, k int) float64 {
		return float64(t[((y>>1)*ThumbW+(x>>1))*c+k])
	}
	msd := func(dx, dy int) (float64, bool) {
		y0, y1 := 0, uh
		if dy > 0 {
			y0 = dy
		} else {
			y1 = uh + dy
		}
		x0, x1 := 0, uw
		if dx > 0 {
			x0 = dx
		} else {
			x1 = uw + dx
		}
		if float64(y1-y0) < 0.5*uh || float64(x1-x0) < 0.4*uw {
			return 0, false
		}
		var sum float64
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				for k := 0; k < c; k++ {
					d := at(a, y, x, k) - at(b, y-dy, x-dx, k)
					sum += d * d
				}
			}
		}
		return sum / float64((y1-y0)*(x1-x0)*c), true
	}
	zero, _ := msd(0, 0)
	if zero <= 0 {
		return 1
	}
	best := zero
	for dy := -panMaxShiftY; dy <= panMaxShiftY; dy++ {
		for dx := -panMaxShiftX; dx <= panMaxShiftX; dx++ {
			if v, ok := msd(dx, dy); ok && v < best {
				best = v
			}
		}
	}
	return math.Sqrt(best / zero)
}

// thumbFlowRho returns sqrt(residual after the best first-order global
// translation / uncompensated residual) for the thumbnail step b → a: the
// least-squares solution of a − b ≈ −(∂b/∂x)·dx − (∂b/∂y)·dy over the
// interior cells (central differences), i.e. a single Lucas–Kanade step.
func thumbFlowRho(a, b []float32, c int) float64 {
	var gxx, gxy, gyy, gxd, gyd, dd float64
	for y := 1; y < ThumbH-1; y++ {
		for x := 1; x < ThumbW-1; x++ {
			i := (y*ThumbW + x) * c
			for k := 0; k < c; k++ {
				gx := 0.5 * (float64(b[i+c+k]) - float64(b[i-c+k]))
				gy := 0.5 * (float64(b[i+ThumbW*c+k]) - float64(b[i-ThumbW*c+k]))
				d := float64(a[i+k]) - float64(b[i+k])
				gxx += gx * gx
				gxy += gx * gy
				gyy += gy * gy
				gxd += gx * d
				gyd += gy * d
				dd += d * d
			}
		}
	}
	det := gxx*gyy - gxy*gxy
	if dd <= 0 || det <= 1e-9*gxx*gyy || det <= 0 {
		return 1
	}
	sx := (gxd*gyy - gyd*gxy) / det
	sy := (gyd*gxx - gxd*gxy) / det
	res := dd - sx*gxd - sy*gyd
	if res < 0 {
		res = 0
	}
	return math.Sqrt(res / dd)
}

// detectShortTransitions returns the hard cuts, cut bursts and linear
// (dissolve/fade) transitions of m, sorted by start. Cuts need only the lag-1
// column; bursts and linear transitions also need thumbnails.
func (cfg LookaheadAnalyzer) detectShortTransitions(m *CostMatrix) []SceneTransition {
	s := newShortScan(m)
	if s == nil {
		return nil
	}
	cuts := s.findCuts()
	out := append([]SceneTransition(nil), cuts...)
	if s.th != nil {
		maxLen := cfg.DissolveMaxLen
		if maxLen < linDefaultMax {
			maxLen = linDefaultMax
		}
		if maxLen > linMaxSearch {
			maxLen = linMaxSearch
		}
		for _, t := range s.findLinear(cuts, maxLen) {
			dup := false
			for _, c := range cuts {
				if absInt((t.StartFrame+1)-c.blendStart()) <= 1 {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, t)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartFrame < out[j].StartFrame })
	return out
}

// blendStart is the first frame that differs from the old scene: the cut
// frame of a hard cut, StartFrame+1 of a span.
func (t SceneTransition) blendStart() int {
	if t.Type == TransitionHardCut {
		return t.StartFrame
	}
	return t.StartFrame + 1
}

// findCuts applies the spike rules to r1.
//
// A hard cut is one frame the previous frame cannot predict, between frames
// that predict each other: an isolated r1 spike. "Isolated" is measured
// against the neighbours' own level, so the rule holds on calm and on busy
// content alike, and periodic low spikes (e.g. keyframe quality pulses) are
// rejected by the minimum level.
//
// Variants:
//   - two-frame cut: one mixed frame between the scenes (frame-rate
//     converted or field-blended sources); the spike is two frames wide.
//   - one-sided cut: the new scene is in fast motion, so the frames after
//     the cut are not calm. The spike must then be near-saturated, isolated
//     against the old scene, and coincide with an isolated thumbnail jump.
//   - burst: a cut into rapidly changing material (flash frames, a graphic
//     stinger) that ends with a cut out of it; reported as one span.
func (s *shortScan) findCuts() []SceneTransition {
	const r = cutIsoRadius
	var out []SceneTransition
	n := s.n
	last := -10
	var onsets, offsets []int
	for j := r + 1; j < n-r-1; j++ {
		pre := maxOf(s.r1[j-r : j])
		post := maxOf(s.r1[j+1 : j+1+r])
		v := s.r1[j]
		if v >= cutOnsetRatio {
			if v-pre >= cutMinIso {
				onsets = append(onsets, j)
			}
			if v-post >= cutMinIso {
				offsets = append(offsets, j)
			}
		}
		if j-last <= 1 {
			continue
		}
		iso := v - math.Max(pre, post)
		switch {
		case v >= cutMinRatio && iso >= cutMinIso:
			out = append(out, SceneTransition{Type: TransitionHardCut, StartFrame: j, EndFrame: j, Score: iso})
			last = j
		case s.sp != nil && v >= cutOnsetRatio && v-pre >= cutMinIso &&
			s.sp[j]/(math.Max(maxOf(s.sp[j-r:j]), maxOf(s.sp[j+1:j+1+r]))+0.5) >= cutThumbIso:
			out = append(out, SceneTransition{Type: TransitionHardCut, StartFrame: j, EndFrame: j, Score: v - pre})
			last = j
		case v >= cutMinRatio && j+r+2 <= n:
			v2 := math.Min(v, s.r1[j+1])
			iso2 := v2 - math.Max(pre, maxOf(s.r1[j+2:j+2+r]))
			if iso2 >= cutMinIso {
				// One mixed frame j between the scenes.
				out = append(out, SceneTransition{Type: TransitionDissolve, StartFrame: j - 1, EndFrame: j + 1, Score: iso2})
				last = j + 1
			}
		}
	}

	// Bursts: an onset spike not already reported, closed by the last offset
	// spike within burstMax frames; neither end may be camera motion.
	if s.th != nil {
		const burstMax = 15
		have := map[int]bool{}
		for _, t := range out {
			have[t.blendStart()] = true
		}
		for _, on := range onsets {
			if have[on] {
				continue
			}
			end := -1
			for _, off := range offsets {
				if off > on && off <= on+burstMax {
					end = off
				}
			}
			if end < 0 || !s.notFastPan(on) || !s.notFastPan(end) {
				continue
			}
			covered := false
			for _, t := range out {
				if t.blendStart() <= on && on <= t.EndFrame {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			out = append(out, SceneTransition{Type: TransitionDissolve, StartFrame: on - 1, EndFrame: end, Score: s.r1[on]})
			have[on] = true
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].StartFrame < out[j].StartFrame })
	}
	return out
}

// candidateRegions returns padded, merged frame ranges in which r1 rises
// above its local floor — the only places a gradual transition is searched.
// The floor is the lower quartile of the 2-frame upper envelope over a wide
// window (the envelope keeps repeated-frame cadences from halving it); single
// low frames inside a run are bridged for the same reason.
func (s *shortScan) candidateRegions() [][2]int {
	n := s.n
	u := make([]float64, n)
	for j := 0; j < n; j++ {
		u[j] = s.r1[j]
		if j > 0 && s.r1[j-1] > u[j] {
			u[j] = s.r1[j-1]
		}
	}
	el := make([]bool, n)
	win := make([]float64, 0, 2*candFloorRadius+1)
	for j := 0; j < n; j++ {
		lo, hi := j-candFloorRadius, j+candFloorRadius+1
		if lo < 0 {
			lo = 0
		}
		if hi > n {
			hi = n
		}
		win = append(win[:0], u[lo:hi]...)
		sort.Float64s(win)
		el[j] = s.r1[j] >= win[len(win)/4]+candDelta
	}
	var regs [][2]int
	for j := 1; j < n; {
		if !el[j] {
			j++
			continue
		}
		a, b := j, j
		k := j + 1
		for k < n && (el[k] || (k+1 < n && el[k+1])) {
			if el[k] {
				b = k
			}
			k++
		}
		lo, hi := a-1-candPad, b+candPad
		if lo < 1 {
			lo = 1
		}
		if hi > n-2 {
			hi = n - 2
		}
		if len(regs) > 0 && lo <= regs[len(regs)-1][1] {
			if hi > regs[len(regs)-1][1] {
				regs[len(regs)-1][1] = hi
			}
		} else if hi > lo {
			regs = append(regs, [2]int{lo, hi})
		}
		j = k + 1
	}
	return regs
}

// linearHyp is one accepted blend hypothesis between clean frames a and b.
type linearHyp struct {
	a, b   int
	s, e   int // first blended frame, first clean new-scene frame
	key    float64
	travel float64
}

// findLinear searches every candidate region for frame pairs (A, B) between
// which the thumbnails travel in a straight line: every intermediate frame M
// satisfies th[M] ≈ (1−α)·th[A] + α·th[B] with α rising from 0 to 1. That is
// exactly what a pixel-wise blend does to any linear function of the frame,
// and what camera or subject motion does not do once it is fast enough to
// defeat motion estimation (which is what made the region a candidate).
//
// Gates, all relative to the hypothesis's own neighbourhood:
//   - travel: the endpoints differ (rms thumbnail distance);
//   - speed gain: the thumbnail moves faster inside than on the flanks;
//   - ratio gain: prediction is worse inside than on the flanks;
//   - linearity: max off-line residual ≤ linMaxResid of the travel, at least
//     linMinMids genuinely intermediate frames, α non-decreasing;
//   - coverage: the change is spread over the frame (an overlay graphic
//     fading in is a perfect blend of a small area — not a scene change);
//   - not a pan: the in-transition steps are not global translations.
//
// The best hypothesis of a region (longest travel, lowest residual) is kept
// and the remainder of the region on each side is searched again.
func (s *shortScan) findLinear(cuts []SceneTransition, maxLen int) []SceneTransition {
	var cutFrames []int
	for _, c := range cuts {
		cutFrames = append(cutFrames, c.blendStart())
		if c.Type != TransitionHardCut {
			cutFrames = append(cutFrames, c.EndFrame)
		}
	}
	sort.Ints(cutFrames)
	var out []SceneTransition
	var search func(lo, hi int)
	search = func(lo, hi int) {
		if hi-lo < 2 {
			return
		}
		h, ok := s.bestLinear(lo, hi, maxLen, cutFrames)
		if !ok {
			return
		}
		out = append(out, SceneTransition{
			Type:       TransitionDissolve,
			StartFrame: h.s - 1,
			EndFrame:   h.e,
			Score:      h.key / (h.travel + 1e-9), // 1 − residual
		})
		search(lo, h.a)
		search(h.b, hi)
	}
	for _, r := range s.candidateRegions() {
		search(r[0], r[1])
	}
	return out
}

// bestLinear returns the best hypothesis in [lo, hi]: the longest travel
// with the lowest residual among those passing every gate.
func (s *shortScan) bestLinear(lo, hi, maxLen int, cutFrames []int) (linearHyp, bool) {
	var best linearHyp
	found := false
	dims := len(s.th[0])
	var flankR, flankS, inR []float64
	var alpha []float64
	d := make([]float64, dims)
	for a := lo; a < hi; a++ {
		// The first cut after a bounds every hypothesis starting at a.
		bMax := hi
		if a+maxLen < bMax {
			bMax = a + maxLen
		}
		ci := sort.SearchInts(cutFrames, a+1)
		if ci < len(cutFrames) && cutFrames[ci]-1 < bMax {
			bMax = cutFrames[ci] - 1
		}
		for b := a + 2; b <= bMax; b++ {
			ta, tb := s.th[a], s.th[b]
			var dn float64
			for i := range d {
				d[i] = float64(tb[i]) - float64(ta[i])
				dn += d[i] * d[i]
			}
			travel := math.Sqrt(dn / float64(dims))
			if travel < linMinTravel {
				continue
			}

			// Flank contrasts.
			fl0, fl1 := a-linFlank, b+1+linFlank
			if fl0 < 0 {
				fl0 = 0
			}
			if fl1 > s.n {
				fl1 = s.n
			}
			flankS = append(flankS[:0], s.sp[fl0:a+1]...)
			flankS = append(flankS, s.sp[b+1:fl1]...)
			speedGain := travel / float64(b-a) / math.Max(medianOf(flankS), 0.3)
			if speedGain < linMinSpeedGain {
				continue
			}
			flankR = append(flankR[:0], s.r1[fl0:a]...)
			flankR = append(flankR, s.r1[b+1:fl1]...)
			inR = append(inR[:0], s.r1[a+1:b+1]...)
			if quantileOf(inR, 0.75)-medianOf(flankR) < linMinRatioGain {
				continue
			}

			// Linearity.
			alpha = alpha[:0]
			maxRes := 0.0
			okLin := true
			for mi := a + 1; mi < b; mi++ {
				tm := s.th[mi]
				var dot, xn float64
				for i := range d {
					x := float64(tm[i]) - float64(ta[i])
					dot += x * d[i]
					xn += x * x
				}
				al := dot / dn
				res := xn - al*al*dn
				if res < 0 {
					res = 0
				}
				res = math.Sqrt(res / dn)
				if res > linMaxResid {
					okLin = false
					break
				}
				if res > maxRes {
					maxRes = res
				}
				if len(alpha) > 0 && al < alpha[len(alpha)-1]-linMaxBackstep {
					okLin = false
					break
				}
				alpha = append(alpha, al)
			}
			if !okLin {
				continue
			}
			mids := 0
			for _, al := range alpha {
				if al > linMidLo && al < 1-linMidLo {
					mids++
				}
			}
			if mids < linMinMids {
				continue
			}
			key := travel * (1 - maxRes)
			if found && key <= best.key {
				continue
			}

			// Coverage: participation ratio of the per-cell change.
			var s1, s2 float64
			for c := 0; c < dims; c += s.tc {
				var q float64
				for k := 0; k < s.tc; k++ {
					q += d[c+k] * d[c+k]
				}
				s1 += math.Sqrt(q)
				s2 += q
			}
			if s2 <= 0 || s1*s1/(float64(dims/s.tc)*s2) < linMinCoverage {
				continue
			}

			// Not a pan: median over the significant steps.
			spMax := maxOf(s.sp[a+1 : b+1])
			pan, steps := 0, 0
			for j := a + 1; j <= b; j++ {
				if s.sp[j] >= 0.25*spMax {
					steps++
					if !s.notPan(j) {
						pan++
					}
				}
			}
			if 2*pan > steps {
				continue
			}

			h := linearHyp{a: a, b: b, s: b, e: b, key: key, travel: travel}
			for i, al := range alpha {
				if al > 0.04 {
					h.s = a + 1 + i
					break
				}
			}
			for i, al := range alpha {
				if al >= 0.96 {
					h.e = a + 1 + i
					break
				}
			}
			if h.s > h.e {
				h.s = h.e
			}
			best, found = h, true
		}
	}
	return best, found
}

// mergeShortAndLegacy combines the short-transition detections with the
// legacy analysis results. The short detector owns hard cuts (the legacy
// score surface cannot separate cuts from motion on real footage); legacy
// dissolves and fades are kept only where no short detection overlaps them.
// Where one does, the short detection stands; a legacy fade passes on its
// type, and a legacy dissolve (the staged plateau measurement, refined to
// frame accuracy) passes on its bounds when they agree with the short ones
// to within a quarter of the blend.
func mergeShortAndLegacy(short, legacy []SceneTransition) []SceneTransition {
	out := append([]SceneTransition(nil), short...)
	for _, t := range legacy {
		if t.Type == TransitionHardCut {
			continue
		}
		over := false
		for i := range out {
			o := &out[i]
			if t.StartFrame <= o.EndFrame+2 && t.EndFrame >= o.StartFrame-2 {
				over = true
				if o.Type != TransitionDissolve || t.EndFrame <= t.StartFrame {
					continue
				}
				switch t.Type {
				case TransitionFadeIn, TransitionFadeOut:
					o.Type = t.Type
				case TransitionDissolve:
					tol := (o.EndFrame - o.StartFrame) / 4
					if tol < 2 {
						tol = 2
					}
					if absInt(t.StartFrame-o.StartFrame) <= tol && absInt(t.EndFrame-o.EndFrame) <= tol {
						o.StartFrame, o.EndFrame = t.StartFrame, t.EndFrame
					}
				}
			}
		}
		if !over {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartFrame < out[j].StartFrame })
	return out
}

// enforceMinSceneLen merges transitions separated by fewer than minLen
// frames into one span from the first one's start to the last one's end: a
// "scene" shorter than the minimum is transition material (flash frames, a
// graphic between two cuts), not a scene. The input must be sorted by start.
func enforceMinSceneLen(ts []SceneTransition, minLen int) []SceneTransition {
	if minLen <= 1 || len(ts) < 2 {
		return ts
	}
	out := ts[:1]
	for _, t := range ts[1:] {
		p := &out[len(out)-1]
		if t.blendStart()-p.EndFrame >= minLen {
			out = append(out, t)
			continue
		}
		start := p.StartFrame
		if p.Type == TransitionHardCut {
			start = p.StartFrame - 1
			if start < 0 {
				start = 0
			}
		}
		end := p.EndFrame
		if t.EndFrame > end {
			end = t.EndFrame
		}
		typ := TransitionDissolve
		if p.Type == TransitionFadeIn || p.Type == TransitionFadeOut {
			typ = p.Type
		} else if t.Type == TransitionFadeIn || t.Type == TransitionFadeOut {
			typ = t.Type
		}
		score := p.Score
		if t.Score > score {
			score = t.Score
		}
		*p = SceneTransition{Type: typ, StartFrame: start, EndFrame: end, Score: score}
	}
	return out
}
