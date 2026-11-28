// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import (
	"math"
	"sort"
)

// Short-transition detection: hard cuts, cut bursts, short-to-medium
// dissolves/fades and wipes, found from two per-frame signals that need no
// retained frames:
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
// gradual transition must pass a thumbnail test: linearity for a blend, or
// per-cell path efficiency for a wipe (see bestWipe). See
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
	linMaxEdge      = 0.04 // largest mix margin separating clean from blended
	linMinEdge      = 0.01 // smallest (long blends; see blendBounds)
	linEndProbe     = 3    // frames beyond each endpoint checked for continued travel
	linDefaultMax   = 64   // longest blend searched when DissolveMaxLen unset
	linMaxSearch    = 240  // hard cap on the searched blend length

	// Wipe hypotheses (see bestWipe).
	wipeMinEta    = 0.68 // per-cell net change / per-cell total variation
	wipeWeakEta   = 0.50 // accepted only next to a transition that stands alone
	wipeAttachGap = 15   // frames between a weak wipe and the transition it joins
	wipeMinTravel = 15.0 // rms thumbnail distance between the endpoints
	wipeMinSteps  = 3    // significant thumbnail steps inside
	wipeEdge      = 0.02 // cumulative-movement fraction marking each bound

	// A frozen picture between two gradual transitions (a full-screen
	// graphic held between its wipe-on and wipe-off) is transition material.
	frozenMaxStep = 0.5 // largest thumbnail step of a frozen picture
	frozenMaxGap  = 64  // longest frozen hold bridged, frames

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
// column; bursts and linear transitions also need thumbnails. maxBlend is the
// longest blend searched, in frames (the caller's DissolveMaxLen as
// configured; ≤ 0 selects linDefaultMax).
func (cfg LookaheadAnalyzer) detectShortTransitions(m *CostMatrix, maxBlend int) []SceneTransition {
	s := newShortScan(m)
	if s == nil {
		return nil
	}
	cuts := s.findCuts()
	out := append([]SceneTransition(nil), cuts...)
	if s.th != nil {
		maxLen := maxBlend
		if maxLen <= 0 {
			maxLen = linDefaultMax
		}
		if maxLen > linMaxSearch {
			maxLen = linMaxSearch
		}
		// A blend of D frames spans D+1 steps between its clean endpoints.
		maxLen++
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
	return s.bridgeFrozenHolds(attachWeakWipes(out))
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
	for j := r; j < n-r; j++ {
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
func (s *shortScan) candidateRegions(maxLen int) [][2]int {
	n := s.n
	// The floor window must see the flanks of the longest blend searched, or
	// the middle of a long uniform run becomes its own floor and the run
	// splits in two.
	radius := candFloorRadius
	if maxLen > radius {
		radius = maxLen
	}
	u := make([]float64, n)
	for j := 0; j < n; j++ {
		u[j] = s.r1[j]
		if j > 0 && s.r1[j-1] > u[j] {
			u[j] = s.r1[j-1]
		}
	}
	el := make([]bool, n)
	win := make([]float64, 0, 2*radius+1)
	for j := 0; j < n; j++ {
		lo, hi := j-radius, j+radius+1
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
	weak   bool // a wipe below wipeMinEta (see attachWeakWipes)
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
		wipe := !ok
		if !ok {
			// No blend: look for a wipe, then for a weak one (kept only if
			// it turns out to adjoin a transition that stands alone).
			if h, ok = s.bestWipe(lo, hi, maxLen, cutFrames, wipeMinEta); !ok {
				if h, ok = s.bestWipe(lo, hi, maxLen, cutFrames, wipeWeakEta); !ok {
					return
				}
				h.weak = true
			}
		}
		left, right := h.a, h.b
		if h.s-1 < left {
			left = h.s - 1
		}
		if h.e > right {
			right = h.e
		}
		if wipe {
			// After the region is divided, so that a neighbouring piece of
			// the same transition is still searched for in its own frames.
			h.s, h.e = s.settleBounds(h.s, h.e, maxLen, cutFrames)
		}
		out = append(out, SceneTransition{
			Type:       TransitionDissolve,
			StartFrame: h.s - 1,
			EndFrame:   h.e,
			Score:      h.key / (h.travel + 1e-9), // 1 − residual, or η for a wipe
			weak:       h.weak,
		})
		search(lo, left)
		search(right, hi)
	}
	for _, r := range s.candidateRegions(maxLen) {
		search(r[0], r[1])
	}
	return out
}

// speedGain compares how fast the thumbnail moves inside a wipe hypothesis
// with how fast it moves on the flanks. Upper quartiles, not medians or means:
// on repeated-frame content (30 fps in a 60 fps stream, slow-motion replays
// at 2:2 or 3:3) most steps are zero, and the quartile reads the speed of
// the frames that do move on both sides alike — so a slow pan in a replay,
// whose steps are no larger inside the hypothesis than outside, shows no
// gain, while one cut elsewhere in the flank does not hide a real one.
func speedGain(inside, flank []float64) float64 {
	return quantileOf(inside, 0.75) / math.Max(quantileOf(flank, 0.75), 0.3)
}

// oneSidedContrast is the flank test for a transition between a calm shot
// and a shot that is itself in fast motion. The thumbnail of the moving shot
// changes as fast as the transition does, so the pooled speed comparison
// fails even though the transition is plain against the calm shot. Each
// flank is then judged on its own: the thumbnail must move faster inside
// than on at least one flank, and on a flank where it does not, prediction
// must be worse inside than on that flank. That is what separates a
// transition INTO motion from the onset of motion within one shot: motion
// estimation follows a moving shot but not the frames that mix two shots,
// while after an onset prediction stays as bad as it became. Upper quartiles
// throughout, for the reason given at speedGain. inQ is the upper quartile
// of r1 over (a, b]; the flanks are [fl0, a] and (b, fl1).
func (s *shortScan) oneSidedContrast(a, b, fl0, fl1 int, inQ float64) bool {
	if fl0 >= a || b+1 >= fl1 {
		return false
	}
	in := s.sp[a+1 : b+1]
	fastPre := speedGain(in, s.sp[fl0:a+1]) >= linMinSpeedGain
	fastPost := speedGain(in, s.sp[b+1:fl1]) >= linMinSpeedGain
	if !fastPre && !fastPost {
		return false
	}
	if !fastPre && inQ-quantileOf(s.r1[fl0:a], 0.75) < linMinRatioGain {
		return false
	}
	if !fastPost && inQ-quantileOf(s.r1[b+1:fl1], 0.75) < linMinRatioGain {
		return false
	}
	return true
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
			if travel/float64(b-a)/math.Max(medianOf(flankS), 0.3) < linMinSpeedGain {
				continue
			}
			flankR = append(flankR[:0], s.r1[fl0:a]...)
			flankR = append(flankR, s.r1[b+1:fl1]...)
			inR = append(inR[:0], s.r1[a+1:b+1]...)
			flankMed := medianOf(flankR)
			if quantileOf(inR, 0.75)-flankMed < linMinRatioGain {
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

			h := linearHyp{a: a, b: b, key: key, travel: travel}
			h.s, h.e = blendBounds(alpha, a, b)
			h.s, h.e = s.extendAlongLine(a, b, h.s, h.e, d, dn, alpha, flankMed, cutFrames)
			if h.e-h.s > maxLen {
				continue // a piece of a blend longer than the caller allows
			}
			best, found = h, true
		}
	}
	return best, found
}

// bestWipe returns the best wipe hypothesis in [lo, hi].
//
// A wipe replaces the picture area by area: each thumbnail cell changes from
// its old-scene value to its new one once, when the wipe's edge crosses it,
// and then stays. The thumbnails do not travel in a straight line (cells
// switch at different times), but every cell's path is efficient: the
// distance it ends up from where it started is close to the total distance
// it moved. Camera and subject motion are the opposite — cells churn as
// content passes through them and end up little changed for all their
// movement. The statistic is
//
//	η = Σ_cells |th[B] − th[A]| / Σ_cells Σ_steps |th[t] − th[t−1]|
//
// which is 1 for a cut, a dissolve or a clean wipe, and measured at
// 0.7–0.95 on broadcast wipes against 0.1–0.3 on pans and camera moves and
// 0.5–0.65 on close-up action with a moving camera. Hypotheses at or above
// wipeMinEta stand alone. Those between wipeWeakEta and wipeMinEta are
// "weak": an animated graphic's wipe-on often scores there (its own artwork
// moves), and so does close-up action, so a weak wipe is kept only where it
// adjoins a transition that stands alone (attachWeakWipes). The remaining gates are those of a blend (travel,
// flank contrasts, coverage, not a pan), with a higher travel bound because
// the test is less specific than linearity, and a minimum number of steps
// because one- and two-step changes belong to the cut rules.
//
// The bounds are where the thumbnail's movement starts and stops: the first
// frame past wipeEdge of the cumulative movement and the first frame at
// 1 − wipeEdge of it. findLinear then walks them outward (settleBounds).
func (s *shortScan) bestWipe(lo, hi, maxLen int, cutFrames []int, minEta float64) (linearHyp, bool) {
	var best linearHyp
	found := false
	cells := len(s.th[0]) / s.tc
	// Per-cell step norms for the region.
	steps := make([][]float32, hi-lo+1)
	for j := lo + 1; j <= hi; j++ {
		row := make([]float32, cells)
		tp, tj := s.th[j-1], s.th[j]
		for c := 0; c < cells; c++ {
			var q float64
			for k := 0; k < s.tc; k++ {
				d := float64(tj[c*s.tc+k]) - float64(tp[c*s.tc+k])
				q += d * d
			}
			row[c] = float32(math.Sqrt(q))
		}
		steps[j-lo] = row
	}
	tv := make([]float64, cells)
	var flankR, flankS, inR []float64
	for a := lo; a < hi; a++ {
		bMax := hi
		if a+maxLen < bMax {
			bMax = a + maxLen
		}
		ci := sort.SearchInts(cutFrames, a+1)
		if ci < len(cutFrames) && cutFrames[ci]-1 < bMax {
			bMax = cutFrames[ci] - 1
		}
		for c := range tv {
			tv[c] = 0
		}
		for b := a + 1; b <= bMax; b++ {
			for c, v := range steps[b-lo] {
				tv[c] += float64(v)
			}
			if b-a < wipeMinSteps {
				continue
			}
			ta, tb := s.th[a], s.th[b]
			var sumNet, sumNet2, sumTV float64
			for c := 0; c < cells; c++ {
				var q float64
				for k := 0; k < s.tc; k++ {
					d := float64(tb[c*s.tc+k]) - float64(ta[c*s.tc+k])
					q += d * d
				}
				sumNet += math.Sqrt(q)
				sumNet2 += q
				sumTV += tv[c]
			}
			travel := math.Sqrt(sumNet2 / float64(cells*s.tc))
			if travel < wipeMinTravel || sumTV <= 0 {
				continue
			}
			eta := sumNet / sumTV
			if eta < minEta {
				continue
			}
			key := travel * eta
			if found && key <= best.key {
				continue
			}
			if sumNet*sumNet/(float64(cells)*sumNet2) < linMinCoverage {
				continue
			}
			fl0, fl1 := a-linFlank, b+1+linFlank
			if fl0 < 0 {
				fl0 = 0
			}
			if fl1 > s.n {
				fl1 = s.n
			}
			flankS = append(flankS[:0], s.sp[fl0:a+1]...)
			flankS = append(flankS, s.sp[b+1:fl1]...)
			flankR = append(flankR[:0], s.r1[fl0:a]...)
			flankR = append(flankR, s.r1[b+1:fl1]...)
			inR = append(inR[:0], s.r1[a+1:b+1]...)
			inQ := quantileOf(inR, 0.75)
			if inQ-medianOf(flankR) < linMinRatioGain {
				continue
			}
			if speedGain(s.sp[a+1:b+1], flankS) < linMinSpeedGain && !s.oneSidedContrast(a, b, fl0, fl1, inQ) {
				continue
			}
			spMax := maxOf(s.sp[a+1 : b+1])
			pan, sig := 0, 0
			var total float64
			for j := a + 1; j <= b; j++ {
				total += s.sp[j]
				if s.sp[j] >= 0.25*spMax {
					sig++
					if !s.notPan(j) {
						pan++
					}
				}
			}
			if sig < wipeMinSteps || 2*pan > sig {
				continue
			}
			h := linearHyp{a: a, b: b, s: b, e: b, key: key, travel: travel}
			var cum float64
			gotS := false
			for j := a + 1; j <= b; j++ {
				cum += s.sp[j]
				if !gotS && cum > wipeEdge*total {
					h.s, gotS = j, true
				}
				if cum >= (1-wipeEdge)*total {
					h.e = j
					break
				}
			}
			best, found = h, true
		}
	}
	return best, found
}

// settleBounds walks the bounds of a wipe outward to where the picture
// settles.
//
// The hypothesis that wins in bestWipe is the one that captures the change
// most efficiently, and that is usually shorter than the wipe: once most of
// the picture has changed, further frames add movement (the graphic's own
// animation, the new scene's motion) faster than they add net change, so η
// falls and a shorter pair scores higher. The frames left out can still move
// a large part of the picture — a full-screen graphic shrinking to nothing.
//
// Each bound therefore moves outward while the thumbnail is still settling:
// some frame within linEndProbe (several, because repeated frames do not
// move) has a step that is at least wipeEdge of the wipe's movement so far —
// the same share of the movement that the bounds discard inside the
// hypothesis — and at least linMinSpeedGain times the upper-quartile step of
// the linFlank frames beyond it. The second condition stops the walk where
// the movement is no longer falling away but has reached the level of the
// scene itself. A walk never crosses a cut and never makes the wipe longer
// than maxLen.
func (s *shortScan) settleBounds(start, end, maxLen int, cutFrames []int) (int, int) {
	lo, hi := 1, s.n-1
	if ci := sort.SearchInts(cutFrames, start); ci > 0 {
		lo = cutFrames[ci-1] + 1
	}
	if ci := sort.SearchInts(cutFrames, end+1); ci < len(cutFrames) {
		hi = cutFrames[ci] - 1
	}
	var total float64
	for j := start; j <= end; j++ {
		total += s.sp[j]
	}
	// settling reports whether the step into frame j stands out from the
	// frames beyond it, flank[0] … flank[len−1].
	settling := func(j int, flank []float64) bool {
		return len(flank) > 0 && s.sp[j] >= wipeEdge*total &&
			s.sp[j] >= linMinSpeedGain*quantileOf(flank, 0.75)
	}
	for moved := true; moved; {
		moved = false
		for k := 1; k <= linEndProbe && end+k <= hi && end+k-start < maxLen; k++ {
			j := end + k
			f1 := j + 1 + linFlank
			if f1 > s.n {
				f1 = s.n
			}
			if settling(j, s.sp[j+1:f1]) {
				for q := end + 1; q <= j; q++ {
					total += s.sp[q]
				}
				end, moved = j, true
				break
			}
		}
	}
	for moved := true; moved; {
		moved = false
		for k := 1; k <= linEndProbe && start-k >= lo && end-(start-k) < maxLen; k++ {
			j := start - k
			f0 := j - linFlank
			if f0 < 0 {
				f0 = 0
			}
			if settling(j, s.sp[f0:j]) {
				for q := j; q < start; q++ {
					total += s.sp[q]
				}
				start, moved = j, true
				break
			}
		}
	}
	return start, end
}

// attachWeakWipes resolves the weak wipes in ts (sorted by start): one that
// lies within wipeAttachGap frames of a gradual transition that stands alone
// is merged into it — the animated lead-in or lead-out of a stinger — and
// the merged span can in turn pick up the next weak piece. The rest are
// dropped. Other weak wipes may lie between a weak wipe and the transition
// it joins (their settled bounds can overlap); a hard cut may not.
func attachWeakWipes(ts []SceneTransition) []SceneTransition {
	for changed := true; changed; {
		changed = false
		for i := range ts {
			if !ts[i].weak {
				continue
			}
			for _, dir := range []int{-1, 1} {
				// The nearest transition on this side that is not itself weak.
				j := i + dir
				for j >= 0 && j < len(ts) && ts[j].weak {
					j += dir
				}
				if j < 0 || j >= len(ts) || ts[j].Type == TransitionHardCut {
					continue
				}
				gap := ts[i].StartFrame - ts[j].EndFrame
				if g := ts[j].StartFrame - ts[i].EndFrame; g > gap {
					gap = g
				}
				if gap > wipeAttachGap {
					continue
				}
				if ts[i].StartFrame < ts[j].StartFrame {
					ts[j].StartFrame = ts[i].StartFrame
				}
				if ts[i].EndFrame > ts[j].EndFrame {
					ts[j].EndFrame = ts[i].EndFrame
				}
				ts = append(ts[:i], ts[i+1:]...)
				changed = true
				break
			}
			if changed {
				break
			}
		}
	}
	out := ts[:0]
	for _, t := range ts {
		if !t.weak {
			out = append(out, t)
		}
	}
	return out
}

// bridgeFrozenHolds merges two consecutive gradual transitions separated by
// a frozen picture — a full-screen graphic held between its wipe-on and its
// wipe-off — into one span. A camera picture is never frozen: sensor noise
// alone moves the thumbnail. The 90th percentile of the steps is used so
// that the faint first and last frames of the wipes themselves, which fall
// outside their measured bounds, do not break the hold. ts must be sorted by
// start.
func (s *shortScan) bridgeFrozenHolds(ts []SceneTransition) []SceneTransition {
	if s.sp == nil || len(ts) < 2 {
		return ts
	}
	out := ts[:1]
	for _, t := range ts[1:] {
		p := &out[len(out)-1]
		gapLo, gapHi := p.EndFrame+1, t.StartFrame
		if p.Type == TransitionHardCut || t.Type == TransitionHardCut ||
			gapHi-gapLo < 0 || gapHi-gapLo+1 > frozenMaxGap ||
			quantileOf(s.sp[gapLo:gapHi+1], 0.9) >= frozenMaxStep {
			out = append(out, t)
			continue
		}
		p.EndFrame = t.EndFrame
		if t.Score > p.Score {
			p.Score = t.Score
		}
	}
	return out
}

// extendAlongLine follows a hypothesis's travel beyond its endpoints.
//
// When both shots are moving, only part of a blend is straight enough to
// pass the linearity bound, and the accepted pair (a, b) is a piece of the
// transition: the thumbnail keeps advancing along the same direction before
// a or after b. The projection onto that direction still marks where the
// travel starts and stops, so each end is walked outward while some frame
// within linEndProbe advances by more than half the blend's own step
// (several frames, because repeated frames do not advance) and prediction
// failed on the way. The end then moves to the last frame of that probe
// which advanced by itself, not to the end of the probe: a probe can reach a
// frame of the neighbouring shot's own motion by way of a real blend step,
// and that frame only rode along. An end that does not move keeps its
// blendBounds value; a walk never crosses a cut.
func (s *shortScan) extendAlongLine(a, b, start, end int, d []float64, dn float64, alpha []float64, flankRatio float64, cutFrames []int) (int, int) {
	var incs []float64
	prev := 0.0
	for _, al := range alpha {
		if dl := al - prev; dl > 1e-3 {
			incs = append(incs, dl)
		}
		prev = al
	}
	if dl := 1 - prev; dl > 1e-3 {
		incs = append(incs, dl)
	}
	if len(incs) == 0 {
		return start, end
	}
	lim := 0.5 * medianOf(incs)
	along := func(from, to int) float64 {
		var dot float64
		tf, tt := s.th[from], s.th[to]
		for i := range d {
			dot += (float64(tt[i]) - float64(tf[i])) * d[i]
		}
		return dot / dn
	}
	// A step counts only if prediction also failed on one of its frames:
	// ordinary motion in the flanking shot projects onto the line too.
	failed := func(from, to int) bool {
		for q := from + 1; q <= to; q++ {
			if s.r1[q] >= flankRatio+0.5*linMinRatioGain {
				return true
			}
		}
		return false
	}
	lo, hi := 0, s.n-1
	if ci := sort.SearchInts(cutFrames, a+1); ci > 0 {
		lo = cutFrames[ci-1]
	}
	if ci := sort.SearchInts(cutFrames, b+1); ci < len(cutFrames) {
		hi = cutFrames[ci] - 1
	}
	f := b
	for moved := true; moved; {
		moved = false
		for k := 1; k <= linEndProbe && f+k <= hi; k++ {
			if along(f, f+k) > lim && failed(f, f+k) {
				// Land on the last frame of the probe that advanced by
				// itself; the frames beyond it only rode along.
				q := f + k
				for q > f+1 && along(q-1, q) <= lim {
					q--
				}
				if along(q-1, q) <= lim {
					q = f + k
				}
				f = q
				moved = true
				break
			}
		}
	}
	if f > b {
		end = f
	}
	g := a
	for moved := true; moved; {
		moved = false
		for k := 1; k <= linEndProbe && g-k >= lo; k++ {
			if along(g-k, g) > lim && failed(g-k, g) {
				q := g - k + 1
				for q < g && along(q-1, q) <= lim {
					q++
				}
				if along(q-1, q) <= lim {
					q = g - k + 1
				}
				g = q - 1
				moved = true
				break
			}
		}
	}
	if g < a {
		start = g + 1
	} else {
		// The first blended frame is one the previous frame failed to
		// predict. When the endpoints are close together, drift in the old
		// shot can lift the mix past the margin a frame or two early; snap
		// forward to the first failed frame if one follows immediately.
		for q := start; q <= start+linEndProbe-1 && q < end; q++ {
			if failed(q-1, q) {
				start = q
				break
			}
		}
	}
	return start, end
}

// blendBounds returns the first blended frame and the first clean new-scene
// frame of a hypothesis from the mix α of its interior frames a+1 … b−1.
//
// A frame is blended when its mix is distinguishable from 0 (still the old
// scene) and from 1 (already the new one). "Distinguishable" is half the
// blend's own step: a D-frame linear blend advances the mix by 1/(D+1) per
// step, so a fixed margin would call the first frames of a long blend clean
// and its last frames finished. The step is the median positive increment
// (repeated frames contribute zero increments and are ignored), and the
// margin is bounded to [linMinEdge, linMaxEdge]: the upper bound is the
// short-blend calibration, the lower one keeps estimation noise in α on
// moving footage from being read as a blend step.
func blendBounds(alpha []float64, a, b int) (start, end int) {
	var incs []float64
	prev := 0.0
	for _, al := range alpha {
		if d := al - prev; d > 1e-3 {
			incs = append(incs, d)
		}
		prev = al
	}
	if d := 1 - prev; d > 1e-3 {
		incs = append(incs, d)
	}
	eps := linMaxEdge
	if len(incs) > 0 {
		if e := 0.5 * medianOf(incs); e < eps {
			eps = e
		}
	}
	if eps < linMinEdge {
		eps = linMinEdge
	}
	start, end = b, b
	for i, al := range alpha {
		if al > eps {
			start = a + 1 + i
			break
		}
	}
	for i, al := range alpha {
		if al >= 1-eps {
			end = a + 1 + i
			break
		}
	}
	if start > end {
		start = end
	}
	return start, end
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
