// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

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
	Description   string   `json:"description,omitempty"`
	Inputs        []Input  `json:"inputs"`
	Graph         GraphDef `json:"graph"`
	Outputs       []Output `json:"outputs"`
	GlobalOptions Options  `json:"global_options,omitempty"`
}

// Input describes a single input source.
type Input struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	// Kind selects how the input is opened. Defaults to "file" (or empty),
	// meaning libavformat probes the URL. "lavfi" opens the input through
	// libavformat's lavfi virtual demuxer (FFmpeg's `-f lavfi -i …`); the
	// URL field then holds the filtergraph spec, e.g.
	// "anullsrc=r=48000:cl=stereo" or "color=black:s=1920x1080:r=30".
	// Required for synthetic sources (silent audio, test cards, color
	// padding tracks) where there is no underlying file to demux.
	Kind    string         `json:"kind,omitempty"`
	Streams []StreamSelect `json:"streams"`
	Options map[string]any `json:"options,omitempty"`
	// MapMetadata, when true, copies the container-level metadata of
	// this input onto every Output that does not set its own
	// `Output.Metadata`. Mirrors FFmpeg's `-map_metadata IDX` when IDX
	// is the index of this input. Multiple inputs with MapMetadata=true
	// merge in declaration order; the last writer wins per key.
	MapMetadata bool `json:"map_metadata,omitempty"`
	// MapChapters, when true, copies the chapter table of this input
	// onto every Output that does not set its own `Output.Chapters`.
	// Mirrors FFmpeg's `-map_chapters IDX`. When more than one input has
	// MapChapters=true the first such input wins (matches FFmpeg's
	// single-source semantics for chapters).
	MapChapters bool `json:"map_chapters,omitempty"`
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
	// UI holds optional editor-only metadata (node positions, etc.). Ignored by
	// the runtime; preserved on round-trip so the visual editor (mediamolder gui)
	// can persist layouts without breaking existing JSONs. Targets schema v1.2.
	UI *GraphUI `json:"ui,omitempty"`
}

// GraphUI carries optional layout metadata for the visual editor. All fields
// are optional and ignored by the runtime.
type GraphUI struct {
	Positions map[string]UIPosition `json:"positions,omitempty"`
}

// UIPosition is a 2D coordinate on the editor canvas.
type UIPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// NodeDef describes a single node in the processing graph.
type NodeDef struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"` // "filter", "encoder", "source", "sink", "go_processor"
	Filter      string         `json:"filter,omitempty"`
	Processor   string         `json:"processor,omitempty"`
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
	ID            string `json:"id"`
	URL           string `json:"url"`
	Format        string `json:"format,omitempty"`
	CodecVideo    string `json:"codec_video,omitempty"`
	CodecAudio    string `json:"codec_audio,omitempty"`
	CodecSubtitle string `json:"codec_subtitle,omitempty"`
	BSFVideo      string `json:"bsf_video,omitempty"`
	BSFAudio      string `json:"bsf_audio,omitempty"`
	// CodecTagVideo / CodecTagAudio / CodecTagSubtitle override the
	// FourCC codec_tag set by the muxer on the corresponding output
	// stream. Equivalent to ffmpeg's -tag:v / -tag:a / -tag:s. Most
	// commonly used to force HEVC in MP4 to "hvc1" for QuickTime/Safari
	// compatibility (vs. the default "hev1"). Must be exactly 4 ASCII
	// characters when set. Applied to both encoder and stream-copy
	// streams of the matching kind.
	CodecTagVideo    string `json:"codec_tag_video,omitempty"`
	CodecTagAudio    string `json:"codec_tag_audio,omitempty"`
	CodecTagSubtitle string `json:"codec_tag_subtitle,omitempty"`
	// EncoderParamsVideo / EncoderParamsAudio / EncoderParamsSubtitle
	// hold codec-specific options (preset, crf, tune, profile, level,
	// g, b, maxrate, bufsize, ...) attached to the implicit encoder
	// inserted by `expandImplicitEncoders`. They are populated by
	// `compat/ffcli` when parsing FFmpeg command lines so that flags
	// like `-crf 22 -preset slow` survive the round-trip into a
	// pipeline.Config and reach the encoder as AVDictionary entries.
	// Ignored when an explicit encoder node is wired upstream of the
	// matching output stream.
	EncoderParamsVideo    map[string]any `json:"encoder_params_video,omitempty"`
	EncoderParamsAudio    map[string]any `json:"encoder_params_audio,omitempty"`
	EncoderParamsSubtitle map[string]any `json:"encoder_params_subtitle,omitempty"`
	// MaxFramesVideo / MaxFramesAudio cap the number of *muxed* packets
	// of the corresponding media type written to this output. Mirrors
	// FFmpeg's `-frames:v N` / `-frames:a N` (also spelt `-vframes` /
	// `-aframes`). 0 means unlimited (default). Once the cap is
	// reached for a stream, further packets on that channel are read
	// and dropped so upstream does not stall; the muxer trailer is
	// written when all input channels close. Required for
	// extract-frame, tile-thumbnails, and scene-image patterns.
	MaxFramesVideo int `json:"max_frames_video,omitempty"`
	MaxFramesAudio int `json:"max_frames_audio,omitempty"`
	// FPSMode controls how the engine reconciles incoming video frame
	// PTS with the encoder's target framerate (the value advertised by
	// the upstream filter graph or `EncoderParamsVideo.framerate`).
	// Mirrors FFmpeg's `-fps_mode` flag (and the legacy `-vsync` alias
	// the `compat/ffcli` importer rewrites). Applies only to video
	// streams; ignored for audio and subtitles.
	//
	// Recognised values:
	//   "" (default) / "passthrough" — pass frames through untouched;
	//                                   identical to FFmpeg `-fps_mode passthrough`.
	//   "vfr"                       — pass frames through, but drop any frame
	//                                   whose PTS is <= the previously emitted
	//                                   PTS (i.e. enforce strict monotonicity).
	//   "cfr"                       — renumber frame PTS at constant 1/framerate
	//                                   intervals; duplicate the previous frame
	//                                   into gaps and drop frames that arrive
	//                                   sooner than half a duration after the
	//                                   last emission. The single biggest cure
	//                                   for HLS/DASH player A/V drift.
	//   "drop"                      — like vfr but also drops duplicates of the
	//                                   previous frame (PTS within ±half a
	//                                   duration window).
	FPSMode string `json:"fps_mode,omitempty"`
	// Metadata is the container-level metadata table written into the
	// output (`-metadata key=value` in FFmpeg). When non-nil it
	// completely replaces any metadata mapped from inputs via
	// `Input.MapMetadata`; when nil and at least one input has
	// MapMetadata=true the merged input metadata is written instead.
	// Per-stream metadata is intentionally not exposed here yet —
	// resolving "which output stream" requires the universal mapper
	// (roadmap §3.2) and will land alongside it.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Chapters is the explicit chapter table for the output. Each
	// chapter's Start/End is in seconds. When non-nil it replaces any
	// chapters mapped from inputs via `Input.MapChapters`; when nil and
	// at least one input has MapChapters=true that input's chapter
	// table is written instead. The container must support chapters
	// (matroska, mp4, ogg, ffmetadata, …) for them to surface.
	Chapters []Chapter      `json:"chapters,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}

// Chapter is one entry in `Output.Chapters`. Start/End are in seconds;
// the runtime converts them to the chapter's microsecond time_base when
// the output is written. Title is the conventional shorthand for the
// `title` metadata key; arbitrary additional metadata may be passed in
// Metadata (which is merged on top of the title).
type Chapter struct {
	ID       int64             `json:"id,omitempty"`
	Start    float64           `json:"start"`
	End      float64           `json:"end"`
	Title    string            `json:"title,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Options holds global pipeline options.
type Options struct {
	Threads        int    `json:"threads,omitempty"`
	ThreadType     string `json:"thread_type,omitempty"` // "frame", "slice", "frame+slice", "" = auto
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
	if cfg.SchemaVersion != "1.0" && cfg.SchemaVersion != "1.1" && cfg.SchemaVersion != "1.2" {
		return fmt.Errorf("unsupported schema_version %q; expected \"1.0\", \"1.1\" or \"1.2\"", cfg.SchemaVersion)
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
		switch out.FPSMode {
		case "", "passthrough", "vfr", "cfr", "drop":
		default:
			return fmt.Errorf("output %q: invalid fps_mode %q (want passthrough|vfr|cfr|drop)", out.ID, out.FPSMode)
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
	// Validate go_processor nodes.
	for i, node := range cfg.Graph.Nodes {
		if node.Type == "go_processor" && node.Processor == "" {
			return fmt.Errorf("node[%d] %q: go_processor requires a \"processor\" field", i, node.ID)
		}
	}
	return nil
}
