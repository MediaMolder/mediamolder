// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// resolveStreamSelection walks `selectors` in declaration order
// against the input's `allStreams` (and its program table when any
// selector uses `Program`) and returns the resolved set of stream
// indices in the order they were first added — exactly the order
// FFmpeg's `fftools/ffmpeg_opt.c::map_manual` produces.
//
// Semantics:
//   - non-Negate selector: add every matching stream to the
//     selection (preserving prior order; duplicates are skipped).
//   - Negate selector: remove every matching stream from the current
//     selection.
//   - All=true: matches every stream of `Type` (and `Program` when
//     Program > 0); All=false: matches only the `Track`-th stream of
//     `Type` (and `Program`).
//   - Optional=true: a missing match is a silent skip; otherwise it
//     is reported as an error.
//
// Mirrors `cmdutils.c::check_stream_specifier`'s `p:N` semantics:
// `Program` matches against the `AVProgram.id` field (NOT the array
// index), so a transport stream with program 1 + program 2 is
// addressed by the PMT-assigned numbers.
func resolveStreamSelection(selectors []StreamSelect, allStreams []av.StreamInfo, programs []av.ProgramInfo) ([]int, error) {
	// Build program-membership lookup: streamIdx → set of program IDs.
	streamPrograms := map[int]map[int]bool{}
	for _, p := range programs {
		for _, idx := range p.StreamIndices {
			if streamPrograms[idx] == nil {
				streamPrograms[idx] = map[int]bool{}
			}
			streamPrograms[idx][p.ID] = true
		}
	}

	matches := func(sel StreamSelect, si av.StreamInfo, count *int) bool {
		if si.Type.String() != sel.Type {
			return false
		}
		if sel.Program > 0 {
			ps := streamPrograms[si.Index]
			if !ps[sel.Program] {
				return false
			}
		}
		if sel.All {
			return true
		}
		// Track-form: count occurrences of (Type, Program) pairs and
		// fire on the Track-th one.
		if *count == sel.Track {
			*count++
			return true
		}
		*count++
		return false
	}

	selection := []int{}
	contains := func(idx int) int {
		for i, v := range selection {
			if v == idx {
				return i
			}
		}
		return -1
	}

	for j, sel := range selectors {
		count := 0
		any := false
		if sel.Negate {
			// Walk allStreams, removing matches.
			for _, si := range allStreams {
				if matches(sel, si, &count) {
					any = true
					if pos := contains(si.Index); pos >= 0 {
						selection = append(selection[:pos], selection[pos+1:]...)
					}
				}
				if !sel.All && count > sel.Track {
					break
				}
			}
		} else {
			for _, si := range allStreams {
				if matches(sel, si, &count) {
					any = true
					if contains(si.Index) < 0 {
						selection = append(selection, si.Index)
					}
				}
				if !sel.All && count > sel.Track {
					break
				}
			}
		}
		if !any && !sel.Optional {
			return nil, missingStreamError(j, sel)
		}
	}
	return selection, nil
}

// streamConsumer captures one media edge's demand on a source input's demuxed
// streams: the media type it carries and, when the edge names an explicit
// track ("in0:a:1"), that track. A type-only edge ("in0:a") or a bare/default
// reference leaves trackKnown false, meaning it consumes every stream of the
// type.
type streamConsumer struct {
	typ        string
	track      int
	trackKnown bool
}

// isMediaEdgeType reports whether an edge Type names a demuxable media stream
// (as opposed to a routing-only edge: metadata/events/file). Accepts both the
// authoring EdgeDef.Type strings and string(graph.PortType) — they share the
// same vocabulary.
func isMediaEdgeType(t string) bool {
	switch t {
	case string(graph.PortVideo), string(graph.PortAudio), string(graph.PortSubtitle),
		string(graph.PortData), string(graph.PortAttachment):
		return true
	default:
		return false
	}
}

// edgeTrack extracts the explicit 0-based track from an edge endpoint port key
// ("v:0" → 0,true; "a:2" → 2,true). A type-only ("v"/"a") or "default" key
// returns (0,false): the edge consumes every track of its type.
func edgeTrack(port string) (int, bool) {
	if i := strings.LastIndex(port, ":"); i >= 0 {
		if n, err := strconv.Atoi(port[i+1:]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// configInputConsumers collects the media consumers of input inputID from the
// authoring edge list (EdgeDef.From like "in0:v:0").
func configInputConsumers(edges []EdgeDef, inputID string) []streamConsumer {
	var cs []streamConsumer
	for _, e := range edges {
		if !isMediaEdgeType(e.Type) {
			continue
		}
		node, port := e.From, ""
		if i := strings.Index(e.From, ":"); i >= 0 {
			node, port = e.From[:i], e.From[i+1:]
		}
		if node != inputID {
			continue
		}
		track, known := edgeTrack(port)
		cs = append(cs, streamConsumer{typ: e.Type, track: track, trackKnown: known})
	}
	return cs
}

// nodeInputConsumers collects the media consumers of a built source node from
// its resolved outbound edges (graph.Edge.FromPort like "v:0").
func nodeInputConsumers(srcNode *graph.Node) []streamConsumer {
	if srcNode == nil {
		return nil
	}
	var cs []streamConsumer
	for _, e := range srcNode.Outbound {
		t := string(e.Type)
		if !isMediaEdgeType(t) {
			continue
		}
		track, known := edgeTrack(e.FromPort)
		cs = append(cs, streamConsumer{typ: t, track: track, trackKnown: known})
	}
	return cs
}

// selectionConsumed reports whether the declared stream ss is read by at least
// one of the given media consumers.
func selectionConsumed(ss StreamSelect, consumers []streamConsumer) bool {
	for _, c := range consumers {
		if c.typ != ss.Type {
			continue
		}
		if ss.All || !c.trackKnown || c.track == ss.Track {
			return true
		}
	}
	return false
}

// streamSelectionDropped reports whether a declared input stream should be
// dropped from demux selection because nothing downstream reads it. A mandatory
// selector (not Optional, not Negate) naming a stream that no edge consumes is
// demuxed by nobody — handlers_source.go routes only edge-referenced streams —
// so requiring its presence in the file would reject jobs that are otherwise
// runnable (e.g. a "copy audio" graph applied to a video-only file, or any
// stale stream declaration the GUI left behind). Mirrors FFmpeg, where an
// unmapped stream's absence is never an error.
//
// Only drops when the input has at least one media consumer edge, so inputs
// opened directly by a FrameSource processor (which carry no outbound AV edges)
// are left untouched.
func streamSelectionDropped(consumers []streamConsumer, ss StreamSelect) bool {
	if ss.Optional || ss.Negate || len(consumers) == 0 {
		return false
	}
	return !selectionConsumed(ss, consumers)
}

// dropUnconsumedSelections returns the subset of sel that survives
// streamSelectionDropped, without mutating sel.
func dropUnconsumedSelections(sel []StreamSelect, consumers []streamConsumer) []StreamSelect {
	out := make([]StreamSelect, 0, len(sel))
	for _, ss := range sel {
		if streamSelectionDropped(consumers, ss) {
			continue
		}
		out = append(out, ss)
	}
	return out
}

func missingStreamError(j int, sel StreamSelect) error {
	descr := "type=" + sel.Type
	if sel.Program > 0 {
		descr = fmt.Sprintf("%s program=%d", descr, sel.Program)
	}
	if sel.All {
		descr = descr + " (all)"
	} else {
		descr = fmt.Sprintf("%s track=%d", descr, sel.Track)
	}
	return fmt.Errorf("streams[%d]: no input stream matches %s (use optional=true to silence)", j, descr)
}
