// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"github.com/MediaMolder/MediaMolder/av"
)

// Null is a passthrough processor that forwards every frame unmodified.
// It is useful for testing, debugging, and as a template for new processors.
type Null struct{}

func (n *Null) Init(map[string]any) error { return nil }

func (n *Null) Process(frame *av.Frame, _ ProcessorContext) (*av.Frame, *Metadata, error) {
	return frame, nil, nil
}

func (n *Null) Close() error { return nil }

func init() {
	Register("null", func() Processor { return &Null{} })
}
