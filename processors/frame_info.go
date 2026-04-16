// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"sync/atomic"

	"github.com/MediaMolder/MediaMolder/av"
)

// FrameInfo is a built-in analysis processor that passes frames through
// unchanged while emitting metadata about each frame's properties:
// width, height, pixel format, PTS, and key-frame status.
//
// Params:
//
//	"log_every": int — emit metadata every N frames (default: 1).
type FrameInfo struct {
	logEvery uint64
	count    atomic.Uint64
}

func (p *FrameInfo) Init(params map[string]any) error {
	p.logEvery = 1
	if v, ok := params["log_every"]; ok {
		switch n := v.(type) {
		case float64:
			if n >= 1 {
				p.logEvery = uint64(n)
			}
		case int:
			if n >= 1 {
				p.logEvery = uint64(n)
			}
		default:
			return fmt.Errorf("frame_info: log_every must be a number, got %T", v)
		}
	}
	return nil
}

func (p *FrameInfo) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	n := p.count.Add(1)
	if n%p.logEvery != 0 {
		return frame, nil, nil
	}
	return frame, &Metadata{
		Custom: map[string]any{
			"width":       frame.Width(),
			"height":      frame.Height(),
			"pix_fmt":     frame.PixFmt(),
			"pts":         frame.PTS(),
			"frame_index": ctx.FrameIndex,
			"stream_id":   ctx.StreamID,
		},
	}, nil
}

func (p *FrameInfo) Close() error { return nil }

func init() {
	Register("frame_info", func() Processor { return &FrameInfo{} })
}
