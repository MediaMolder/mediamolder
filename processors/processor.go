// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package processors defines the interface and registry for go_processor
// graph nodes. Processors receive decoded frames (video or audio) and may
// transform, analyse, or annotate them before passing them downstream.
package processors

import (
	"context"

	"github.com/MediaMolder/MediaMolder/av"
)

// Processor is the interface every go_processor node must implement.
type Processor interface {
	// Init is called once during graph construction, before the first frame.
	// params come directly from the JSON config's "params" object.
	Init(params map[string]any) error

	// Process is called for every frame arriving on the node's input pad.
	//
	// Frame ownership: the runtime provides a valid, ref-counted frame.
	// The processor may modify it in-place or return a new frame (the
	// runtime handles refcounting for the original). Return a nil frame
	// to drop (consume) the frame entirely.
	//
	// metadata is optional side-data emitted via the event bus.
	Process(frame *av.Frame, ctx ProcessorContext) (out *av.Frame, metadata *Metadata, err error)

	// Close is called once on pipeline shutdown (even on error).
	Close() error
}

// ProcessorContext carries per-frame runtime information.
type ProcessorContext struct {
	StreamID   string       // e.g. "v:0"
	MediaType  av.MediaType // video / audio / subtitle
	PTS        int64
	FrameIndex uint64
	Context    context.Context // for cancellation / deadlines
}

// Metadata carries arbitrary results produced by a processor (detections,
// scores, custom key-value data, etc.). Non-nil metadata is published on the
// pipeline event bus after each Process call.
type Metadata struct {
	Detections   []Detection    `json:"detections,omitempty"`
	QualityScore float64        `json:"quality_score,omitempty"`
	Custom       map[string]any `json:"custom,omitempty"`
}

// Detection represents a single detected object in a video frame.
type Detection struct {
	Label      string     `json:"label"`
	Confidence float64    `json:"confidence"`
	BBox       [4]float64 `json:"bbox"` // x1, y1, x2, y2 (pixel coordinates)
	TrackID    int        `json:"track_id,omitempty"`
}
