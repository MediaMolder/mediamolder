// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
)

// ---------- Go Processor handler ----------

func (r *graphRunner) handleGoProcessor(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	proc := r.goProcessors[node.ID]
	if proc == nil {
		// Pure-events-sink nodes (metadata_file_writer without inner_processor)
		// are tracked in pureEventSinkNodes. They have no AV frame loop.
		if _, ok := r.pureEventSinkNodes[node.ID]; ok {
			return nil
		}
		return fmt.Errorf("go_processor handler: no processor for node %q", node.ID)
	}
	// Event-driven go_processors (e.g. twelvelabs_indexer wired via an
	// "events" edge from an input or sink) have no AV frame channel.
	// Their work is dispatched via OnSegmentCompleted / metadata emitter
	// rather than the scheduler, so the handler is a no-op.
	if len(ins) == 0 {
		if _, ok := r.eventDrivenGoProcessors[node.ID]; ok {
			return nil
		}
	}
	if len(ins) != 1 {
		return fmt.Errorf("go_processor node %q: expected 1 input, got %d", node.ID, len(ins))
	}

	// Determine the media type from the inbound edge.
	var mediaType av.MediaType
	if len(node.Inbound) > 0 {
		mediaType = portTypeToAVMediaType(node.Inbound[0].Type)
	}

	var frameIndex uint64
	// Track which cutGates this processor's metadata keys can drive so we
	// can advance/finish their progress cursor in lockstep with frame PTS.
	progressGates := map[*cutGate]struct{}{}
	for _, gs := range r.segmentCuts {
		for _, g := range gs {
			progressGates[g] = struct{}{}
		}
	}
	finishProgress := func() {
		for g := range progressGates {
			g.finishProgress()
		}
	}
	defer finishProgress()
	for v := range ins[0] {
		f := v.(*av.Frame)
		frameStart := time.Now()

		pctx := processors.ProcessorContext{
			StreamID:   node.ID,
			MediaType:  mediaType,
			PTS:        f.PTS(),
			FrameIndex: frameIndex,
			Context:    ctx,
		}

		out, md, err := proc.Process(f, pctx)
		if err != nil {
			f.Close()
			return fmt.Errorf("go_processor %q: %w", node.ID, err)
		}

		// Advance every gate's progress cursor to this frame's source PTS
		// (in microseconds) so audio writers know all cuts ≤ this PTS have
		// been seen.
		if len(progressGates) > 0 {
			if us, ok := ptsToMicros(f.PTS(), r.goProcessorInputTB[node.ID]); ok {
				for g := range progressGates {
					g.advanceProgress(us)
				}
			}
		}

		// Emit metadata on the event bus if provided.
		if md != nil && r.pipe != nil {
			r.pipe.events.Post(ProcessorMetadata{
				NodeID:     node.ID,
				FrameIndex: frameIndex,
				PTS:        f.PTS(),
				Metadata:   md,
			})
			// Also write to any EventSink nodes connected via "events" edges.
			for _, s := range r.eventsSinks[node.ID] {
				s.Write(pctx, md)
			}
			// Signal segment_sink outputs watching any of the Custom keys.
			// Track whether any cut flag was set so we can force an IDR on the
			// output frame; the encoder then produces a keyframe at the exact
			// cut boundary rather than waiting for its next scheduled GOP.
			cutSignaled := false
			for key, val := range md.Custom {
				if val == true {
					gates := r.segmentCuts[key]
					if len(gates) == 0 {
						continue
					}
					// Convert the current frame's PTS into microseconds using
					// the resolved input time-base so cross-stream comparisons
					// (video cut vs. audio packet PTS) at the sink are valid.
					tb := r.goProcessorInputTB[node.ID]
					cutUS, ok := ptsToMicros(f.PTS(), tb)
					if !ok {
						continue
					}
					for _, g := range gates {
						g.signal(cutUS)
					}
					cutSignaled = true
				}
			}
			// Only close the input frame if the processor returned a new one.
			// When out == f (pass-through) closing f would free the AVFrame
			// that out still points to, making f.p nil on the next line.
			if out != f {
				f.Close()
			}
			f = out
			if cutSignaled && f != nil {
				f.SetPictType(av.PictureTypeI)
			}
		}

		frameIndex++
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))

		// nil output means the processor consumed (dropped) the frame.
		if out == nil {
			f.Close()
			continue
		}

		// No downstream AV channels: this go_processor is a terminal frame
		// processor (e.g. a scene detector whose AV output was redirected to
		// a source by rewriteGoProcessorCopyEdges). Close the frame and loop.
		if len(outs) == 0 {
			f.Close()
			continue
		}

		// Send output to all downstream channels.
		for _, ch := range outs {
			select {
			case ch <- f:
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			}
		}
	}
	return nil
}
