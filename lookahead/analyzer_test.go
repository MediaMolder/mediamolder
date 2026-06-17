// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import (
	"encoding/csv"
	"math"
	"os"
	"strconv"
	"testing"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// makeMatrix builds a CostMatrix with the given ratio values using the default
// Fibonacci lag schedule for the given lookahead length l.
// ratioFn(j, lagVal) returns the inter/intra ratio for frame j at 1-based lag lagVal.
func makeMatrix(n, l int, ratioFn func(j, lagVal int) float32, intraCostFn func(j int) float32) *CostMatrix {
	lags := fibLags(l)
	return makeMatrixWithLags(n, lags, ratioFn, intraCostFn)
}

// makeMatrixWithLags builds a CostMatrix with an explicit (custom) lag schedule.
// This is used to simulate runs with the "lags" param (e.g. long-lag sets for
// targeting synthetic dissolves) instead of the fib default. lags should be
// sorted ascending; L is set to the maximum.
func makeMatrixWithLags(n int, lags []int, ratioFn func(j, lagVal int) float32, intraCostFn func(j int) float32) *CostMatrix {
	maxL := 0
	for _, k := range lags {
		if k > maxL {
			maxL = k
		}
	}
	m := &CostMatrix{N: n, L: maxL, Lags: append([]int(nil), lags...)}
	m.IntraCost = make([]float32, n)
	m.InterCost = make([][]float32, n)
	m.AvgLuma = make([]float32, n)
	m.Ratio = make([][]float32, n)
	for j := 0; j < n; j++ {
		intra := float32(1000)
		if intraCostFn != nil {
			intra = intraCostFn(j)
		}
		m.IntraCost[j] = intra
		m.AvgLuma[j] = 128 // default mid-grey
		row := make([]float32, len(lags))
		interRow := make([]float32, len(lags))
		for i, lagVal := range lags {
			r := ratioFn(j, lagVal)
			row[i] = r
			if intra > 0 {
				interRow[i] = r * intra
			}
		}
		m.Ratio[j] = row
		m.InterCost[j] = interRow
	}
	return m
}

// defaultAnalyzer returns a zero-value analyzer (all defaults applied in Analyze).
func defaultAnalyzer() *LookaheadAnalyzer { return &LookaheadAnalyzer{} }

// ─── Analyze validation ──────────────────────────────────────────────────────

func TestAnalyze_NilMatrix(t *testing.T) {
	_, err := defaultAnalyzer().Analyze(nil)
	if err == nil {
		t.Fatal("expected error for nil matrix")
	}
}

func TestAnalyze_EmptyMatrix(t *testing.T) {
	_, err := defaultAnalyzer().Analyze(&CostMatrix{})
	if err == nil {
		t.Fatal("expected error for empty matrix")
	}
}

// ─── normalizeMatrix ─────────────────────────────────────────────────────────

func TestNormalizeMatrix_Clamp(t *testing.T) {
	m := makeMatrix(3, 4, func(j, lagVal int) float32 {
		switch {
		case j == 0:
			return -0.5 // below 0 → should become 0
		case j == 1:
			return 1.5 // above 1 → should become 1
		default:
			return 0.3
		}
	}, nil)
	cfg := defaultAnalyzer().withDefaults(m.L)
	norm := cfg.normalizeMatrix(m)
	for i := range m.Lags {
		if norm[0][i] != 0 {
			t.Errorf("norm[0][%d] = %g, want 0 (clamped from -0.5)", i, norm[0][i])
		}
		if norm[1][i] != 1 {
			t.Errorf("norm[1][%d] = %g, want 1 (clamped from 1.5)", i, norm[1][i])
		}
		if math.Abs(norm[2][i]-0.3) > 1e-6 {
			t.Errorf("norm[2][%d] = %g, want 0.3", i, norm[2][i])
		}
	}
}

// ─── fitBaselines ────────────────────────────────────────────────────────────

func TestFitBaselines_LinearInliers(t *testing.T) {
	// Frames have ratio = 0.05 + 0.03*lagVal (well within inlier threshold 0.4).
	// Expected: alpha ≈ 0.05, beta ≈ 0.03 for frames with enough refs.
	const N, L = 20, 10
	m := makeMatrix(N, L, func(j, lagVal int) float32 {
		return float32(0.05 + 0.03*float64(lagVal))
	}, nil)
	cfg := defaultAnalyzer().withDefaults(L)
	norm := cfg.normalizeMatrix(m)
	alpha, beta := cfg.fitBaselines(norm, m)

	// Check frame 15 (has 10 inlier lags available).
	j := 15
	if math.Abs(alpha[j]-0.05) > 0.02 {
		t.Errorf("alpha[%d] = %g, want ≈0.05", j, alpha[j])
	}
	if math.Abs(beta[j]-0.03) > 0.01 {
		t.Errorf("beta[%d] = %g, want ≈0.03", j, beta[j])
	}
}

func TestFitBaselines_NoInliers(t *testing.T) {
	// All ratios are 0.9 (above inlier threshold 0.4): alpha should be
	// the mean of all available lags; beta should be 0.
	const N, L = 10, 5
	m := makeMatrix(N, L, func(j, lagVal int) float32 { return 0.9 }, nil)
	cfg := defaultAnalyzer().withDefaults(L)
	norm := cfg.normalizeMatrix(m)
	alpha, beta := cfg.fitBaselines(norm, m)

	j := 5 // has 5 refs available
	if math.Abs(alpha[j]-0.9) > 0.01 {
		t.Errorf("alpha[%d] = %g, want ≈0.9 (mean of clamped ratios)", j, alpha[j])
	}
	if beta[j] != 0 {
		t.Errorf("beta[%d] = %g, want 0 (no inliers → flat baseline)", j, beta[j])
	}
}

// ─── buildScoreSurface ───────────────────────────────────────────────────────

func TestBuildScoreSurface_PeakAtCut(t *testing.T) {
	// Simulate a hard cut at frame 20: for j ≥ 20 and lagVal = j−20+1,
	// excess[j][idx(lagVal)] = 0.8. All other excess values are 0.
	const N, L = 40, 25
	const cutAt = 20
	m := &CostMatrix{N: N, L: L, Lags: fibLags(L)}
	excess := make([][]float64, N)
	for j := 0; j < N; j++ {
		row := make([]float64, len(m.Lags))
		lagVal := j - cutAt + 1 // anti-diagonal: ref = j - lagVal = cutAt - 1
		if lagVal >= 1 && lagVal < j {
			if idx := lagIndex(m.Lags, lagVal); idx >= 0 {
				row[idx] = 0.8
			}
		}
		excess[j] = row
	}
	cfg := (&LookaheadAnalyzer{AggWindow: 5}).withDefaults(L)
	surface := cfg.buildScoreSurface(excess, m)

	// Surface should peak near frame 20.
	peak := 0
	for c := 1; c < N; c++ {
		if surface[c] > surface[peak] {
			peak = c
		}
	}
	if math.Abs(float64(peak-cutAt)) > 2 {
		t.Errorf("surface peak at %d, want near %d", peak, cutAt)
	}
	if surface[peak] < 0.4 {
		t.Errorf("surface[%d] = %g, want ≥ 0.4", peak, surface[peak])
	}
}

// ─── findPeaks ───────────────────────────────────────────────────────────────

func TestFindPeaks_NoPeaks(t *testing.T) {
	cfg := (&LookaheadAnalyzer{HardCutThreshold: 0.45}).withDefaults(10)
	surface := make([]float64, 50) // all zero
	if peaks := cfg.findPeaks(surface); len(peaks) != 0 {
		t.Errorf("expected no peaks, got %v", peaks)
	}
}

func TestFindPeaks_SinglePeak(t *testing.T) {
	cfg := (&LookaheadAnalyzer{HardCutThreshold: 0.45, MinSceneLen: 5}).withDefaults(10)
	surface := make([]float64, 50)
	surface[20] = 0.8
	peaks := cfg.findPeaks(surface)
	if len(peaks) != 1 || peaks[0] != 20 {
		t.Errorf("peaks = %v, want [20]", peaks)
	}
}

func TestFindPeaks_NMS(t *testing.T) {
	// Two peaks 3 frames apart (< MinSceneLen=5): only the higher one kept.
	cfg := (&LookaheadAnalyzer{HardCutThreshold: 0.45, MinSceneLen: 5}).withDefaults(10)
	surface := make([]float64, 50)
	surface[20] = 0.6
	surface[22] = 0.9 // higher, closer than MinSceneLen
	peaks := cfg.findPeaks(surface)
	if len(peaks) != 1 || peaks[0] != 22 {
		t.Errorf("NMS: peaks = %v, want [22]", peaks)
	}
}

func TestFindPeaks_TwoSeparatedPeaks(t *testing.T) {
	cfg := (&LookaheadAnalyzer{HardCutThreshold: 0.45, MinSceneLen: 5}).withDefaults(10)
	surface := make([]float64, 60)
	surface[10] = 0.7
	surface[30] = 0.8
	peaks := cfg.findPeaks(surface)
	if len(peaks) != 2 || peaks[0] != 10 || peaks[1] != 30 {
		t.Errorf("two peaks: got %v, want [10 30]", peaks)
	}
}

// ─── full Analyze pipeline ───────────────────────────────────────────────────

func TestAnalyze_SameScene_NoTransitions(t *testing.T) {
	// All ratios are small and uniform (same scene throughout).
	// No transitions should be detected.
	const N, L = 60, 20
	m := makeMatrix(N, L, func(j, lagVal int) float32 {
		return float32(0.05 + 0.01*float64(lagVal))
	}, nil)
	tr, err := defaultAnalyzer().Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(tr) != 0 {
		t.Errorf("expected no transitions, got %d: %v", len(tr), tr)
	}
}

func TestAnalyze_HardCut(t *testing.T) {
	// Simulate a hard cut at frame 30 in a 60-frame sequence.
	// For j ≥ 30: ratio[j][lagVal] is high when lagVal > j−29 (reference is pre-cut),
	//             otherwise 0.1 (reference is post-cut, same scene).
	const N, L = 60, 30
	const cutAt = 30
	m := makeMatrix(N, L, func(j, lagVal int) float32 {
		if j >= cutAt && lagVal > j-cutAt {
			return 0.9
		}
		return 0.1
	}, nil)
	a := &LookaheadAnalyzer{
		HardCutThreshold: 0.45,
		ExcessThreshold:  0.15,
		AggWindow:        5,
		MinSceneLen:      10,
	}
	tr, err := a.Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(tr) == 0 {
		t.Fatal("expected at least one transition, got none")
	}
	// The detected cut should be within 5 frames of the true cut.
	best := tr[0]
	for _, t2 := range tr[1:] {
		if t2.Score > best.Score {
			best = t2
		}
	}
	if math.Abs(float64(best.StartFrame-cutAt)) > 5 {
		t.Errorf("cut detected at %d, want near %d (score=%.3f)", best.StartFrame, cutAt, best.Score)
	}
	if best.Type != TransitionHardCut {
		t.Errorf("type = %d, want TransitionHardCut (%d)", best.Type, TransitionHardCut)
	}
}

func TestAnalyze_Dissolve(t *testing.T) {
	// Model a dissolve: frame j in [dissolveStart, dissolveEnd] has content
	// that blends from scene A to scene B.  Within such a frame, lags
	// referencing scene A (k ≥ j−dissolveStart+1) have high ratio, while
	// lags referencing within the dissolve (k < j−dissolveStart+1) are lower.
	// This produces excess spread across *multiple lags in a single row*.
	const N, L = 80, 40
	const dissolveStart, dissolveEnd = 30, 40
	m := makeMatrix(N, L, func(j, lagVal int) float32 {
		if j < dissolveStart || j > dissolveEnd {
			// Same scene: low smooth ratio.
			return float32(0.05 + 0.01*float64(lagVal))
		}
		// Dissolve frame: lags that reach into the pre-dissolve content are high.
		// A 1-based lagVal means the reference is frame j−lagVal.
		// If j−lagVal < dissolveStart, reference is pure scene A → high ratio.
		refFrame := j - lagVal
		if refFrame < dissolveStart {
			return 0.9
		}
		return float32(0.05 + 0.01*float64(lagVal))
	}, nil)
	a := &LookaheadAnalyzer{
		HardCutThreshold: 0.35,
		ExcessThreshold:  0.10,
		DissolveMinLen:   3,
		AggWindow:        5,
		MinSceneLen:      10,
	}
	tr, err := a.Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	found := false
	for _, t2 := range tr {
		// A detected transition should overlap the dissolve region.
		// With Fibonacci lags the blendStart estimate may be earlier than
		// dissolveStart, so check overlap [StartFrame, EndFrame] ∩ [dissolveStart-5, dissolveEnd+5].
		if t2.StartFrame <= dissolveEnd+5 && t2.EndFrame >= dissolveStart-5 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no transition detected near dissolve [%d,%d]; got %v",
			dissolveStart, dissolveEnd, tr)
	}
}

func TestAnalyze_FlashFiltered(t *testing.T) {
	// Simulate a flash at frame 20: cuts at 20 and 21, then frame 22 is NOT
	// similar to 21 (ratio[22][0] is high) → both should be filtered.
	const N, L = 60, 10
	m := makeMatrix(N, L, func(j, lagVal int) float32 {
		// High excess at frames 20 and 21 (simulates two adjacent cuts).
		if (j == 20 || j == 21) && lagVal <= 3 {
			return 0.95
		}
		// Frame 22 vs 21 (lag=1): also high → not stable → flash.
		if j == 22 && lagVal == 1 {
			return 0.9
		}
		return 0.05
	}, nil)
	a := &LookaheadAnalyzer{
		HardCutThreshold: 0.45,
		ExcessThreshold:  0.15,
		AggWindow:        3,
		MinSceneLen:      2,
	}
	tr, err := a.Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, t2 := range tr {
		if t2.StartFrame == 20 || t2.StartFrame == 21 {
			t.Errorf("flash at frame %d should have been filtered; transitions: %v",
				t2.StartFrame, tr)
		}
	}
}

func TestAnalyze_TwoCuts(t *testing.T) {
	// Two hard cuts at frames 20 and 50 in a 80-frame sequence.
	const N, L = 80, 30
	cuts := []int{20, 50}
	m := makeMatrix(N, L, func(j, lagVal int) float32 {
		for _, c := range cuts {
			if j >= c && lagVal > j-c {
				return 0.9
			}
		}
		return 0.1
	}, nil)
	a := &LookaheadAnalyzer{
		HardCutThreshold: 0.45,
		ExcessThreshold:  0.15,
		AggWindow:        5,
		MinSceneLen:      10,
	}
	tr, err := a.Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(tr) < 2 {
		t.Errorf("expected ≥ 2 transitions, got %d: %v", len(tr), tr)
	}
}

func TestWithDefaults_ZeroFields(t *testing.T) {
	a := LookaheadAnalyzer{}
	cfg := a.withDefaults(40)
	if cfg.HardCutThreshold != 0.5 {
		t.Errorf("HardCutThreshold = %g, want 0.5", cfg.HardCutThreshold)
	}
	if cfg.ExcessThreshold != 0.15 {
		t.Errorf("ExcessThreshold = %g, want 0.15", cfg.ExcessThreshold)
	}
	if cfg.DissolveMinLen != 2 {
		t.Errorf("DissolveMinLen = %d, want 2", cfg.DissolveMinLen)
	}
	if cfg.DissolveMaxLen != 20 { // l/2 = 40/2 = 20
		t.Errorf("DissolveMaxLen = %d, want 20", cfg.DissolveMaxLen)
	}
	if cfg.AggWindow != 5 {
		t.Errorf("AggWindow = %d, want 5", cfg.AggWindow)
	}
	if cfg.MinSceneLen != 15 {
		t.Errorf("MinSceneLen = %d, want 15", cfg.MinSceneLen)
	}
}

// TestAnalyze_SlowDissolve models a long gradual dissolve (30 frames) using
// a lag-dependent ramp in ratios. With the min-baseline + widened width scan
// the detector should report a dissolve (not hard cut) whose span overlaps
// the blend interval even when the surface peak is moderate.
func TestAnalyze_SlowDissolve(t *testing.T) {
	const N, L = 120, 40
	const blendStart, blendEnd = 40, 70 // 31-frame dissolve
	m := makeMatrix(N, L, func(j, lagVal int) float32 {
		if j < blendStart || j > blendEnd {
			return float32(0.04 + 0.008*float64(lagVal))
		}
		ref := j - lagVal
		if lagVal >= 3 && ref < blendStart {
			return 0.92 // lags reaching pre-blend are high; produces row width
		}
		return float32(0.06 + 0.01*float64(lagVal))
	}, nil)
	a := &LookaheadAnalyzer{
		HardCutThreshold: 0.22, // graduals produce lower but wider surface
		ExcessThreshold:  0.10,
		DissolveMinLen:   2, // allow leading edge of long dissolve to qualify
		AggWindow:        5,
		MinSceneLen:      8,
	}
	trs, err := a.Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	found := false
	for _, tr := range trs {
		if tr.StartFrame <= blendEnd+10 && tr.EndFrame >= blendStart-5 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected transition overlapping long dissolve [%d,%d], got %v", blendStart, blendEnd, trs)
	}
}

// TestAnalyze_SyntheticSequenceEditorDissolves models the exact pattern observed
// when running the motion-compensated detector on video produced by the
// sequence_editor (dissolve_test_xfs.json generator) and logging the costs
// with a custom long-ish lag schedule (dissolve_test_x264_costs_longlags.csv).
//
// Ground truth: 12 synthetic linear blends at nominal cuts ~10s,20s,...120s
// (frames ~300,599,...3596 at 29.97 fps), with blend durations 0.125s → 4.0s
// (roughly 4 to 120 frames). With a custom schedule the CSV shows strong ratio
// spikes (often stepping to ~0.99-1.0) at short-blend starts (all provided lags
// affected), with weaker/sustained elevation for longer blends (longer lags
// provide clearer signal). Short lags give tighter temporal support for bounds;
// the upper-half lags produce the has_long_bad + min_bad_j span used for dissolve
// Start/End on these synthetics.
//
// This test constructs a matrix using a representative custom lag schedule
// (includes both a small lag for short-D coverage and lags ≥45 to exercise the
// long-lag paths in classifyTransitions / classifyOne) with similar notch
// patterns at two simulated GT locations, then verifies that the tuned
// "synthetic blend" params cause the detector to report TransitionDissolve.
//
// The params (lower threshold, lower excess, higher agg, lower minLen,
// larger dissolve_max) were informed by offline diagnostics on the real long-lags
// CSV. The ratio (inter vs intra) is the primary signal.
func TestAnalyze_SyntheticSequenceEditorDissolves(t *testing.T) {
	// Representative custom schedule for the xfs synthetics (improved set derived
	// from GT durations 4..120f + CSV observations). A small lag (3) is kept
	// "clean" (low during the simulated blends) to provide a good baseline fit.
	// A medium lag (12) that fits inside a modest agg window receives the notch
	// so the score surface can fire from excess on a within-agg lagVal. Larger
	// the upper-half lags exercise has_long_bad, min_bad_j span for bounds,
	// and the long-lag effectiveWidth boost in classify.
	customLags := []int{3, 12, 25, 45, 60}

	const N = 200
	// short strong notch (models gt0 D~4 etc.): the medium lag (visible to
	// surface with agg~15) and the long lags go high for a brief window while
	// the small baseline lag stays low → clean high excess on the surface lag.
	shortCut := 50
	shortDur := 4

	// longer sustained notch (models medium/long GTs): high on the larger lags
	// (the upper half for this schedule). The j-span where has_long_bad is true drives blendStart/End
	// via min_bad_j; this also exercises the "long lag presence → dissolve" path.
	longCut := 120
	longDur := 30

	m := makeMatrixWithLags(N, customLags, func(j, lagVal int) float32 {
		// normal content: moderate rise with lag (long-range prediction is
		// often already poor in this source; see control frames in the CSV).
		base := float32(0.15 + 0.02*float64(lagVal))

		// short strong notch: high on the surface-visible medium lag and on
		// long lags (models the step-to-high on the lags present in a custom
		// schedule). Keep lag=3 at base so baseline fit yields low alpha and
		// the excess on 12 (and 25+) is large.
		if j >= shortCut && j < shortCut+shortDur {
			if lagVal >= 12 {
				return 0.96
			}
		}
		// longer sustained elevation on the upper-half lags of this schedule
		// (25,45,60 for the test's customLags). Those set has_long_bad and feed
		// the temporal min_bad_j used for the final dissolve frame range.
		if j >= longCut && j < longCut+longDur {
			if lagVal >= 25 {
				return 0.78
			}
		}
		return base
	}, nil)

	// Tuned for the observed CSV notch patterns + long-lag schedule behavior on
	// the xfs synthetic blends (agg large enough for the first notched lag to
	// contribute to surface; lower excess etc. for the moderate sustained case).
	a := &LookaheadAnalyzer{
		HardCutThreshold: 0.32,
		ExcessThreshold:  0.09,
		DissolveMinLen:   2,
		AggWindow:        15,
		MinSceneLen:      8,
		DissolveMaxLen:   80,
	}
	trs, err := a.Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// The high-ratio notch patterns (short strong on the medium+long lags with
	// clean small-lag baseline + longer sustained on k>=25/45) should cause the
	// dissolve classification path (row width or long-lag has_long_bad +
	// min_bad_j temporal support, plus effectiveWidth boost) to emit at least
	// one TransitionDissolve. Exact bounds here are secondary; the goal is
	// that xfs-style synthetic notch patterns on a custom lag schedule produce
	// dissolves rather than hard cuts or being missed.
	foundDissolve := false
	for _, tr := range trs {
		if tr.Type == TransitionDissolve {
			foundDissolve = true
			break
		}
	}
	if !foundDissolve {
		t.Errorf("expected at least one TransitionDissolve for the synthetic high-ratio notch patterns (short strong + longer on custom lags), got %v", trs)
	}
	// The short strong notch should produce a reasonably high surface score.
	if len(trs) == 0 || trs[0].Score < 0.3 {
		t.Errorf("score for the primary candidate too low for the strong notch pattern: %v", trs)
	}
}

// loadDissolveTestCSV loads the real cost-matrix CSV emitted by a
// scene_change_mc run over dissolve_test_out.mp4 (lags 5..120), or skips the
// test when no copy is available.
func loadDissolveTestCSV(t *testing.T) *CostMatrix {
	t.Helper()
	candPaths := []string{
		"/Volumes/SSD/mediamoldertests/dissolve_test_costs.csv",
		"/Volumes/SSD/mediamoldertests/dissolve_test_x264_costs_revisedlonglags.csv",
		"../dissolve_test_x264_costs_revisedlonglags.csv",
		"./dissolve_test_x264_costs_revisedlonglags.csv",
	}
	var f *os.File
	var err error
	for _, p := range candPaths {
		f, err = os.Open(p)
		if err == nil {
			break
		}
	}
	if f == nil {
		t.Skipf("CSV not available (tried %d paths); last err: %v", len(candPaths), err)
		return nil
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) < 2 {
		t.Fatal("empty csv")
	}
	// Header-aware column lookup: older capture CSVs lack the energy column
	// and newer ones may gain columns, so fixed indices would silently
	// mislabel data.
	col := map[string]int{}
	for c, name := range rows[0] {
		col[name] = c
	}
	data := rows[1:]
	N := len(data)

	lags := []int{5, 15, 30, 45, 60, 75, 90, 105, 120}
	m := &CostMatrix{
		N:         N,
		L:         120,
		Lags:      lags,
		IntraCost: make([]float32, N),
		AvgLuma:   make([]float32, N),
		Ratio:     make([][]float32, N),
		InterCost: make([][]float32, N),
	}
	energyCol, hasEnergy := col["energy"]
	if hasEnergy {
		m.Energy = make([]float32, N)
	}
	parse := func(row []string, c int) float32 {
		if c < 0 || c >= len(row) {
			return 0
		}
		v, _ := strconv.ParseFloat(row[c], 64)
		return float32(v)
	}
	at := func(name string) int {
		if c, ok := col[name]; ok {
			return c
		}
		return -1
	}
	lagCol := func(prefix string, lag int) int { return at(prefix + strconv.Itoa(lag)) }
	for j, row := range data {
		if len(row) < 3 {
			continue
		}
		m.IntraCost[j] = parse(row, at("intra_cost"))
		m.AvgLuma[j] = parse(row, at("avg_luma"))
		if hasEnergy {
			m.Energy[j] = parse(row, energyCol)
		}
		m.Ratio[j] = make([]float32, len(lags))
		m.InterCost[j] = make([]float32, len(lags))
		for li, lag := range lags {
			m.Ratio[j][li] = parse(row, lagCol("ratio_lag", lag))
			m.InterCost[j][li] = parse(row, lagCol("inter_lag", lag))
		}
	}
	return m
}

// TestDetectPlateauDissolves_SyntheticExact verifies the ramp-foot recovery
// on an ideal linear cross-fade: every lag k > D saturates between its
// linear entry/exit ramps, and the two-point extrapolation through the 50%
// and 20% crossings must land exactly on the blend bounds. This is the
// regression test for the scan-window-span artifact: the old excess-span
// bounds reported content-driven intervals instead of the blend.
func TestDetectPlateauDissolves_SyntheticExact(t *testing.T) {
	lags := []int{5, 15, 30, 45, 60}
	const N = 400
	cases := []struct{ S, D int }{
		{200, 20}, // short blend: lags 30,45,60 qualify
		{150, 40}, // medium blend: lags 45,60 qualify
	}
	for _, tc := range cases {
		S, D := tc.S, tc.D
		alpha := func(f int) float64 {
			if f < S {
				return 0
			}
			if f >= S+D {
				return 1
			}
			return float64(f-S+1) / float64(D+1)
		}
		m := makeMatrixWithLags(N, lags, func(j, lagVal int) float32 {
			mm := alpha(j) - alpha(j-lagVal)
			if mm < 0 {
				mm = -mm
			}
			return float32(0.30 + 0.695*mm)
		}, nil)

		a := &LookaheadAnalyzer{DissolveMaxLen: 60}
		cfg := a.withDefaults(m.L)
		norm := cfg.normalizeMatrix(m)
		out := cfg.detectPlateauDissolves(norm, m)
		if len(out) != 1 {
			t.Fatalf("S=%d D=%d: expected 1 plateau dissolve, got %v", S, D, out)
		}
		tr := out[0]
		if tr.Type != TransitionDissolve {
			t.Errorf("S=%d D=%d: type = %v, want dissolve", S, D, tr.Type)
		}
		// StartFrame = last unblended frame = S−1; EndFrame = first unblended
		// frame of the new scene = S+D.
		if tr.StartFrame != S-1 || tr.EndFrame != S+D {
			t.Errorf("S=%d D=%d: bounds [%d,%d], want [%d,%d]",
				S, D, tr.StartFrame, tr.EndFrame, S-1, S+D)
		}

		// End-to-end: Analyze must emit the same measured bounds (the plateau
		// dissolve replaces any overlapping surface-path event).
		trs, err := a.Analyze(m)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		found := false
		for _, tr := range trs {
			if tr.Type == TransitionDissolve && tr.StartFrame == S-1 && tr.EndFrame == S+D {
				found = true
			}
		}
		if !found {
			t.Errorf("S=%d D=%d: Analyze did not emit the plateau-measured dissolve: %v", S, D, trs)
		}
	}
}

// TestDetectPlateauDissolves_HardCutSkipped: a hard cut saturates every lag
// too (D=0), but the implied duration is below DissolveMinLen, so the plateau
// detector must not claim it as a dissolve (the surface path owns hard cuts).
func TestDetectPlateauDissolves_HardCutSkipped(t *testing.T) {
	lags := []int{5, 15, 30, 45, 60}
	const N, cut = 300, 150
	m := makeMatrixWithLags(N, lags, func(j, lagVal int) float32 {
		if j >= cut && j-lagVal < cut {
			return 0.99
		}
		return 0.30
	}, nil)
	a := &LookaheadAnalyzer{DissolveMaxLen: 60}
	cfg := a.withDefaults(m.L)
	norm := cfg.normalizeMatrix(m)
	if out := cfg.detectPlateauDissolves(norm, m); len(out) != 0 {
		t.Errorf("expected no plateau dissolves for a hard cut, got %v", out)
	}
}

// TestAnalyze_PlateauDissolves_RealCSV runs the full batch Analyze (with the
// dissolve_test_motion_scenes2.json analyzer parameters) over the real CSV
// and checks the detected dissolves against the sequence-editor ground truth:
// 11 cross-fades at 10 s intervals with durations 7..120 frames.
func TestAnalyze_PlateauDissolves_RealCSV(t *testing.T) {
	m := loadDissolveTestCSV(t)
	if m == nil {
		return
	}

	// GT from dissolve_test_xfs.json (no dissolve at frame 0; the final
	// junction at ~3596 is a hard cut).
	gtStarts := []int{300, 599, 899, 1199, 1499, 1798, 2098, 2398, 2697, 2997, 3297}
	gtDurs := []int{7, 11, 15, 22, 30, 45, 60, 75, 90, 105, 120}

	a := &LookaheadAnalyzer{
		HardCutThreshold: 0.30,
		ExcessThreshold:  0.10,
		AggWindow:        150,
		DissolveMinLen:   2,
		DissolveMaxLen:   150,
		MinSceneLen:      8,
		// Default prediction-failure (saturation) level of 0.985.
	}
	trs, err := a.Analyze(m)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	var dissolves []SceneTransition
	for _, tr := range trs {
		if tr.Type == TransitionDissolve {
			dissolves = append(dissolves, tr)
		}
	}
	t.Logf("%d transitions total, %d dissolves", len(trs), len(dissolves))

	hits := 0
	var absStartErrs []int
	matched := make([]bool, len(dissolves))
	for gi, gs := range gtStarts {
		gd := gtDurs[gi]
		bestIdx, bestErr := -1, 1<<30
		for di, d := range dissolves {
			if matched[di] {
				continue
			}
			// Overlap with the GT blend interval [gs-1, gs+gd].
			if d.StartFrame <= gs+gd && d.EndFrame >= gs-1 {
				e := abs(d.StartFrame - (gs - 1))
				if e < bestErr {
					bestErr = e
					bestIdx = di
				}
			}
		}
		if bestIdx < 0 {
			t.Logf("GT %2d  start=%4d dur=%3d  MISS", gi, gs, gd)
			continue
		}
		matched[bestIdx] = true
		hits++
		d := dissolves[bestIdx]
		durErr := (d.EndFrame - d.StartFrame - 1) - gd // detected blend frames − GT blend frames
		absStartErrs = append(absStartErrs, abs(d.StartFrame-(gs-1)))
		t.Logf("GT %2d  start=%4d dur=%3d  det=[%4d,%4d] score=%.2f  startErr=%+d durErr=%+d",
			gi, gs, gd, d.StartFrame, d.EndFrame, d.Score, d.StartFrame-(gs-1), durErr)
	}
	extra := 0
	for di, d := range dissolves {
		if !matched[di] {
			extra++
			t.Logf("EXTRA dissolve [%4d,%4d] score=%.2f", d.StartFrame, d.EndFrame, d.Score)
		}
	}

	sortInts(absStartErrs)
	medStartErr := 0
	if len(absStartErrs) > 0 {
		medStartErr = absStartErrs[len(absStartErrs)/2]
	}
	t.Logf("hits=%d/%d extras=%d medStartErr=%d", hits, len(gtStarts), extra, medStartErr)

	if hits < 11 {
		t.Errorf("expected all 11 GT dissolves detected, got %d", hits)
	}
	if medStartErr > 4 {
		t.Errorf("median |start error| = %d frames, want <= 4", medStartErr)
	}
	if extra > 2 {
		t.Errorf("too many spurious dissolves: %d", extra)
	}
}

// sortInts sorts a small int slice in place (test helper).
func sortInts(s []int) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// -----------------------------------------------------------------------------
// New harness for iterative tuning of dissolve start/end on the motion-scenes
// synthetic (sequence_editor linear cross-fades with the GT table the user
// provided). Self-contained (hard-coded GT + observed baseline predictions from
// the jsonl) so it can run in CI without external volumes. The goal is to
// drive param sweeps (coarse_prediction_distance, refined_prediction_distances,
// agg_window, etc.) and small algorithm tweaks until we have 12/12 recall with
// low start/dur error and almost no junk wide low-score companions.
// -----------------------------------------------------------------------------

type gtDissolve struct {
	start, end, dur int
}

func motionScenesGT() []gtDissolve {
	const fps = 29.97
	starts := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120}
	durs := []float64{0.125, 0.25, 0.375, 0.5, 0.75, 1, 1.5, 2, 2.5, 3, 3.5, 4}
	out := make([]gtDissolve, len(starts))
	for i := range starts {
		s := int(starts[i]*fps + 0.5)
		d := int(durs[i]*fps + 0.5)
		out[i] = gtDissolve{start: s, end: s + d, dur: d}
	}
	return out
}

type predDissolve struct {
	start, end int
	score      float64
}

// Hard-coded from the user's dissolve_scene_changes.jsonl (the 22 events
// produced by the aggressive params in dissolve_test_motion_scenes2.json).
func motionScenesPredsBaseline() []predDissolve {
	raw := []struct {
		start, dur int
		score      float64
	}{
		{119, 275, 0.4574}, {120, 82, 0.4636},
		{299, 16, 0.9957}, {310, 324, 0.4445},
		{597, 12, 0.99295}, {899, 17, 0.9954}, {926, 324, 0.4677},
		{1198, 17, 0.9919}, {1208, 324, 0.4502},
		{1486, 96, 0.9954}, {1599, 324, 0.4485},
		{1800, 31, 0.9924}, {2095, 73, 0.9944}, {2138, 324, 0.4595},
		{2403, 61, 0.99096}, {2461, 324, 0.4279},
		{2694, 72, 0.9933}, {2795, 324, 0.4476},
		{3000, 94, 0.9922}, {3136, 324, 0.4445},
		{3415, 324, 0.4554}, {3613, 98, 0.9882},
	}
	p := make([]predDissolve, len(raw))
	for i, r := range raw {
		p[i] = predDissolve{start: r.start, end: r.start + r.dur, score: r.score}
	}
	return p
}

func matchAndReport(t *testing.T, gts []gtDissolve, preds []predDissolve) {
	type hit struct {
		gi, pi, se, de int
	}
	var hits []hit
	used := make([]bool, len(preds))
	for gi, g := range gts {
		best := -1
		bestErr := 1 << 30
		gc := (g.start + g.end) / 2
		for pi, p := range preds {
			if used[pi] {
				continue
			}
			pc := (p.start + p.end) / 2
			ov := max(0, min(g.end, p.end)-max(g.start, p.start))
			if ov > 0 || abs(pc-gc) <= 30 {
				e := abs(p.start - g.start)
				if e < bestErr {
					bestErr = e
					best = pi
				}
			}
		}
		if best >= 0 {
			p := preds[best]
			used[best] = true
			hits = append(hits, hit{gi, best, p.start - g.start, (p.end - p.start) - g.dur})
		}
	}
	junk := 0
	for i, p := range preds {
		if !used[i] && (p.end-p.start > 150 || p.score < 0.6) {
			junk++
		}
	}

	t.Logf("GTs=%d  Preds=%d  Hits=%d  Junk(wide/low-score)=%d", len(gts), len(preds), len(hits), junk)
	t.Logf("GT# GTs  GTd | pStart pDur | sErr dErr | score")
	for _, h := range hits {
		g := gts[h.gi]
		p := preds[h.pi]
		t.Logf("%3d %4d %4d | %6d %4d | %4d %4d | %.2f",
			h.gi, g.start, g.dur, p.start, p.end-p.start, h.se, h.de, p.score)
	}
	if len(hits) < 10 || junk > 5 {
		t.Logf("Baseline shows the characteristic pattern (good high-score short events + many junk wide low-score events). Use this test for iterative improvement.")
	}
}

func TestAnalyze_DissolveAccuracy_MotionScenes_Baseline(t *testing.T) {
	gts := motionScenesGT()
	preds := motionScenesPredsBaseline()
	matchAndReport(t, gts, preds)
	// This test documents the starting point. Once we have tuned params or
	// small algorithm changes that improve the numbers (fewer junk, better
	// start/dur errors on the 12 GTs, still 12/12 recall), we will add
	// concrete assertions here and in a sweep helper.
}

// TestPlateauConsensus_DropsSubDurationLags verifies the cross-lag consistency
// gate: saturation physically requires k > D, but real SATD concavity pushes
// mid-blend ratios at k ≲ D over the threshold, producing group members whose
// runs and feet sit INSIDE the blend. Their own measured d is under-stated for
// the same reason, so the per-member d ≤ k+k/4 gate passes them and they drag
// the endpoint medians into the blend (measured on real footage: a 105-frame
// blend's end consensus came from three sub-duration lags, landing 31 frames
// early). The gate must derive the duration from the longest lags and drop
// members whose lag cannot span it.
func TestPlateauConsensus_DropsSubDurationLags(t *testing.T) {
	const N = 600
	const S, D = 150, 100 // blend [150,250); E = 250
	alpha := func(f int) float64 {
		if f < S {
			return 0
		}
		if f >= S+D {
			return 1
		}
		return float64(f-S+1) / float64(D+1)
	}
	// Long lags (k > D) follow the true linear geometry; sub-duration lags
	// (k < D) get a synthetic concave mid-blend bump: a saturated trapezoid
	// centred mid-blend whose feet land well inside the blend, with measured
	// width ≈ 50 ≤ k+k/4 so the per-member gate cannot reject it.
	bump := func(j int) float64 {
		const mid = S + D/2 // 200
		d := j - mid
		if d < 0 {
			d = -d
		}
		switch {
		case d <= 15:
			return 1.0
		case d <= 25:
			return 1.0 - float64(d-15)/10
		default:
			return 0
		}
	}
	m := makeMatrixWithLags(N, []int{45, 60, 120, 135}, func(j, lagVal int) float32 {
		var mm float64
		if lagVal > D {
			mm = alpha(j) - alpha(j-lagVal)
			if mm < 0 {
				mm = -mm
			}
		} else {
			mm = bump(j)
		}
		return float32(0.30 + 0.695*mm)
	}, nil)

	a := &LookaheadAnalyzer{DissolveMaxLen: 130}
	cfg := a.withDefaults(m.L)
	norm := cfg.normalizeMatrix(m)
	out := cfg.detectPlateauDissolves(norm, m)
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(out), out)
	}
	ev := out[0]
	if ev.underLagged {
		t.Errorf("event flagged underLagged despite two spanning lags")
	}
	// Without the gate the end median includes the bumps' feet (~225) and
	// lands ~12 early; with it, only the spanning lags vote.
	if abs(ev.StartFrame-(S-1)) > 2 || abs(ev.EndFrame-(S+D)) > 2 {
		t.Errorf("got [%d,%d], want [%d,%d] ±2", ev.StartFrame, ev.EndFrame, S-1, S+D)
	}
}

// TestPlateauConsensus_FlagsUnderLagged: when NO measured lag spans the
// group's own duration estimate (every member saturated mid-blend), the
// bounds are untrustworthy but the event is real — it must be kept and
// flagged so the staged pass re-measures the region with longer lags.
func TestPlateauConsensus_FlagsUnderLagged(t *testing.T) {
	const N = 600
	const S, D = 150, 100
	// Per-lag trapezoid centred mid-blend, shaped like real mid-blend
	// saturation: wide enough that the trailing foot minus k still lands
	// past the leading foot (the member survives rampFeet with a small
	// positive d that passes its own d <= k+k/4 gate), with the saturated
	// run starting far before that measured end (hs << e, the under-lagged
	// signature).
	bump := func(j, k int) float64 {
		mid, half, ramp := S+D/2, k/2+4, 8
		d := j - mid
		if d < 0 {
			d = -d
		}
		switch {
		case d <= half:
			return 1.0
		case d <= half+ramp:
			return 1.0 - float64(d-half)/float64(ramp)
		default:
			return 0
		}
	}
	m := makeMatrixWithLags(N, []int{45, 60}, func(j, lagVal int) float32 {
		return float32(0.30 + 0.695*bump(j, lagVal))
	}, nil)

	a := &LookaheadAnalyzer{DissolveMaxLen: 130}
	cfg := a.withDefaults(m.L)
	norm := cfg.normalizeMatrix(m)
	out := cfg.detectPlateauDissolves(norm, m)
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(out), out)
	}
	if !out[0].underLagged {
		t.Errorf("event not flagged underLagged; got %+v", out[0])
	}
}
