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

func TestParseConfigSchemaV11(t *testing.T) {
	cfg := `{
  "schema_version": "1.1",
  "inputs": [{"id":"src","url":"in.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],
  "graph": {
    "nodes": [
      {"id":"proc","type":"go_processor","processor":"null"}
    ],
    "edges": [
      {"from":"src:v:0","to":"proc:default","type":"video"},
      {"from":"proc:default","to":"out:v","type":"video"}
    ]
  },
  "outputs": [{"id":"out","url":"out.mp4","codec_video":"libx264"}]
}`
	parsed, err := ParseConfig([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if parsed.SchemaVersion != "1.1" {
		t.Errorf("schema_version = %q, want 1.1", parsed.SchemaVersion)
	}
	if parsed.Graph.Nodes[0].Processor != "null" {
		t.Errorf("processor = %q, want null", parsed.Graph.Nodes[0].Processor)
	}
}

func TestParseConfigGoProcessorMissingProcessor(t *testing.T) {
	cfg := `{
  "schema_version": "1.1",
  "inputs": [{"id":"src","url":"in.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],
  "graph": {
    "nodes": [
      {"id":"proc","type":"go_processor"}
    ],
    "edges": []
  },
  "outputs": [{"id":"out","url":"out.mp4"}]
}`
	_, err := ParseConfig([]byte(cfg))
	if err == nil {
		t.Fatal("expected error for go_processor without processor field")
	}
}

// ── Wave 10 #56: HardwareDevices + NodeDef.Device ────────────────────────

func TestParseConfigHardwareDevicesRoundTrip(t *testing.T) {
	raw := `{
  "schema_version": "1.0",
  "inputs": [{"id":"src","url":"in.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],
  "graph": {
    "nodes": [
      {"id":"enc","type":"encoder","device":"gpu0","params":{"codec":"h264_nvenc"}}
    ],
    "edges": [
      {"from":"src:v:0","to":"enc:default","type":"video"},
      {"from":"enc:default","to":"out:v","type":"video"}
    ]
  },
  "outputs": [{"id":"out","url":"out.mp4"}],
  "hardware_devices": [
    {"name":"gpu0","type":"cuda","device":"0"}
  ]
}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.HardwareDevices) != 1 {
		t.Fatalf("len(hardware_devices) = %d, want 1", len(cfg.HardwareDevices))
	}
	hd := cfg.HardwareDevices[0]
	if hd.Name != "gpu0" || hd.Type != "cuda" || hd.Device != "0" {
		t.Errorf("hardware_device = %+v, want {Name:gpu0, Type:cuda, Device:0}", hd)
	}
	if cfg.Graph.Nodes[0].Device != "gpu0" {
		t.Errorf("node device = %q, want gpu0", cfg.Graph.Nodes[0].Device)
	}
}

func TestParseConfigHardwareDeviceDuplicateName(t *testing.T) {
	raw := `{
  "schema_version": "1.0",
  "inputs": [{"id":"src","url":"in.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],
  "graph": {"nodes":[],"edges":[]},
  "outputs": [{"id":"out","url":"out.mp4"}],
  "hardware_devices": [
    {"name":"gpu0","type":"cuda"},
    {"name":"gpu0","type":"vaapi"}
  ]
}`
	_, err := ParseConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected error for duplicate hardware_devices name")
	}
}

func TestParseConfigHardwareDeviceEmptyType(t *testing.T) {
	raw := `{
  "schema_version": "1.0",
  "inputs": [{"id":"src","url":"in.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],
  "graph": {"nodes":[],"edges":[]},
  "outputs": [{"id":"out","url":"out.mp4"}],
  "hardware_devices": [{"name":"gpu0","type":""}]
}`
	_, err := ParseConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected error for empty hardware_devices type")
	}
}

func TestParseConfigNodeDeviceUnknownRef(t *testing.T) {
	raw := `{
  "schema_version": "1.0",
  "inputs": [{"id":"src","url":"in.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],
  "graph": {
    "nodes": [{"id":"enc","type":"encoder","device":"missing","params":{"codec":"libx264"}}],
    "edges": [
      {"from":"src:v:0","to":"enc:default","type":"video"},
      {"from":"enc:default","to":"out:v","type":"video"}
    ]
  },
  "outputs": [{"id":"out","url":"out.mp4"}]
}`
	_, err := ParseConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected error for node.device referencing unknown hardware_device")
	}
}
