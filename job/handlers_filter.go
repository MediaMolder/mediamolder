// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// filterInternalThreads returns the per-graph thread cap stamped on
// node.Internal.Filter by NormalizeConfig (Milestone B). Returns 0
// when no cap is set, which lets libavfilter pick its default.
func filterInternalThreads(node *graph.Node) int {
	if node.Internal.Filter == nil {
		return 0
	}
	return node.Internal.Filter.Threads
}

// ---------- Filter handler ----------

func (r *graphRunner) handleFilter(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	fg := r.filters[node.ID]
	if fg == nil {
		return fmt.Errorf("filter handler: no filter graph for node %q", node.ID)
	}

	// Simple 1→1 fast-path.
	if len(ins) == 1 && len(outs) == 1 {
		return r.handleSimpleFilter(ctx, node, fg, ins[0], outs[0])
	}

	// Multi-input / multi-output: serialise all filter-graph operations
	// through a single goroutine to satisfy FFmpeg's thread-safety contract.
	t := perfTrackerFrom(ctx)

	type filterMsg struct {
		padIdx int
		frame  *av.Frame // nil = this input is exhausted
	}

	msgCh := make(chan filterMsg, 8*len(ins))

	var wg sync.WaitGroup
	for i, in := range ins {
		i, in := i, in
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range in {
				msgCh <- filterMsg{padIdx: i, frame: v.(*av.Frame)}
			}
			msgCh <- filterMsg{padIdx: i, frame: nil}
		}()
	}
	go func() {
		wg.Wait()
		close(msgCh)
	}()

	pullOutputs := func() error {
		for oi := range outs {
			for {
				f, err := av.AllocFrame()
				if err != nil {
					return err
				}
				if err := fg.PullFrameAt(oi, f); err != nil {
					f.Close()
					if av.IsEAgain(err) || av.IsEOF(err) {
						break
					}
					return err
				}
				if perfSend(ctx, outs[oi], f, t) {
					f.Close()
					return ctx.Err()
				}
			}
		}
		return nil
	}

	for {
		var msg filterMsg
		var ok bool
		select {
		case msg, ok = <-msgCh:
		default:
			t.BeginIdle()
			select {
			case msg, ok = <-msgCh:
				t.EndIdle()
			case <-ctx.Done():
				t.EndIdle()
				return ctx.Err()
			}
		}
		if !ok {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if msg.frame == nil {
			// Flush this input pad.
			if err := fg.FlushAt(msg.padIdx); err != nil && !av.IsEOF(err) {
				return err
			}
		} else {
			if err := fg.PushFrameAt(msg.padIdx, msg.frame); err != nil {
				msg.frame.Close()
				// EOF from a buffersrc means the filter has decided it
				// no longer wants frames on this pad (e.g. xfade after
				// the transition window has fully consumed an input,
				// or any "trim"-style filter past its endpoint). Mirror
				// fftools/ffmpeg_filter.c's behaviour: treat the pad as
				// drained and continue pulling outputs / feeding other
				// pads. EAGAIN is also benign — the buffersrc is full
				// and downstream needs to pull first; we'll get a
				// pullOutputs() pass below.
				if !av.IsEOF(err) && !av.IsEAgain(err) {
					return err
				}
			} else {
				msg.frame.Close()
			}
		}
		if err := pullOutputs(); err != nil {
			return err
		}
	}

	// Final drain of all outputs.
	for oi := range outs {
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := fg.PullFrameAt(oi, f); err != nil {
				f.Close()
				if av.IsEOF(err) || av.IsEAgain(err) {
					break
				}
				return err
			}
			if perfSend(ctx, outs[oi], f, t) {
				f.Close()
				return ctx.Err()
			}
		}
	}
	return nil
}

// handleSimpleFilter processes a single-input single-output filter chain.
func (r *graphRunner) handleSimpleFilter(ctx context.Context, node *graph.Node, fg *av.FilterGraph, in <-chan any, out chan<- any) error {
	t := perfTrackerFrom(ctx)
	pull := func() error {
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := fg.PullFrame(f); err != nil {
				f.Close()
				if av.IsEAgain(err) || av.IsEOF(err) {
					return nil
				}
				return err
			}
			if perfSend(ctx, out, f, t) {
				f.Close()
				return ctx.Err()
			}
		}
	}

	for {
		v, cancelled := perfReceive(ctx, in, t)
		if cancelled {
			break
		}
		recv := time.Now()
		f := v.(*av.Frame)
		if err := fg.PushFrame(f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if err := pull(); err != nil {
			return err
		}
		t.RecordFrameLatency(time.Since(recv))
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(recv))
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Flush and drain.
	if err := fg.Flush(); err != nil && !av.IsEOF(err) {
		return err
	}
	return pull()
}

// ---------- FilterSource handler (Wave 7 #36c) ----------

// handleFilterSource pumps frames from a source-only filter graph (built by
// createFilterSource via av.NewSourceFilterGraph) into one outbound channel
// per buffer sink. The source filter inside the graph (color, testsrc, sine,
// anullsrc, …) generates frames synchronously inside libavfilter — this
// handler just drains each sink in round-robin until every sink reports EOF.
//
// Bounded sources (testsrc2=duration=N, color=d=N, sine=duration=N, …)
// terminate naturally. Unbounded sources (no duration / nb_frames) only
// stop when the context is cancelled by the runtime, typically because the
// downstream encoder/sink has finished and closed its output channel.
func (r *graphRunner) handleFilterSource(ctx context.Context, node *graph.Node, outs []chan<- any) error {
	fg := r.filters[node.ID]
	if fg == nil {
		return fmt.Errorf("filter_source handler: no filter graph for node %q", node.ID)
	}
	if len(outs) != fg.NumOutputs() {
		return fmt.Errorf("filter_source %q: outs=%d but graph has %d sinks", node.ID, len(outs), fg.NumOutputs())
	}
	if fg.NumInputs() != 0 {
		return fmt.Errorf("filter_source %q: graph has %d buffer sources, expected 0", node.ID, fg.NumInputs())
	}

	done := make([]bool, len(outs))
	remaining := len(outs)

	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		anyProgress := false
		for oi := range outs {
			if done[oi] {
				continue
			}
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := fg.PullFrameAt(oi, f); err != nil {
				f.Close()
				if av.IsEOF(err) {
					done[oi] = true
					remaining--
					continue
				}
				if av.IsEAgain(err) {
					// Source filters never legitimately return EAGAIN —
					// they generate frames synchronously. Treat as EOF
					// to avoid spinning.
					done[oi] = true
					remaining--
					continue
				}
				return err
			}
			anyProgress = true
			select {
			case outs[oi] <- f:
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			}
		}
		if !anyProgress {
			// Defensive: shouldn't happen given the EAGAIN→done coercion
			// above, but guards against a libavfilter bug producing an
			// infinite no-progress loop.
			break
		}
	}
	return nil
}

// ---------- FilterSink handler (Wave 7 #36d) ----------

// handleFilterSink pushes frames from N inbound channels into the buffer
// sources of a sink-only filter graph (built by createFilterSink via
// av.NewSinkFilterGraph). The graph terminates every input pad in a
// libavfilter sink (nullsink, anullsink, or a chain ending in one such
// as `ebur128,anullsink` or `ametadata=mode=print:file=…,anullsink`),
// so there are no buffer sinks to drain. Frames are consumed for their
// side effects and discarded.
func (r *graphRunner) handleFilterSink(ctx context.Context, node *graph.Node, ins []<-chan any) error {
	fg := r.filters[node.ID]
	if fg == nil {
		return fmt.Errorf("filter_sink handler: no filter graph for node %q", node.ID)
	}
	if len(ins) != fg.NumInputs() {
		return fmt.Errorf("filter_sink %q: ins=%d but graph has %d sources", node.ID, len(ins), fg.NumInputs())
	}
	if fg.NumOutputs() != 0 {
		return fmt.Errorf("filter_sink %q: graph has %d buffer sinks, expected 0", node.ID, fg.NumOutputs())
	}

	// Single-input fast path.
	if len(ins) == 1 {
		for v := range ins[0] {
			f := v.(*av.Frame)
			if err := fg.PushFrame(f); err != nil {
				f.Close()
				if av.IsEOF(err) || av.IsEAgain(err) {
					continue
				}
				return err
			}
			f.Close()
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := fg.Flush(); err != nil && !av.IsEOF(err) {
			return err
		}
		return nil
	}

	// Multi-input: serialise all filter-graph operations through a
	// single goroutine (mirrors handleFilter).
	type filterMsg struct {
		padIdx int
		frame  *av.Frame // nil = this input is exhausted
	}
	msgCh := make(chan filterMsg, 8*len(ins))

	var wg sync.WaitGroup
	for i, in := range ins {
		i, in := i, in
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range in {
				msgCh <- filterMsg{padIdx: i, frame: v.(*av.Frame)}
			}
			msgCh <- filterMsg{padIdx: i, frame: nil}
		}()
	}
	go func() {
		wg.Wait()
		close(msgCh)
	}()

	for msg := range msgCh {
		if err := ctx.Err(); err != nil {
			return err
		}
		if msg.frame == nil {
			if err := fg.FlushAt(msg.padIdx); err != nil && !av.IsEOF(err) {
				return err
			}
			continue
		}
		if err := fg.PushFrameAt(msg.padIdx, msg.frame); err != nil {
			msg.frame.Close()
			if !av.IsEOF(err) && !av.IsEAgain(err) {
				return err
			}
		} else {
			msg.frame.Close()
		}
	}
	return nil
}

func (r *graphRunner) createFilter(dag *graph.Graph, node *graph.Node) (*av.FilterGraph, error) {
	params := node.Params
	// Loudnorm pass-2 shuttle: read the JSON stats file written by
	// pass 1 and inject measured_I / measured_TP / measured_LRA /
	// measured_thresh / offset into the loudnorm node's params before
	// the filter graph is instantiated. See pipeline/loudnorm.go.
	if node.Filter == "loudnorm" && node.Internal.Filter != nil && node.Internal.Filter.LoudnormPass == 2 {
		statsPath := node.Internal.Filter.LoudnormStatsFile
		measured, err := loadLoudnormMeasurements(statsPath)
		if err != nil {
			return nil, fmt.Errorf("filter %q: %w", node.ID, err)
		}
		merged := make(map[string]any, len(params)+len(measured))
		for k, v := range params {
			merged[k] = v
		}
		for k, v := range measured {
			merged[k] = v
		}
		params = merged
	}
	filterSpec := buildFilterSpec(NodeDef{
		Filter: node.Filter,
		Params: params,
	})
	threads := 0
	if node.Internal.Filter != nil {
		threads = node.Internal.Filter.Threads
	}

	// Simple 1→1 filter — fast path, but only when input and output
	// media types match. Cross-media-type filters (showwavespic,
	// showspectrumpic, showvolume, ...) need the complex graph because
	// NewVideoFilterGraph / NewAudioFilterGraph hard-wire the buffersink
	// type to match the buffersrc type. (Wave 7 #37)
	crossMedia := node.OutputMediaType != "" && len(node.Inbound) > 0 && node.OutputMediaType != node.Inbound[0].Type
	if len(node.Inbound) == 1 && len(node.Outbound) == 1 && !crossMedia {
		si, err := r.resolveStreamInfo(dag, node)
		if err != nil {
			return nil, fmt.Errorf("filter %q: %w", node.ID, err)
		}
		if node.Inbound[0].Type == graph.PortVideo {
			return av.NewVideoFilterGraph(av.VideoFilterGraphConfig{
				Width:      si.Width,
				Height:     si.Height,
				PixFmt:     si.PixFmt,
				TBNum:      si.TimeBase[0],
				TBDen:      si.TimeBase[1],
				SARNum:     1,
				SARDen:     1,
				FRNum:      si.FrameRate[0],
				FRDen:      si.FrameRate[1],
				FilterSpec: filterSpec,
				Threads:    threads,
			})
		}
		return av.NewAudioFilterGraph(av.AudioFilterGraphConfig{
			SampleFmt:  si.SampleFmt,
			SampleRate: si.SampleRate,
			Channels:   si.Channels,
			FilterSpec: filterSpec,
			Threads:    threads,
		})
	}

	// Multi-input / multi-output: use complex filter graph.
	inputs := make([]av.FilterPadConfig, len(node.Inbound))
	for i, e := range node.Inbound {
		si, err := r.resolveEdgeStreamInfo(dag, e)
		if err != nil {
			return nil, fmt.Errorf("filter %q input %d: %w", node.ID, i, err)
		}
		inputs[i] = av.FilterPadConfig{
			Label:      fmt.Sprintf("in%d", i),
			MediaType:  portTypeToAVMediaType(e.Type),
			Width:      si.Width,
			Height:     si.Height,
			PixFmt:     si.PixFmt,
			TBNum:      si.TimeBase[0],
			TBDen:      si.TimeBase[1],
			SARNum:     1,
			SARDen:     1,
			FRNum:      si.FrameRate[0],
			FRDen:      si.FrameRate[1],
			SampleFmt:  si.SampleFmt,
			SampleRate: si.SampleRate,
			Channels:   si.Channels,
		}
	}

	outputs := make([]av.FilterOutputConfig, len(node.Outbound))
	for i, e := range node.Outbound {
		outputs[i] = av.FilterOutputConfig{
			Label:     fmt.Sprintf("out%d", i),
			MediaType: portTypeToAVMediaType(e.Type),
		}
	}

	spec := buildComplexFilterSpec(filterSpec, len(inputs), len(outputs))

	return av.NewComplexFilterGraph(av.ComplexFilterGraphConfig{
		Inputs:     inputs,
		Outputs:    outputs,
		FilterSpec: spec,
		Threads:    threads,
	})
}

// createFilterSource builds a source-only filter graph for a KindFilterSource
// node. The node's Filter + Params describe a libavfilter source filter
// (color, testsrc, sine, anullsrc, …) and the resulting graph has zero
// buffer sources and one buffer sink per outbound edge. Wave 7 #36c.
func (r *graphRunner) createFilterSource(node *graph.Node) (*av.FilterGraph, error) {
	if len(node.Outbound) == 0 {
		return nil, fmt.Errorf("filter_source %q has no outbound edges", node.ID)
	}
	// Wave 7 #36e: rewrite per-node `protocol_whitelist` shortcut as a
	// libavformat `format_opts` entry on `movie` / `amovie` so the
	// underlying demuxer actually honours the policy.
	params := movieFilterParamsForSpec(node.Filter, node.Params)
	base := buildFilterSpec(NodeDef{Filter: node.Filter, Params: params})
	spec := buildSourceFilterSpec(base, len(node.Outbound))

	outputs := make([]av.FilterOutputConfig, len(node.Outbound))
	for i, e := range node.Outbound {
		outputs[i] = av.FilterOutputConfig{
			Label:     fmt.Sprintf("out%d", i),
			MediaType: portTypeToAVMediaType(e.Type),
		}
	}

	return av.NewSourceFilterGraph(av.SourceFilterGraphConfig{
		Outputs:    outputs,
		FilterSpec: spec,
		Threads:    filterInternalThreads(node),
	})
}

// buildSourceFilterSpec wraps a source filter chain with output pad labels.
// For a single-output source: "color=c=red:s=320x240:d=1[out0]".
// For an N-output source (e.g. testsrc + split): the base spec already
// produces N pads and we append [out0][out1]… to label them.
func buildSourceFilterSpec(base string, numOut int) string {
	var sb strings.Builder
	sb.WriteString(base)
	for i := 0; i < numOut; i++ {
		fmt.Fprintf(&sb, "[out%d]", i)
	}
	return sb.String()
}

// createFilterSink builds a sink-only filter graph for a KindFilterSink
// node. The node's Filter + Params describe a libavfilter sink chain
// terminating in nullsink/anullsink (e.g. "nullsink" or
// "ebur128=peak=true,anullsink"); the resulting graph has one buffer
// source per inbound edge and zero buffer sinks. Wave 7 #36d.
func (r *graphRunner) createFilterSink(dag *graph.Graph, node *graph.Node) (*av.FilterGraph, error) {
	if len(node.Inbound) == 0 {
		return nil, fmt.Errorf("filter_sink %q has no inbound edges", node.ID)
	}
	base := buildFilterSpec(NodeDef{Filter: node.Filter, Params: node.Params})
	spec := buildSinkFilterSpec(base, len(node.Inbound))

	inputs := make([]av.FilterPadConfig, len(node.Inbound))
	for i, e := range node.Inbound {
		si, err := r.resolveEdgeStreamInfo(dag, e)
		if err != nil {
			return nil, fmt.Errorf("filter_sink %q input %d: %w", node.ID, i, err)
		}
		inputs[i] = av.FilterPadConfig{
			Label:      fmt.Sprintf("in%d", i),
			MediaType:  portTypeToAVMediaType(e.Type),
			Width:      si.Width,
			Height:     si.Height,
			PixFmt:     si.PixFmt,
			TBNum:      si.TimeBase[0],
			TBDen:      si.TimeBase[1],
			SARNum:     1,
			SARDen:     1,
			FRNum:      si.FrameRate[0],
			FRDen:      si.FrameRate[1],
			SampleFmt:  si.SampleFmt,
			SampleRate: si.SampleRate,
			Channels:   si.Channels,
		}
	}

	return av.NewSinkFilterGraph(av.SinkFilterGraphConfig{
		Inputs:     inputs,
		FilterSpec: spec,
		Threads:    filterInternalThreads(node),
	})
}

// buildSinkFilterSpec wraps a sink filter chain with input pad labels.
// For a single-input sink: "[in0]nullsink".
// For an N-input sink (e.g. concat + nullsink): "[in0][in1]concat=…,nullsink".
func buildSinkFilterSpec(base string, numIn int) string {
	var sb strings.Builder
	for i := 0; i < numIn; i++ {
		fmt.Fprintf(&sb, "[in%d]", i)
	}
	sb.WriteString(base)
	return sb.String()
}
