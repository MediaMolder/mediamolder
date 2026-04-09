package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Config is the top-level MediaMolder pipeline configuration (JSON schema v1.0).
// It maps 1:1 with the JSON command payload described in the project specification.
type Config struct {
	SchemaVersion string   `json:"schema_version"`
	Inputs        []Input  `json:"inputs"`
	Graph         GraphDef `json:"graph"`
	Outputs       []Output `json:"outputs"`
	GlobalOptions Options  `json:"global_options,omitempty"`
}

// Input describes a single input source.
type Input struct {
	ID      string         `json:"id"`
	URL     string         `json:"url"`
	Streams []StreamSelect `json:"streams"`
	Options map[string]any `json:"options,omitempty"`
}

// StreamSelect selects a specific stream from an input.
type StreamSelect struct {
	InputIndex int    `json:"input_index"`
	Type       string `json:"type"`  // "video", "audio", "subtitle", "data"
	Track      int    `json:"track"` // zero-based track number within the type
}

// GraphDef is the directed acyclic graph of processing nodes and edges.
type GraphDef struct {
	Nodes []NodeDef `json:"nodes"`
	Edges []EdgeDef `json:"edges"`
}

// NodeDef describes a single node in the processing graph.
type NodeDef struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"` // "filter", "encoder", "source", "sink"
	Filter      string         `json:"filter,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	ErrorPolicy *ErrorPolicy   `json:"error_policy,omitempty"`
}

// EdgeDef describes a directed edge between two nodes.
type EdgeDef struct {
	From string `json:"from"` // "nodeID:port" or "inputID:v:0"
	To   string `json:"to"`   // "nodeID:port" or "outputID:v"
	Type string `json:"type"` // "video", "audio", "subtitle", "data"
}

// Output describes a single output sink.
type Output struct {
	ID            string         `json:"id"`
	URL           string         `json:"url"`
	Format        string         `json:"format,omitempty"`
	CodecVideo    string         `json:"codec_video,omitempty"`
	CodecAudio    string         `json:"codec_audio,omitempty"`
	CodecSubtitle string         `json:"codec_subtitle,omitempty"`
	BSFVideo      string         `json:"bsf_video,omitempty"`
	BSFAudio      string         `json:"bsf_audio,omitempty"`
	Options       map[string]any `json:"options,omitempty"`
}

// Options holds global pipeline options.
type Options struct {
	Threads        int    `json:"threads,omitempty"`
	HardwareAccel  string `json:"hw_accel,omitempty"`
	HardwareDevice string `json:"hw_device,omitempty"`
	Realtime       bool   `json:"realtime,omitempty"`
}

// ErrorPolicy defines how a node handles errors.
type ErrorPolicy struct {
	Policy       string `json:"policy"` // "abort", "skip", "retry", "fallback"
	MaxRetries   int    `json:"max_retries,omitempty"`
	FallbackNode string `json:"fallback_node,omitempty"`
}

// ParseConfig parses and validates a JSON pipeline config from raw bytes.
// Unknown fields are rejected (strict mode).
func ParseConfig(data []byte) (*Config, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()

	var cfg Config
	if err := d.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, validate(&cfg)
}

// ParseConfigFile reads and parses a JSON pipeline config from a file.
func ParseConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	return ParseConfig(data)
}

// validate performs semantic validation beyond what JSON unmarshaling checks.
func validate(cfg *Config) error {
	if cfg.SchemaVersion == "" {
		return fmt.Errorf("config missing required field schema_version")
	}
	if cfg.SchemaVersion != "1.0" {
		return fmt.Errorf("unsupported schema_version %q; expected \"1.0\"", cfg.SchemaVersion)
	}
	if len(cfg.Inputs) == 0 {
		return fmt.Errorf("config must have at least one input")
	}
	if len(cfg.Outputs) == 0 {
		return fmt.Errorf("config must have at least one output")
	}
	// All input IDs must be unique.
	seen := map[string]bool{}
	for i, inp := range cfg.Inputs {
		if inp.ID == "" {
			return fmt.Errorf("input[%d] missing id", i)
		}
		if seen[inp.ID] {
			return fmt.Errorf("duplicate input id %q", inp.ID)
		}
		seen[inp.ID] = true
		if inp.URL == "" {
			return fmt.Errorf("input %q missing url", inp.ID)
		}
		for j, s := range inp.Streams {
			if s.Type == "" {
				return fmt.Errorf("input %q streams[%d] missing type", inp.ID, j)
			}
		}
	}
	// All output IDs must be unique.
	seen = map[string]bool{}
	for i, out := range cfg.Outputs {
		if out.ID == "" {
			return fmt.Errorf("output[%d] missing id", i)
		}
		if seen[out.ID] {
			return fmt.Errorf("duplicate output id %q", out.ID)
		}
		seen[out.ID] = true
		if out.URL == "" {
			return fmt.Errorf("output %q missing url", out.ID)
		}
	}
	// Edge types must be valid.
	validTypes := map[string]bool{
		"video": true, "audio": true, "subtitle": true, "data": true,
	}
	for i, e := range cfg.Graph.Edges {
		if !validTypes[e.Type] {
			return fmt.Errorf("edge[%d] has invalid type %q", i, e.Type)
		}
	}
	return nil
}
