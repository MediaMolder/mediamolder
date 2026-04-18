// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"math"

	"github.com/MediaMolder/MediaMolder/av"
)

// SceneChange is a built-in analysis processor that detects scene changes
// between consecutive frames using the same algorithm as FFmpeg's scdet filter.
//
// The detection uses two independent heuristics (either or both may be enabled):
//
//   - Content change (MAFD + diff): computes the Mean Absolute Frame Difference
//     on the luma channel — for YUV formats this reads the Y plane directly
//     (zero conversion, zero allocation), falling back to GRAY8 via swscale
//     for RGB. The score is min(mafd, |mafd − prev_mafd|), which suppresses
//     false positives from gradual pans/zooms (identical to scdet's algorithm).
//   - PTS gap: flags a scene change when the PTS delta between consecutive
//     frames exceeds a threshold.
//
// The frame is always passed through unchanged.
//
// Params:
//
//	"threshold":     float64 — 0-100, min scdet score to flag (default: 10, matching scdet).
//	"pts_threshold": int64   — min PTS gap to flag (default: 0 = disabled).
type SceneChange struct {
	threshold    float64
	ptsThreshold int64

	prevFrame *av.Frame
	prevMAFD  float64
	prevPTS   int64
	seenFirst bool
}

func (p *SceneChange) Init(params map[string]any) error {
	p.threshold = 10.0 // scdet default
	p.ptsThreshold = 0

	if v, ok := params["threshold"]; ok {
		switch n := v.(type) {
		case float64:
			if n < 0 || n > 100 {
				return fmt.Errorf("scene_change: threshold must be 0-100, got %f", n)
			}
			p.threshold = n
		default:
			return fmt.Errorf("scene_change: threshold must be a number, got %T", v)
		}
	}

	if v, ok := params["pts_threshold"]; ok {
		switch n := v.(type) {
		case float64:
			p.ptsThreshold = int64(n)
		case int:
			p.ptsThreshold = int64(n)
		default:
			return fmt.Errorf("scene_change: pts_threshold must be a number, got %T", v)
		}
	}

	return nil
}

func (p *SceneChange) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	pts := ctx.PTS

	if !p.seenFirst {
		p.seenFirst = true
		p.prevPTS = pts
		clone, err := frame.Clone()
		if err == nil {
			p.prevFrame = clone
		}
		return frame, nil, nil
	}

	var reasons []string
	custom := map[string]any{}

	// PTS gap check.
	if p.ptsThreshold > 0 {
		ptsDelta := pts - p.prevPTS
		if ptsDelta < 0 {
			ptsDelta = -ptsDelta
		}
		if ptsDelta > p.ptsThreshold {
			reasons = append(reasons, "pts_gap")
			custom["pts_delta"] = ptsDelta
		}
	}
	p.prevPTS = pts

	// Luma-based scene change — same algorithm as FFmpeg scdet:
	//   mafd  = mean absolute frame difference (0–100)
	//   diff  = |mafd - prev_mafd|
	//   score = min(mafd, diff)
	// This suppresses slow gradual changes (pans, zooms, fades) while
	// catching hard cuts where both metrics spike simultaneously.
	if p.threshold > 0 && p.prevFrame != nil {
		mafd, err := av.FrameSceneScore(p.prevFrame, frame)
		if err == nil {
			diff := math.Abs(mafd - p.prevMAFD)
			score := math.Min(mafd, diff)
			p.prevMAFD = mafd

			if score >= p.threshold {
				reasons = append(reasons, "content_change")
				custom["mafd"] = math.Round(mafd*1000) / 1000
				custom["score"] = math.Round(score*1000) / 1000
			}
		}
	}

	// Update stored previous frame.
	if p.prevFrame != nil {
		p.prevFrame.Close()
		p.prevFrame = nil
	}
	clone, err := frame.Clone()
	if err == nil {
		p.prevFrame = clone
	}

	if len(reasons) == 0 {
		return frame, nil, nil
	}

	custom["scene_change"] = true
	custom["reasons"] = reasons
	custom["frame_index"] = ctx.FrameIndex

	return frame, &Metadata{Custom: custom}, nil
}

func (p *SceneChange) Close() error {
	if p.prevFrame != nil {
		p.prevFrame.Close()
		p.prevFrame = nil
	}
	return nil
}

func init() {
	Register("scene_change", func() Processor { return &SceneChange{} })
}
