// Copyright (C) 2026 Thomas Vaughan
//
// SPDX-License-Identifier: GPL-3.0-or-later

package ffcli

// hls_dash.go — Translate FFmpeg `-hls_*` / `-dash_*` / segmenter
// AVOption CLI flags into the typed job.HLSOptions /
// job.DASHOptions structs. Mirrors the AVOption tables in
// libavformat/hlsenc.c and libavformat/dashenc.c.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/job"
)

// setHLSOption populates one field of job.HLSOptions from a
// raw `-hls_*` / `-master_pl_name` / `-var_stream_map` /
// `-start_number` CLI flag value.
func setHLSOption(h *job.HLSOptions, flag, raw string) error {
	switch flag {
	case "-hls_time":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("%s: invalid value %q (want seconds)", flag, raw)
		}
		h.Time = f
	case "-hls_init_time":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("%s: invalid value %q (want seconds)", flag, raw)
		}
		h.InitTime = f
	case "-hls_list_size":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return fmt.Errorf("%s: invalid value %q (want non-negative integer)", flag, raw)
		}
		h.ListSize = n
	case "-hls_playlist_type":
		h.PlaylistType = raw
	case "-hls_segment_type":
		h.SegmentType = raw
	case "-hls_segment_filename":
		h.SegmentFilename = raw
	case "-hls_fmp4_init_filename":
		h.FMP4InitFilename = raw
	case "-master_pl_name":
		h.MasterPlName = raw
	case "-var_stream_map":
		h.VarStreamMap = raw
	case "-start_number":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return fmt.Errorf("%s: invalid value %q (want non-negative integer)", flag, raw)
		}
		h.StartNumber = n
	case "-hls_flags":
		h.Flags = splitFlagList(raw)
	default:
		return fmt.Errorf("setHLSOption: unhandled flag %q", flag)
	}
	return nil
}

// setDASHOption populates one field of job.DASHOptions from a
// raw DASH-muxer CLI flag value.
func setDASHOption(d *job.DASHOptions, flag, raw string) error {
	switch flag {
	case "-seg_duration":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("%s: invalid value %q (want seconds)", flag, raw)
		}
		d.SegDuration = f
	case "-frag_duration":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("%s: invalid value %q (want seconds)", flag, raw)
		}
		d.FragDuration = f
	case "-window_size":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return fmt.Errorf("%s: invalid value %q (want non-negative integer)", flag, raw)
		}
		d.WindowSize = n
	case "-extra_window_size":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return fmt.Errorf("%s: invalid value %q (want non-negative integer)", flag, raw)
		}
		d.ExtraWindowSize = n
	case "-init_seg_name":
		d.InitSegName = raw
	case "-media_seg_name":
		d.MediaSegName = raw
	case "-single_file":
		b, err := parseBoolFlag(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", flag, err)
		}
		d.SingleFile = b
	case "-use_template":
		b, err := parseBoolFlag(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", flag, err)
		}
		d.UseTemplate = &b
	case "-use_timeline":
		b, err := parseBoolFlag(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", flag, err)
		}
		d.UseTimeline = &b
	case "-streaming":
		b, err := parseBoolFlag(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", flag, err)
		}
		d.Streaming = b
	case "-adaptation_sets":
		d.AdaptationSets = raw
	case "-hls_playlist":
		b, err := parseBoolFlag(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", flag, err)
		}
		d.HLSPlaylist = b
	case "-ldash":
		b, err := parseBoolFlag(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", flag, err)
		}
		d.LDash = b
	case "-dash_flags":
		d.Flags = splitFlagList(raw)
	default:
		return fmt.Errorf("setDASHOption: unhandled flag %q", flag)
	}
	return nil
}

// splitFlagList parses libavutil's `+`-joined flag list (e.g.
// `delete_segments+independent_segments`) into discrete tokens.
func splitFlagList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "+")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseBoolFlag accepts the values libavutil's AV_OPT_TYPE_BOOL
// CLI parser recognises (`0`/`1`, `true`/`false`).
func parseBoolFlag(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes":
		return true, nil
	case "0", "false", "off", "no":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q", raw)
}
