// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Full-resolution edge measurement (phase 1: measure + log only).
//
// For each detected dissolve, a handful of frames around the refined edges
// are re-measured with H.264-style inter prediction at NATIVE resolution
// (see internal/fullres.go — 4× the samples and full high-frequency detail
// make low-alpha blend frames visible that the half-res lookahead cannot
// see). Two complementary geometries per edge, both established by the
// lowres pipeline:
//
//   - narrow lags k ∈ {1,2,3}: low within-scene floors (short-range
//     prediction works through motion), each pair straddles a small alpha
//     step — the first cost change is at exactly S, the reverse return to
//     floor at exactly E;
//   - a wide window kW > D: the reference sits in pure old-scene content
//     and the predicted frame slides across the edge, so the interpretation
//     is unambiguous — but the floor is kW-frames-apart within-scene
//     prediction, which rises with kW on moving content. Wide-mode
//     reference frames form a sparse island (the same 13 frames shifted by
//     kW), so a large kW costs no extra decode.
//
// Frames come from a FrameProvider (windowed re-decode — full-res retention
// is impossible at ~9 MB/frame). Phase 1 only logs the measurements into
// the matrix (and therefore the cost-matrix CSV) for offline calibration;
// no edges are adopted.
package lookahead

import (
	"fmt"
	"sort"

	imgmath "github.com/MediaMolder/MediaMolder/lookahead/internal"
)

// FrameProvider supplies full-resolution luma planes for individual frames,
// typically by re-decoding a small window of the source. Implementations
// must verify alignment (the caller trusts frame indices absolutely);
// returning an error skips full-res measurement for the requesting dissolve.
type FrameProvider interface {
	FullresLuma(frameIdx int) (luma []byte, w, h, stride int, err error)
}

const (
	// fullresEdgeWin: frames measured on each side of a provisional edge.
	fullresEdgeWin = 6
	// fullresWideMargin: the wide-mode distance is the duration estimate
	// plus this margin, keeping the reference in pure old-scene (forward)
	// or pure new-scene (reverse) content across the whole window.
	fullresWideMargin = 8
)

var fullresNarrowLags = [...]int{1, 2, 3}

// ensureFrCol returns the FrRatio column index for forward distance k,
// appending a zero-filled column the first time k is seen (the RevRatio
// pattern).
func (m *CostMatrix) ensureFrCol(k int) int {
	for i, kk := range m.FrLags {
		if kk == k {
			return i
		}
	}
	m.FrLags = append(m.FrLags, k)
	if m.FrRatio == nil {
		m.FrRatio = make([][]float32, m.N)
	}
	for j := range m.FrRatio {
		m.FrRatio[j] = append(m.FrRatio[j], 0)
	}
	return len(m.FrLags) - 1
}

// ensureFrRevCol is ensureFrCol for the reverse direction.
func (m *CostMatrix) ensureFrRevCol(k int) int {
	for i, kk := range m.FrRevLags {
		if kk == k {
			return i
		}
	}
	m.FrRevLags = append(m.FrRevLags, k)
	if m.FrRevRatio == nil {
		m.FrRevRatio = make([][]float32, m.N)
	}
	for j := range m.FrRevRatio {
		m.FrRevRatio[j] = append(m.FrRevRatio[j], 0)
	}
	return len(m.FrRevLags) - 1
}

const (
	// fullresMaxAdoptD: full-resolution measurement only refines the END of
	// blends at or below this duration. Above it, narrow lags see only noise
	// (the per-frame blend fraction ~1/(D+1) is invisible even at native
	// resolution) and the wide window is a flat motion-decorrelation floor
	// (predicting across the whole blend decorrelates regardless of content).
	// Long-blend edges are owned by the energy dip and reverse foot. Measured
	// on validation content: the full-res reverse end-foot beats the lowres
	// stack on 15- and 22-frame blends and is a no-op below; D ≥ 30 produces
	// no usable edge.
	fullresMaxAdoptD = 25
	// fullresEndMinContrast: a full-res reverse lag's plateau→floor contrast
	// must clear this to vote (same scale as the lowres edge gates).
	fullresEndMinContrast = 0.12
)

// fullresEndFoot derives a refined END from the full-resolution reverse
// narrow-lag ratios already stored in the matrix. The reverse ratio at a
// short distance is elevated while the predicted frame still carries
// old-scene content and returns to the new-scene floor at exactly E; per
// lag the foot is the first frame at/below 20 % of the plateau height, and
// the per-lag feet are combined by median (robust to a single lag's
// floor-scan outlier). Returns (E, true) when at least two narrow lags
// agree with adequate contrast; otherwise (E0, false).
func fullresEndFoot(m *CostMatrix, S, E0 int) (int, bool) {
	rev := func(j, k int) (float64, bool) {
		for i, kk := range m.FrRevLags {
			if kk != k {
				continue
			}
			if j < 0 || j >= len(m.FrRevRatio) || i >= len(m.FrRevRatio[j]) {
				return 0, false
			}
			v := float64(m.FrRevRatio[j][i])
			if v <= 0 {
				return 0, false
			}
			return v, true
		}
		return 0, false
	}
	var feet []int
	for _, k := range fullresNarrowLags {
		var fl []float64
		for j := E0 + 1; j <= E0+4; j++ {
			if v, ok := rev(j, k); ok {
				fl = append(fl, v)
			}
		}
		if len(fl) < 3 {
			continue
		}
		floor := 0.0
		for _, v := range fl {
			floor += v
		}
		floor /= float64(len(fl))
		plateau := 0.0
		for j := E0 - 5; j < E0; j++ {
			if v, ok := rev(j, k); ok && v > plateau {
				plateau = v
			}
		}
		if plateau-floor < fullresEndMinContrast {
			continue
		}
		vb := floor + 0.2*(plateau-floor)
		for j := E0 - 5; j <= E0+5; j++ {
			if v, ok := rev(j, k); ok && v <= vb {
				feet = append(feet, j)
				break
			}
		}
	}
	if len(feet) < 2 {
		return E0, false
	}
	sort.Ints(feet)
	return feet[len(feet)/2], true
}

// fullresEdgeMeasure measures the full-resolution forward and reverse cost
// ratios around a dissolve's edges, stores them in the matrix (so they
// appear in the cost-matrix CSV), and — for short/mid blends — returns a
// refined END from the reverse measurement. The start is never adopted: the
// forward narrow ramp still loses the blend's low-α head even at full
// resolution, so it cannot beat the plateau-consensus start. Returns
// (refinedEnd, nPairs, error); refinedEnd == endFrame when nothing was
// adopted (long blend, weak contrast, or provider failure).
func fullresEdgeMeasure(m *CostMatrix, provider FrameProvider, startFrame, endFrame int) (int, int, error) {
	S := startFrame + 1
	E := endFrame
	D := E - S
	if provider == nil || D < 1 {
		return endFrame, 0, nil
	}
	kWide := D + fullresWideMargin
	if kWide > imgmath.MaxLag {
		kWide = 0 // beyond cache slots; narrow-only for very long blends
	}

	// Measurement plan: (predicted j, distance k, reverse?) tuples.
	type meas struct {
		j, k int
		rev  bool
	}
	var plan []meas
	addFwd := func(j, k int) {
		if j-k >= 0 && j < m.N {
			plan = append(plan, meas{j, k, false})
		}
	}
	addRev := func(j, k int) {
		if j >= 0 && j+k < m.N {
			plan = append(plan, meas{j, k, true})
		}
	}
	for j := S - fullresEdgeWin; j <= S+fullresEdgeWin; j++ {
		for _, k := range fullresNarrowLags {
			addFwd(j, k)
		}
		if kWide > 0 {
			addFwd(j, kWide)
		}
	}
	for j := E - fullresEdgeWin; j <= E+fullresEdgeWin; j++ {
		for _, k := range fullresNarrowLags {
			addRev(j, k)
		}
		if kWide > 0 {
			addRev(j, kWide)
		}
	}
	if len(plan) == 0 {
		return endFrame, 0, nil
	}

	// Fetch every needed frame in ascending order (lets a re-decoding
	// provider stream forward instead of seeking per frame), then build
	// the full-res measurement frames once each.
	need := map[int]bool{}
	for _, p := range plan {
		need[p.j] = true
		if p.rev {
			need[p.j+p.k] = true
		} else {
			need[p.j-p.k] = true
		}
	}
	idxs := make([]int, 0, len(need))
	for j := range need {
		idxs = append(idxs, j)
	}
	sort.Ints(idxs)
	frames := make(map[int]*imgmath.LowresFrame, len(idxs))
	for _, j := range idxs {
		luma, w, h, stride, err := provider.FullresLuma(j)
		if err != nil {
			return endFrame, 0, fmt.Errorf("frame %d: %w", j, err)
		}
		fr, err := imgmath.InitFullres(luma, w, h, stride)
		if err != nil {
			return endFrame, 0, fmt.Errorf("frame %d: %w", j, err)
		}
		frames[j] = fr
	}

	for _, p := range plan {
		fenc := frames[p.j]
		var ref *imgmath.LowresFrame
		if p.rev {
			ref = frames[p.j+p.k]
		} else {
			ref = frames[p.j-p.k]
		}
		var inter, intra int32
		if p.rev {
			inter, intra = imgmath.LowresFrameCostReverse(fenc, ref, p.k-1)
		} else {
			inter, intra = imgmath.LowresFrameCost(fenc, ref, p.k-1)
		}
		if intra <= 0 {
			continue
		}
		r := float64(inter) / float64(intra)
		if r > 1 {
			r = 1
		}
		if p.rev {
			col := m.ensureFrRevCol(p.k)
			m.FrRevRatio[p.j][col] = float32(r)
		} else {
			col := m.ensureFrCol(p.k)
			m.FrRatio[p.j][col] = float32(r)
		}
	}

	// Adopt a refined END for short/mid blends only. The reverse end-foot is
	// a direct, sharp measurement (no systematic bias was observed), so it is
	// taken when it has multi-lag support and stays within a polish-scale
	// drift of the incoming end. Start is never adopted (see doc comment).
	refinedEnd := endFrame
	if D <= fullresMaxAdoptD {
		if e, ok := fullresEndFoot(m, S, E); ok && absInt(e-E) <= max(4, D/2) && e > S {
			refinedEnd = e
		}
	}
	return refinedEnd, len(plan), nil
}

// AlignmentSAD downsamples a re-decoded full-resolution luma plane with the
// scanner's own lowres filter and returns its mean absolute difference
// against the RETAINED lowres plane for frame idx. Near-zero means the
// re-decode is aligned with the first pass; anything else means the decode
// window landed on the wrong frames. Requires RetainAllLowres.
func (s *LookaheadScanner) AlignmentSAD(idx int, luma []byte, w, h, stride int) (float64, error) {
	all := s.AllLowres()
	if idx < 0 || idx >= len(all) || all[idx] == nil {
		return 0, fmt.Errorf("AlignmentSAD: no retained lowres for frame %d", idx)
	}
	lr, err := imgmath.InitLowres(luma, w, h, stride)
	if err != nil {
		return 0, err
	}
	return lr.PlaneSAD(all[idx])
}
