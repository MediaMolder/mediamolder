// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"sync/atomic"

	"github.com/MediaMolder/MediaMolder/av"
)

// FrameCounter is a built-in processor that passes frames through while
// counting them. On each frame it emits Metadata with the running count.
// Params:
//
//	"log_every": int — emit metadata every N frames (default: 1).
type FrameCounter struct {
	logEvery uint64
	count    atomic.Uint64
}

func (p *FrameCounter) Init(params map[string]any) error {
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
			return fmt.Errorf("frame_counter: log_every must be a number, got %T", v)
		}
	}
	return nil
}

func (p *FrameCounter) Process(frame *av.Frame, _ ProcessorContext) (*av.Frame, *Metadata, error) {
	n := p.count.Add(1)
	if n%p.logEvery == 0 {
		return frame, &Metadata{
			Custom: map[string]any{"frame_count": n},
		}, nil
	}
	return frame, nil, nil
}

func (p *FrameCounter) Close() error { return nil }

func init() {
	Register("frame_counter", func() Processor { return &FrameCounter{} })
}
