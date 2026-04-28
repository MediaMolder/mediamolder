// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package compat

import "testing"

// TestLoadRegistry asserts that capabilities.yaml is well-formed,
// every flag has a valid status, and the file has at least the
// roadmap §2.1–§2.7 sections seeded.
func TestLoadRegistry(t *testing.T) {
	r, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if r.SchemaVersion != 1 {
		t.Errorf("schema_version: got %d, want 1", r.SchemaVersion)
	}
	required := []string{
		"inputs",
		"stream_selection",
		"filtergraph",
		"encoders",
		"muxers",
		"subtitles",
		"devices_advanced",
	}
	have := map[string]bool{}
	for _, sec := range r.Sections {
		have[sec.ID] = true
	}
	for _, id := range required {
		if !have[id] {
			t.Errorf("missing required section %q", id)
		}
	}
	counts := r.Counts()
	total := 0
	for _, n := range counts {
		total += n
	}
	if total < 50 {
		t.Errorf("registry has only %d flag entries; expected >= 50", total)
	}
	if counts[StatusCovered] == 0 {
		t.Errorf("registry has no covered flags – sanity check failed")
	}
	t.Logf("registry: %d flags – covered=%d partial=%d missing=%d out-of-scope=%d",
		total,
		counts[StatusCovered],
		counts[StatusPartial],
		counts[StatusMissing],
		counts[StatusOutOfScope],
	)
}

// TestRegistrySchemaPointersForCovered asserts that every entry
// marked "covered" has a schema pointer that is not "n/a" – the
// whole point of the registry is to be navigable from a flag to the
// code/schema that backs it. ("partial" is allowed to point at
// "n/a" because the underlying capability often reaches FFmpeg via
// a generic libavfilter / AVDict passthrough that has no first-class
// schema home yet.)
func TestRegistrySchemaPointersForCovered(t *testing.T) {
	r, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	for _, sec := range r.Sections {
		for _, f := range sec.Flags {
			if f.Status != StatusCovered {
				continue
			}
			if f.Schema == "" || f.Schema == "n/a" {
				t.Errorf("section %q flag %q: status=covered but schema=%q (covered flags must point at a schema field or code location)",
					sec.ID, f.Flag, f.Schema)
			}
		}
	}
}
