// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Ported scene detection integration using PySceneDetect's ThresholdDetector.

import (
	"fmt"
	"math"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
	"github.com/MediaMolder/MediaMolder/PySceneDetect/detectors"
	"github.com/MediaMolder/MediaMolder/av"
)

// SceneChangeThreshold detects scene changes using PySceneDetect's ThresholdDetector.
// It tracks fade-in / fade-out transitions based on average frame brightness and
// emits Metadata when a fade-in follows a fade-out with sufficient elapsed frames.
//
// The processor is registered as "scene_change_threshold".
//
// Params:
//
//	"threshold":       float64 — brightness threshold in [0,255] (default 12.0)
//	"min_scene_len":   int/float64/string — min frames or duration like "0.6s" (default 15)
//	"method":          string  — "floor" (default, fade-to-black) or "ceiling" (fade-to-white)
//	"fade_bias":       float64 — [-1.0,1.0]; -1=cut at fade-out, 0=midpoint, +1=cut at fade-in (default 0.0)
//	"add_final_scene": bool    — emit a cut if video ends on a fade-out (default false)
//	                             Note: this cut cannot be surfaced through the Processor interface.
//	"frame_rate":      float64 — stream frame rate for FrameTimecode construction (default 25.0)
type SceneChangeThreshold struct {
	detector  *detectors.ThresholdDetector
	frameRate float64
}

func (p *SceneChangeThreshold) Init(params map[string]any) error {
	threshold := 12.0
	var minSceneLen any = 15
	method := detectors.ThresholdMethodFloor
	fadeBias := 0.0
	addFinalScene := false
	p.frameRate = 25.0

	for k, v := range params {
		switch k {
		case "threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_threshold: threshold must be a number, got %T", v)
			}
			threshold = f
		case "min_scene_len":
			minSceneLen = v
		case "method":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("scene_change_threshold: method must be string, got %T", v)
			}
			switch s {
			case "floor":
				method = detectors.ThresholdMethodFloor
			case "ceiling":
				method = detectors.ThresholdMethodCeiling
			default:
				return fmt.Errorf("scene_change_threshold: method must be \"floor\" or \"ceiling\", got %q", s)
			}
		case "fade_bias":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_threshold: fade_bias must be a number, got %T", v)
			}
			fadeBias = f
		case "add_final_scene":
			b, ok := v.(bool)
			if !ok {
				return fmt.Errorf("scene_change_threshold: add_final_scene must be bool, got %T", v)
			}
			addFinalScene = b
		case "frame_rate":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_threshold: frame_rate must be a number, got %T", v)
			}
			p.frameRate = f
		default:
			return fmt.Errorf("scene_change_threshold: unknown param %q", k)
		}
	}

	d, err := detectors.NewThresholdDetector(threshold, minSceneLen, fadeBias, addFinalScene, method)
	if err != nil {
		return fmt.Errorf("scene_change_threshold: %w", err)
	}
	p.detector = d
	return nil
}

func (p *SceneChangeThreshold) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	bgr, err := frame.ToBGR24()
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_threshold: ToBGR24: %w", err)
	}

	fd := &psd.FrameData{
		Width:  frame.Width(),
		Height: frame.Height(),
		BGR:    bgr,
	}
	tc, err := psd.NewFrameTimecode(int64(ctx.FrameIndex), p.frameRate)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_threshold: FrameTimecode: %w", err)
	}

	cuts, err := p.detector.ProcessFrame(tc, fd)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_threshold: ProcessFrame: %w", err)
	}
	if len(cuts) == 0 {
		return frame, nil, nil
	}

	// average_rgb score is the content_val for this detector.
	score := math.Round(p.detector.LastAvgRGB()*1000) / 1000
	return frame, &Metadata{Custom: map[string]any{
		"scene_change": true,
		"detector":     "threshold",
		"frame_index":  cuts[0].FrameNum(),
		"timecode":     cuts[0].Timecode(),
		"pts":          ctx.PTS,
		"score":        score,
		"average_rgb":  score,
	}}, nil
}

func (p *SceneChangeThreshold) Close() error {
	return nil
}

func init() {
	Register("scene_change_threshold", func() Processor { return &SceneChangeThreshold{} })
}
