// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Ported scene detection integration using PySceneDetect's HashDetector.

import (
	"fmt"
	"math"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
	"github.com/MediaMolder/MediaMolder/PySceneDetect/detectors"
	"github.com/MediaMolder/MediaMolder/av"
)

// SceneChangeHash detects scene changes using PySceneDetect's HashDetector.
// It computes a DCT-based perceptual hash for each frame and fires when the
// normalised Hamming distance between consecutive hashes meets the threshold.
//
// The processor is registered as "scene_change_hash".
//
// Params:
//
//	"threshold":     float64 — normalised Hamming distance in [0,1] (default 0.395)
//	"size":          int     — hash dimension; hash is size×size bits (default 16)
//	"lowpass":       int     — resize factor; frame resized to (size*lowpass)² (default 2)
//	"min_scene_len": int/float64/string — min frames or duration like "0.6s" (default 15)
//	"frame_rate":    float64 — stream frame rate; auto-detected from the input stream when omitted (default 25.0 if unknown)
type SceneChangeHash struct {
	hook      fileWriteHook
	detector  *detectors.HashDetector
	frameRate float64
}

func (p *SceneChangeHash) Init(params map[string]any) error {
	threshold := 0.395
	size := 16
	lowpass := 2
	var minSceneLen any = 15
	p.frameRate = 25.0

	var err error
	params, err = p.hook.initFromParams("scene_change_hash", params)
	if err != nil {
		return err
	}

	for k, v := range params {
		switch k {
		case "threshold":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_hash: threshold must be a number, got %T", v)
			}
			threshold = f
		case "size":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_hash: size must be an integer, got %T", v)
			}
			size = n
		case "lowpass":
			n, ok := numToInt(v)
			if !ok {
				return fmt.Errorf("scene_change_hash: lowpass must be an integer, got %T", v)
			}
			lowpass = n
		case "min_scene_len":
			minSceneLen = v
		case "frame_rate":
			f, ok := numToFloat64(v)
			if !ok {
				return fmt.Errorf("scene_change_hash: frame_rate must be a number, got %T", v)
			}
			p.frameRate = f
		default:
			return fmt.Errorf("scene_change_hash: unknown param %q", k)
		}
	}

	d, err := detectors.NewHashDetector(threshold, minSceneLen, size, lowpass)
	if err != nil {
		return fmt.Errorf("scene_change_hash: %w", err)
	}
	p.detector = d
	return nil
}

func (p *SceneChangeHash) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	bgr, err := frame.ToBGR24()
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_hash: ToBGR24: %w", err)
	}

	fd := &psd.FrameData{
		Width:  frame.Width(),
		Height: frame.Height(),
		BGR:    bgr,
	}
	tc, err := psd.NewFrameTimecode(int64(ctx.FrameIndex), p.frameRate)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_hash: FrameTimecode: %w", err)
	}

	cuts, err := p.detector.ProcessFrame(tc, fd)
	if err != nil {
		return frame, nil, fmt.Errorf("scene_change_hash: ProcessFrame: %w", err)
	}
	if len(cuts) == 0 {
		return frame, nil, nil
	}

	score := math.Round(p.detector.LastHashDist()*1000) / 1000
	md := &Metadata{Custom: map[string]any{
		"scene_change": true,
		"detector":     "hash",
		"frame_index":  cuts[0].FrameNum(),
		"timecode":     cuts[0].Timecode(),
		"pts":          ctx.PTS,
		"score":        score,
		"hash_dist":    score,
	}}
	p.hook.write(ctx, md)
	return frame, md, nil
}

func (p *SceneChangeHash) Close() error {
	return p.hook.close()
}

func init() {
	Register("scene_change_hash", func() Processor { return &SceneChangeHash{} })
}
