// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Ported scene detection integration using PySceneDetect's HistogramDetector.

import (
	"fmt"
	"math"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
	"github.com/MediaMolder/MediaMolder/PySceneDetect/detectors"
	"github.com/MediaMolder/MediaMolder/av"
)

// SceneChangeHistogram detects scene changes using PySceneDetect's
// HistogramDetector.  It compares per-frame luminance histograms using the
// Pearson correlation coefficient and fires when the correlation falls below
// (1 − threshold).
//
// The processor is registered as "scene_change_histogram".
//
// Params:
//
//	"threshold":     float64 — maximum tolerated frame-to-frame difference in [0,1] (default 0.05)
//	"bins":          int     — number of histogram bins (default 256)
//	"min_scene_len": int/float64/string — min frames or duration like "0.6s" (default 15)
//	"frame_rate":    float64 — stream frame rate for FrameTimecode construction (default 25.0)
type SceneChangeHistogram struct {
	hook      fileWriteHook
	detector  *detectors.HistogramDetector
	frameRate float64
}

func (p *SceneChangeHistogram) Init(params map[string]any) error {
	threshold := 0.05
	bins := 256
	var minSceneLen any = 15
	p.frameRate = 25.0

	var err error
	params, err = p.hook.initFromParams("scene_change_histogram", params)
	if err != nil {
		return err
	}

	for k, v := range params {
		switch k {
		case "threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_histogram: threshold must be a number, got %T", v)
			}
			threshold = f
		case "bins":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_histogram: bins must be an integer, got %T", v)
			}
			bins = n
		case "min_scene_len":
			minSceneLen = v
		case "frame_rate":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_histogram: frame_rate must be a number, got %T", v)
			}
			p.frameRate = f
		default:
			return fmt.Errorf("scene_change_histogram: unknown param %q", k)
		}
	}

	d, err := detectors.NewHistogramDetector(threshold, bins, minSceneLen)
	if err != nil {
		return fmt.Errorf("scene_change_histogram: %w", err)
	}
	p.detector = d
	return nil
}

func (p *SceneChangeHistogram) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	bgr, err := frame.ToBGR24()
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_histogram: ToBGR24: %w", err)
	}

	fd := &psd.FrameData{
		Width:  frame.Width(),
		Height: frame.Height(),
		BGR:    bgr,
	}
	tc, err := psd.NewFrameTimecode(int64(ctx.FrameIndex), p.frameRate)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_histogram: FrameTimecode: %w", err)
	}

	cuts, err := p.detector.ProcessFrame(tc, fd)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_histogram: ProcessFrame: %w", err)
	}
	if len(cuts) == 0 {
		return frame, nil, nil
	}

	// hist_diff is the Pearson correlation (high = similar). Report raw value and
	// a score = 1 - correlation so that higher score means more scene change.
	corr := math.Round(p.detector.LastHistDiff()*1000) / 1000
	score := math.Round((1.0-p.detector.LastHistDiff())*1000) / 1000
	md := &Metadata{Custom: map[string]any{
		"scene_change": true,
		"detector":     "histogram",
		"frame_index":  cuts[0].FrameNum(),
		"timecode":     cuts[0].Timecode(),
		"pts":          ctx.PTS,
		"score":        score,
		"hist_diff":    corr,
	}}
	p.hook.write(ctx, md)
	return frame, md, nil
}

func (p *SceneChangeHistogram) Close() error {
	return p.hook.close()
}

func init() {
	Register("scene_change_histogram", func() Processor { return &SceneChangeHistogram{} })
}
