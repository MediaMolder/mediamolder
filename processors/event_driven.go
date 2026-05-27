// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import "context"

// SegmentEvent is the payload delivered to a SegmentEventConsumer when an
// upstream segment_sink output finishes writing one segment file.
type SegmentEvent struct {
	OutputID     string // id of the segment_sink output that produced the file
	FilePath     string // absolute or relative path to the completed file
	SegmentIndex int    // zero-based index assigned by the sink
}

// SegmentEventConsumer is an optional Processor extension implemented by
// event-driven nodes that act on completed-segment files emitted by an
// upstream segment_sink.
//
// The engine registers a consumer when it sees an "events" edge whose source
// is a sink node and whose target is a go_processor that implements this
// interface. OnSegmentCompleted is called inline from the sink goroutine in
// arrival order; implementations should be fast or dispatch to a worker
// goroutine.
type SegmentEventConsumer interface {
	OnSegmentCompleted(ctx context.Context, ev SegmentEvent)
}

// MetadataEmitter is a callback the engine installs on event-driven
// processors so they can post Metadata events outside of the per-frame
// Process call. The provided Metadata is published on the pipeline event
// bus and forwarded to any "events" edge sinks downstream of the processor.
type MetadataEmitter func(md *Metadata)

// AsyncMetadataProcessor is an optional Processor extension implemented by
// event-driven nodes that need to emit Metadata events asynchronously (i.e.
// from a worker goroutine, not from Process). The engine calls
// SetMetadataEmitter once immediately after Init.
type AsyncMetadataProcessor interface {
	SetMetadataEmitter(emit MetadataEmitter)
}
