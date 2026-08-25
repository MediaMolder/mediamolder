// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/cbs/report"
)

func TestBitstreamTrace_InitValidation(t *testing.T) {
	p := &BitstreamTrace{}
	if err := p.Init(map[string]any{"output_file": "/tmp/x.json"}); err == nil {
		t.Fatal("missing url must fail")
	}
	p = &BitstreamTrace{}
	if err := p.Init(map[string]any{"url": "in.mp4"}); err == nil {
		t.Fatal("missing output_file must fail")
	}
	p = &BitstreamTrace{}
	if err := p.Init(map[string]any{"url": "in.mp4", "output_file": "rel/path.json"}); err == nil {
		t.Fatal("relative output_file must fail")
	}
	p = &BitstreamTrace{}
	out := filepath.Join(t.TempDir(), "t.json")
	if err := p.Init(map[string]any{"url": "in.mp4", "output_file": out, "detail": "everything"}); err == nil {
		t.Fatal("bad detail must fail")
	}
}

func TestBitstreamTrace_RunJSON(t *testing.T) {
	input := "../av/testdata/tiny.mp4"
	if _, err := os.Stat(input); err != nil {
		t.Skip("tiny.mp4 not available:", err)
	}
	out := filepath.Join(t.TempDir(), "trace.json")
	p := &BitstreamTrace{}
	if err := p.Init(map[string]any{
		"url":           input,
		"output_file":   out,
		"output_format": "json",
		"detail":        "elements",
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	src := doc["source"].(map[string]any)
	if src["codec"] != "h264" || src["format"] != "avcc" {
		t.Fatalf("source: %v", src)
	}
	if src["nal_length_size"].(float64) != 4 {
		t.Fatalf("nal_length_size: %v", src["nal_length_size"])
	}
	xd := doc["extradata"].(map[string]any)
	if n := len(xd["units"].([]any)); n != 2 { // SPS + PPS
		t.Fatalf("extradata units: %d", n)
	}
	pkts := doc["packets"].([]any)
	if len(pkts) != 20 {
		t.Fatalf("packets: %d, want 20", len(pkts))
	}
	st := doc["stats"].(map[string]any)
	if st["errors"].(float64) != 0 {
		t.Fatalf("stats.errors: %v", st["errors"])
	}
	first := pkts[0].(map[string]any)
	if first["key_frame"] != true {
		t.Fatal("first packet should be a key frame")
	}
	// time / dts_time are populated from the stream time base.
	tb := src["time_base"].([]any)
	den := tb[1].(float64)
	for _, p := range pkts {
		pm := p.(map[string]any)
		sec, ok := pm["time"].(float64)
		if !ok {
			t.Fatalf("packet %v missing time", pm["index"])
		}
		if want := pm["pts"].(float64) / den; sec != want {
			t.Fatalf("packet %v time %v, want %v", pm["index"], sec, want)
		}
		if _, ok := pm["dts_time"].(float64); !ok {
			t.Fatalf("packet %v missing dts_time", pm["index"])
		}
	}
	// Coded pictures are typed records with a derived Picture Order
	// Count; the IDR's picture has POC 0 and x264 spaces frames by 2.
	var pocs []float64
	for _, p := range pkts {
		for _, u := range p.(map[string]any)["units"].([]any) {
			um := u.(map[string]any)
			if pic, ok := um["picture"].(map[string]any); ok {
				poc, ok := pic["poc"].(float64)
				if !ok {
					t.Fatalf("picture record missing poc: %v", pic)
				}
				pocs = append(pocs, poc)
			}
		}
	}
	// pocs is in decode order; as a set it must be exactly the even
	// values 0..38 (x264 spaces frame POCs by 2; 20 frames, one IDR).
	if len(pocs) != 20 || pocs[0] != 0 {
		t.Fatalf("derived pocs: %v", pocs)
	}
	seen := map[float64]bool{}
	for _, v := range pocs {
		seen[v] = true
	}
	for i := 0; i < 20; i++ {
		if !seen[float64(2*i)] {
			t.Fatalf("poc %d missing from %v", 2*i, pocs)
		}
	}
}

func TestBitstreamTrace_JSONLAndFilters(t *testing.T) {
	input := "../av/testdata/tiny.mp4"
	if _, err := os.Stat(input); err != nil {
		t.Skip("tiny.mp4 not available:", err)
	}
	var buf bytes.Buffer
	_, err := RunBitstreamTrace(context.Background(), TraceConfig{
		URL: input,
		Options: report.Options{
			Format:     "jsonl",
			Detail:     "summary",
			MaxPackets: 3,
		},
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	// header + extradata + 3 packets + violations + stats
	if len(lines) != 7 {
		t.Fatalf("jsonl lines: %d\n%s", len(lines), buf.String())
	}
}

// TestBitstreamTrace_TextGolden is the cgo golden tier: the full driver
// over a container, diffed against the trace_headers capture from the
// reference FFmpeg build. "Packet:" lines are compared structurally but
// not by timestamp: ffmpeg's CLI rescales packet timestamps before the
// BSF sees them, while the driver reports demuxer-native values.
func TestBitstreamTrace_TextGolden(t *testing.T) {
	cases := []struct{ input, golden string }{
		{"../av/testdata/tiny.mp4", "../cbs/testdata/golden/tiny.mp4.txt"},
		{"../cbs/testdata/tiny_hevc.mp4", "../cbs/testdata/golden/tiny_hevc.mp4.txt"},
		{"../cbs/testdata/tiny_av1.mp4", "../cbs/testdata/golden/tiny_av1.mp4.txt"},
		{"../cbs/testdata/tiny.ivf", "../cbs/testdata/golden/tiny.ivf.txt"},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.input), func(t *testing.T) {
			if _, err := os.Stat(c.input); err != nil {
				t.Skip("fixture not available:", err)
			}
			goldenRaw, err := os.ReadFile(c.golden)
			if err != nil {
				t.Skip("golden not available:", err)
			}
			var buf bytes.Buffer
			_, err = RunBitstreamTrace(context.Background(), TraceConfig{
				URL:     c.input,
				Options: report.Options{Format: "text", Detail: "elements"},
			}, &buf)
			if err != nil {
				t.Fatal(err)
			}
			got := splitTraceLines(buf.String())
			want := splitTraceLines(string(goldenRaw))
			if len(got) != len(want) {
				t.Fatalf("line count: got %d, want %d", len(got), len(want))
			}
			for i := range got {
				g, w := got[i], want[i]
				if strings.HasPrefix(w, "Packet: ") {
					// Same size prefix and flags, timestamps may differ.
					gp := strings.SplitN(g, ", pts", 2)[0]
					wp := strings.SplitN(w, ", pts", 2)[0]
					gp = strings.SplitN(gp, ", no pts", 2)[0]
					wp = strings.SplitN(wp, ", no pts", 2)[0]
					if gp != wp {
						t.Fatalf("line %d packet mismatch:\ngot  %q\nwant %q", i+1, g, w)
					}
					continue
				}
				if g != w {
					t.Fatalf("line %d:\ngot  %q\nwant %q", i+1, g, w)
				}
			}
		})
	}
}

func splitTraceLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
