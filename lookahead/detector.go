// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// LookaheadDetector wraps LookaheadScanner + LookaheadAnalyzer as a
// goscenedetect.SceneDetector.
//
// After the removal of streaming mode the detector is purely batch:
// ProcessFrame only feeds the scanner (cheap coarse pass). All detection
// (including staged refinement when the scanner has retained lowres frames)
// happens in PostProcess, which calls Analyze or AnalyzeStaged on the
// completed (possibly augmented) CostMatrix.
//
// EventBufferLength returns 0; the SceneManager must hold frames open until
// PostProcess completes.
package lookahead

import (
	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
)

// LookaheadDetector implements goscenedetect.SceneDetector using the
// motion-compensated cost-matrix algorithm.
//
// Usage:
//
//	d, _ := NewLookaheadDetector(40, LookaheadAnalyzer{})
//	sm := goscenedetect.NewSceneManager(nil)
//	sm.AddDetector(d)
//	// … feed frames …
//	cuts, _ := d.PostProcess(lastTimecode)
type LookaheadDetector struct {
	scanner  *LookaheadScanner
	analyzer LookaheadAnalyzer
	tcs      []psd.FrameTimecode // tcs[j] = timecode of the j-th processed frame
	stats    *psd.StatsManager
}

// NewLookaheadDetector creates a detector with the given lookahead length.
// l must be in [1, MaxLag] (currently 256 to support custom long-lag schedules
// for synthetic dissolve testing, e.g. lags up to 120 from dissolve_test_xfs.json).
// analyzer may be zero-valued; missing fields are filled with defaults inside Analyze.
func NewLookaheadDetector(l int, analyzer LookaheadAnalyzer) (*LookaheadDetector, error) {
	s, err := NewLookaheadScanner(l)
	if err != nil {
		return nil, err
	}
	return &LookaheadDetector{scanner: s, analyzer: analyzer}, nil
}

// SetStats attaches an optional StatsManager for per-frame metric recording.
// Must be called before the first ProcessFrame call.
func (d *LookaheadDetector) SetStats(s *psd.StatsManager) {
	d.stats = s
	if s != nil {
		s.RegisterMetrics(d.GetMetrics())
	}
}

// ProcessFrame implements goscenedetect.SceneDetector.
// Extracts the BT.601 luma plane from frame.BGR and feeds it to the scanner.
// Always returns nil — cuts are deferred to PostProcess.
func (d *LookaheadDetector) ProcessFrame(t psd.FrameTimecode, frame *psd.FrameData) ([]psd.FrameTimecode, error) {
	luma := BGRToLuma(frame.BGR, frame.Width, frame.Height)
	if err := d.scanner.AddFrame(luma, frame.Width, frame.Height, frame.Width); err != nil {
		return nil, err
	}
	d.tcs = append(d.tcs, t)

	if d.stats != nil {
		m := d.scanner.Matrix()
		j := m.N - 1
		var r1 float32
		if j >= 0 && len(m.Ratio[j]) > 0 {
			r1 = m.Ratio[j][0]
		}
		d.stats.SetMetrics(t.FrameNum(), map[string]float64{
			"lookahead_ratio_lag1": float64(r1),
		})
	}
	return nil, nil
}

// PostProcess implements goscenedetect.SceneDetector.
// Runs the analyser on the completed cost matrix and returns the timecodes
// of all detected scene transitions (hard cuts, dissolves, fades).
// Flashes are filtered out by the analyser and are not returned.
func (d *LookaheadDetector) PostProcess(_ psd.FrameTimecode) ([]psd.FrameTimecode, error) {
	if d.scanner.Matrix().N == 0 {
		return nil, nil
	}
	transitions, err := d.analyzer.Analyze(d.scanner.Matrix())
	if err != nil {
		return nil, err
	}
	cuts := make([]psd.FrameTimecode, 0, len(transitions))
	for _, tr := range transitions {
		if tr.StartFrame >= 0 && tr.StartFrame < len(d.tcs) {
			cuts = append(cuts, d.tcs[tr.StartFrame])
		}
	}
	return cuts, nil
}

// GetMetrics implements goscenedetect.SceneDetector.
func (d *LookaheadDetector) GetMetrics() []string {
	return []string{"lookahead_ratio_lag1"}
}

// EventBufferLength implements goscenedetect.SceneDetector.
// Returns 0 because all cuts are deferred to PostProcess.
func (d *LookaheadDetector) EventBufferLength() int64 { return 0 }

// BGRToLuma converts a packed BGR24 image to an 8-bit luma (Y) plane
// using BT.601 coefficients.
// Integer approximation: Y = (77*R + 150*G + 29*B + 128) >> 8
// Maximum rounding error: ±1 LSB.
func BGRToLuma(bgr []byte, w, h int) []byte {
	n := w * h
	luma := make([]byte, n)
	for i := 0; i < n; i++ {
		b := int(bgr[i*3])
		g := int(bgr[i*3+1])
		r := int(bgr[i*3+2])
		luma[i] = byte((77*r + 150*g + 29*b + 128) >> 8)
	}
	return luma
}
