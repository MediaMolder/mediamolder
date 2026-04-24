// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MediaMolder/MediaMolder/av"
)

// probeRequest is the JSON body accepted by POST /api/probe.
type probeRequest struct {
	URL     string            `json:"url"`
	Options map[string]string `json:"options,omitempty"`
}

// probedStream is the per-stream payload returned by POST /api/probe.
//
// Field names match the canonical attribute keys used by the GUI's edge
// attribute inference (frontend/src/lib/streamAttrs.ts) so the frontend can
// merge probed values straight into the inference map.
type probedStream struct {
	Index         int     `json:"index"`
	Type          string  `json:"type"` // "video" | "audio" | "subtitle" | "data"
	Codec         string  `json:"codec,omitempty"`
	Width         int     `json:"width,omitempty"`
	Height        int     `json:"height,omitempty"`
	PixFmt        string  `json:"pix_fmt,omitempty"`
	FrameRate     string  `json:"frame_rate,omitempty"` // formatted as "num/den" or decimal
	SampleRate    int     `json:"sample_rate,omitempty"`
	SampleFmt     string  `json:"sample_fmt,omitempty"`
	Channels      int     `json:"channels,omitempty"`
	ChannelLayout string  `json:"channel_layout,omitempty"`
	DurationSec   float64 `json:"duration_sec,omitempty"`
	TimeBaseNum   int     `json:"time_base_num,omitempty"`
	TimeBaseDen   int     `json:"time_base_den,omitempty"`
}

// probeResponse is the body returned by POST /api/probe.
type probeResponse struct {
	URL     string         `json:"url"`
	Streams []probedStream `json:"streams"`
}

// handleProbe opens the requested URL with libavformat, runs
// avformat_find_stream_info, and reports per-stream technical metadata.
//
// The endpoint is intentionally narrow: it does not decode any packets, so
// probing is cheap and safe to call from the Inspector "Get properties"
// button on every Input node click. Any libav error is surfaced verbatim.
func handleProbe(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req probeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if req.URL == "" {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("url is required"))
		return
	}

	ctx, err := av.OpenInput(req.URL, req.Options)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, fmt.Errorf("open %q: %w", req.URL, err))
		return
	}
	defer ctx.Close()

	streams, err := ctx.AllStreams()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("read streams: %w", err))
		return
	}

	out := probeResponse{URL: req.URL, Streams: make([]probedStream, 0, len(streams))}
	for _, s := range streams {
		ps := probedStream{
			Index:       s.Index,
			Type:        s.Type.String(),
			Codec:       av.CodecName(s.CodecID),
			TimeBaseNum: s.TimeBase[0],
			TimeBaseDen: s.TimeBase[1],
		}
		if s.TimeBase[1] > 0 && s.Duration > 0 {
			ps.DurationSec = float64(s.Duration) * float64(s.TimeBase[0]) / float64(s.TimeBase[1])
		}
		switch s.Type.String() {
		case "video":
			ps.Width = s.Width
			ps.Height = s.Height
			ps.PixFmt = av.PixFmtName(s.PixFmt)
			if s.FrameRate[0] > 0 && s.FrameRate[1] > 0 {
				ps.FrameRate = formatFrameRate(s.FrameRate[0], s.FrameRate[1])
			}
		case "audio":
			ps.SampleRate = s.SampleRate
			ps.SampleFmt = av.SampleFmtName(s.SampleFmt)
			ps.Channels = s.Channels
			ps.ChannelLayout = av.DefaultChannelLayoutName(s.Channels)
		}
		out.Streams = append(out.Streams, ps)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// formatFrameRate renders num/den as a friendly decimal (or fraction for
// non-integer rates that don't round cleanly to two decimals).
func formatFrameRate(num, den int) string {
	if den == 0 {
		return ""
	}
	if num%den == 0 {
		return fmt.Sprintf("%d", num/den)
	}
	val := float64(num) / float64(den)
	// Render common drop-frame rates as fractions for clarity.
	if num == 30000 && den == 1001 {
		return "29.97"
	}
	if num == 24000 && den == 1001 {
		return "23.976"
	}
	if num == 60000 && den == 1001 {
		return "59.94"
	}
	return fmt.Sprintf("%.3f", val)
}
