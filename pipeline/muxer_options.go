// Copyright (C) 2026 Thomas Vaughan
//
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"fmt"
	"strconv"
	"strings"
)

// buildMuxerOptions renders the AVDictionary that the runtime hands
// to `av.OutputFormatContext.WriteHeaderWithOptions`. Typed
// `Output.HLS` / `Output.DASH` fields are emitted under their
// libavformat AVOption names (see `libavformat/hlsenc.c`,
// `libavformat/dashenc.c`); generic `Output.Options` is layered in
// first so a typed field always wins on key collision. Returns nil
// when no options are set.
func buildMuxerOptions(out *Output) map[string]string {
	if out == nil {
		return nil
	}
	dict := map[string]string{}
	for k, v := range out.Options {
		if k == "" {
			continue
		}
		// Output-side timing keys are consumed by
		// resolveOutputTiming and must not be passed through to
		// the muxer (libavformat would either reject them as
		// unknown AVOptions or, worse, interpret them differently).
		switch k {
		case "ss", "t", "to":
			continue
		}
		dict[k] = fmt.Sprintf("%v", v)
	}
	// Per-output timestamp / mux-buffer policy. AVFormatContext's
	// `max_delay` and the muxer-private `preload` are integers in
	// AV_TIME_BASE units (microseconds); FFmpeg's CLI takes float
	// seconds and multiplies by AV_TIME_BASE before pushing to
	// libavformat (see fftools/ffmpeg_mux_init.c L3444-L3447).
	if out.MuxDelay > 0 {
		dict["max_delay"] = strconv.FormatInt(int64(out.MuxDelay*1_000_000), 10)
	}
	if out.MuxPreload > 0 {
		dict["preload"] = strconv.FormatInt(int64(out.MuxPreload*1_000_000), 10)
	}
	if out.AvoidNegativeTS != "" {
		dict["avoid_negative_ts"] = out.AvoidNegativeTS
	}
	if h := out.HLS; h != nil {
		if h.Time > 0 {
			dict["hls_time"] = strconv.FormatFloat(h.Time, 'f', -1, 64)
		}
		if h.InitTime > 0 {
			dict["hls_init_time"] = strconv.FormatFloat(h.InitTime, 'f', -1, 64)
		}
		if h.ListSize > 0 {
			dict["hls_list_size"] = strconv.Itoa(h.ListSize)
		}
		if h.PlaylistType != "" {
			dict["hls_playlist_type"] = h.PlaylistType
		}
		if h.SegmentType != "" {
			dict["hls_segment_type"] = h.SegmentType
		}
		if h.SegmentFilename != "" {
			dict["hls_segment_filename"] = h.SegmentFilename
		}
		if h.FMP4InitFilename != "" {
			dict["hls_fmp4_init_filename"] = h.FMP4InitFilename
		}
		if h.StartNumber > 0 {
			dict["start_number"] = strconv.Itoa(h.StartNumber)
		}
		if h.MasterPlName != "" {
			dict["master_pl_name"] = h.MasterPlName
		}
		if h.VarStreamMap != "" {
			dict["var_stream_map"] = h.VarStreamMap
		}
		if len(h.Flags) > 0 {
			dict["hls_flags"] = strings.Join(h.Flags, "+")
		}
	}
	if d := out.DASH; d != nil {
		if d.SegDuration > 0 {
			dict["seg_duration"] = strconv.FormatFloat(d.SegDuration, 'f', -1, 64)
		}
		if d.FragDuration > 0 {
			dict["frag_duration"] = strconv.FormatFloat(d.FragDuration, 'f', -1, 64)
		}
		if d.WindowSize > 0 {
			dict["window_size"] = strconv.Itoa(d.WindowSize)
		}
		if d.ExtraWindowSize > 0 {
			dict["extra_window_size"] = strconv.Itoa(d.ExtraWindowSize)
		}
		if d.InitSegName != "" {
			dict["init_seg_name"] = d.InitSegName
		}
		if d.MediaSegName != "" {
			dict["media_seg_name"] = d.MediaSegName
		}
		if d.SingleFile {
			dict["single_file"] = "1"
		}
		if d.UseTemplate != nil {
			dict["use_template"] = boolDictValue(*d.UseTemplate)
		}
		if d.UseTimeline != nil {
			dict["use_timeline"] = boolDictValue(*d.UseTimeline)
		}
		if d.Streaming {
			dict["streaming"] = "1"
		}
		if d.AdaptationSets != "" {
			dict["adaptation_sets"] = d.AdaptationSets
		}
		if d.HLSPlaylist {
			dict["hls_playlist"] = "1"
		}
		if d.LDash {
			dict["ldash"] = "1"
		}
		if len(d.Flags) > 0 {
			dict["dash_flags"] = strings.Join(d.Flags, "+")
		}
	}
	if len(dict) == 0 {
		return nil
	}
	return dict
}

func boolDictValue(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
