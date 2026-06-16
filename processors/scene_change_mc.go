// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Motion-compensated scene-change detector using the lookahead cost-matrix
// algorithm (batch only).
//
// The processor accumulates frames during Process() (via the lowres lookahead
// scanner) and performs the full (optionally staged) analysis in Close().
// There is no streaming / low-latency emission path. Results (hard cuts,
// dissolves, fades) are emitted only when the job finishes (or via
// LookaheadDetector.PostProcess for the goscenedetect adapter).
//
// Dissolve detection uses the high-accuracy plateau method (per-lag saturated
// high-ratio runs + ramp-foot extrapolation) when long lags are present in the
// relevant regions, followed by progressive lag narrowing: each detected
// dissolve's edges are re-measured with forward and reverse (future-reference)
// prediction at distances 5→3→2→1 for frame-accurate bounds on short blends.
// For performance the processor uses a cheap multi-scale coarse first pass
// (lag 1 + coarse_prediction_distance(s) across the whole video) followed by
// targeted refinement measurements only in windows around coarse candidates.
// See the authoritative algorithm description in
// private_local/dissolve_transition_detection_algorithm.md and the staged
// design rationale in
// private_local/incremental_motion_compensated_scene_detection.md.
//
// Because all detection happens in Close() after the frame progress bar reaches
// 100 %, the processor implements AsyncMetadataProcessor and posts log-panel
// updates for each staged-refinement step (coarse-pass result, each rough
// dissolve region being refined, final analysis, and the detected transitions)
// so the post-frames phase is no longer silent in the GUI.
//
// The processor is registered as "scene_change_mc".

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
	"github.com/MediaMolder/MediaMolder/go_scene_detect/detectors"
	"github.com/MediaMolder/MediaMolder/lookahead"
)

// SceneChangeMC detects scene changes using the motion-compensated lookahead
// cost-matrix algorithm (batch only). All analysis and emission happens in
// Close() after the full input has been accumulated.
//
// The processor is registered as "scene_change_mc".
//
// Params:
//
//	"coarse_prediction_distance": int | []int — prediction distance(s) used (alongside lag 1) in the cheap coarse
//	                              first pass over the entire video (default 5). A single value keeps the legacy
//	                              behaviour; a multi-scale set (e.g. [30, 90]) is recommended because a lag k only
//	                              forms a flat-top saturated plateau on dissolves shorter than k — a short distance
//	                              localizes short blends, a long one localizes long blends. Cost is (1 + count)
//	                              lowres ME per frame, so this is the main performance control. Accepts the alias
//	                              "coarse_prediction_distances".
//	"refined_prediction_distances": []int — menu of prediction distances (formerly called "lags") from which the
//	                              staged refinement stage chooses values near an estimated dissolve duration D
//	                              (e.g. [5,15,30,45,60,75,90,105,120]). For each rough dissolve region found by the
//	                              coarse pass, the driver picks suitable distance(s) from this list plus a narrow
//	                              set (1-5) and computes the ratios *only* inside an intelligently sized window
//	                              around the candidate (roughly ±D). This gives the plateau detector the well-matched
//	                              long distances it needs for accurate saturated-run + ramp-foot bounds, without
//	                              evaluating them on stable content. If omitted, a built-in default menu is used.
//	                              The cost_matrix_csv will contain the coarse columns for every frame and the
//	                              additional refined columns only in the regions that were actually refined.
//	"threshold":        float64 — hard-cut score surface peak threshold; default 0.50
//	"excess_threshold": float64 — excess E[j][k] threshold for transition typing; default 0.15
//	"dissolve_min_len": int     — min measured blend duration to call a dissolve; default 2
//	"min_scene_len":    int/float64/string — min frames or duration like "0.5s" (default 15)
//	"fade_threshold":   float64 — normalised luma [0–1] below which a frame is dark; default 0.10 (valley fallback; new digital+linear path uses ~0/255 anchors)
//	"fade_white_threshold": float64 — normalised luma [0–1] above which a frame is bright; default 0.90; >1.0 disables
//	"fade_min_len":     int     — min dark/bright frames for a valid fade; default 3 (also min ramp frames to digital b/w)
//	"fade_max_len":     int     — max dark/bright frames before treating as programme black/white; default 120
//	"dissolve_max_len": int     — max active-lag width for dissolve/fade classification; 0=L/2. Increase for long dissolves (e.g. 30+ frames).
//	"agg_window":       int     — frames for score-surface aggregation (larger aids slow dissolves); default 5.
//	"prediction_failure_threshold": float64 — the inter/intra cost-ratio level (0–1, near 1.0) at which temporal prediction
//	                              from a reference is treated as having completely failed (the frame looks like a different
//	                              scene — the signature of a blend). This is the plateau saturation level used to extract
//	                              saturated runs: higher is stricter (only the most fully-failed frames count), lower
//	                              captures more of the flanking ramps (noisier). Default 0.985.
//	                              (Deprecated alias: "default_high_ratio_threshold".)
//	"prediction_failure_thresholds": map — optional per-distance overrides keyed by reference distance (lag). Example:
//	                              {"45": 0.99, "120": 0.975}. Keys may be integers or strings.
//	                              (Deprecated alias: "high_ratio_thresholds".)
//	"frame_rate":       float64 — stream frame rate (for timecode generation); default 25.0
//	"output_file":      string  — path to write results (jsonl/csv/timecodes)
//	"output_format":    string  — "jsonl" (default), "csv", "timecodes"
//	"fullres_refine":   bool    — measure full-resolution H.264-style inter-prediction ratios around each
//	                              detected dissolve's edges and refine the END of short/mid blends (D<=25)
//	                              from the full-res reverse end-foot, which beats the half-res stack there.
//	                              Long blends and the start are out of full resolution's reach and left
//	                              unchanged. Requires "source_url" and windowed re-decode of the input;
//	                              every re-decoded frame is fingerprint-verified against the retained lowres
//	                              plane. Adds ratio_frK/ratio_frrevK CSV columns. Default false.
//	"source_url":       string  — the input URL/path scene_change_mc may re-open for fullres_refine
//	                              (the processor itself only sees decoded frames off the graph).
//	"cost_matrix_csv":  string  — if set to an absolute path, emits a CSV log of the full augmented
//	                              x264-ported lookahead cost matrix, written once after staged refinement.
//	                              One row per frame j; per forward distance K: inter_lagK + ratio_lagK
//	                              (predict j from PAST j−K); per reverse distance K used in end refinement:
//	                              ratio_revK (predict j from FUTURE j+K). Coarse distances are present for
//	                              every frame; refined/reverse ones only inside candidate-dissolve windows
//	                              (0 elsewhere). For debugging the detector's internal costs.
//
// Tuning for synthetic dissolves generated by sequence_editor (see dissolve_test_xfs.json):
// The exact blend windows are known (linear pixel blends of length "transition"."duration"
// starting at each clip's nominal cut time). A custom lag set such as
// [5,15,30,45,60,75,90,105,120] provides a small lag for short-D differential signal +
// exact matches for the longer GT durations. The staged path (when enabled) uses a cheap
// coarse prefix for candidate finding and then computes the relevant long lags only in
// the windows around detected dissolves, dramatically reducing cost while still feeding
// the plateau detector the lags it needs for accurate bounds.
//
// The ratio (inter vs intra) provides the strongest aligned signal.
//
// See also the design in private_local/incremental_motion_compensated_scene_detection.md
// and the authoritative plateau algorithm description in
// private_local/dissolve_transition_detection_algorithm.md.
type SceneChangeMC struct {
	hook         fileWriteHook
	scanner      *lookahead.LookaheadScanner
	analyzer     lookahead.LookaheadAnalyzer
	tcs          []psd.FrameTimecode
	frameRate    float64
	lookaheadLen int // max lag seen (used for documentation / future buffer sizing only)

	// full-resolution edge measurement (phase 1: measure + log)
	fullresRefine bool
	sourceURL     string
	framePTS      []int64
	frProvider    *fullresProvider

	// cost matrix logging (x264 intra/inter costs)
	costMatrixPath string
	costFile       *os.File
	costCSV        *csv.Writer

	// coarsePredictionDistances are the prediction distances used (alongside
	// lag 1) in the cheap first pass over the whole video. A single distance is
	// accepted for back-compat, but a multi-scale set (e.g. [30, 90]) is
	// recommended: a short distance forms clean flat-top plateaus on short
	// dissolves and a long one on longer dissolves (a single lag k only
	// plateaus on blends shorter than k). The pass measures lag 1 + each
	// distance, so cost is (1 + len) lowres ME per frame.
	coarsePredictionDistances []int

	// refinedPredictionDistances is the menu of prediction distances used
	// during the refinement stage (the parameter was previously called "lags").
	// When a dissolve candidate of estimated duration D is found from the
	// coarse pass, the staged driver selects distance(s) near D from this list
	// (plus a narrow 1-5 set) and computes the inter/intra ratios *only* for
	// frames in a window around the candidate. This is the main control for
	// supporting long dissolves accurately without paying the full cost on
	// every frame.
	refinedPredictionDistances []int

	// emitted tracks StartFrame values for which we have already emitted an
	// event (used for deduplication across any internal multi-pass logic).
	emitted map[int]bool

	// emit, when installed by the engine (AsyncMetadataProcessor), lets the
	// processor post status/log updates to the GUI outside the per-frame
	// Process loop — in particular during the post-frames staged refinement in
	// Close(), which is otherwise silent after the progress bar hits 100%.
	emit MetadataEmitter
}

// SetMetadataEmitter implements processors.AsyncMetadataProcessor. The engine
// installs the emitter once after Init; it is used only for progress/log
// updates (the authoritative detections are still written by the file hook).
func (p *SceneChangeMC) SetMetadataEmitter(emit MetadataEmitter) {
	p.emit = emit
}

// LookbackFrames implements processors.FrameLookahead. With streaming mode
// removed, scene transitions are only known after the full input has been
// processed (Close / PostProcess). Returning 0 is correct and conservative.
func (p *SceneChangeMC) LookbackFrames() int {
	return 0
}

func (p *SceneChangeMC) Init(params map[string]any) error {
	p.frameRate = 25.0
	var parsedLookahead int // parsed from "lookahead" param; informational only (we use a fixed cheap coarse schedule internally)
	var minSceneLenRaw any

	var err error
	params, err = p.hook.initFromParams("scene_change_mc", params)
	if err != nil {
		return err
	}

	for k, v := range params {
		switch k {
		case "lookahead":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: lookahead must be an integer, got %T", v)
			}
			parsedLookahead = n
			p.lookaheadLen = parsedLookahead // keep for future / logging; accumulation uses fixed cheap coarse set
		case "threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: threshold must be a number, got %T", v)
			}
			p.analyzer.HardCutThreshold = f
		case "excess_threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: excess_threshold must be a number, got %T", v)
			}
			p.analyzer.ExcessThreshold = f
		case "dissolve_min_len":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: dissolve_min_len must be an integer, got %T", v)
			}
			p.analyzer.DissolveMinLen = n
		case "fade_threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: fade_threshold must be a number, got %T", v)
			}
			p.analyzer.FadeBlackThreshold = f
		case "fade_min_len":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: fade_min_len must be an integer, got %T", v)
			}
			p.analyzer.FadeMinLen = n
		case "fade_max_len":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: fade_max_len must be an integer, got %T", v)
			}
			p.analyzer.FadeMaxLen = n
		case "dissolve_max_len":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: dissolve_max_len must be an integer, got %T", v)
			}
			p.analyzer.DissolveMaxLen = n
		// "default_high_ratio_threshold" is the deprecated alias for the old name.
		case "prediction_failure_threshold", "default_high_ratio_threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: %s must be a number, got %T", k, v)
			}
			p.analyzer.PredictionFailureThreshold = f
		// "high_ratio_thresholds" is the deprecated alias for the old name.
		case "prediction_failure_thresholds", "high_ratio_thresholds":
			if m, ok := v.(map[any]any); ok {
				for kk, vv := range m {
					if lag, ok := numToInt(kk); ok {
						if th, ok := numToFloat64(vv); ok {
							if p.analyzer.PredictionFailureThresholds == nil {
								p.analyzer.PredictionFailureThresholds = map[int]float64{}
							}
							p.analyzer.PredictionFailureThresholds[lag] = th
						}
					}
				}
			} else if m, ok := v.(map[string]any); ok {
				for ks, vv := range m {
					// support string keys like "45": 0.95
					var lag int
					if n, err := strconv.Atoi(ks); err == nil {
						lag = n
					} else if n2, ok := numToInt(ks); ok {
						lag = n2
					} else {
						continue
					}
					if th, ok := numToFloat64(vv); ok {
						if p.analyzer.PredictionFailureThresholds == nil {
							p.analyzer.PredictionFailureThresholds = map[int]float64{}
						}
						p.analyzer.PredictionFailureThresholds[lag] = th
					}
				}
			}
		case "agg_window":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: agg_window must be an integer, got %T", v)
			}
			p.analyzer.AggWindow = n
		case "fade_white_threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: fade_white_threshold must be a number, got %T", v)
			}
			p.analyzer.FadeWhiteThreshold = f
		case "min_scene_len":
			minSceneLenRaw = v
		case "frame_rate":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_mc: frame_rate must be a number, got %T", v)
			}
			p.frameRate = f
		case "fullres_refine":
			b, ok := v.(bool)
			if !ok {
				return fmt.Errorf("scene_change_mc: fullres_refine must be a bool, got %T", v)
			}
			p.fullresRefine = b
		case "source_url":
			u, ok := v.(string)
			if !ok {
				return fmt.Errorf("scene_change_mc: source_url must be a string, got %T", v)
			}
			p.sourceURL = u
		case "cost_matrix_csv":
			pth, ok := v.(string)
			if !ok {
				return fmt.Errorf("scene_change_mc: cost_matrix_csv must be a string path, got %T", v)
			}
			p.costMatrixPath = pth
		case "coarse_prediction_distance", "coarse_prediction_distances":
			if arr, ok := v.([]any); ok {
				ds := []int{}
				for _, vv := range arr {
					if n, ok := numToInt(vv); ok && n > 0 {
						ds = append(ds, n)
					}
				}
				if len(ds) > 0 {
					p.coarsePredictionDistances = ds
				}
			} else if n, ok := numToInt(v); ok {
				if n > 0 {
					p.coarsePredictionDistances = []int{n}
				}
			} else {
				return fmt.Errorf("scene_change_mc: coarse_prediction_distance must be an integer or array of positive integers, got %T", v)
			}
		case "refined_prediction_distances", "lags": // "lags" kept for backward compatibility during rename
			if arr, ok := v.([]any); ok {
				ls := []int{}
				for _, vv := range arr {
					if n, ok := numToInt(vv); ok && n > 0 {
						ls = append(ls, n)
					}
				}
				if len(ls) > 0 {
					p.refinedPredictionDistances = ls
				}
			} else {
				return fmt.Errorf("scene_change_mc: refined_prediction_distances must be an array of positive integers, got %T", v)
			}
		default:
			return fmt.Errorf("scene_change_mc: unknown param %q", k)
		}
	}

	if minSceneLenRaw != nil {
		n, err := detectors.ResolveMinSceneLen(minSceneLenRaw, p.frameRate)
		if err != nil {
			return fmt.Errorf("scene_change_mc: min_scene_len: %w", err)
		}
		p.analyzer.MinSceneLen = int(n)
	}

	// In staged mode we accumulate using a cheap coarse first pass over the whole
	// video: lag 1 (hard cuts) plus the configured coarse prediction distance(s).
	// A multi-scale set lets a short distance plateau on short blends and a long
	// one on longer blends, since a lag k only forms a flat top for D < k.
	coarseDists := p.coarsePredictionDistances
	if len(coarseDists) == 0 {
		coarseDists = []int{5}
	}
	lagSet := map[int]bool{1: true}
	for _, d := range coarseDists {
		lagSet[d] = true
	}
	coarseLags := make([]int, 0, len(lagSet))
	for d := range lagSet {
		coarseLags = append(coarseLags, d)
	}
	sort.Ints(coarseLags)
	s, err := lookahead.NewLookaheadScannerWithLags(coarseLags)
	if err != nil {
		return fmt.Errorf("scene_change_mc: %w", err)
	}
	p.scanner = s
	p.lookaheadLen = coarseLags[len(coarseLags)-1]

	// Enable retention so the staged driver (AnalyzeStaged) can perform
	// targeted refinement using distances from refined_prediction_distances
	// only around the candidate regions discovered from the coarse pass.
	p.scanner.RetainAllLowres()

	p.emitted = make(map[int]bool)

	if p.costMatrixPath != "" {
		if err := p.openCostMatrixLog(); err != nil {
			return err
		}
	}
	return nil
}

// openCostMatrixLog opens (and sanitizes) the CSV path for the x264 cost matrix.
// The contents are written once at Close() (writeCostMatrixCSV), after staged
// refinement has populated all forward and reverse columns — writing per-frame
// during accumulation would only capture the cheap coarse columns. Best-effort
// sanitization mirrors fileWriteHook to prevent path traversal.
func (p *SceneChangeMC) openCostMatrixLog() error {
	path := filepath.Clean(p.costMatrixPath)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("scene_change_mc: cost_matrix_csv must be an absolute path, got %q", path)
	}
	fsRoot := string(filepath.Separator)
	rel, relErr := filepath.Rel(fsRoot, path)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("scene_change_mc: cost_matrix_csv %q is not within an accessible filesystem root", path)
	}
	safePath := filepath.Join(fsRoot, rel)

	f, err := os.Create(safePath)
	if err != nil {
		return fmt.Errorf("scene_change_mc: create cost_matrix_csv %q: %w", safePath, err)
	}
	p.costFile = f
	p.costCSV = csv.NewWriter(f)
	return nil
}

// writeCostMatrixCSV writes the full augmented cost matrix once, after staged
// refinement. Each row is a frame j; for every forward distance K there are
// inter_lagK (raw pCost) and ratio_lagK (= predict j from the PAST reference
// j−K), and for every reverse distance K computed during end refinement a
// ratio_revK (= predict j from the FUTURE reference j+K). Columns that were not
// computed for a given frame are 0 (coarse distances are present everywhere;
// refined/reverse ones only inside the windows around candidate dissolves).
// bgrMeanUV returns the frame's mean chroma (full-range BT.601 U and V,
// centred on 128) from a packed BGR24 buffer of n pixels. The transform is
// linear, so the mean U/V equal the transform of the mean B/G/R — one
// integer-sum pass, no per-pixel conversion. Coefficients match the
// 77/150/29 luma weights used by lookahead.BGRToLuma.
func bgrMeanUV(bgr []byte, n int) (float32, float32) {
	if n <= 0 || len(bgr) < n*3 {
		return 0, 0
	}
	var sb, sg, sr int64
	for i := 0; i < n; i++ {
		sb += int64(bgr[i*3])
		sg += int64(bgr[i*3+1])
		sr += int64(bgr[i*3+2])
	}
	fn := float64(n)
	b, g, r := float64(sb)/fn, float64(sg)/fn, float64(sr)/fn
	u := 128 - 0.168736*r - 0.331264*g + 0.5*b
	v := 128 + 0.5*r - 0.418688*g - 0.081312*b
	return float32(u), float32(v)
}

func (p *SceneChangeMC) writeCostMatrixCSV() {
	if p.costCSV == nil {
		return
	}
	m := p.scanner.Matrix()
	if m == nil {
		return
	}

	hdr := []string{"frame", "intra_cost", "avg_luma", "avg_u", "avg_v", "energy"}
	for _, lag := range m.Lags {
		hdr = append(hdr, fmt.Sprintf("inter_lag%d", lag), fmt.Sprintf("ratio_lag%d", lag))
	}
	for _, lag := range m.RevLags {
		hdr = append(hdr, fmt.Sprintf("ratio_rev%d", lag))
	}
	for _, lag := range m.FrLags {
		hdr = append(hdr, fmt.Sprintf("ratio_fr%d", lag))
	}
	for _, lag := range m.FrRevLags {
		hdr = append(hdr, fmt.Sprintf("ratio_frrev%d", lag))
	}
	_ = p.costCSV.Write(hdr)

	for j := 0; j < m.N; j++ {
		energyVal := float32(0)
		if j < len(m.Energy) {
			energyVal = m.Energy[j]
		}
		uVal, vVal := float32(0), float32(0)
		if j < len(m.AvgU) && j < len(m.AvgV) {
			uVal, vVal = m.AvgU[j], m.AvgV[j]
		}
		rec := []string{
			fmt.Sprintf("%d", j),
			fmt.Sprintf("%.6g", m.IntraCost[j]),
			fmt.Sprintf("%.6g", m.AvgLuma[j]),
			fmt.Sprintf("%.6g", uVal),
			fmt.Sprintf("%.6g", vVal),
			fmt.Sprintf("%.6g", energyVal),
		}
		for i := range m.Lags {
			interVal := 0.0
			if i < len(m.InterCost[j]) {
				interVal = float64(m.InterCost[j][i])
			}
			ratioVal := 0.0
			if i < len(m.Ratio[j]) {
				ratioVal = float64(m.Ratio[j][i])
			}
			rec = append(rec, fmt.Sprintf("%.6g", interVal), fmt.Sprintf("%.6g", ratioVal))
		}
		for i := range m.RevLags {
			revVal := 0.0
			if j < len(m.RevRatio) && i < len(m.RevRatio[j]) {
				revVal = float64(m.RevRatio[j][i])
			}
			rec = append(rec, fmt.Sprintf("%.6g", revVal))
		}
		for i := range m.FrLags {
			v := 0.0
			if j < len(m.FrRatio) && i < len(m.FrRatio[j]) {
				v = float64(m.FrRatio[j][i])
			}
			rec = append(rec, fmt.Sprintf("%.6g", v))
		}
		for i := range m.FrRevLags {
			v := 0.0
			if j < len(m.FrRevRatio) && i < len(m.FrRevRatio[j]) {
				v = float64(m.FrRevRatio[j][i])
			}
			rec = append(rec, fmt.Sprintf("%.6g", v))
		}
		_ = p.costCSV.Write(rec)
	}
	p.costCSV.Flush()
}

func (p *SceneChangeMC) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	bgr, err := frame.ToBGR24()
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_mc: ToBGR24: %w", err)
	}

	luma := lookahead.BGRToLuma(bgr, frame.Width(), frame.Height())
	if err := p.scanner.AddFrame(luma, frame.Width(), frame.Height(), frame.Width()); err != nil {
		return frame, nil, fmt.Errorf("scene_change_mc: AddFrame: %w", err)
	}
	// Mean chroma rides in the cost matrix next to AvgLuma: the scanner is
	// luma-only, but the mean-step edge refinement and the CSV consume the
	// U/V means as additional channels.
	if mm := p.scanner.Matrix(); mm != nil {
		u, v := bgrMeanUV(bgr, frame.Width()*frame.Height())
		mm.AvgU = append(mm.AvgU, u)
		mm.AvgV = append(mm.AvgV, v)
	}
	// The cost-matrix CSV is written in full at Close(), after staged refinement
	// has populated every forward and reverse column.

	p.framePTS = append(p.framePTS, ctx.PTS)

	tc, err := psd.NewFrameTimecode(int64(ctx.FrameIndex), p.frameRate)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_mc: FrameTimecode: %w", err)
	}
	p.tcs = append(p.tcs, tc)

	// In batch mode we accumulate only. All detection and emission happens
	// in Close() after the full CostMatrix (and any staged refinement) is
	// available. No per-frame metadata is returned.
	return frame, nil, nil
}

// Close runs the (staged, when enabled) batch analysis on the completed
// cost matrix and emits all detected transitions. This is the only place
// where scene changes are reported.
func (p *SceneChangeMC) Close() error {
	if p.scanner != nil {
		if p.scanner != nil {
			// Run the staged (incremental) analysis. The scanner was fed with a
			// cheap coarse schedule (controlled by coarse_prediction_distance)
			// and has retained lowres frames. AnalyzeStaged will discover rough
			// dissolve regions from the cheap data, then selectively compute
			// extra prediction distances drawn from refined_prediction_distances
			// (the menu of distances near estimated D) only in the temporal
			// windows around those regions. It finally runs the full high-quality
			// Analyze on the augmented matrix.
			if p.fullresRefine && p.sourceURL != "" {
				p.frProvider = newFullresProvider(p.sourceURL, p.framePTS, p.scanner)
				p.analyzer.FullresProvider = p.frProvider
				defer p.frProvider.Close()
			} else if p.fullresRefine {
				if p.emit != nil {
					p.emit(&Metadata{Progress: true,
						LogMessage: "scene_change_mc: fullres_refine enabled but source_url missing — skipping full-res measurement"})
				}
			}
			transitions, err := p.analyzer.AnalyzeStaged(p.scanner, p.refinedPredictionDistances, p.reportStaged)
			if err == nil {
				for _, tr := range transitions {
					if p.emitted[tr.StartFrame] {
						continue
					}
					md := p.transitionMetadata(tr)
					p.hook.write(ProcessorContext{}, md)
					// Surface the detection in the GUI log panel too (the file
					// hook only writes to the output file). Best-effort, SSE-only.
					if p.emit != nil && md.LogMessage != "" {
						p.emit(&Metadata{Progress: true, LogMessage: md.LogMessage})
					}
					p.emitted[tr.StartFrame] = true
				}
			}
		}
	}
	if p.costCSV != nil {
		// Write the full augmented matrix (forward + reverse columns) now that
		// staged refinement has populated it.
		p.writeCostMatrixCSV()
		p.costCSV.Flush()
		if p.costFile != nil {
			p.costFile.Close()
		}
		p.costCSV = nil
		p.costFile = nil
	}
	return p.hook.close()
}

// tcAt returns the timecode string for frame index i, or a frame-number
// fallback when no timecode was recorded for it.
func (p *SceneChangeMC) tcAt(i int) string {
	if i >= 0 && i < len(p.tcs) {
		return p.tcs[i].Timecode()
	}
	return fmt.Sprintf("frame %d", i)
}

// reportStaged turns a lookahead.StagedProgress into a GUI log/progress event.
// It is the progress callback handed to AnalyzeStaged so the post-frames
// refinement phase (silent today after the bar reaches 100%) reports what it is
// doing, including the rough dissolve estimates it is about to refine.
func (p *SceneChangeMC) reportStaged(pr lookahead.StagedProgress) {
	if p.emit == nil {
		return
	}
	var msg string
	switch pr.Phase {
	case "coarse":
		msg = fmt.Sprintf("scene_change_mc: coarse pass done — %d candidate region(s) to refine", pr.Current)
	case "refine":
		msg = fmt.Sprintf("scene_change_mc: refining %d/%d — rough dissolve %s–%s (~%d frames)",
			pr.Current, pr.Total, p.tcAt(pr.Lo), p.tcAt(pr.Hi), pr.EstD)
	case "final":
		msg = "scene_change_mc: final analysis on refined matrix…"
	case "escalate":
		msg = fmt.Sprintf("scene_change_mc: dissolve %s–%s under-lagged — re-measuring with longer prediction distances",
			p.tcAt(pr.Lo), p.tcAt(pr.Hi))
	case "fullres":
		switch {
		case pr.Total < 0:
			msg = fmt.Sprintf("scene_change_mc: dissolve %s–%s full-res measurement SKIPPED (decode/alignment failure)",
				p.tcAt(pr.Lo), p.tcAt(pr.Hi))
		case pr.FullresEndUsed:
			msg = fmt.Sprintf("scene_change_mc: dissolve %s–%s full-res end adopted (%d prediction pairs)",
				p.tcAt(pr.Lo), p.tcAt(pr.Hi), pr.Total)
		default:
			msg = fmt.Sprintf("scene_change_mc: dissolve %s–%s full-res edges measured, kept (%d prediction pairs)",
				p.tcAt(pr.Lo), p.tcAt(pr.Hi), pr.Total)
		}
	case "energy":
		verdict := "edges kept"
		if pr.EnergyUsed {
			verdict = "energy edges adopted"
		}
		if pr.MeanStepUsed {
			verdict += " + chroma/luma step edges adopted"
		}
		msg = fmt.Sprintf("scene_change_mc: dissolve %s–%s energy dip SNR %.1f — %s",
			p.tcAt(pr.Lo), p.tcAt(pr.Hi), pr.DipSNR, verdict)
	default:
		return
	}
	p.emit(&Metadata{Progress: true, LogMessage: msg})
}

func (p *SceneChangeMC) transitionMetadata(tr lookahead.SceneTransition) *Metadata {
	frameIdx := tr.StartFrame
	var tc psd.FrameTimecode
	if frameIdx >= 0 && frameIdx < len(p.tcs) {
		tc = p.tcs[frameIdx]
	}
	ttypes := [...]string{"hard_cut", "dissolve", "fade_in", "fade_out", "flash"}
	ttype := "unknown"
	if int(tr.Type) < len(ttypes) {
		ttype = ttypes[tr.Type]
	}
	meta := map[string]any{
		"scene_change":    true,
		"detector":        "mc",
		"frame_index":     frameIdx,
		"timecode":        tc.Timecode(),
		"transition_type": ttype,
		"score":           tr.Score,
	}
	var logMsg string
	timecodeStr := tc.Timecode()
	if tr.EndFrame > tr.StartFrame {
		meta["dissolve_frames"] = tr.EndFrame - tr.StartFrame + 1
		logMsg = fmt.Sprintf("%s %s (%d frames, score %.2f)", timecodeStr, ttype, tr.EndFrame-tr.StartFrame+1, tr.Score)
	} else {
		logMsg = fmt.Sprintf("%s %s (score %.2f)", timecodeStr, ttype, tr.Score)
	}
	return &Metadata{Custom: meta, LogMessage: logMsg}
}

func init() {
	Register("scene_change_mc", func() Processor { return &SceneChangeMC{} })
}
