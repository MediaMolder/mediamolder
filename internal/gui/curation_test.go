// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/processors"
)

// TestCuratedNodesResolveToRealEntries asserts that every key in the
// curatedNodes registry corresponds to either an actual libavcodec
// encoder, a libavfilter filter, or a registered Go processor. This
// catches typos and FFmpeg-side renames at build time instead of
// silently dropping curated entries from the "Common" palette view.
//
// Keys that are absent from the running build (e.g. libfdk_aac when
// FFmpeg was configured without --enable-libfdk-aac) are tolerated: a
// missing key just means the entry will not appear in /api/nodes at
// all, which is the same handling as for any other unbuilt codec /
// filter. We only fail on keys that name nothing the running binary
// could possibly emit.
func TestCuratedNodesResolveToRealEntries(t *testing.T) {
	t.Parallel()

	// Build name sets from the live registries.
	encoders := map[string]struct{}{}
	for _, c := range av.ListCodecs() {
		if c.IsEncoder {
			encoders[c.Name] = struct{}{}
		}
	}
	filters := map[string]struct{}{}
	for _, f := range av.ListFilters() {
		filters[f.Name] = struct{}{}
	}
	procs := map[string]struct{}{}
	for _, n := range processors.Names() {
		procs[n] = struct{}{}
	}

	// Curated keys we know depend on optional FFmpeg features. If the
	// running build doesn't have them, skip — don't fail. Anything
	// outside this set MUST resolve.
	optional := map[string]struct{}{
		"libx264": {}, "libx265": {}, "libsvtav1": {}, "libaom-av1": {},
		"libvpx-vp9": {}, "libvpx": {}, "libfdk_aac": {}, "libopus": {},
		"libmp3lame": {}, "libvorbis": {},
		"h264_videotoolbox": {}, "hevc_videotoolbox": {}, "prores_videotoolbox": {},
		"h264_nvenc": {}, "hevc_nvenc": {}, "av1_nvenc": {},
		"h264_qsv": {}, "hevc_qsv": {}, "av1_qsv": {},
		"h264_amf": {}, "hevc_amf": {},
		"h264_vaapi": {}, "hevc_vaapi": {},
		"subtitles": {}, "ass": {}, // require --enable-libass
		"tonemap":       {}, // requires libavfilter built with HDR support
		"drawtext":      {}, // requires --enable-libfreetype
		"frame_counter": {}, "frame_info": {}, "scene_change": {},
	}

	for name := range curatedNodes {
		if _, ok := encoders[name]; ok {
			continue
		}
		if _, ok := filters[name]; ok {
			continue
		}
		if _, ok := procs[name]; ok {
			continue
		}
		if _, ok := optional[name]; ok {
			continue
		}
		t.Errorf("curatedNodes key %q does not match any encoder, filter, or processor in the running build", name)
	}
}
