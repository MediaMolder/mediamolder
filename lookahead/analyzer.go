// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import (
	"fmt"
	"math"
	"sort"
)

// TransitionType classifies the nature of a detected scene transition.
type TransitionType int

const (
	TransitionHardCut  TransitionType = iota // abrupt scene change (1 frame)
	TransitionDissolve                       // gradual blend between scenes
	TransitionFadeIn                         // fade from black/white into scene
	TransitionFadeOut                        // fade from scene to black/white
	TransitionFlash                          // single anomalous frame; filtered out
)

// SceneTransition describes one detected scene transition.
type SceneTransition struct {
	Type       TransitionType
	StartFrame int     // last unblended frame before the transition (0-based); equals EndFrame for hard cuts
	EndFrame   int     // first unblended frame after the transition; for hard cuts this is the first frame of the new scene
	Score      float64 // peak S[c] value

	// underLagged marks a plateau dissolve whose consensus group had no two
	// distinct lags spanning its own duration estimate — the bounds are
	// suspect (mid-blend saturation) and the staged pass should re-measure
	// the region with longer lags before trusting them.
	underLagged bool
}

// plateauEstimate is one per-lag measurement of a dissolve, derived from a
// saturated run of ratio values (a "plateau") on a single lag signal.
//
// For a cross-fade occupying frames [S, S+D) (S = first blended frame,
// E = S+D = first pure frame of the new scene), every lag k > D saturates
// around the frames j ∈ [E, S+k−1]: the reference j−k is pure old-scene
// content while j is pure new-scene content, so inter prediction fails
// completely.  On real footage the threshold crossings sit partway up the
// ramps flanking this saturated core (SATD mismatch rises steeply at small
// blend fractions), so the raw run bounds are biased estimators.  The blend
// bounds are instead recovered from the ramp feet: a two-point linear
// extrapolation through the 50% and 20% crossing levels of each ramp
// projects it down to its local baseline.  The leading-ramp foot lands on
// the last unblended frame before S, and the trailing-ramp foot lands on
// E+k (the last frame whose reference still falls inside the blend), so
// every lag k > D yields an independent estimate of S and E.  Lags k ≤ D
// never saturate and drop out automatically; the per-event consensus is the
// median across qualifying lags.
type plateauEstimate struct {
	k     int     // lag value
	li    int     // lag column index in the matrix (norm[j][li])
	hs    int     // first frame of the saturated run
	he    int     // last frame of the saturated run
	s     int     // estimated first blended frame (leading ramp foot + 1)
	e     int     // estimated blend end = first pure new-scene frame (trailing foot − k)
	level float64 // mean ratio over the run
}

// LookaheadAnalyzer analyses a CostMatrix produced by LookaheadScanner and
// returns the list of detected scene transitions.  Transitions of type
// TransitionFlash are filtered out before returning.
//
// Zero-value fields are replaced with documented defaults by Analyze.
type LookaheadAnalyzer struct {
	// FullresProvider, when non-nil, enables full-resolution edge
	// measurement around each detected dissolve in AnalyzeStaged (phase 1:
	// measurements are logged to the matrix/CSV only; edges are not
	// changed). See fullres.go.
	FullresProvider FrameProvider

	// HardCutThreshold is the minimum score surface value S[c] required to
	// call a frame a scene-change candidate.
	// Default: 0.50 (adjusted > bias floor ≈0.44 of accurate x264 lowres costs with lam=1).
	HardCutThreshold float64

	// ExcessThreshold is the minimum excess E[j][k] that counts as transition
	// activity when measuring the active-lag width for transition typing.
	// Default: 0.15.
	ExcessThreshold float64

	// DissolveMinLen is the minimum active-lag width to classify a transition
	// as a dissolve or fade rather than a hard cut.
	// Default: 3.
	DissolveMinLen int

	// DissolveMaxLen caps how wide a dissolve/fade region can be.
	// If 0, the analyser uses L/2 (minimum 1).
	DissolveMaxLen int

	// AggWindow is the number of rows used in temporal aggregation (step 2e).
	// Default: 5.
	AggWindow int

	// MinSceneLen is the minimum number of frames between two consecutive
	// emitted scene boundaries.
	// Default: 15.
	MinSceneLen int

	// FadeBlackThreshold is the normalised luma level [0–1] below which a
	// frame is considered dark (part of a fade to black).
	// At 8-bit depth this maps to AvgLuma < FadeBlackThreshold × 255.
	// Default: 0.10 (luma < 25.5 out of 255).
	// (Fallback valley detector; digital+linear ramp path prefers exact ~0/255 anchors per spec.)
	FadeBlackThreshold float64

	// FadeWhiteThreshold is the normalised luma level [0–1] above which a
	// frame is considered bright (part of a fade to white).
	// At 8-bit depth this maps to AvgLuma > FadeWhiteThreshold × 255.
	// Set above 1.0 to disable white-fade detection entirely.
	// Default: 0.90 (luma > 229.5 out of 255).
	FadeWhiteThreshold float64

	// FadeMinLen is the minimum number of consecutive dark/bright frames
	// required to treat a luma valley/peak as a fade rather than noise.
	// Also applies as min ramp frames for digital b/w + linear ramp detection.
	// Default: 3.
	FadeMinLen int

	// FadeMaxLen caps the dark/bright region width; an interval longer than
	// this is classified as programme black/white rather than a fade and is
	// not emitted.
	// Default: 120 (≈ 5 s at 24 fps).
	FadeMaxLen int

	// PredictionFailureThreshold is the inter/intra cost-ratio level (near 1.0)
	// at which temporal prediction from a reference is treated as having
	// completely failed — i.e. the frame looks like a different scene than its
	// reference, the signature of a blend/cut. It is the plateau saturation
	// level used to extract saturated runs: higher is stricter (only the most
	// fully-failed frames count), lower captures more of the flanking ramps.
	// Default defaultSaturation (0.985).
	//
	// PredictionFailureThresholds optionally overrides it per reference distance
	// (lag), keyed by lag value (5, 15, 45, ...); a distance without an entry
	// falls back to PredictionFailureThreshold.
	//
	// (Formerly DefaultHighRatioThreshold / HighRatioThresholds, which could only
	// raise a fixed 0.985 floor; this is now the saturation level itself.)
	PredictionFailureThresholds map[int]float64
	PredictionFailureThreshold  float64
}

// EmissionLag is deprecated and retained only for compatibility during the
// streaming removal. It now always returns 0. Streaming mode has been removed
// entirely; the detector is batch-only (see LookaheadDetector.PostProcess and
// the scene_change_mc processor Close path). Callers that previously used the
// return value for LookbackFrames / delay buffering should now assume results
// are only available after the full input has been processed.
func (a LookaheadAnalyzer) EmissionLag(l int) int {
	return 0
}

// predictionFailureThreshold returns the prediction-failure (plateau
// saturation) level for the given reference distance (lag). Per-distance
// values (from PredictionFailureThresholds) take precedence; otherwise
// PredictionFailureThreshold (or 0.97) is used.
func (cfg LookaheadAnalyzer) predictionFailureThreshold(lag int) float64 {
	if cfg.PredictionFailureThresholds != nil {
		if t, ok := cfg.PredictionFailureThresholds[lag]; ok {
			return t
		}
	}
	if cfg.PredictionFailureThreshold != 0 {
		return cfg.PredictionFailureThreshold
	}
	return defaultSaturation
}

// withDefaults returns a copy of a with zero fields replaced by defaults.
func (a LookaheadAnalyzer) withDefaults(l int) LookaheadAnalyzer {
	if a.HardCutThreshold == 0 {
		a.HardCutThreshold = 0.50 // > bias floor ~0.44 from x264-accurate lowres costs.
		// For clean synthetic linear blends (e.g. sequence_editor dissolves in
		// dissolve_test_xfs.json), the surface peaks can be lower (0.2-0.5 range
		// observed in dissolve_test_x264_costs.csv); use the "threshold" param on
		// scene_change_mc (0.30-0.35) or larger AggWindow for those cases.
	}
	if a.ExcessThreshold == 0 {
		a.ExcessThreshold = 0.15 // lowered from 0.20 for better sensitivity on gradual blends while still filtering noise.
	}
	if a.DissolveMinLen == 0 {
		a.DissolveMinLen = 2 // allow short leading-edge of a dissolve or very short synthetic blends to qualify.
	}
	if a.DissolveMaxLen == 0 {
		a.DissolveMaxLen = l / 2
		if a.DissolveMaxLen < 1 {
			a.DissolveMaxLen = 1
		}
	}
	if a.AggWindow == 0 {
		a.AggWindow = 5
	}
	if a.MinSceneLen == 0 {
		a.MinSceneLen = 15
	}
	if a.FadeBlackThreshold == 0 {
		a.FadeBlackThreshold = 0.10
	}
	if a.FadeWhiteThreshold == 0 {
		a.FadeWhiteThreshold = 0.90
	}
	if a.FadeMinLen == 0 {
		a.FadeMinLen = 3
	}
	if a.FadeMaxLen == 0 {
		a.FadeMaxLen = 120
	}
	if a.PredictionFailureThreshold == 0 {
		a.PredictionFailureThreshold = defaultSaturation
	}
	if a.PredictionFailureThresholds == nil {
		a.PredictionFailureThresholds = make(map[int]float64)
	}
	return a
}

// Analyze runs the full M4 pipeline on m and returns detected transitions.
func (a *LookaheadAnalyzer) Analyze(m *CostMatrix) ([]SceneTransition, error) {
	if m == nil || m.N == 0 {
		return nil, fmt.Errorf("lookahead: CostMatrix is empty")
	}
	cfg := a.withDefaults(m.L)
	norm := cfg.normalizeMatrix(m)
	alpha, beta := cfg.fitBaselines(norm, m)
	excess := cfg.buildExcess(norm, m, alpha, beta)
	surface := cfg.buildScoreSurface(excess, m)
	peaks := cfg.findPeaks(surface)
	transitions := cfg.classifyTransitions(peaks, surface, excess, norm, m)
	transitions = cfg.applyFlashFilter(transitions, norm, m)

	// Add luma-based digital black/white + linear ramp fades (batch path).
	// These are independent of the lag schedule and are found in the
	// PostProcess / full-matrix path.
	lumaFades := cfg.detectBatchDigitalLumaFades(m)
	if len(lumaFades) > 0 {
		// simple append; in practice cost peaks may overlap but luma ones preferred for fades
		transitions = append(transitions, lumaFades...)
		// caller can sort if needed, but usually already ordered
	}

	// Plateau-based dissolve detection: per-lag high-ratio plateau runs encode
	// the blend bounds exactly (see plateauEstimate).  Only engage for
	// schedules that contain "long" lags; small-L unit-test matrices keep the
	// surface-only behaviour.
	var plateaus []SceneTransition
	if len(m.Lags) > 0 && m.Lags[len(m.Lags)-1] >= 20 {
		plateaus = cfg.detectPlateauDissolves(norm, m)
	}

	// De-dupe: the surface path can produce "dissolve" artifacts on pure hard
	// cuts (long post-cut tail on large k's).  When a surface dissolve is very
	// close to a hard cut, keep the hard cut.
	cleaned := transitions[:0]
	for i, t := range transitions {
		keep := true
		if t.Type == TransitionDissolve {
			for j := 0; j < i; j++ {
				ot := transitions[j]
				if ot.Type == TransitionHardCut && absInt(ot.StartFrame-t.StartFrame) < 5 {
					keep = false
					break
				}
			}
		}
		if keep {
			cleaned = append(cleaned, t)
		}
	}
	transitions = cleaned

	// Merge: when plateau measurement found dissolves it owns dissolve
	// reporting for this window.  Surface-path dissolve typing is unreliable
	// on long-lag schedules (temporal aggregation smears long-lag excess into
	// wide dissolve-shaped responses across ordinary content), so all
	// surface dissolves are dropped; a real blend always saturates the lags
	// longer than itself and is therefore covered by a plateau measurement.
	// Hard cuts are kept unless they fall inside a measured blend.
	if len(plateaus) > 0 {
		kept := transitions[:0]
		for _, t := range transitions {
			drop := t.Type == TransitionDissolve
			if t.Type == TransitionHardCut {
				for _, pd := range plateaus {
					if t.StartFrame <= pd.EndFrame+2 && t.EndFrame >= pd.StartFrame-2 {
						drop = true
						break
					}
				}
			}
			if !drop {
				kept = append(kept, t)
			}
		}
		transitions = append(kept, plateaus...)
		sort.Slice(transitions, func(i, j int) bool {
			return transitions[i].StartFrame < transitions[j].StartFrame
		})
	}

	return transitions, nil
}

// absInt is a tiny local helper (no extra imports).
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// detectBatchDigitalLumaFades scans the full CostMatrix AvgLuma for digital
// (near 0/255) frames reached via near-linear ramp, emitting anchored
// FadeOut (end at digital) / FadeIn (start at digital).
func (cfg LookaheadAnalyzer) detectBatchDigitalLumaFades(m *CostMatrix) []SceneTransition {
	if m == nil || m.N == 0 {
		return nil
	}
	var out []SceneTransition
	// Use same relaxed near-digital threshold as the (now removed) streaming
	// fade logic (lowres hpel mean can read 0.000x-0.13 on solid black).
	const digitalEps = 1.0
	const minR2 = 0.60 // match stream relaxation for artistic real-data ramps
	minL := cfg.FadeMinLen
	if minL < 2 {
		minL = 2
	}
	i := 0
	for i < m.N {
		l := m.AvgLuma[i]
		isBlack := l <= digitalEps
		isWhite := l >= 255.0-digitalEps
		if !isBlack && !isWhite {
			i++
			continue
		}
		toWhite := isWhite
		// Refined collection (ramp only, no flat tail; digCount>=1 sufficient on landing).
		var seqL, seqF []float64
		bound := 32.0
		if toWhite {
			bound = 224.0
		}
		// find first (most recent in small window) digital using consistent eps
		firstDig := i
		for j := i; j >= 0 && j > i-5; j-- {
			ll := float64(m.AvgLuma[j])
			if (toWhite && ll >= 254.0) || (!toWhite && ll <= 1.0) {
				firstDig = j
			}
		}
		// count consec digital at/after the hit (allow 1 on the crossing frame itself)
		digCnt := 0
		for j := i; j >= 0; j-- {
			ll := float64(m.AvgLuma[j])
			if (toWhite && ll >= 254.0) || (!toWhite && ll <= 1.0) {
				digCnt++
			} else {
				break
			}
		}
		if digCnt < 1 {
			i++
			continue
		}
		// collect ramp leading to firstDig (exclude flats)
		seqL = nil
		seqF = nil
		j := firstDig
		ll := float64(m.AvgLuma[j])
		seqL = append([]float64{ll}, seqL...)
		seqF = append([]float64{float64(j)}, seqF...)
		prevL := ll
		for j = firstDig - 1; j >= 0; j-- {
			ll = float64(m.AvgLuma[j])
			if toWhite {
				if ll < bound {
					break
				}
				if ll > prevL+2 {
					break
				}
			} else {
				if ll > bound {
					break
				}
				if ll < prevL-2 {
					break
				}
			}
			seqL = append([]float64{ll}, seqL...)
			seqF = append([]float64{float64(j)}, seqF...)
			prevL = ll
			if len(seqL) > 200 {
				break
			}
		}
		if len(seqL) >= minL {
			slope, _, rr := linReg(seqF, seqL)
			r2 := rr * rr
			endL := seqL[len(seqL)-1]
			goodEnd := (toWhite && endL >= 254.0) || (!toWhite && endL <= 1.0)
			goodSlope := (toWhite && slope > 0) || (!toWhite && slope < 0)
			accept := (r2 >= minR2 && goodEnd && goodSlope)
			if !accept && !toWhite && endL <= 1.5 && digCnt >= 1 {
				// bypass for real dim-to-black like casino (pre >3 to accept ~14 start)
				has := false
				for _, v := range seqL {
					if v > 3.0 {
						has = true
						break
					}
				}
				if has {
					accept = true
				}
			}
			if accept {
				var score float64
				if !toWhite {
					score = 1.0 - float64(endL)/255.0
				} else {
					score = float64(endL) / 255.0
				}
				if score < 0 {
					score = 0
				} else if score > 1 {
					score = 1
				}
				rStart := int(seqF[0])
				fadeOutS := rStart - 1
				if fadeOutS < 0 {
					fadeOutS = 0
				}
				out = append(out, SceneTransition{
					Type:       TransitionFadeOut,
					StartFrame: fadeOutS,
					EndFrame:   firstDig, // anchor at digital
					Score:      score,
				})
				// plateau end
				plateauEnd := i
				for k := i + 1; k < m.N; k++ {
					kk := float64(m.AvgLuma[k])
					if (toWhite && kk >= 254.0) || (!toWhite && kk <= 1.0) {
						plateauEnd = k
					} else {
						break
					}
				}
				out = append(out, SceneTransition{
					Type:       TransitionFadeIn,
					StartFrame: plateauEnd,
					EndFrame:   plateauEnd,
					Score:      score,
				})
				i = plateauEnd + 1
				continue
			}
		}
		i++
	}
	return out
}

// normalizeMatrix clamps all Ratio values to [0, 1].
// Returns a new [][]float64 of dimensions [N][len(Lags)].
func (cfg LookaheadAnalyzer) normalizeMatrix(m *CostMatrix) [][]float64 {
	out := make([][]float64, m.N)
	for j := 0; j < m.N; j++ {
		row := make([]float64, len(m.Lags))
		for i := range m.Lags {
			r := float64(m.Ratio[j][i])
			if r < 0 {
				r = 0
			} else if r > 1 {
				r = 1
			}
			row[i] = r
		}
		out[j] = row
	}
	return out
}

// fitBaselines fits a per-frame linear model α + β·k to the early (k ≤ 5)
// inlier lags for each frame j.  An inlier is a lag where ratio[j][i] < 0.35
// (avoiding fitting the transition itself). For slow dissolves/fades the
// early-lag window is deliberately small (dense prefix) and we fall back to
// the *min* early ratio when no clean inliers — this keeps baseline low so
// excess remains visible across long gradual transitions.
//
// k is the actual lag value (from m.Lags); i is its index in norm[j].
//
// Returns alpha[N] and beta[N].
func (cfg LookaheadAnalyzer) fitBaselines(norm [][]float64, m *CostMatrix) (alpha, beta []float64) {
	const inlierThreshold = 0.35
	const maxBaselineLag = 5

	alpha = make([]float64, m.N)
	beta = make([]float64, m.N)

	for j := 0; j < m.N; j++ {
		// Ordinary least-squares on inlier lags where k ≤ maxBaselineLag and k < j.
		var n, sx, sy, sxx, sxy float64
		var anyRef bool
		for i, k := range m.Lags {
			if k > maxBaselineLag || k >= j {
				break // lags are sorted
			}
			anyRef = true
			if norm[j][i] < inlierThreshold {
				x := float64(k)
				y := norm[j][i]
				n++
				sx += x
				sy += y
				sxx += x * x
				sxy += x * y
			}
		}

		switch {
		case !anyRef:
			// frame 0 — no references at all, leave alpha=0
		case n == 0:
			// No clean inliers (common in slow ramps): use *min* of available
			// early ratios as baseline. This is conservative for gradual
			// transitions — keeps baseline low so excess = ratio - min stays
			// high over the dissolve width instead of being absorbed into the fit.
			var minR float64 = 1
			var any bool
			for i, k := range m.Lags {
				if k >= j {
					break
				}
				if k <= maxBaselineLag {
					if norm[j][i] < minR {
						minR = norm[j][i]
					}
					any = true
				}
			}
			if any {
				alpha[j] = minR
			} else {
				// fallback to mean of whatever we have
				var sum float64
				var cnt int
				for i, k := range m.Lags {
					if k >= j {
						break
					}
					sum += norm[j][i]
					cnt++
				}
				if cnt > 0 {
					alpha[j] = sum / float64(cnt)
				}
			}
		case n == 1:
			alpha[j] = sy
		default:
			denom := n*sxx - sx*sx
			if math.Abs(denom) < 1e-12 {
				alpha[j] = sy / n
			} else {
				b := (n*sxy - sx*sy) / denom
				a := (sy - b*sx) / n
				alpha[j] = a
				beta[j] = b
			}
		}
	}
	return
}

// buildExcess computes E[j][i] = max(0, norm[j][i] − baseline(j, Lags[i])).
// The returned slice has dimensions [N][len(Lags)].
func (cfg LookaheadAnalyzer) buildExcess(norm [][]float64, m *CostMatrix, alpha, beta []float64) [][]float64 {
	excess := make([][]float64, m.N)
	for j := 0; j < m.N; j++ {
		row := make([]float64, len(m.Lags))
		for i, k := range m.Lags {
			e := norm[j][i] - (alpha[j] + beta[j]*float64(k))
			if e > 0 {
				row[i] = e
			}
		}
		excess[j] = row
	}
	return excess
}

// buildScoreSurface computes S[c] = mean E[j][idx(j−c+1)] for j ∈ [c, c+W−1],
// where idx(lag) is the position of the 1-based lag in m.Lags and
// lag = j−c+1 (reference frame = j−lag = c−1 along the anti-diagonal).
// Only lags present in the Fibonacci schedule contribute.
func (cfg LookaheadAnalyzer) buildScoreSurface(excess [][]float64, m *CostMatrix) []float64 {
	surface := make([]float64, m.N)
	w := cfg.AggWindow
	for c := 0; c < m.N; c++ {
		var sum float64
		var count int
		for j := c; j < c+w && j < m.N; j++ {
			lagVal := j - c + 1 // 1-based lag; reference = j−lagVal = c−1
			if lagVal > j {
				continue // reference c−1 < 0 (only when c=0)
			}
			idx := lagIndex(m.Lags, lagVal)
			if idx < 0 {
				continue // lag not in schedule
			}
			sum += excess[j][idx]
			count++
		}
		if count > 0 {
			surface[c] = sum / float64(count)
		}
	}
	return surface
}

// findPeaks returns the frame indices (ascending) where surface[c] exceeds
// HardCutThreshold.  Adjacent candidates within MinSceneLen are resolved in
// favour of the higher-scoring one (non-maximum suppression).
func (cfg LookaheadAnalyzer) findPeaks(surface []float64) []int {
	var candidates []int
	for c, s := range surface {
		if s > cfg.HardCutThreshold {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	kept := []int{candidates[0]}
	for _, c := range candidates[1:] {
		prev := kept[len(kept)-1]
		if c-prev < cfg.MinSceneLen {
			if surface[c] > surface[prev] {
				kept[len(kept)-1] = c
			}
		} else {
			kept = append(kept, c)
		}
	}
	return kept
}

// classifyTransitions assigns a TransitionType to each peak frame c.
//
// For each peak, it measures the active-lag width *within individual rows*
// across the aggregation window.  A hard cut produces excess at exactly one
// lag per row (k = j−c on the anti-diagonal); a dissolve produces excess
// spanning multiple lags within a single row.  The maximum single-row width
// determines the classification.
func (cfg LookaheadAnalyzer) classifyTransitions(peaks []int, surface []float64, excess [][]float64, norm [][]float64, m *CostMatrix) []SceneTransition {
	globalMean := meanIntraCost(m)
	out := make([]SceneTransition, 0, len(peaks))

	for _, c := range peaks {
		// Measure the maximum single-row active-lag width.
		// Scan a modestly wider window around c (not just [c,c+w)) so that
		// for long dissolves the full lag-span is captured even if the
		// surface peak is broad or offset. Width in lag frames (not indices).
		maxRowWidth := 0
		widestRowMin := -1
		widestRowMax := -1

		// Widen the scan significantly to capture the full temporal support of long-lag bad prediction
		// for long dissolves (up to ~200 frames lookback/lookahead around the peak).
		scanLo := max(0, c-200)
		scanHi := min(m.N, c+200)
		min_bad_j := m.N
		max_bad_j := -1

		// longLagStart: index into m.Lags from which lags are considered "long" for
		// this schedule. Using the upper half generalizes the old hard-coded fib
		// targets (8,13,21) and the fixed >=45 cutoff so that custom lag lists
		// (e.g. [5,15,30,45,60,75,90,105,120]) automatically use their own longer
		// entries for has_long_bad (min_bad_j collection) and longLagWidth.
		longLagStart := 0
		if len(m.Lags) > 0 {
			longLagStart = len(m.Lags) / 2
		}

		for j := scanLo; j < scanHi; j++ {
			rowMin := m.L + 1
			rowMax := -1
			rowMinLag := 0
			rowMaxLag := 0
			has_long_bad := false
			for i, k := range m.Lags {
				if k >= j {
					break
				}
				if excess[j][i] > cfg.ExcessThreshold {
					if i < rowMin {
						rowMin = i
						rowMinLag = k
					}
					if i > rowMax {
						rowMax = i
						rowMaxLag = k
					}
					if i >= longLagStart {
						has_long_bad = true
					}
				}
			}
			if rowMax >= 0 {
				rw := rowMaxLag - rowMinLag
				if rw > maxRowWidth {
					maxRowWidth = rw
					widestRowMin = rowMinLag
					widestRowMax = rowMaxLag
				}
			}
			if has_long_bad {
				if j < min_bad_j {
					min_bad_j = j
				}
				if j > max_bad_j {
					max_bad_j = j
				}
			}
		}

		// Long-lag specific width: if any of the longer lags in the *current*
		// schedule (upper half, starting at longLagStart) showed bad prediction
		// (excess) in the scan window around c, treat that as a blend signature.
		// This boosts effectiveWidth so the transition can qualify as a dissolve
		// (see usage below). Replaces the previous hard-coded fib targets
		// {8,13,21}. Works for both default fib schedules and custom "lags" lists.
		longLagWidth := 0
		for j := scanLo; j < scanHi; j++ {
			for ii := longLagStart; ii < len(m.Lags); ii++ {
				k := m.Lags[ii]
				if k >= j {
					break
				}
				if excess[j][ii] > cfg.ExcessThreshold {
					longLagWidth = max(longLagWidth, 1) // at least one long lag bad
					// could count distinct or max span, but presence is sufficient for synthetic
				}
			}
		}

		// Dual "future-span" (column persistence) using the reverse view on the *existing* matrix.
		// Per the symmetry presumption: if future frames are poorly predicted by a pre-c ref,
		// that pre-ref (as "current" from the future's perspective) is a persistently bad predictor.
		// For each pre-ref r < c seen in the scan, count the span of later j that have high excess
		// exactly at the lag that makes ref = j - lag = r. maxFutureSpan gives a vertical measure
		// complementary to horizontal rowWidth; either can qualify a gradual dissolve.
		maxFutureSpan := 0
		// Map from refFrame -> [minJ, maxJ] among j in scan that have high excess when referencing it.
		refSpans := map[int][2]int{}
		for j := scanLo; j < scanHi; j++ {
			for i, k := range m.Lags {
				if k >= j {
					break
				}
				if excess[j][i] > cfg.ExcessThreshold {
					refFrame := j - k
					if refFrame >= c { // only pre-c refs are "reverse" evidence for a transition at/near c
						continue
					}
					if sp, ok := refSpans[refFrame]; ok {
						if j < sp[0] {
							sp[0] = j
						}
						if j > sp[1] {
							sp[1] = j
						}
						refSpans[refFrame] = sp
					} else {
						refSpans[refFrame] = [2]int{j, j}
					}
				}
			}
		}
		for _, sp := range refSpans {
			spn := sp[1] - sp[0] + 1
			if spn > maxFutureSpan {
				maxFutureSpan = spn
			}
		}

		ttype := TransitionHardCut
		start, end := c, c

		effectiveWidth := maxRowWidth
		if maxFutureSpan > effectiveWidth {
			effectiveWidth = maxFutureSpan
		}
		if longLagWidth > 0 {
			effectiveWidth = max(effectiveWidth, 3) // treat presence of long-lag excess as equivalent to modest width for dissolve qualification
		}
		if effectiveWidth >= cfg.DissolveMinLen && effectiveWidth <= cfg.DissolveMaxLen {
			// For schedules using long lags (e.g. user's [15,30,45,60,90,120] for the
			// dissolve_test_xfs synthetic blends), prefer the actual temporal extent
			// of frames that showed high excess on long lags. This directly measures
			// the period of poor long-lag prediction caused by the linear cross-fade,
			// giving start/end much closer to the true blend window than lag-subtraction
			// (which shifts by ~lag/2).
			// Reject the span when it exceeds DissolveMaxLen: on real content
			// long-lag excess can stay above threshold for hundreds of frames
			// (natural decorrelation), and using that span verbatim produced
			// scan-window-wide artifacts.  Plateau detection supplies accurate
			// bounds for those events instead.
			blendStart, blendEnd := c, c
			if min_bad_j < m.N && max_bad_j >= 0 && max_bad_j-min_bad_j <= cfg.DissolveMaxLen {
				blendStart = min_bad_j
				blendEnd = max_bad_j
			} else if widestRowMax > 0 || widestRowMin > 0 {
				// fallback for short-lag or fib schedules
				blendStart = c - widestRowMax
				if blendStart < 0 {
					blendStart = 0
				}
				blendEnd = c - widestRowMin
				if blendEnd < blendStart {
					blendEnd = blendStart
				}
			} else if maxFutureSpan > 0 {
				// Derive from the j-span of the persistent bad pre-refs.
				minJ, maxJ := m.N, 0
				for _, sp := range refSpans { // refSpans still in scope from the scan above
					if sp[0] < minJ {
						minJ = sp[0]
					}
					if sp[1] > maxJ {
						maxJ = sp[1]
					}
				}
				if minJ < m.N {
					blendStart = minJ - 1
					if blendStart < 0 {
						blendStart = 0
					}
				}
				if maxJ > 0 {
					blendEnd = maxJ + 1
					if blendEnd >= m.N {
						blendEnd = m.N - 1
					}
				}
			}

			// Fade detection: intra cost suppressed in the blended region.
			fadeSig := false
			if globalMean > 0 {
				var localSum float64
				count := 0
				for f := blendStart; f <= blendEnd && f < m.N; f++ {
					localSum += float64(m.IntraCost[f])
					count++
				}
				if count > 0 && localSum/float64(count) < 0.5*globalMean {
					fadeSig = true
				}
			}

			if fadeSig {
				if blendStart > 0 && float64(m.IntraCost[blendStart-1]) < 0.5*globalMean {
					ttype = TransitionFadeIn
				} else {
					ttype = TransitionFadeOut
				}
			} else {
				ttype = TransitionDissolve
			}
			// Set start/end to the unblended frames surrounding the blended region.
			// start: last unblended frame before blending begins.
			// end:   first unblended frame after blending ends.
			start = blendStart - 1
			if start < 0 {
				start = 0
			}
			end = blendEnd + 1
			if end >= m.N {
				end = m.N - 1
			}
		}

		out = append(out, SceneTransition{
			Type:       ttype,
			StartFrame: start,
			EndFrame:   end,
			Score:      surface[c],
		})
	}
	return out
}

// applyFlashFilter removes TransitionHardCut entries that are part of a flash
// pair: two adjacent hard cuts separated by ≤ 1 frame where the "new scene"
// does not persist (R[c+2][lag=1] is high, meaning frame c+2 is not similar to
// c+1).  k=1 is always present in the Fibonacci schedule.
//
// Mirrors x264's scenecut() flash filter using the full cost matrix.
func (cfg LookaheadAnalyzer) applyFlashFilter(transitions []SceneTransition, norm [][]float64, m *CostMatrix) []SceneTransition {
	if len(transitions) < 2 {
		return transitions
	}
	// Index of k=1 in m.Lags (always 0 in the Fibonacci schedule).
	lag1idx := lagIndex(m.Lags, 1)
	flash := make([]bool, len(transitions))
	for i := 0; i < len(transitions)-1; i++ {
		ti := transitions[i]
		tj := transitions[i+1]
		if ti.Type != TransitionHardCut || tj.Type != TransitionHardCut {
			continue
		}
		if tj.StartFrame-ti.StartFrame > 2 {
			continue
		}
		// Flash heuristic: check whether the frame after the pair is stable.
		// R[c+2][lag=1]: how well c+2 is predicted by c+1.
		// If high → c+1 is an anomalous flash frame → mark both as flashes.
		c2 := ti.StartFrame + 2
		if c2 >= m.N || lag1idx < 0 {
			flash[i] = true
			flash[i+1] = true
			continue
		}
		if norm[c2][lag1idx] > cfg.HardCutThreshold {
			flash[i] = true
			flash[i+1] = true
		}
	}
	out := make([]SceneTransition, 0, len(transitions))
	for i, t := range transitions {
		if !flash[i] {
			out = append(out, t)
		}
	}
	return out
}

// meanIntraCost returns the mean of m.IntraCost, or 0 for an empty matrix.
func meanIntraCost(m *CostMatrix) float64 {
	if m.N == 0 {
		return 0
	}
	var sum float64
	for _, v := range m.IntraCost {
		sum += float64(v)
	}
	return sum / float64(m.N)
}

// max and min are small helpers (the package already uses max/min in other
// methods; these ensure the new multi-scale code compiles cleanly).
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// defaultSaturation is the default prediction-failure (plateau saturation)
// level when none is configured: the cost-ratio at which inter prediction is
// treated as having completely failed. It anchors run extraction on the
// saturated core, where the run structure is clean and consistent across lags.
// Callers can override it (globally or per reference distance) via
// PredictionFailureThreshold / PredictionFailureThresholds.
const defaultSaturation = 0.985

// shortDissolveLen is the duration (frames) below which the baseline-free
// leading-edge start is used instead of the ramp-foot consensus. Below it the
// leading ramp is steep enough that the plateau corner is essentially the start
// and the absolute-step foot walk is reliable; above it the gradual ramp needs
// the scale-invariant relative-crossing extrapolation of the ramp foot.
const shortDissolveLen = 12

// detectPlateauDissolves finds dissolves by extracting per-lag saturated
// ratio runs, recovering each run's implied blend bounds from its ramp feet
// (see plateauEstimate), and clustering by midpoint the per-lag estimates that
// agree on the same transition.  At least two lags must agree before an event
// is emitted.  Consensus bounds use different lags per endpoint: the end is the
// median over the shortest saturating lags (k ≈ D, tightest trailing ramp); the
// start is the all-lag ramp-foot median, except for short blends (D <
// shortDissolveLen) where a baseline-free leading-edge foot is used instead
// (see leadingEdgeStart and dissolve_transition_detection_algorithm_new.md).
// Fade intervals (collapsed intra cost) are skipped here: the luma fade paths
// own those events.
func (cfg LookaheadAnalyzer) detectPlateauDissolves(norm [][]float64, m *CostMatrix) []SceneTransition {
	if len(m.Lags) == 0 || m.N == 0 {
		return nil
	}
	const minRun = 2
	const maxGap = 3 // bridge brief dropouts inside a saturated run

	var ests []plateauEstimate
	for i, k := range m.Lags {
		sat := cfg.predictionFailureThreshold(k)
		type run struct{ hs, he int }
		var runs []run
		for j := k + 1; j < m.N; {
			if norm[j][i] < sat {
				j++
				continue
			}
			hs := j
			for j < m.N && norm[j][i] >= sat {
				j++
			}
			he := j - 1
			for len(runs) > 0 && hs-runs[len(runs)-1].he-1 <= maxGap {
				hs = runs[len(runs)-1].hs
				runs = runs[:len(runs)-1]
			}
			runs = append(runs, run{hs, he})
		}
		reach := k + cfg.DissolveMaxLen
		for _, r := range runs {
			if r.he-r.hs+1 < minRun {
				continue
			}
			if r.he >= m.N-1 {
				continue // run truncated by end of stream: trailing foot unknowable
			}
			s, e, ok := cfg.rampFeet(norm, i, k, sat, r.hs, r.he, reach, m.N)
			if !ok {
				continue
			}
			d := e - s
			if s <= 0 || d < cfg.DissolveMinLen {
				continue // degenerate, or hard-cut territory (surface path owns those)
			}
			if cfg.DissolveMaxLen > 0 && d > cfg.DissolveMaxLen {
				continue
			}
			if d > k+k/4 {
				continue // a lag-k signal cannot saturate on a blend longer than k
			}
			sum := 0.0
			for q := r.hs; q <= r.he; q++ {
				sum += norm[q][i]
			}
			ests = append(ests, plateauEstimate{
				k: k, li: i, hs: r.hs, he: r.he, s: s, e: e,
				level: sum / float64(r.he-r.hs+1),
			})
		}
	}
	if len(ests) == 0 {
		return nil
	}

	// Sort by blend midpoint (s+e is 2× the midpoint) so per-lag estimates of
	// one event are adjacent. The midpoint is far more stable across lags than
	// either endpoint alone — leading- and trailing-foot errors are largely
	// independent and cancel — so it is the robust key for *grouping*.
	sort.Slice(ests, func(a, b int) bool {
		ma, mb := ests[a].s+ests[a].e, ests[b].s+ests[b].e
		if ma != mb {
			return ma < mb
		}
		return ests[a].k < ests[b].k
	})

	const nConsensus = 3 // longest/shortest lags blended into each endpoint

	globalMean := meanIntraCost(m)
	var out []SceneTransition
	ci := 0
	for ci < len(ests) {
		g0 := ests[ci]
		// Midpoint agreement tolerance: per-lag foot estimates of one event
		// scatter by a small fraction of D plus quantisation noise.
		tol := max(12, min(60, g0.e-g0.s))
		group := []plateauEstimate{g0}
		cj := ci + 1
		for cj < len(ests) && (ests[cj].s+ests[cj].e)-(g0.s+g0.e) <= 2*tol {
			group = append(group, ests[cj])
			cj++
		}
		ci = cj

		// Two-lag gate: every true blend of length D saturates all lags k > D,
		// so genuine events have multi-lag support; a lone saturated excursion
		// on real content is noise.
		if distinctLags(group) < 2 {
			continue
		}

		// Cross-lag consistency gate: saturation physically requires k > D, so
		// a member whose lag is shorter than the group's own duration estimate
		// cannot be a real plateau — it is mid-blend concave saturation (real
		// SATD response pushes mid-blend ratios over the threshold at k ≲ D),
		// whose run starts and feet land INSIDE the blend and poison both
		// endpoint medians (measured: a 105-frame blend's end consensus came
		// from three sub-duration lags and landed 31 frames early). The
		// per-member d ≤ k+k/4 gate cannot catch these because their own d is
		// under-measured for the same reason; the duration estimate must come
		// from the longest lags, which are the least truncated. When fewer
		// than two distinct lags survive, keep the unfiltered group (the event
		// is real, and the two-lag gate already passed).
		//
		// Separately, a valid member's saturated run STARTS at the blend end
		// (run = [E, S+k−1], so hs ≈ e); a run that starts well before its own
		// measured end is mid-blend saturation. When EVERY member shows that
		// signature, no measured lag spans the blend at all and the bounds —
		// especially the duration, which chose the region's lag set in the
		// first place — are under-measured: flag the emitted transition for
		// lag escalation (see AnalyzeStaged).
		underLagged := true
		for _, g := range group {
			if g.hs >= g.e-g.k/4 {
				underLagged = false
				break
			}
		}
		{
			byK := append([]plateauEstimate(nil), group...)
			sort.Slice(byK, func(a, b int) bool { return byK[a].k > byK[b].k })
			var dh []int
			for i := 0; i < len(byK) && i < 3; i++ {
				dh = append(dh, byK[i].e-byK[i].s)
			}
			dHat := medianInt(dh)
			kept := make([]plateauEstimate, 0, len(group))
			for _, g := range group {
				if g.k >= dHat {
					kept = append(kept, g)
				}
			}
			if distinctLags(kept) >= 2 {
				group = kept
			}
		}

		// End from the shortest few saturating lags: the shortest lags that still
		// saturate have k ≈ D and the tightest trailing ramp, whereas a long lag
		// k over-states the end of a short blend by ~k.
		byLag := append([]plateauEstimate(nil), group...)
		sort.Slice(byLag, func(a, b int) bool { return byLag[a].k < byLag[b].k })
		var ev []int
		for i := 0; i < len(byLag) && len(ev) < nConsensus; i++ {
			ev = append(ev, byLag[i].e)
		}
		e := medianInt(ev)

		// Start: the scale-invariant ramp-foot consensus (median of the per-lag
		// estimates) works across the full duration range and is used for all
		// but the shortest blends.
		ss := make([]int, len(group))
		for gi, g := range group {
			ss[gi] = g.s
		}
		s := medianInt(ss)

		// For short blends the leading ramp is only ~D frames, so the plateau
		// corner is essentially the start and a baseline-free leading-edge walk
		// localizes it without the ramp-foot percentile (unreliable when
		// high-motion content never settles to a low ratio between blends). We
		// restrict it to short blends because a baseline-free knee cannot be
		// scale-invariant: the longer, gradual ramps need the relative-crossing
		// extrapolation the ramp foot provides. See
		// dissolve_transition_detection_algorithm_new.md.
		if e-s < shortDissolveLen {
			// Use only the shortest few saturating lags: they have the cleanest
			// leading edge and a low pre-blend content level, so the cross-lag
			// average descends cleanly to the foot. Long lags (high in the
			// pre-blend content) would flatten the average and stall the walk.
			n := nConsensus
			if n > len(byLag) {
				n = len(byLag)
			}
			short := byLag[:n]
			hsv := make([]int, n)
			for i, g := range short {
				hsv[i] = g.hs
			}
			pLeft := medianInt(hsv)
			// The leading ramp of a short blend is at most ~D < shortDissolveLen
			// frames, so bound the walk-back tightly. Tying it to the saturated-run
			// width (he-hs) over-reaches badly when only long lags saturated — their
			// runs are ~k wide, which would let the walk drift through high long-lag
			// content all the way to the window edge.
			maxBack := 2 * shortDissolveLen
			s = leadingEdgeStart(norm, short, pLeft, maxBack)
		}
		if e-s < cfg.DissolveMinLen {
			continue
		}

		meanLevel := 0.0
		for _, g := range group {
			meanLevel += g.level
		}
		meanLevel /= float64(len(group))
		if meanLevel > 1 {
			meanLevel = 1
		}

		// Fade intervals (intra cost collapsed) belong to the luma fade paths.
		if globalMean > 0 {
			var sum float64
			cnt := 0
			for f := s; f <= e && f < m.N; f++ {
				sum += float64(m.IntraCost[f])
				cnt++
			}
			if cnt > 0 && sum/float64(cnt) < 0.5*globalMean {
				continue
			}
		}
		out = append(out, SceneTransition{
			Type:        TransitionDissolve,
			StartFrame:  max(0, s-1),
			EndFrame:    min(m.N-1, e),
			Score:       meanLevel,
			underLagged: underLagged,
		})
	}

	// NMS between plateau dissolves closer than MinSceneLen: keep the higher
	// score.
	if len(out) > 1 {
		kept := out[:0]
		for _, t := range out {
			if n := len(kept); n > 0 && t.StartFrame-kept[n-1].StartFrame < cfg.MinSceneLen {
				if t.Score > kept[n-1].Score {
					kept[n-1] = t
				}
				continue
			}
			kept = append(kept, t)
		}
		out = kept
	}
	return out
}

// leadingEdgeStart returns the dissolve start (first blended frame) as the foot
// of the leading edge, found without any baseline level.
//
// During the leading edge (frames S..E) every qualifying lag's reference is
// still pure pre-blend, so the rising ramp is identical across those lags;
// averaging the normalized ratio across them yields a clean, low-noise ramp.
// Walking back from the aligned plateau corner pLeft (= median of the per-lag
// saturated-run starts), the averaged ramp descends monotonically until it
// reaches the pre-dissolve content level — that turning point is the foot. We
// stop at the running minimum and break once the averaged value rises back
// above it (by a small tolerance), i.e. once the walk leaves the ramp and
// re-enters content. No percentile/baseline is assumed, so a high pre-dissolve
// content ratio cannot drag the start as the ramp-foot extrapolation does.
func leadingEdgeStart(norm [][]float64, group []plateauEstimate, pLeft, maxBack int) int {
	avg := func(j int) (float64, bool) {
		sum, n := 0.0, 0
		for _, g := range group {
			if j >= 0 && j < len(norm) && g.li >= 0 && g.li < len(norm[j]) {
				sum += norm[j][g.li]
				n++
			}
		}
		if n == 0 {
			return 0, false
		}
		return sum / float64(n), true
	}
	lo := pLeft - maxBack
	if lo < 0 {
		lo = 0
	}
	best, ok := avg(pLeft)
	if !ok {
		return pLeft
	}
	foot := pLeft
	// The (steep, short) leading ramp descends by more than minDrop per frame;
	// once a step fails to drop that much we have reached the pre-dissolve
	// content level (the foot). Used only for short blends, where the ramp is
	// steep enough that this absolute step test is reliable.
	const minDrop = 0.01
	for j := pLeft - 1; j >= lo; j-- {
		v, ok := avg(j)
		if !ok {
			break
		}
		if v <= best-minDrop {
			best = v
			foot = j
			continue
		}
		break // descent flattened or reversed: foot reached
	}
	return foot + 1 // first blended frame = last unblended foot + 1
}

// rampFeet projects the ramps flanking a saturated run [hs, he] on lag index
// li down to their local baselines and returns the implied blend bounds
// (s = first blended frame, e = first pure new-scene frame).  Each foot is
// found by locating the 50% and 20% crossing levels of the ramp relative to a
// robust local baseline (10th percentile of the flanking window) and
// extrapolating the line through them to 0%.
//
// The baseline is measured over a LOCAL window of ~k frames, not the full
// search reach. The leading/trailing ramp is at most D < k frames long, so the
// pre/post content level is captured within ~k frames of the run. A wider
// window lets the 10th percentile pick up unrelated low stretches far away;
// for a long lag whose content stays high right up to the blend (no low
// baseline) that drags the 20% crossing — and hence the extrapolated foot —
// tens of frames off (the cause of a short blend's start landing ~100 frames
// early when only long lags saturate).
func (cfg LookaheadAnalyzer) rampFeet(norm [][]float64, li, k int, sat float64, hs, he, reach, n int) (s, e int, ok bool) {
	lo := max(k+1, hs-reach)
	hi := min(n-1, he+reach)
	if hs-lo < 5 || hi-he < 5 {
		return 0, 0, false
	}
	baseWin := k
	if baseWin < 16 {
		baseWin = 16
	}
	preLo := max(lo, hs-baseWin)
	postHi := min(hi, he+baseWin)
	pre := make([]float64, 0, hs-preLo)
	for j := preLo; j < hs; j++ {
		pre = append(pre, norm[j][li])
	}
	post := make([]float64, 0, postHi-he)
	for j := he + 1; j <= postHi; j++ {
		post = append(post, norm[j][li])
	}
	f1, ok1 := footScan(norm, li, hs-1, -1, lo, hs-1, percentile(pre, 0.10), sat)
	f2, ok2 := footScan(norm, li, he+1, +1, he+1, hi, percentile(post, 0.10), sat)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return f1 + 1, f2 - k, true
}

// footScan walks from start by step within [lo, hi] looking for the first
// frame at or below the 50% level (ja) and then the 20% level (jb) of the
// ramp between base and sat, and extrapolates the line through the two
// crossings down to the baseline.
func footScan(norm [][]float64, li, start, step, lo, hi int, base, sat float64) (int, bool) {
	va := base + 0.5*(sat-base)
	vb := base + 0.2*(sat-base)
	ja, jb := -1, -1
	for j := start; j >= lo && j <= hi; j += step {
		v := norm[j][li]
		if ja < 0 && v <= va {
			ja = j
		}
		if v <= vb {
			jb = j
			break
		}
	}
	if ja < 0 || jb < 0 {
		return 0, false
	}
	g := ja - jb
	if g < 0 {
		g = -g
	}
	// The 50%→20% segment spans 30% of the ramp height; another 20% remains
	// below jb, so the foot sits g·(0.2/0.3) frames beyond jb.
	return jb + step*int(float64(g)*(0.2/0.3)+0.5), true
}

// percentile returns the p-quantile (0 ≤ p < 1) of v by sorting a copy.
func percentile(v []float64, p float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[int(p*float64(len(s)))]
}

// distinctLags returns the number of distinct lag values among the estimates.
func distinctLags(g []plateauEstimate) int {
	seen := map[int]bool{}
	for _, e := range g {
		seen[e.k] = true
	}
	return len(seen)
}

// medianInt returns the median of v (upper median for even lengths).
func medianInt(v []int) int {
	s := append([]int(nil), v...)
	sort.Ints(s)
	return s[len(s)/2]
}

// linReg performs ordinary least-squares fit for y = slope*x + intercept.
// Returns slope, intercept, rSquared.
// (Moved here from the removed streaming path because it is still used by
// the batch digital luma-ramp fade detector.)
func linReg(xs, ys []float64) (slope, intercept, rSquared float64) {
	n := float64(len(xs))
	if n < 2 {
		return 0, 0, 0
	}
	var sumx, sumy, sumx2, sumxy, sumy2 float64
	for i := range xs {
		x := xs[i]
		y := ys[i]
		sumx += x
		sumy += y
		sumx2 += x * x
		sumxy += x * y
		sumy2 += y * y
	}
	denom := n*sumx2 - sumx*sumx
	if math.Abs(denom) < 1e-9 {
		return 0, sumy / n, 0
	}
	slope = (n*sumxy - sumx*sumy) / denom
	intercept = (sumy - slope*sumx) / n
	// r-squared
	meanY := sumy / n
	var ssTot, ssRes float64
	for i := range ys {
		y := ys[i]
		pred := slope*xs[i] + intercept
		ssTot += (y - meanY) * (y - meanY)
		ssRes += (y - pred) * (y - pred)
	}
	if ssTot < 1e-9 {
		rSquared = 1
	} else {
		rSquared = 1 - ssRes/ssTot
	}
	return
}
