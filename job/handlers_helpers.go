// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
)

// ---------- Helpers ----------

// audioLayoutChannels returns the channel count for a named FFmpeg channel
// layout (e.g. "stereo" → 2, "5.1" → 6). Returns 0 for unrecognised names.
func audioLayoutChannels(layout string) int {
	switch layout {
	case "mono":
		return 1
	case "stereo":
		return 2
	case "2.1", "3.0", "3.0(back)", "surround":
		return 3
	case "4.0", "quad", "quad(side)", "3.1":
		return 4
	case "5.0", "5.0(side)", "4.1":
		return 5
	case "5.1", "5.1(side)", "6.0", "6.0(front)", "hexagonal", "3.1.2":
		return 6
	case "6.1", "6.1(back)", "6.1(front)", "7.0", "7.0(front)":
		return 7
	case "7.1", "7.1(wide)", "7.1(wide-side)", "octagonal":
		return 8
	default:
		return 0
	}
}

func (r *graphRunner) findOutputConfig(id string) *Output {
	for i := range r.cfg.Outputs {
		if r.cfg.Outputs[i].ID == id {
			return &r.cfg.Outputs[i]
		}
	}
	return nil
}

// resolveStreamInfo returns the upstream StreamInfo for a node by walking
// inbound edges back to a source.
func (r *graphRunner) resolveStreamInfo(dag *graph.Graph, node *graph.Node) (av.StreamInfo, error) {
	if len(node.Inbound) == 0 {
		return av.StreamInfo{}, fmt.Errorf("node %q has no inbound edges", node.ID)
	}
	return r.resolveEdgeStreamInfo(dag, node.Inbound[0])
}

func (r *graphRunner) resolveEdgeStreamInfo(dag *graph.Graph, e *graph.Edge) (av.StreamInfo, error) {
	from := e.From
	switch from.Kind {
	case graph.KindSource:
		src := r.sources[from.ID]
		if src == nil {
			return av.StreamInfo{}, fmt.Errorf("no source resources for node %q", from.ID)
		}
		mt := portTypeToAVMediaType(e.Type)
		for _, si := range src.streams {
			if si.Type == mt {
				return si, nil
			}
		}
		return av.StreamInfo{}, fmt.Errorf("source %q has no %v stream", from.ID, e.Type)
	case graph.KindFilter:
		// If the upstream filter graph is already built (topological order
		// guarantees this), query its actual output dimensions rather than
		// tracing all the way back to the source. This is critical for
		// chained filters (e.g. scale → fps) where the downstream node must
		// be initialised with the scaled, not the source, dimensions.
		if fg := r.filters[from.ID]; fg != nil {
			padIdx := 0
			if e.FromPort != "default" {
				if n, err := strconv.Atoi(e.FromPort); err == nil {
					padIdx = n
				}
			}
			si, err := r.resolveStreamInfo(dag, from)
			if err != nil {
				return av.StreamInfo{}, err
			}
			switch e.Type {
			case graph.PortVideo:
				if w := fg.OutputWidth(padIdx); w > 0 {
					si.Width = w
				}
				if h := fg.OutputHeight(padIdx); h > 0 {
					si.Height = h
				}
				if pf := fg.OutputPixFmt(padIdx); pf >= 0 {
					si.PixFmt = pf
				}
				// Frame-rate metadata advertised by the upstream filter (e.g.
				// fps, framerate, minterpolate, settb) overrides the source's
				// guessed rate. Required so downstream filters that demand a
				// constant frame rate (xfade, framerate, minterpolate) can
				// configure their buffersrc with the correct value.
				if frn, frd := fg.OutputFrameRate(padIdx); frn > 0 && frd > 0 {
					si.FrameRate = [2]int{frn, frd}
				}
				if tbn, tbd := fg.OutputTimeBase(padIdx); tbn > 0 && tbd > 0 {
					si.TimeBase = [2]int{tbn, tbd}
				}
			case graph.PortAudio:
				if sr := fg.OutputSampleRate(padIdx); sr > 0 {
					si.SampleRate = sr
				}
				if ch := fg.OutputChannels(padIdx); ch > 0 {
					si.Channels = ch
				}
				if sf := fg.OutputSampleFmt(padIdx); sf >= 0 {
					si.SampleFmt = sf
				}
				if tbn, tbd := fg.OutputTimeBase(padIdx); tbn > 0 && tbd > 0 {
					si.TimeBase = [2]int{tbn, tbd}
				}
			}
			return si, nil
		}
		return r.resolveStreamInfo(dag, from)
	case graph.KindFilterSource:
		// Wave 7 #36c: a filter_source has no inbound edges, so the
		// only authoritative source for downstream geometry/format
		// metadata is the buffer sink the source filter feeds.
		fg := r.filters[from.ID]
		if fg == nil {
			return av.StreamInfo{}, fmt.Errorf("no filter graph for filter_source %q", from.ID)
		}
		padIdx := 0
		if e.FromPort != "default" {
			if n, err := strconv.Atoi(e.FromPort); err == nil {
				padIdx = n
			}
		}
		si := av.StreamInfo{Type: portTypeToAVMediaType(e.Type)}
		switch e.Type {
		case graph.PortVideo:
			si.Width = fg.OutputWidth(padIdx)
			si.Height = fg.OutputHeight(padIdx)
			if pf := fg.OutputPixFmt(padIdx); pf >= 0 {
				si.PixFmt = pf
			}
			if frn, frd := fg.OutputFrameRate(padIdx); frn > 0 && frd > 0 {
				si.FrameRate = [2]int{frn, frd}
			}
			if tbn, tbd := fg.OutputTimeBase(padIdx); tbn > 0 && tbd > 0 {
				si.TimeBase = [2]int{tbn, tbd}
			}
		case graph.PortAudio:
			if sr := fg.OutputSampleRate(padIdx); sr > 0 {
				si.SampleRate = sr
			}
			if ch := fg.OutputChannels(padIdx); ch > 0 {
				si.Channels = ch
			}
			if sf := fg.OutputSampleFmt(padIdx); sf >= 0 {
				si.SampleFmt = sf
			}
			if tbn, tbd := fg.OutputTimeBase(padIdx); tbn > 0 && tbd > 0 {
				si.TimeBase = [2]int{tbn, tbd}
			}
		}
		return si, nil
	case graph.KindGoProcessor:
		// FrameSource processors have no inbound edges; ask the processor
		// directly for its output format before falling back to upstream
		// traversal. A MultiStreamSource (e.g. sequence_editor → video+audio)
		// declares one StreamInfo per stream — pick the one matching this
		// edge's media type so the audio encoder reads the sequence's
		// sample-rate/channels and the video encoder its geometry.
		if proc, ok := r.goProcessors[from.ID]; ok {
			if ms, ok := proc.(processors.MultiStreamSource); ok {
				mt := portTypeToAVMediaType(e.Type)
				for _, si := range ms.OutputStreams() {
					if si.Type == mt {
						return si, nil
					}
				}
			}
			if info, ok := proc.(processors.FrameSourceInfo); ok {
				si, err := info.OutputStreamInfo()
				if err != nil {
					return av.StreamInfo{}, fmt.Errorf("node %q: %w", from.ID, err)
				}
				return si, nil
			}
		}
		// Fall back to walking upstream edges for processors with inbound edges.
		return r.resolveStreamInfo(dag, from)
	default:
		return av.StreamInfo{}, fmt.Errorf("cannot resolve stream info from node %q (kind=%v)", from.ID, from.Kind)
	}
}

// upstreamFilterGraph returns the av.FilterGraph for the immediate upstream
// filter, if any. Used to query output dimensions after scaling/padding.
func (r *graphRunner) upstreamFilterGraph(_ *graph.Graph, node *graph.Node) *av.FilterGraph {
	if len(node.Inbound) == 0 {
		return nil
	}
	from := node.Inbound[0].From
	if from.Kind == graph.KindFilter {
		return r.filters[from.ID]
	}
	return nil
}

// buildComplexFilterSpec wraps a base filter spec with input/output pad labels.
// For overlay: "[in0][in1]overlay[out0]"
// For split:   "[in0]split[out0][out1]"
func buildComplexFilterSpec(baseSpec string, numIn, numOut int) string {
	var sb strings.Builder
	for i := 0; i < numIn; i++ {
		fmt.Fprintf(&sb, "[in%d]", i)
	}
	sb.WriteString(baseSpec)
	for i := 0; i < numOut; i++ {
		fmt.Fprintf(&sb, "[out%d]", i)
	}
	return sb.String()
}

func portTypeToAVMediaType(pt graph.PortType) av.MediaType {
	switch pt {
	case graph.PortVideo:
		return av.MediaTypeVideo
	case graph.PortAudio:
		return av.MediaTypeAudio
	case graph.PortSubtitle:
		return av.MediaTypeSubtitle
	case graph.PortAttachment:
		return av.MediaTypeAttachment
	default:
		return av.MediaTypeUnknown
	}
}

func paramString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	// JSON numbers unmarshal into map[string]any as float64. Format them as
	// integers (no decimal/scientific notation) so that Sscanf %d parses them
	// correctly (e.g. 7000000.0 → "7000000", not "7e+06").
	if f, ok := v.(float64); ok {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprintf("%v", v)
}

func paramInt(m map[string]any, key string) int {
	s := paramString(m, key)
	if s == "" {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

func paramInt64(m map[string]any, key string) int64 {
	s := paramString(m, key)
	if s == "" {
		return 0
	}
	var n int64
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

// applyOutputMetadata writes container-level metadata onto muxer
// according to the precedence rules documented at the call site:
// Output.Metadata wins outright; otherwise an explicit
// metadata_writer/reader graph-node pair wins (Wave 2 #11);
// otherwise every input with Input.MapMetadata=true contributes in
// declaration order.
func (r *graphRunner) applyOutputMetadata(muxer *av.OutputFormatContext, out *Output) error {
	if out.Metadata != nil {
		return muxer.SetMetadata(out.Metadata)
	}
	// Wave 2 #11: metadata_writer node targeting this output wins
	// over the input-wide MapMetadata fallback.
	if md, ok := r.routedContainerMetadata(out.ID); ok {
		return muxer.SetMetadata(md)
	}
	var merged map[string]string
	for _, in := range r.cfg.Inputs {
		if !in.MapMetadata {
			continue
		}
		src := r.sources[in.ID]
		if src == nil || src.input == nil {
			continue
		}
		for k, v := range src.input.Metadata() {
			if merged == nil {
				merged = make(map[string]string)
			}
			merged[k] = v
		}
	}
	if merged == nil {
		return nil
	}
	return muxer.SetMetadata(merged)
}

// applyOutputChapters writes chapters onto muxer with the same
// precedence as applyOutputMetadata, except chapters are not merged
// across inputs (FFmpeg's `-map_chapters` is single-source); the first
// input with MapChapters=true wins.
func (r *graphRunner) applyOutputChapters(muxer *av.OutputFormatContext, out *Output) error {
	if len(out.Chapters) > 0 {
		for _, ch := range out.Chapters {
			if err := muxer.AddChapter(ch.ID, ch.Start, ch.End, ch.Title, ch.Metadata); err != nil {
				return err
			}
		}
		return nil
	}
	// Wave 2 #11: metadata_writer node with section=chapters
	// targeting this output wins.
	if chs, ok := r.routedChapters(out.ID); ok {
		for _, ch := range chs {
			if err := muxer.AddChapter(ch.ID, ch.Start, ch.End, ch.Title, ch.Metadata); err != nil {
				return err
			}
		}
		return nil
	}
	for _, in := range r.cfg.Inputs {
		if !in.MapChapters {
			continue
		}
		src := r.sources[in.ID]
		if src == nil || src.input == nil {
			continue
		}
		for _, ch := range src.input.Chapters() {
			if err := muxer.AddChapter(ch.ID, ch.Start, ch.End, ch.Title, ch.Metadata); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// routedContainerMetadata resolves a metadata_writer node targeting
// outputID with section!=chapters and walks back along its inbound
// metadata edges to find a metadata_reader node, returning the source
// input's container metadata. Returns ok=false when no such routing
// exists.
func (r *graphRunner) routedContainerMetadata(outputID string) (map[string]string, bool) {
	reader := r.findMetadataReaderFor(outputID, false)
	if reader == nil {
		return nil, false
	}
	src := paramString(reader.Params, "source")
	in := r.sources[src]
	if in == nil || in.input == nil {
		return nil, false
	}
	md := in.input.Metadata()
	if md == nil {
		md = map[string]string{}
	}
	return md, true
}

// routedChapters mirrors routedContainerMetadata for the chapter
// section.
func (r *graphRunner) routedChapters(outputID string) ([]av.ChapterInfo, bool) {
	reader := r.findMetadataReaderFor(outputID, true)
	if reader == nil {
		return nil, false
	}
	src := paramString(reader.Params, "source")
	in := r.sources[src]
	if in == nil || in.input == nil {
		return nil, false
	}
	return in.input.Chapters(), true
}

// findMetadataReaderFor locates the metadata_reader connected by a
// metadata edge to a metadata_writer targeting outputID. When
// wantChapters is true the writer/reader pair must opt into
// section=chapters; otherwise both must be the default (global)
// section.
func (r *graphRunner) findMetadataReaderFor(outputID string, wantChapters bool) *NodeDef {
	wantSection := "global"
	if wantChapters {
		wantSection = "chapters"
	}
	nodesByID := make(map[string]*NodeDef, len(r.cfg.Graph.Nodes))
	for i := range r.cfg.Graph.Nodes {
		n := &r.cfg.Graph.Nodes[i]
		nodesByID[n.ID] = n
	}
	for i := range r.cfg.Graph.Nodes {
		w := &r.cfg.Graph.Nodes[i]
		if w.Type != "metadata_writer" {
			continue
		}
		if paramString(w.Params, "target") != outputID {
			continue
		}
		section := paramString(w.Params, "section")
		if section == "" {
			section = "global"
		}
		if section != wantSection {
			continue
		}
		// Find metadata edge feeding this writer.
		for _, e := range r.cfg.Graph.Edges {
			if e.Type != "metadata" || edgeNodeID(e.To) != w.ID {
				continue
			}
			reader := nodesByID[edgeNodeID(e.From)]
			if reader == nil || reader.Type != "metadata_reader" {
				continue
			}
			rsec := paramString(reader.Params, "section")
			if rsec == "" {
				rsec = "global"
			}
			if rsec != wantSection {
				continue
			}
			return reader
		}
	}
	return nil
}

// edgeNodeID strips the optional ":port" / ":type:track" suffix from
// an edge endpoint reference.
func edgeNodeID(ref string) string {
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		return ref[:i]
	}
	return ref
}
