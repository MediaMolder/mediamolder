// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

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
		// FrameSource processors generate their own frames and require no
		// inbound AV edge.  Dispatch via Run()/RunStreams() and forward each
		// produced frame to the matching downstream channels, recording node
		// metrics and render progress so the source node is visible (a
		// FrameSource has no inbound loop that would otherwise tick the metrics)
		// and the GUI shows frame X of N.
		if src, ok := proc.(processors.FrameSource); ok {
			var nm *NodeMetrics
			if r.pipe != nil {
				nm = r.pipe.Metrics().Node(node.ID)
			}

			// Determine the output streams this source emits. A
			// MultiStreamSource declares several (e.g. sequence_editor →
			// video + audio); a plain FrameSource emits a single stream whose
			// info comes from FrameSourceInfo (historically video).
			var streams []av.StreamInfo
			if ms, ok := proc.(processors.MultiStreamSource); ok {
				streams = ms.OutputStreams()
			} else if fi, ok := proc.(processors.FrameSourceInfo); ok {
				if si, e := fi.OutputStreamInfo(); e == nil {
					streams = []av.StreamInfo{si}
				}
			}
			if len(streams) == 0 {
				streams = []av.StreamInfo{{Type: av.MediaTypeVideo}}
			}

			// Route each declared stream to the outbound channels whose port
			// type matches its media type. Mirrors handleSource's per-type
			// routing so a frame reaches only consumers that asked for that
			// media type.
			streamToChans := make([][]int, len(streams))
			anyMatched := false
			for si := range streams {
				for j, e := range node.Outbound {
					if portTypeToAVMediaType(e.Type) == streams[si].Type {
						streamToChans[si] = append(streamToChans[si], j)
						anyMatched = true
					}
				}
			}
			// Fallback: if no edge type matched (e.g. a single untyped
			// "default" edge), broadcast stream 0 to every channel — the
			// historical single-stream behaviour.
			if !anyMatched && len(outs) > 0 {
				all := make([]int, len(outs))
				for j := range outs {
					all[j] = j
				}
				streamToChans[0] = all
			}

			// Master stream drives metrics/progress: the first video stream,
			// else stream 0.
			master := 0
			for si := range streams {
				if streams[si].Type == av.MediaTypeVideo {
					master = si
					break
				}
			}
			fps := 0.0
			if streams[master].FrameRate[1] > 0 {
				fps = float64(streams[master].FrameRate[0]) / float64(streams[master].FrameRate[1])
			}
			var total int64
			if fp, ok := proc.(processors.FrameSourceProgress); ok {
				total = fp.OutputFrameCount()
			}
			// The progress denominator is the sequence's own duration, not the
			// (much longer) source files' durations.
			if nm != nil && total > 0 && fps > 0 {
				nm.SetMediaDuration(time.Duration(float64(total) / fps * float64(time.Second)))
			}

			var produced int64 // master-stream frames produced
			prev := time.Now()
			lastLog := prev
			postProgress := func() {
				if r.pipe == nil {
					return
				}
				custom := map[string]any{"rendered": produced}
				msg := fmt.Sprintf("rendered %d frames", produced)
				if total > 0 {
					pct := 100 * float64(produced) / float64(total)
					custom["total"] = total
					custom["fraction"] = float64(produced) / float64(total)
					msg = fmt.Sprintf("rendered %d / %d frames (%.0f%%)", produced, total, pct)
				}
				custom["message"] = msg
				r.pipe.events.Post(ProcessorMetadata{
					NodeID:     node.ID,
					FrameIndex: uint64(produced),
					Metadata:   &processors.Metadata{Progress: true, Custom: custom},
				})
			}

			// sendStream forwards f to the channels mapped to the given stream,
			// cloning for all but the last recipient so each consumer owns an
			// independent reference. Closes f when the stream has no consumer.
			sendStream := func(stream int, f *av.Frame) error {
				if stream < 0 || stream >= len(streamToChans) || len(streamToChans[stream]) == 0 {
					f.Close()
					return nil
				}
				chans := streamToChans[stream]
				for i, idx := range chans {
					toSend := f
					if i < len(chans)-1 {
						cl, err := f.Clone()
						if err != nil {
							f.Close()
							return err
						}
						toSend = cl
					}
					select {
					case outs[idx] <- toSend:
					case <-ctx.Done():
						if toSend != f {
							toSend.Close()
						}
						f.Close()
						return ctx.Err()
					}
				}
				if stream == master {
					produced++
					now := time.Now()
					if nm != nil {
						nm.RecordLatency(now.Sub(prev))
						if fps > 0 {
							nm.AdvanceMediaPTS(time.Duration(float64(produced) / fps * float64(time.Second)))
						}
					}
					prev = now
					if now.Sub(lastLog) >= time.Second {
						lastLog = now
						postProgress()
					}
				}
				return nil
			}

			var err error
			if ms, ok := proc.(processors.MultiStreamSource); ok {
				err = ms.RunStreams(ctx, sendStream)
			} else {
				err = src.Run(ctx, func(f *av.Frame) error { return sendStream(0, f) })
			}
			postProgress() // final tally
			return err
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

	// If the processor implements FrameLookahead it needs a delay buffer so
	// that when a windowed detector confirms a cut at frame K (while
	// processing frame K+lookback), frame K is still available to receive the
	// IDR annotation before being forwarded to the encoder.
	lookback := 0
	if la, ok := proc.(processors.FrameLookahead); ok {
		lookback = la.LookbackFrames()
	}
	type delayEntry struct{ frame *av.Frame }
	var delayBuf []delayEntry
	tb := r.goProcessorInputTB[node.ID]

	// sendFrame forwards f to all downstream AV channels.
	sendFrame := func(f *av.Frame) error {
		for _, ch := range outs {
			select {
			case ch <- f:
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			}
		}
		return nil
	}

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

		// Advance every gate's progress cursor so audio writers know all
		// cuts up to that PTS have been confirmed. When a delay buffer is
		// active, the frame being emitted this iteration is delayBuf[0]
		// (2 frames behind f), so using delayBuf[0].PTS() prevents the
		// progress boundary from outrunning the cut signal and sending
		// audio to the wrong segment.
		if len(progressGates) > 0 {
			var progressPTS int64
			if lookback > 0 && len(delayBuf) > 0 {
				progressPTS = delayBuf[0].frame.PTS()
			} else {
				progressPTS = f.PTS()
			}
			if us, ok := ptsToMicros(progressPTS, tb); ok {
				for g := range progressGates {
					g.advanceProgress(us)
				}
			}
		}

		// Emit metadata on the event bus if provided.
		if md != nil && r.pipe != nil {
			// When a delay buffer is active, the cut was detected looking back
			// at delayBuf[0] — update the metadata PTS to that frame's PTS so
			// the metadata file records the accurate cut timestamp.
			if lookback > 0 && len(delayBuf) > 0 {
				if md.Custom != nil {
					md.Custom["pts"] = delayBuf[0].frame.PTS()
				}
			}
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
			// correct output frame (the cut frame, not the current frame).
			cutSignaled := false
			for key, val := range md.Custom {
				if val == true {
					gates := r.segmentCuts[key]
					if len(gates) == 0 {
						continue
					}
					// Use the cut frame's PTS (delayBuf[0]) when a delay buffer
					// is active; otherwise fall back to the current frame.
					var cutPTS int64
					if lookback > 0 && len(delayBuf) > 0 {
						cutPTS = delayBuf[0].frame.PTS()
					} else {
						cutPTS = f.PTS()
					}
					cutUS, ok := ptsToMicros(cutPTS, tb)
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
			// Set IDR annotation: on the cut frame (delayBuf[0]) when using a
			// delay buffer, on the current frame otherwise.
			if cutSignaled {
				if lookback > 0 && len(delayBuf) > 0 {
					if err := markFrameForceKeyframe(delayBuf[0].frame); err != nil {
						return fmt.Errorf("go_processor %q: mark force keyframe: %w", node.ID, err)
					}
				} else if f != nil {
					if err := markFrameForceKeyframe(f); err != nil {
						return fmt.Errorf("go_processor %q: mark force keyframe: %w", node.ID, err)
					}
				}
			}
		}

		frameIndex++
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))

		// nil output means the processor consumed (dropped) the frame.
		if out == nil {
			f.Close()
			// With a delay buffer still forward the oldest held frame so
			// the buffer does not grow unboundedly.
			if lookback > 0 && len(delayBuf) >= lookback {
				oldest := delayBuf[0]
				delayBuf = delayBuf[1:]
				if len(outs) == 0 {
					oldest.frame.Close()
				} else {
					if err := sendFrame(oldest.frame); err != nil {
						return err
					}
				}
			}
			continue
		}

		// No downstream AV channels: this go_processor is a terminal frame
		// processor (e.g. a scene detector whose AV output was redirected to
		// a source by rewriteGoProcessorCopyEdges). Close the frame and loop.
		if len(outs) == 0 {
			f.Close()
			continue
		}

		if lookback > 0 {
			// Push the current output frame into the delay buffer.
			delayBuf = append(delayBuf, delayEntry{frame: f})
			// Once the buffer is full, forward the oldest frame (which already
			// has any IDR annotation applied above).
			if len(delayBuf) > lookback {
				oldest := delayBuf[0]
				delayBuf = delayBuf[1:]
				if err := sendFrame(oldest.frame); err != nil {
					return err
				}
			}
		} else {
			// No delay buffer: send immediately.
			if err := sendFrame(f); err != nil {
				return err
			}
		}
	}

	// Drain any frames still in the delay buffer at EOS.
	for _, e := range delayBuf {
		if len(outs) == 0 {
			e.frame.Close()
			continue
		}
		if err := sendFrame(e.frame); err != nil {
			return err
		}
	}
	return nil
}
