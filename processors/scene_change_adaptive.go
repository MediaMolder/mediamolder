// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Ported scene detection integration using PySceneDetect's AdaptiveDetector.

import (
	"fmt"
	"math"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
	"github.com/MediaMolder/MediaMolder/PySceneDetect/detectors"
	"github.com/MediaMolder/MediaMolder/av"
)

// SceneChangeAdaptive detects scene changes using PySceneDetect's AdaptiveDetector.
// It computes a rolling-window adaptive ratio across HSV colour channels and fires
// when the ratio and minimum content value thresholds are both exceeded.
//
// The processor is registered as "scene_change_adaptive".
//
// Params:
//
//	"adaptive_threshold": float64 — adaptive ratio threshold (default 3.0)
//	"min_scene_len":      int/float64/string — min frames or duration like "0.6s" (default 15)
//	"window_width":       int     — frames on each side of the target frame (default 2; min 1)
//	"min_content_val":    float64 — minimum content_val the target frame must reach (default 15.0)
//	"luma_only":          bool    — use luma-only weights; default false
//	"kernel_size":        int     — dilation kernel size; 0 = auto (default)
//	"frame_rate":         float64 — stream frame rate; auto-detected from the input stream when omitted (default 25.0 if unknown)
type SceneChangeAdaptive struct {
	hook      fileWriteHook
	detector  *detectors.AdaptiveDetector
	frameRate float64
}

func (p *SceneChangeAdaptive) Init(params map[string]any) error {
	adaptiveThreshold := 3.0
	var minSceneLen any = 15
	windowWidth := 2
	minContentVal := 15.0
	lumaOnly := false
	weights := detectors.DefaultContentWeights
	kernelSize := 0
	p.frameRate = 25.0

	var err error
	params, err = p.hook.initFromParams("scene_change_adaptive", params)
	if err != nil {
		return err
	}

	for k, v := range params {
		switch k {
		case "adaptive_threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_adaptive: adaptive_threshold must be a number, got %T", v)
			}
			adaptiveThreshold = f
		case "min_scene_len":
			minSceneLen = v
		case "window_width":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_adaptive: window_width must be an integer, got %T", v)
			}
			windowWidth = n
		case "min_content_val":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_adaptive: min_content_val must be a number, got %T", v)
			}
			minContentVal = f
		case "luma_only":
			b, ok := v.(bool)
			if !ok {
				return fmt.Errorf("scene_change_adaptive: luma_only must be bool, got %T", v)
			}
			lumaOnly = b
			if b {
				weights = detectors.LumaOnlyWeights
			}
		case "kernel_size":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_adaptive: kernel_size must be an integer, got %T", v)
			}
			kernelSize = n
		case "frame_rate":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_adaptive: frame_rate must be a number, got %T", v)
			}
			p.frameRate = f
		default:
			return fmt.Errorf("scene_change_adaptive: unknown param %q", k)
		}
	}

	d, err := detectors.NewAdaptiveDetector(
		adaptiveThreshold,
		minSceneLen,
		windowWidth,
		minContentVal,
		weights,
		lumaOnly,
		kernelSize,
	)
	if err != nil {
		return fmt.Errorf("scene_change_adaptive: %w", err)
	}
	p.detector = d
	return nil
}

func (p *SceneChangeAdaptive) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	bgr, err := frame.ToBGR24()
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_adaptive: ToBGR24: %w", err)
	}

	fd := &psd.FrameData{
		Width:  frame.Width(),
		Height: frame.Height(),
		BGR:    bgr,
	}
	tc, err := psd.NewFrameTimecode(int64(ctx.FrameIndex), p.frameRate)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_adaptive: FrameTimecode: %w", err)
	}

	cuts, err := p.detector.ProcessFrame(tc, fd)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_adaptive: ProcessFrame: %w", err)
	}
	if len(cuts) == 0 {
		return frame, nil, nil
	}

	score := math.Round(p.detector.Score()*1000) / 1000
	md := &Metadata{Custom: map[string]any{
		"scene_change": true,
		"detector":     "adaptive",
		"frame_index":  cuts[0].FrameNum(),
		"timecode":     cuts[0].Timecode(),
		"pts":          ctx.PTS,
		"score":        score,
		"content_val":  score,
	}}
	p.hook.write(ctx, md)
	return frame, md, nil
}

func (p *SceneChangeAdaptive) Close() error {
	return p.hook.close()
}

func init() {
	Register("scene_change_adaptive", func() Processor { return &SceneChangeAdaptive{} })
}
