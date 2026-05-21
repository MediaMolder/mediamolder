// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Ported scene detection integration using PySceneDetect's ContentDetector.

import (
	"fmt"
	"math"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
	"github.com/MediaMolder/MediaMolder/PySceneDetect/detectors"
	"github.com/MediaMolder/MediaMolder/av"
)

// SceneChangeContent detects scene changes using PySceneDetect's ContentDetector.
// It computes a weighted frame score across HSV colour channels (and optionally
// Canny edges), emitting Metadata when the score exceeds the threshold.
//
// The processor is registered as "scene_change_content".
//
// Params:
//
//	"threshold":     float64 — cut trigger score (default 27.0)
//	"min_scene_len": int/float64/string — min frames or duration like "0.6s" (default 15)
//	"luma_only":     bool    — use luma-only weights; default false
//	"filter_mode":   string  — "merge" (default) or "suppress"
//	"kernel_size":   int     — dilation kernel size; 0 = auto (default)
//	"frame_rate":    float64 — stream frame rate for FrameTimecode construction (default 25.0)
type SceneChangeContent struct {
	hook      fileWriteHook
	detector  *detectors.ContentDetector
	frameRate float64
}

func (p *SceneChangeContent) Init(params map[string]any) error {
	threshold := 27.0
	var minSceneLen any = 15
	weights := detectors.DefaultContentWeights
	kernelSize := 0
	filterMode := psd.FlashFilterModeMerge
	p.frameRate = 25.0

	var err error
	params, err = p.hook.initFromParams("scene_change_content", params)
	if err != nil {
		return err
	}

	for k, v := range params {
		switch k {
		case "threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_content: threshold must be a number, got %T", v)
			}
			threshold = f
		case "min_scene_len":
			minSceneLen = v
		case "luma_only":
			b, ok := v.(bool)
			if !ok {
				return fmt.Errorf("scene_change_content: luma_only must be bool, got %T", v)
			}
			if b {
				weights = detectors.LumaOnlyWeights
			}
		case "filter_mode":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("scene_change_content: filter_mode must be string, got %T", v)
			}
			switch s {
			case "merge":
				filterMode = psd.FlashFilterModeMerge
			case "suppress":
				filterMode = psd.FlashFilterModeSuppress
			default:
				return fmt.Errorf("scene_change_content: filter_mode must be \"merge\" or \"suppress\", got %q", s)
			}
		case "kernel_size":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_content: kernel_size must be an integer, got %T", v)
			}
			kernelSize = n
		case "frame_rate":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_content: frame_rate must be a number, got %T", v)
			}
			p.frameRate = f
		default:
			return fmt.Errorf("scene_change_content: unknown param %q", k)
		}
	}

	d, err := detectors.NewContentDetector(threshold, minSceneLen, weights, kernelSize, filterMode)
	if err != nil {
		return fmt.Errorf("scene_change_content: %w", err)
	}
	p.detector = d
	return nil
}

func (p *SceneChangeContent) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	bgr, err := frame.ToBGR24()
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_content: ToBGR24: %w", err)
	}

	fd := &psd.FrameData{
		Width:  frame.Width(),
		Height: frame.Height(),
		BGR:    bgr,
	}
	tc, err := psd.NewFrameTimecode(int64(ctx.FrameIndex), p.frameRate)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_content: FrameTimecode: %w", err)
	}

	cuts, err := p.detector.ProcessFrame(tc, fd)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_content: ProcessFrame: %w", err)
	}
	if len(cuts) == 0 {
		return frame, nil, nil
	}

	score := math.Round(p.detector.Score()*1000) / 1000
	md := &Metadata{Custom: map[string]any{
		"scene_change": true,
		"detector":     "content",
		"frame_index":  cuts[0].FrameNum(),
		"timecode":     cuts[0].Timecode(),
		"pts":          ctx.PTS,
		"score":        score,
		"content_val":  score,
	}}
	p.hook.write(ctx, md)
	return frame, md, nil
}

func (p *SceneChangeContent) Close() error {
	return p.hook.close()
}

func init() {
	Register("scene_change_content", func() Processor { return &SceneChangeContent{} })
}

// numToFloat64 converts common numeric types to float64.
func numToFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// numToInt converts common numeric types to int.
func numToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	}
	return 0, false
}
