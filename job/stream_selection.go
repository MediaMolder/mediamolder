// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
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
