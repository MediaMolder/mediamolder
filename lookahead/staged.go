// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package lookahead provides the motion-compensated (MC) scene detector.
//
// The detector is batch-only. The scanner is fed every frame with a cheap
// small-lag "coarse" schedule. When full lowres retention is enabled
// (RetainAllLowres), AnalyzeStaged can discover rough dissolve candidate
// regions from the cheap matrix and then call Refine to compute additional
// "intelligently chosen" lags (typically one near the estimated dissolve
// duration D plus a narrow 1–5 set for edge precision) only inside the
// relevant temporal windows.
//
// The high-accuracy plateau dissolve detector (saturated high-ratio runs on
// lags k > D + ramp-foot extrapolation + cross-lag median) lives in analyzer.go
// (detectPlateauDissolves) and is the primary consumer of the refined data.
package lookahead

import (
	"sort"
)

// LagsForDuration returns the lags to compute when refining a candidate region
// of estimated width d frames. It returns a narrow prefix (1-5) plus a
// multi-scale ladder of every menu value up to d, plus the two smallest menu
// values above d. The ladder (rather than only a band near d) matters because d
// comes from the coarse plateau width, which on a long coarse lag is ~the lag
// length regardless of the true blend duration — so a region may contain a
// blend of any shorter duration, and we must supply a lag matched to each
// scale for the plateau detector to resolve it.
//
// If the menu is empty a reasonable default set is used. The returned slice
// is sorted and deduplicated.
func LagsForDuration(d int, menu []int) []int {
	if d < 1 {
		d = 1
	}
	res := map[int]bool{}

	// Narrow prefix for edge precision and good baselines in the refined window.
	for i := 1; i <= 5; i++ {
		res[i] = true
	}

	if len(menu) == 0 {
		menu = defaultLagMenu
	}

	// Multi-scale ladder up to d. A candidate region's estimated width d comes
	// from the coarse plateau, which on a long coarse lag is ~the lag length
	// regardless of the true blend duration — so a region may contain a blend of
	// *any* shorter duration. Refining with every menu scale up to d lets the
	// plateau detector resolve the blend at whatever lag is matched to its true
	// length (and the shortest-saturating-lag end/start then localizes it).
	for _, k := range menu {
		if k > 5 && k <= d {
			res[k] = true
		}
	}

	// The plateau detector only fires when at least two lags strictly longer
	// than the blend saturate and agree (the two-lag gate). Force-include the
	// two smallest menu values greater than d so a blend near the top of the
	// ladder still has two qualifying lags.
	above := make([]int, 0, len(menu))
	for _, k := range menu {
		if k > d {
			above = append(above, k)
		}
	}
	sort.Ints(above)
	for i := 0; i < len(above) && i < 2; i++ {
		res[above[i]] = true
	}

	out := make([]int, 0, len(res))
	for k := range res {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// defaultLagMenu is the pool of refinement lags used when the caller supplies
// none (see LagsForDuration and the escalation pass in AnalyzeStaged).
var defaultLagMenu = []int{5, 8, 15, 21, 30, 45, 60, 75, 90, 105, 120, 135}

// maxPopulatedLag returns the largest lag with any measured forward ratio in
// [lo, hi], or 0 when none is.
func maxPopulatedLag(m *CostMatrix, lo, hi int) int {
	for c := len(m.Lags) - 1; c >= 0; c-- {
		for j := max(0, lo); j <= hi && j < m.N; j++ {
			if c < len(m.Ratio[j]) && m.Ratio[j][c] > 0 {
				return m.Lags[c]
			}
		}
	}
	return 0
}

// dissolveRegion describes a rough temporal interval that should receive
// extra lag measurements during the refinement stage.
type dissolveRegion struct {
	lo   int // inclusive
	hi   int // inclusive
	estD int // rough duration in frames
}

// findRoughDissolveRegions produces candidate regions for targeted refinement.
// It combines:
//   - dissolves (and wide transitions) reported by a first Analyze on the
//     current (usually coarse-lag) matrix;
//   - simple sustained high-ratio runs on the longest currently populated lag
//     (this catches long blends even when only small lags have been computed
//     so far).
//
// The regions are merged with a small tolerance and returned sorted by start.
func findRoughDissolveRegions(m *CostMatrix, cfg LookaheadAnalyzer) []dissolveRegion {
	if m == nil || m.N == 0 || len(m.Lags) == 0 {
		return nil
	}

	var regions []dissolveRegion

	// Seed from a normal (cheap) analysis pass. This reliably gives us hard
	// cuts and short dissolves; for longer ones it may give a weak or shifted
	// detection that is still useful as a seed for refinement.
	if trans, err := cfg.Analyze(m); err == nil {
		for _, t := range trans {
			// Skip low-confidence wide dissolve seeds: on the coarse matrix the
			// surface path emits broad, low-score dissolve-shaped responses that
			// would otherwise size huge (and wasteful) refine windows around the
			// wrong region. Hard cuts and high-score seeds are still trusted.
			if t.Type == TransitionDissolve && t.Score < 0.6 {
				continue
			}
			width := t.EndFrame - t.StartFrame + 1
			if width < 1 {
				width = 1
			}
			regions = append(regions, dissolveRegion{
				lo:   max(0, t.StartFrame-3),
				hi:   min(m.N-1, t.EndFrame+3),
				estD: width,
			})
		}
	}

	// Additional presence signal: a *sustained saturated run* on any coarse lag
	// — the distinct dissolve pattern (a flat top pinned near 1.0). Unlike an
	// absolute "ratio is high" test, this does not fire on spiky high-motion
	// content, which never holds saturation, so a long coarse lag no longer
	// over-seeds the content gaps between blends (the cause of fused short
	// blends). With a multi-scale coarse schedule a short lag plateaus on short
	// blends and a long lag on longer ones; whichever lag matches seeds the
	// region. Blends longer than every coarse lag have no flat top here and are
	// seeded by the cheap Analyze surface above instead.
	const presenceSat = 0.95
	const minRun = 3
	for li, k := range m.Lags {
		inRun := false
		runLo := 0
		flush := func(end int) {
			if inRun && end-runLo >= minRun {
				regions = append(regions, dissolveRegion{
					lo:   max(0, runLo-2),
					hi:   min(m.N-1, end+1),
					estD: end - runLo,
				})
			}
			inRun = false
		}
		for j := k + 1; j < m.N; j++ {
			r := 0.0
			if li < len(m.Ratio[j]) {
				r = float64(m.Ratio[j][li])
			}
			if r >= presenceSat {
				if !inRun {
					inRun, runLo = true, j
				}
				continue
			}
			flush(j)
		}
		flush(m.N - 1)
	}

	if len(regions) <= 1 {
		return regions
	}

	// Merge overlapping or nearly-adjacent regions.
	sort.Slice(regions, func(i, j int) bool { return regions[i].lo < regions[j].lo })

	merged := regions[:1]
	for _, r := range regions[1:] {
		last := &merged[len(merged)-1]
		if r.lo <= last.hi+6 { // small tolerance to bridge noise
			if r.hi > last.hi {
				last.hi = r.hi
			}
			if r.estD > last.estD {
				last.estD = r.estD
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// StagedProgress reports one stage of AnalyzeStaged so callers can surface
// status updates (e.g. to a GUI log panel) during the otherwise-silent
// post-frames refinement work. All frame fields are 0-based frame indices.
type StagedProgress struct {
	Phase   string // "coarse", "refine" (one region), "escalate", "energy", "final"
	Current int    // refine: 1-based region index; coarse: number of regions found
	Total   int    // refine: total number of regions
	Lo, Hi  int    // refine/escalate/energy: the dissolve region (frame indices)
	EstD    int    // refine/energy: estimated dissolve length in frames

	DipSNR         float64 // energy: fitted AC-energy dip depth over flank noise
	EnergyUsed     bool    // energy: a dip-fit edge was adopted into the bounds
	MeanStepUsed   bool    // energy: a channel-mean step-fit edge was adopted
	FullresEndUsed bool    // fullres: the full-res reverse end-foot was adopted
}

// AnalyzeStaged implements the incremental / staged measurement strategy for
// motion-compensated scene detection.
//
// It is designed to be called after the scanner has been fed the entire video
// using a cheap small-lag schedule (e.g. lags {1,5}) **and** after
// scanner.RetainAllLowres() was called so that AllLowres() can supply the
// low-resolution frames for on-demand extra measurements.
//
// The refinementMenu (when non-empty) acts as the pool of "interesting" lag
// values. For each rough dissolve region discovered on the cheap coarse data,
// the implementation picks lag(s) near the estimated duration D from this menu
// (plus a narrow 1-5 set for precise edge placement) and calls Refine only for
// the relevant frame window around that region.
//
// report, when non-nil, is invoked at each stage of the work (coarse-pass done,
// each region being refined, final analysis) so the caller can emit progress /
// log updates. It is called synchronously from this goroutine.
//
// After the targeted refinements, the normal high-quality Analyze (including
// plateau-based dissolve detection) is run on the now selectively augmented
// matrix. The result is returned.
//
// If the scanner does not have retained lowres frames, AnalyzeStaged falls
// back to ordinary Analyze(m) for compatibility.
func (a *LookaheadAnalyzer) AnalyzeStaged(s *LookaheadScanner, refinementMenu []int, report func(StagedProgress)) ([]SceneTransition, error) {
	if s == nil {
		return nil, nil
	}
	m := s.Matrix()
	if m == nil || m.N == 0 {
		return nil, nil
	}

	lowres := s.AllLowres()
	if len(lowres) == 0 {
		// No retention — we cannot do targeted extra work. Fall back.
		return a.Analyze(m)
	}

	cfg := a.withDefaults(m.L)

	// First pass on the cheap coarse matrix. This already gives excellent
	// hard cuts and short dissolves via the surface path.
	initial, _ := cfg.Analyze(m)

	// Discover regions that are likely to benefit from more/better-chosen lags.
	regions := findRoughDissolveRegions(m, cfg)
	if report != nil {
		report(StagedProgress{Phase: "coarse", Current: len(regions)})
	}

	for i, r := range regions {
		if r.estD < cfg.DissolveMinLen {
			r.estD = cfg.DissolveMinLen
		}
		if r.estD > cfg.DissolveMaxLen {
			r.estD = cfg.DissolveMaxLen
		}
		if report != nil {
			report(StagedProgress{
				Phase:   "refine",
				Current: i + 1,
				Total:   len(regions),
				Lo:      r.lo,
				Hi:      r.hi,
				EstD:    r.estD,
			})
		}
		// Choose intelligently: narrow set + lag(s) close to estD drawn from the menu.
		targets := LagsForDuration(r.estD, refinementMenu)

		// Add a safety margin on each side (user example: for D≈30 use ±30).
		margin := r.estD
		if margin < 8 {
			margin = 8
		}
		winLo := r.lo - margin
		winHi := r.hi + margin

		_ = s.Refine(m, lowres, winLo, winHi, targets)
	}

	if report != nil {
		report(StagedProgress{Phase: "final"})
	}

	// Second (final) analysis on the augmented matrix. The plateau detector
	// will now see well-matched long lags exactly where it needs them, and the
	// narrow lags help produce tighter start/end for the refined events.
	final, err := cfg.Analyze(m)
	if err != nil {
		return initial, nil
	}

	// Lag escalation: a dissolve flagged underLagged had no two lags spanning
	// its own duration estimate — every lag in its consensus group saturated
	// mid-blend, so its bounds (and especially its duration, which feeds the
	// region's lag choice in the first place) are under-measured. Re-measure
	// the region with the next menu lags above the longest one populated
	// there and re-analyze; one round is normally enough (the menu tops out
	// quickly), but allow two for a badly underestimated region.
	for round := 0; round < 2; round++ {
		escalated := false
		for _, t := range final {
			if t.Type != TransitionDissolve || !t.underLagged {
				continue
			}
			kMax := maxPopulatedLag(m, t.StartFrame, t.EndFrame)
			menu := refinementMenu
			if len(menu) == 0 {
				menu = defaultLagMenu
			}
			var add []int
			for _, k := range menu {
				if k > kMax {
					add = append(add, k)
				}
			}
			sort.Ints(add)
			if len(add) > 2 {
				add = add[:2]
			}
			if len(add) == 0 {
				continue
			}
			if report != nil {
				report(StagedProgress{
					Phase: "escalate",
					Lo:    t.StartFrame,
					Hi:    t.EndFrame,
					EstD:  t.EndFrame - t.StartFrame,
				})
			}
			// A lag k saturates on [E, S+k−1], so the measurement window must
			// reach ~k frames past the start; the leading side needs only the
			// usual baseline margin.
			_ = s.Refine(m, lowres, t.StartFrame-8, t.EndFrame+add[len(add)-1], add)
			escalated = true
		}
		if !escalated {
			break
		}
		f2, err2 := cfg.Analyze(m)
		if err2 != nil {
			break
		}
		final = f2
	}

	// Progressive lag narrowing: re-measure each dissolve's edges with
	// successively shorter forward and reverse prediction distances down to
	// lag 1 (see refineDissolveEdges). All measurements land in the matrix and
	// therefore in the cost-matrix CSV.
	for i := range final {
		t := &final[i]
		if t.Type != TransitionDissolve {
			continue
		}
		ns, ne, info := refineDissolveEdges(s, m, lowres, t.StartFrame, t.EndFrame)
		if ne > ns {
			t.StartFrame, t.EndFrame = ns, ne
		}
		if report != nil && (info.DipSNR > 0 || info.MeanStepUsed) {
			report(StagedProgress{
				Phase:        "energy",
				Lo:           t.StartFrame,
				Hi:           t.EndFrame,
				EstD:         t.EndFrame - t.StartFrame,
				DipSNR:       info.DipSNR,
				EnergyUsed:   info.EnergyUsed,
				MeanStepUsed: info.MeanStepUsed,
			})
		}
		// Full-resolution edge measurement. Runs after all lowres stages so
		// the windows sit on the best available edges; logs its measurements
		// to the matrix/CSV and adopts a refined END for short/mid blends
		// (the reverse end-foot beats the lowres stack there; long blends and
		// the forward start are out of full resolution's reach). Provider
		// failures skip silently into the report.
		if a.FullresProvider != nil {
			newE, n, err := fullresEdgeMeasure(m, a.FullresProvider, t.StartFrame, t.EndFrame)
			adopted := false
			if err == nil && newE != t.EndFrame && newE > t.StartFrame {
				t.EndFrame = newE
				adopted = true
			}
			if report != nil {
				pr := StagedProgress{
					Phase:          "fullres",
					Lo:             t.StartFrame,
					Hi:             t.EndFrame,
					EstD:           t.EndFrame - t.StartFrame,
					Total:          n,
					FullresEndUsed: adopted,
				}
				if err != nil {
					pr.Total = -1 // provider failure: measurement skipped
				}
				report(pr)
			}
		}
	}

	return mergeStagedResults(initial, final), nil
}

// mergeStagedResults combines the coarse (initial) and refined (final) passes.
//
// The refined pass owns dissolve reporting: it sees well-matched long lags
// exactly where blends occur, so its plateau bounds supersede any coarse-pass
// dissolve. The coarse matrix (a long lag + a large agg_window) emits wide,
// low-score surface dissolves that its own plateau suppression cannot drop —
// it has too few long lags to measure a plateau and clear them. Re-adding those
// here is what produced the junk wide companions next to every real event, so
// we drop all initial dissolves and carry over only initial hard cuts / fades
// that the refined pass does not already cover (by interval overlap).
func mergeStagedResults(initial, final []SceneTransition) []SceneTransition {
	out := append([]SceneTransition(nil), final...)
	for _, t := range initial {
		if t.Type == TransitionDissolve {
			continue
		}
		if overlapsAny(t, final) {
			continue
		}
		out = append(out, t)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].StartFrame < out[j].StartFrame
	})

	return out
}

// overlapsAny reports whether t's [StartFrame,EndFrame] interval overlaps any
// transition in set (±2 frame tolerance).
func overlapsAny(t SceneTransition, set []SceneTransition) bool {
	for _, o := range set {
		if t.StartFrame <= o.EndFrame+2 && t.EndFrame >= o.StartFrame-2 {
			return true
		}
	}
	return false
}
