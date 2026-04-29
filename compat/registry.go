// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package compat provides the FFmpeg ↔ MediaMolder capability
// registry: a machine-readable inventory of every FFmpeg CLI flag
// tracked by docs/ffmpeg-coverage-roadmap.md §2, with each flag tagged
// `covered` / `partial` / `missing` / `out-of-scope` and pointed at
// the schema field that handles it.
//
// The registry is the source of truth that the GUI's "unsupported
// flag" import report and the compat/ffcli round-trip test selector
// both consume. Keep capabilities.yaml in sync with the roadmap.
package compat

import (
	_ "embed"
	"fmt"

	yaml "go.yaml.in/yaml/v2"
)

//go:embed capabilities.yaml
var capabilitiesYAML []byte

// Status is one of "covered", "partial", "missing", "out-of-scope".
type Status string

const (
	StatusCovered     Status = "covered"
	StatusPartial     Status = "partial"
	StatusMissing     Status = "missing"
	StatusOutOfScope  Status = "out-of-scope"
)

// Flag is one entry in the registry: an FFmpeg CLI flag (or
// capability) and its current MediaMolder coverage status.
type Flag struct {
	Flag   string `yaml:"flag"`
	Status Status `yaml:"status"`
	Schema string `yaml:"schema"`
	Notes  string `yaml:"notes,omitempty"`
}

// Section groups flags by FFmpeg subsystem (inputs, filtergraph,
// encoders, muxers, …), mirroring the roadmap §2 headings.
type Section struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Flags []Flag `yaml:"flags"`
}

// Registry is the parsed capabilities.yaml file.
type Registry struct {
	SchemaVersion int       `yaml:"schema_version"`
	Sections      []Section `yaml:"sections"`
}

// LoadRegistry parses the embedded capabilities.yaml and returns the
// registry. It returns a non-nil error if the YAML is malformed or
// any flag has an unknown status.
func LoadRegistry() (*Registry, error) {
	var r Registry
	if err := yaml.Unmarshal(capabilitiesYAML, &r); err != nil {
		return nil, fmt.Errorf("compat: parse capabilities.yaml: %w", err)
	}
	if r.SchemaVersion != 1 {
		return nil, fmt.Errorf("compat: unsupported registry schema_version %d (want 1)", r.SchemaVersion)
	}
	for si, sec := range r.Sections {
		if sec.ID == "" {
			return nil, fmt.Errorf("compat: section %d has empty id", si)
		}
		for fi, f := range sec.Flags {
			if f.Flag == "" {
				return nil, fmt.Errorf("compat: section %q flag %d has empty flag", sec.ID, fi)
			}
			switch f.Status {
			case StatusCovered, StatusPartial, StatusMissing, StatusOutOfScope:
			default:
				return nil, fmt.Errorf("compat: section %q flag %q: unknown status %q", sec.ID, f.Flag, f.Status)
			}
			if f.Schema == "" {
				return nil, fmt.Errorf("compat: section %q flag %q: schema pointer is required (use \"n/a\" if intentionally absent)", sec.ID, f.Flag)
			}
		}
	}
	return &r, nil
}

// Counts returns a status histogram across every flag in the registry.
func (r *Registry) Counts() map[Status]int {
	out := map[Status]int{}
	for _, sec := range r.Sections {
		for _, f := range sec.Flags {
			out[f.Status]++
		}
	}
	return out
}
