// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"encoding/json"
	"testing"
)

var validConfig = `{
  "schema_version": "1.0",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [
        {"input_index": 0, "type": "video", "track": 0}
      ]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "scale",
        "type": "filter",
        "filter": "scale",
        "params": {"width": 1280, "height": 720}
      }
    ],
    "edges": [
      {"from": "src:v:0", "to": "scale:default", "type": "video"},
      {"from": "scale:default", "to": "out:v", "type": "video"}
    ]
  },
  "outputs": [
    {
      "id": "out",
      "url": "output.mp4",
      "codec_video": "libx264"
    }
  ]
}`

func TestParseValidConfig(t *testing.T) {
	cfg, err := ParseConfig([]byte(validConfig))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.SchemaVersion != "1.0" {
		t.Errorf("schema_version = %q, want 1.0", cfg.SchemaVersion)
	}
	if len(cfg.Inputs) != 1 {
		t.Errorf("len(inputs) = %d, want 1", len(cfg.Inputs))
	}
	if cfg.Inputs[0].ID != "src" {
		t.Errorf("input id = %q, want src", cfg.Inputs[0].ID)
	}
	if len(cfg.Graph.Nodes) != 1 {
		t.Errorf("len(nodes) = %d, want 1", len(cfg.Graph.Nodes))
	}
	if len(cfg.Graph.Edges) != 2 {
		t.Errorf("len(edges) = %d, want 2", len(cfg.Graph.Edges))
	}
}

func TestParseConfigMissingSchemaVersion(t *testing.T) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(validConfig), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "schema_version")
	data, _ := json.Marshal(m)
	_, err := ParseConfig(data)
	if err == nil {
		t.Fatal("expected error for missing schema_version, got nil")
	}
}

func TestParseConfigWrongSchemaVersion(t *testing.T) {
	bad := `{"schema_version":"2.0","inputs":[{"id":"a","url":"a","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"b","url":"b"}]}`
	_, err := ParseConfig([]byte(bad))
	if err == nil {
		t.Fatal("expected error for schema_version 2.0")
	}
}

func TestParseConfigRejectsUnknownFields(t *testing.T) {
	bad := `{"schema_version":"1.0","unknown_field":true,"inputs":[{"id":"a","url":"a","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"b","url":"b"}]}`
	_, err := ParseConfig([]byte(bad))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestParseConfigNoDuplicateIDs(t *testing.T) {
	bad := `{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]},{"id":"a","url":"y","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`
	_, err := ParseConfig([]byte(bad))
	if err == nil {
		t.Fatal("expected error for duplicate input id")
	}
}

func TestParseConfigInvalidEdgeType(t *testing.T) {
	bad := `{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[{"from":"a:v:0","to":"out:v","type":"bogus"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`
	_, err := ParseConfig([]byte(bad))
	if err == nil {
		t.Fatal("expected error for invalid edge type")
	}
}

func TestFilterSpecBuilder(t *testing.T) {
	node := NodeDef{
		ID:     "scale",
		Type:   "filter",
		Filter: "scale",
		Params: map[string]any{"width": 1280, "height": 720},
	}
	spec := buildFilterSpec(node)
	if spec == "" || spec == "null" {
		t.Errorf("unexpected empty filter spec")
	}
	if len(spec) < 5 {
		t.Errorf("filter spec too short: %q", spec)
	}
}
