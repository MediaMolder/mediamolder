// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestExamplesAllSinksHaveEncoder loads every JobConfig in
// testdata/examples and asserts that, after configToGraphDef (which
// runs expandImplicitEncoders), the inbound side of every sink is
// driven by an encoder node. This is the exact precondition the
// runtime later checks in createSink ("inbound from X has no
// encoder") and matches the GUI's materializeImplicitEncoders pass.
//
// Catches regressions where a graph topology (filter chain, processor
// passthrough, etc.) bypasses the implicit-encoder splice.
func TestExamplesAllSinksHaveEncoder(t *testing.T) {
	const examplesDir = "../testdata/examples"
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("read examples dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no examples found in %s", examplesDir)
	}

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		name := ent.Name()
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(examplesDir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			// The shipped examples use empty url placeholders so users
			// fill them in via the file picker. Inject dummy values
			// before parsing so ParseConfig's URL-required validation
			// doesn't reject the file before we get to graph build.
			var cfg Config
			d := json.NewDecoder(bytes.NewReader(data))
			d.DisallowUnknownFields()
			if err := d.Decode(&cfg); err != nil {
				t.Fatalf("decode example: %v", err)
			}
			for i := range cfg.Inputs {
				if cfg.Inputs[i].URL == "" {
					cfg.Inputs[i].URL = "in.mp4"
				}
			}
			for i := range cfg.Outputs {
				if cfg.Outputs[i].URL == "" {
					cfg.Outputs[i].URL = "out.mp4"
				}
			}
			if err := validate(&cfg); err != nil {
				t.Fatalf("validate: %v", err)
			}

			def := configToGraphDef(&cfg)

			// Build the DAG so we get the same node-kind classification
			// the runtime uses. This catches "has no encoder" errors at
			// the same layer the runtime would, plus any other graph
			// validation failure.
			dag, err := graph.Build(def)
			if err != nil {
				t.Fatalf("graph.Build after implicit-encoder expansion: %v", err)
			}

			// Verify every sink's inbound edges originate from an encoder.
			for _, node := range dag.Order {
				if node.Kind != graph.KindSink {
					continue
				}
				for _, e := range node.Inbound {
					if e.From == nil {
						t.Errorf("sink %q has inbound edge with nil source", node.ID)
						continue
					}
					if e.From.Kind != graph.KindEncoder {
						t.Errorf("sink %q: inbound from %q is %v, expected KindEncoder",
							node.ID, e.From.ID, e.From.Kind)
					}
				}
			}
		})
	}
}
