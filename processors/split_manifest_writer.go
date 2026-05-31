// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
)

// splitManifest is the JSON document written by SplitManifestWriter and read
// by the orchestrator when materialising fanout_dynamic child tasks.
// Keep JSON tags in sync with orchestrator.splitManifest.
type splitManifest struct {
	Splitter string         `json:"splitter"`
	InputURI string         `json:"input_uri,omitempty"`
	Segments []splitSegment `json:"segments"`
}

// splitSegment describes one temporal slice of the source media.
// Keep JSON tags in sync with orchestrator.splitSegment.
type splitSegment struct {
	Index    int     `json:"index"`
	InPoint  float64 `json:"inpoint"`
	OutPoint float64 `json:"outpoint"`
}

// SplitManifestWriter is a processor that accumulates frame timing data during
// a pipeline run and writes a job.SplitManifest JSON file on Close.
// It is designed to run as the sole processor in a producer stage of a
// fanout_dynamic distribution; the orchestrator reads the output file when
// materialising the child encode tasks.
//
// Params:
//
//	"splitter":   "scene_list" | "byte_range" (required)
//	"output_file": absolute path to write the manifest JSON (required)
//	"input_uri":   source media URI to record in the manifest (optional)
//	"fps":         frames per second of the source, used to convert frame
//	               index to wall-clock seconds (required for byte_range;
//	               optional for scene_list where cut times are derived from
//	               the inner processor's FrameIndex output)
//	"count":       number of segments to emit (required for byte_range)
//
// For splitter="scene_list" all parameters of the inner "scene_change"
// processor are also accepted (e.g. "threshold", "pts_threshold").
type SplitManifestWriter struct {
	splitter   string
	outputFile string
	inputURI   string
	fps        float64

	// scene_list state
	inner Processor // wrapped scene_change instance
	cuts  []float64 // scene-cut times in seconds

	// byte_range state
	count     int
	lastFrame uint64
}

func (p *SplitManifestWriter) Init(params map[string]any) error {
	sp, _ := params["splitter"].(string)
	if sp == "" {
		return fmt.Errorf("split_manifest_writer: \"splitter\" param is required (scene_list, byte_range)")
	}
	if sp != "scene_list" && sp != "byte_range" {
		return fmt.Errorf("split_manifest_writer: unknown splitter %q (want scene_list or byte_range)", sp)
	}
	p.splitter = sp

	outFile, _ := params["output_file"].(string)
	if outFile == "" {
		return fmt.Errorf("split_manifest_writer: \"output_file\" param is required")
	}
	// Sanitize path (mirrors fileWriteHook CWE-022 prevention).
	outFile = filepath.Clean(outFile)
	if !filepath.IsAbs(outFile) {
		return fmt.Errorf("split_manifest_writer: output_file must be an absolute path, got %q", outFile)
	}
	fsRoot := string(filepath.Separator)
	rel, err := filepath.Rel(fsRoot, outFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("split_manifest_writer: output_file %q is outside accessible root", outFile)
	}
	p.outputFile = filepath.Join(fsRoot, rel)

	p.inputURI, _ = params["input_uri"].(string)

	if v, ok := params["fps"]; ok {
		switch n := v.(type) {
		case float64:
			p.fps = n
		case int:
			p.fps = float64(n)
		default:
			return fmt.Errorf("split_manifest_writer: fps must be a number, got %T", v)
		}
	}

	if sp == "byte_range" {
		if p.fps <= 0 {
			return fmt.Errorf("split_manifest_writer: fps is required for byte_range splitter")
		}
		raw, ok := params["count"]
		if !ok {
			return fmt.Errorf("split_manifest_writer: \"count\" param is required for byte_range splitter")
		}
		switch n := raw.(type) {
		case float64:
			p.count = int(n)
		case int:
			p.count = n
		default:
			return fmt.Errorf("split_manifest_writer: count must be an integer, got %T", raw)
		}
		if p.count < 1 {
			return fmt.Errorf("split_manifest_writer: count must be ≥ 1, got %d", p.count)
		}
		return nil
	}

	// scene_list: instantiate and init the inner scene_change processor.
	// Strip our own keys before forwarding.
	innerParams := make(map[string]any, len(params))
	for k, v := range params {
		switch k {
		case "splitter", "output_file", "input_uri", "fps":
			// consumed by us; do not forward
		default:
			innerParams[k] = v
		}
	}
	sc, err2 := Get("scene_change")
	if err2 != nil {
		return fmt.Errorf("split_manifest_writer: %w", err2)
	}
	if err2 = sc.Init(innerParams); err2 != nil {
		return fmt.Errorf("split_manifest_writer: scene_change init: %w", err2)
	}
	p.inner = sc
	return nil
}

func (p *SplitManifestWriter) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	p.lastFrame = ctx.FrameIndex

	if p.splitter == "scene_list" && p.inner != nil {
		out, md, err := p.inner.Process(frame, ctx)
		if err != nil {
			return out, md, err
		}
		if md != nil {
			if sc, ok := md.Custom["scene_change"].(bool); ok && sc {
				// Record the cut time in seconds.
				t := p.ptsToSeconds(ctx)
				p.cuts = append(p.cuts, t)
			}
		}
		return out, nil, nil // do not forward scene_change events to the bus
	}

	// byte_range: pass through; we only need the last FrameIndex.
	return frame, nil, nil
}

func (p *SplitManifestWriter) Close() error {
	var innerErr error
	if p.inner != nil {
		innerErr = p.inner.Close()
	}

	manifest := p.buildManifest()

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("split_manifest_writer: marshal manifest: %w", err)
	}
	if err := os.WriteFile(p.outputFile, data, 0o600); err != nil {
		return fmt.Errorf("split_manifest_writer: write manifest %q: %w", p.outputFile, err)
	}
	return innerErr
}

func (p *SplitManifestWriter) buildManifest() splitManifest {
	m := splitManifest{
		Splitter: p.splitter,
		InputURI: p.inputURI,
	}

	switch p.splitter {
	case "scene_list":
		totalSecs := p.ptsSecsFromFrame(p.lastFrame)
		// Build one segment per inter-cut interval.
		boundaries := append([]float64{0}, p.cuts...)
		boundaries = append(boundaries, totalSecs)
		for i := 0; i < len(boundaries)-1; i++ {
			outpoint := boundaries[i+1]
			if i == len(boundaries)-2 {
				outpoint = 0 // last segment: let ffmpeg read to EOF
			}
			m.Segments = append(m.Segments, splitSegment{
				Index:    i,
				InPoint:  boundaries[i],
				OutPoint: outpoint,
			})
		}
		if len(m.Segments) == 0 {
			// No cuts detected → single segment covering the whole file.
			m.Segments = []splitSegment{{Index: 0, InPoint: 0, OutPoint: 0}}
		}

	case "byte_range":
		totalSecs := p.ptsSecsFromFrame(p.lastFrame)
		if totalSecs <= 0 || p.count <= 1 {
			m.Segments = []splitSegment{{Index: 0, InPoint: 0, OutPoint: 0}}
		} else {
			dur := totalSecs / float64(p.count)
			for i := 0; i < p.count; i++ {
				in := float64(i) * dur
				out := float64(i+1) * dur
				if i == p.count-1 {
					out = 0 // last segment to EOF
				}
				m.Segments = append(m.Segments, splitSegment{
					Index:    i,
					InPoint:  in,
					OutPoint: out,
				})
			}
		}
	}

	return m
}

// ptsToSeconds converts the current frame's index to a wall-clock second
// position. When fps is unknown, the raw FrameIndex is returned unchanged
// (useful for unit-testing with fps=1).
func (p *SplitManifestWriter) ptsToSeconds(ctx ProcessorContext) float64 {
	return p.ptsSecsFromFrame(ctx.FrameIndex)
}

func (p *SplitManifestWriter) ptsSecsFromFrame(frameIndex uint64) float64 {
	if p.fps > 0 {
		return float64(frameIndex) / p.fps
	}
	return float64(frameIndex)
}

func init() {
	Register("split_manifest_writer", func() Processor { return &SplitManifestWriter{} })
}
