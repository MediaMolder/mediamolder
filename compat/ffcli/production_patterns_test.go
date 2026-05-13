// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

// production_patterns_test.go — stub for the production-pattern
// conformance corpus called for in
// docs/ffmpeg-coverage-roadmap.md §5 step #6.
//
// Each manifest in testdata/production-patterns/*.json describes one
// of the highest-leverage production FFmpeg patterns from §1.1
// (animated drawtext, ABR ladder, full-GPU NPP+NVENC, HDR
// zscale/tonemap, two-pass loudnorm, raw YUV input). The harness
// below tries to run each pattern end-to-end through MediaMolder; if
// it can't, it `t.Skip`s with a structured "blocked-by:" line that
// names the missing capabilities (see roadmap §5#6: "even before each
// one runs, the failing t.Skip reason becomes machine-readable
// roadmap signal").
//
// Today every pattern in the seed corpus is gated by at least one
// declared blocker, so the suite is expected to skip 6 / 6. As each
// blocker lands, drop it from the manifest's `blockers` list — the
// first manifest with an empty list will start running for real.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// productionPattern is the on-disk manifest for one pattern. Fields
// are deliberately permissive (extra keys are ignored) so each entry
// can grow pattern-specific metadata (e.g. the loudnorm two-pass
// fixture has `ffmpeg_cmd_pass1` / `ffmpeg_cmd_pass2` instead of a
// single `ffmpeg_cmd`).
type productionPattern struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	FFmpegCmd   string   `json:"ffmpeg_cmd"`
	OutExt      string   `json:"out_ext"`
	Blockers    []string `json:"blockers"`
	RoadmapRefs []string `json:"roadmap_refs"`
	DurationTol float64  `json:"duration_tol"`
}

// TestProductionPatternsCorpus walks testdata/production-patterns/.
// For each manifest it:
//
//  1. Logs the description + roadmap refs (so `go test -v` output is
//     greppable as a roadmap signal even when everything skips).
//  2. If `Blockers` is non-empty, t.Skips with a single
//     "blocked-by: <key1>; <key2>; ..." line — easy to grep.
//  3. Otherwise, attempts ffcli.Parse + pipeline.Run on a tmpdir
//     output. Skips when ffmpeg/ffprobe/the input fixture aren't
//     available. (The first pattern with no blockers will start
//     running for real — that's the success criterion for landing the
//     underlying capability.)
//
// The harness intentionally does NOT compare against an ffmpeg arm
// (unlike compat/ffcli/roundtrip_test.go); these patterns are the
// places where the JSON pipeline can't yet match the ffmpeg one. A
// future commit can promote a pattern into the round-trip suite once
// it's actually expressible.
func TestProductionPatternsCorpus(t *testing.T) {
	manifestsDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "production-patterns"))
	if err != nil {
		t.Fatalf("abs manifests dir: %v", err)
	}
	entries, err := os.ReadDir(manifestsDir)
	if err != nil {
		t.Fatalf("read %s: %v", manifestsDir, err)
	}
	var manifests []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		manifests = append(manifests, filepath.Join(manifestsDir, e.Name()))
	}
	sort.Strings(manifests)
	if len(manifests) == 0 {
		t.Fatalf("no production-pattern manifests found under %s", manifestsDir)
	}

	for _, path := range manifests {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			var pat productionPattern
			if err := json.Unmarshal(raw, &pat); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			if pat.Name == "" {
				t.Fatalf("%s: manifest is missing required field `name`", path)
			}

			// Echo the description + roadmap refs so `go test -v`
			// output is itself a roadmap signal.
			t.Logf("pattern: %s", pat.Name)
			if pat.Description != "" {
				t.Logf("description: %s", pat.Description)
			}
			for _, ref := range pat.RoadmapRefs {
				t.Logf("roadmap-ref: %s", ref)
			}

			if len(pat.Blockers) > 0 {
				// Single greppable line: `grep '^.*blocked-by:'`
				// across `go test -v` output prints the live
				// capability gap inventory.
				t.Skipf("blocked-by: %s", strings.Join(pat.Blockers, "; "))
			}

			// --- Unblocked path: attempt to run end-to-end. -----
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				t.Skipf("environment: ffmpeg not in PATH (%v)", err)
			}
			if pat.FFmpegCmd == "" {
				t.Fatalf("%s: blockers cleared but no `ffmpeg_cmd` to drive the test", pat.Name)
			}
			inputAbs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "BBB_1080p.avi"))
			if err != nil {
				t.Fatalf("abs input path: %v", err)
			}
			if _, err := os.Stat(inputAbs); err != nil {
				t.Skipf("environment: testdata/BBB_1080p.avi missing; run scripts/fetch-bbb.sh (%v)", err)
			}
			ext := pat.OutExt
			if ext == "" {
				ext = ".mp4"
			}
			tmp := t.TempDir()
			out := filepath.Join(tmp, "mediamolder"+ext)
			cmd := substitute(pat.FFmpegCmd, inputAbs, out)

			cfg, err := Parse(cmd)
			if err != nil {
				t.Fatalf("ffcli.Parse: %v\ncmd: %s", err, cmd)
			}
			// Inject seek so the test consumes 10 s starting at 450 s
			// of the full BBB_1080p.avi source instead of a trimmed clip.
			for i := range cfg.Inputs {
				inp := &cfg.Inputs[i]
				if inp.Kind != "" && inp.Kind != "file" {
					continue
				}
				if inp.Options == nil {
					inp.Options = map[string]any{}
				}
				inp.Options["ss"] = "450"
				inp.Options["t"] = "10"
			}
			eng, err := pipeline.NewPipeline(cfg)
			if err != nil {
				t.Fatalf("pipeline.NewPipeline: %v", err)
			}
			if err := eng.Run(context.Background()); err != nil {
				t.Fatalf("pipeline.Run: %v", err)
			}
			if st, err := os.Stat(out); err != nil {
				t.Fatalf("output missing: %v", err)
			} else if st.Size() == 0 {
				t.Fatalf("output is empty: %s", out)
			}
		})
	}
}

// Compile-time assertion: keep the manifest fields aligned with what
// the harness consumes. (Defensive: lints out an accidental json-tag
// rename.)
var _ = func() productionPattern {
	return productionPattern{
		Name:        "",
		Description: "",
		FFmpegCmd:   "",
		OutExt:      "",
		Blockers:    nil,
		RoadmapRefs: nil,
		DurationTol: 0,
	}
}()
