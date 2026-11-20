// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

func TestCUDAEncoderRequiresExplicitOptions(t *testing.T) {
	r := newGraphRunner(&Config{}, minimalPipeline())
	src := &graph.Node{ID: "src", Kind: graph.KindSource}
	r.sources[src.ID] = &sourceResources{
		streams: map[int]av.StreamInfo{0: {Type: av.MediaTypeVideo, Width: 64, Height: 48}},
	}

	for _, tc := range []struct {
		name     string
		codec    string
		swPixFmt string
		want     string
	}{
		{"software codec", "libx264", "nv12", "requires an NVENC video encoder"},
		{"missing sw_pix_fmt", "h264_nvenc", "", "requires sw_pix_fmt"},
		{"missing device", "h264_nvenc", "nv12", "requires a named CUDA device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &graph.Node{ID: "enc", Kind: graph.KindEncoder,
				Params:  map[string]any{"codec": tc.codec, "pix_fmt": "cuda", "sw_pix_fmt": tc.swPixFmt},
				Inbound: []*graph.Edge{{From: src, Type: graph.PortVideo}},
			}
			enc, err := r.createEncoder(nil, node)
			if enc != nil {
				_ = enc.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("createEncoder() error = %v, want %q", err, tc.want)
			}
		})
	}
}
