// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/MediaMolder/MediaMolder/graph"
)

// applyLoudnormShuttle walks the graph for `loudnorm` filter nodes
// and threads the EBU R128 two-pass shuttle state from the run's
// outputs onto each node. Mirrors the orchestration FFmpeg users
// hand-roll today by piping pass 1's stderr JSON into pass 2's
// `measured_*` parameters (libavfilter/af_loudnorm.c::uninit, lines
// 830-935: when `print_format == JSON` and `stats_file` is set, the
// filter writes input_i / input_tp / input_lra / input_thresh /
// target_offset to a UTF-8 JSON file via avpriv_fopen_utf8).
//
// Pass 1 (analysis): the runtime injects `print_format=json` and
// `stats_file=<prefix>-<idx>.json` directly onto the loudnorm node's
// Params so libavfilter writes the measurements out at uninit.
//
// Pass 2 (apply): the runtime stamps `Internal.Filter.LoudnormPass=2`
// and `Internal.Filter.LoudnormStatsFile=<path>` markers onto the
// node; createFilter resolves the markers, reads + parses the JSON,
// and injects `measured_I` / `measured_TP` / `measured_LRA` /
// `measured_thresh` / `offset` into the node's params before the
// filter graph is instantiated. Errors (missing / unparseable stats
// file) flow up through createFilter, matching the symmetry FFmpeg
// users get from the manual two-run recipe.
//
// The pass and stats-file prefix are read from cfg.Outputs. At most
// one non-zero LoudnormPass may be present across all outputs in a
// single run (mirrors the hand-rolled recipe: one job invocation =
// one pass), and the LoudnormStatsFile prefix from the first
// non-zero output wins (empty defaults to `mm-loudnorm`).
func applyLoudnormShuttle(cfg *Config, def *graph.Def) error {
	pass := 0
	prefix := ""
	for _, out := range cfg.Outputs {
		if out.LoudnormPass == 0 {
			continue
		}
		if pass != 0 && pass != out.LoudnormPass {
			return fmt.Errorf("loudnorm shuttle: conflicting loudnorm_pass values across outputs (got %d and %d) — a single run can carry only one pass", pass, out.LoudnormPass)
		}
		pass = out.LoudnormPass
		if prefix == "" && out.LoudnormStatsFile != "" {
			prefix = out.LoudnormStatsFile
		}
	}
	if pass == 0 {
		return nil
	}
	if prefix == "" {
		prefix = "mm-loudnorm"
	}

	idx := 0
	for i := range def.Nodes {
		n := &def.Nodes[i]
		if n.Type != "filter" || n.Filter != "loudnorm" {
			continue
		}
		statsPath := fmt.Sprintf("%s-%d.json", prefix, idx)
		idx++
		if n.Params == nil {
			n.Params = map[string]any{}
		}
		// Merge the existing FilterInternal (e.g. Threads stamped by
		// the per-node thread cap pass) with the loudnorm shuttle
		// state.
		fi := n.Internal.Filter
		if fi == nil {
			fi = &graph.FilterInternal{}
			n.Internal.Filter = fi
		}
		switch pass {
		case 1:
			// libavfilter writes the JSON directly via stats_file;
			// no runtime read-back needed. Force print_format=json
			// even if the user wrote something else, so the
			// downstream pass-2 parser can rely on the format.
			n.Params["print_format"] = "json"
			n.Params["stats_file"] = statsPath
			fi.LoudnormPass = 1
			fi.LoudnormStatsFile = statsPath
		case 2:
			fi.LoudnormPass = 2
			fi.LoudnormStatsFile = statsPath
		}
		n.Internal.Generated = &graph.GeneratedNode{
			By:     "applyLoudnormShuttle",
			From:   n.ID,
			Reason: fmt.Sprintf("loudnorm two-pass shuttle, pass %d, stats %q", pass, statsPath),
		}
	}
	return nil
}

// loudnormStatsJSON mirrors the JSON object emitted by
// libavfilter/af_loudnorm.c::uninit (lines 877-885). Every value is
// printed as a `"%.2f"` decimal string in the source, including
// `target_offset`, so the Go side parses them with strconv.ParseFloat
// rather than letting encoding/json coerce them as numbers.
type loudnormStatsJSON struct {
	InputI            string `json:"input_i"`
	InputTP           string `json:"input_tp"`
	InputLRA          string `json:"input_lra"`
	InputThresh       string `json:"input_thresh"`
	OutputI           string `json:"output_i"`
	OutputTP          string `json:"output_tp"`
	OutputLRA         string `json:"output_lra"`
	OutputThresh      string `json:"output_thresh"`
	NormalizationType string `json:"normalization_type"`
	TargetOffset      string `json:"target_offset"`
}

// loadLoudnormMeasurements reads and parses a pass-1 stats JSON file,
// returning the measured_* / offset parameter map ready to be merged
// into a loudnorm filter node's Params for pass 2. The mapping is
// fixed by the AVOptions on the loudnorm filter (libavfilter/
// af_loudnorm.c::loudnorm_options): JSON `input_i` -> `measured_I`,
// `input_tp` -> `measured_TP`, `input_lra` -> `measured_LRA`,
// `input_thresh` -> `measured_thresh`, `target_offset` -> `offset`.
func loadLoudnormMeasurements(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loudnorm shuttle: read pass-1 stats %q: %w", path, err)
	}
	var s loudnormStatsJSON
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("loudnorm shuttle: parse pass-1 stats %q: %w", path, err)
	}
	parse := func(jsonKey, raw string) (float64, error) {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("loudnorm shuttle: stats %q: invalid %s %q: %w", path, jsonKey, raw, err)
		}
		return v, nil
	}
	out := map[string]any{}
	for _, m := range []struct {
		jsonKey string
		raw     string
		optKey  string
	}{
		{"input_i", s.InputI, "measured_I"},
		{"input_tp", s.InputTP, "measured_TP"},
		{"input_lra", s.InputLRA, "measured_LRA"},
		{"input_thresh", s.InputThresh, "measured_thresh"},
		{"target_offset", s.TargetOffset, "offset"},
	} {
		v, err := parse(m.jsonKey, m.raw)
		if err != nil {
			return nil, err
		}
		out[m.optKey] = v
	}
	return out, nil
}
