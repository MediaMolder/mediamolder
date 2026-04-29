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
	// CopyTS preserves the original demuxer timestamps end-to-end
	// instead of rebasing every input to start at PTS 0. Mirrors
	// FFmpeg's global `-copyts` flag, which is global in the FFmpeg
	// CLI for exactly this reason: it changes both the demuxer-side
	// `ts_offset` (suppressing the `-timestamp` shift normally applied
	// after `-ss`) and the meaning of any output-side `-ss` / `-to`
	// (which become absolute timeline values rather than offsets from
	// the input's start). When false (default) the runtime keeps its
	// existing behaviour of shifting every demuxed PTS/DTS by
	// `-ts_offset = -seek_target` so downstream nodes see streams
	// rooted at 0; when true, the shift is suppressed and output
	// trim windows are interpreted in the input's native timeline.
	// Required for accurate broadcast / HLS PTS handling and for
	// scenarios where downstream tooling expects original wall-clock
	// timestamps (e.g. ad-cue insertion, EBU R128 long-form
	// loudness reports). See `fftools/ffmpeg_demux.c`'s
	// `ts_offset` computation and `ffmpeg_mux.c::of_streamcopy`.
	CopyTS bool `json:"copy_ts,omitempty"`
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
	// StreamLoop counts how many additional times the demuxer should
	// rewind to the start and re-emit packets after EOF. `0` (default)
	// disables looping, `N>0` plays the input N+1 times total, and
	// `-1` loops forever (until a downstream node — typically a
	// `-shortest` sibling — closes the pipeline). Mirrors FFmpeg's
	// per-input `-stream_loop N` flag (parsed in
	// fftools/ffmpeg_opt.c, enforced by fftools/ffmpeg_demux.c's
	// `seek_to_start` + `ts_fixup` cycle): on EOF the runtime
	// captures `max_pts - min_pts` of the first iteration as the
	// loop's media duration, calls `avformat_seek_file(..., 0)` to
	// rewind, and adds the accumulated cycle duration to every
	// subsequent packet's PTS/DTS so timestamps remain monotone
	// across iterations. The unblock pattern is the canonical
	// "watermark / bug overlay loop" job: a 5 s logo PNG looped
	// indefinitely against a finite main video with `-shortest`
	// ending the output. Without this flag the overlay runs out at
	// 5 s and the rest of the main clip carries the last logo
	// frame instead.
	StreamLoop int `json:"stream_loop,omitempty"`
	// ITSOffset shifts every packet's PTS/DTS by this many seconds
	// at demux time. Mirrors FFmpeg's per-input `-itsoffset T`
	// flag (parsed as OPT_TYPE_TIME in fftools/ffmpeg_opt.c, stored
	// in `InputFile.input_ts_offset`, applied via
	// `pkt->pts += av_rescale_q(ifile->ts_offset, AV_TIME_BASE_Q,
	// pkt->time_base)` in ts_fixup). Positive values delay the
	// input on the global timeline (the file's t=0 lands at
	// t=ITSOffset in the output); negative values advance it.
	// Composes additively with the implicit `-ss` ts_offset shift
	// the runtime already applies, exactly as FFmpeg composes
	// `f->ts_offset = o->input_ts_offset - timestamp`. The
	// canonical use case is correcting A/V slip on dubbed sources
	// (e.g. `-itsoffset -0.030 -i dubbed_audio.wav` to advance the
	// dub by 30 ms against the picture). Sub-millisecond resolution
	// is preserved end-to-end via AV_TIME_BASE microseconds.
	ITSOffset float64 `json:"itsoffset,omitempty"`
	// ReadRate paces packet reads to (ReadRate × realtime). `0`
	// (default) disables pacing — the demuxer reads as fast as the
	// pipeline can consume. `1.0` mirrors FFmpeg's `-re` flag
	// (read at native frame rate, the canonical live-restream
	// throttle); `2.0` reads at 2× realtime; `0.5` at half speed.
	// Implementation mirrors fftools/ffmpeg_demux.c::readrate_sleep:
	// per-packet, the runtime computes
	// `max_pts = stream_ts_offset + initial_burst_us +
	//           wallclock_elapsed_us × ReadRate` and, when the
	// packet's PTS exceeds that limit, sleeps for the difference
	// before forwarding the packet downstream. Required for
	// every "live-restream / RTMP / SRT egress" pipeline (without
	// pacing the muxer overruns the wallclock budget the
	// destination expects), and for any HLS / DASH push that
	// relies on segment-duration walltime equalling media
	// duration. Negative values are rejected by validate().
	ReadRate float64 `json:"read_rate,omitempty"`
	// ReadRateInitialBurst is the size of the unpaced burst window
	// at the start of the input, expressed in seconds of media
	// time. Mirrors FFmpeg's `-readrate_initial_burst SECS` flag.
	// Defaults to `0.5` (the FFmpeg default) when ReadRate is
	// non-zero and this field is unset; ignored when ReadRate is
	// zero. The first `ReadRateInitialBurst` seconds of every
	// stream are read at full speed regardless of pacing — useful
	// for filling a downstream segmenter's lookahead before the
	// throttle kicks in. Negative values are rejected by
	// validate().
	ReadRateInitialBurst float64 `json:"read_rate_initial_burst,omitempty"`
	// ReadRateCatchup is the multiplier used to recover from a
	// pacing lag. Mirrors FFmpeg's `-readrate_catchup` flag and
	// must be ≥ ReadRate when both are set. When the runtime
	// detects that the demuxer has fallen behind the schedule by
	// more than 0.3 s of media time (matches the same threshold
	// in fftools/ffmpeg_demux.c::readrate_sleep), it switches to
	// pacing at this higher rate until the lag is gone. Defaults
	// to `ReadRate × 1.05` when unset and ReadRate is non-zero;
	// ignored when ReadRate is zero. Rejected by validate when
	// `0 < ReadRateCatchup < ReadRate`.
	ReadRateCatchup float64 `json:"read_rate_catchup,omitempty"`
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
	// AudioSync requests audio resync compensation in front of the
	// audio encoder, mirroring the legacy FFmpeg `-async N` flag
	// (which FFmpeg 8.0 removed in favour of
	// `-af aresample=async=N`). When non-zero the runtime injects an
	// `aresample` libavfilter node between the upstream graph and the
	// audio encoder so swresample's soft / hard compensation engine
	// (libswresample/swresample.c::swr_next_pts) keeps the output
	// sample-clock locked to the demuxer-side PTS:
	//
	//   1     — only correct the start of the stream by padding with
	//          silence or trimming so the first sample lands at PTS 0
	//          (renders as `aresample=async=1:first_pts=0`); no later
	//          corrections are applied.
	//   N>1   — continuous soft compensation; up to N samples per second
	//          are stretched / squeezed to keep the output PTS aligned
	//          with the input PTS (`aresample=async=N`). 1000 is the
	//          most common production value.
	//
	// 0 (default) leaves the audio path untouched. Negative values are
	// rejected by validate(). Applies only to outputs that emit a
	// transcoded audio stream; pure stream-copy outputs are unaffected
	// because no filter graph runs.
	AudioSync int `json:"audio_sync,omitempty"`
	// Shortest, when true, instructs the runtime to stop muxing as
	// soon as the shortest stream feeding this output ends. Mirrors
	// FFmpeg's `-shortest` flag (per-output scope; see
	// `fftools/ffmpeg_mux_init.c`'s sync-queue setup). Required for
	// the entire "add a music track to a silent clip" / "watermark
	// loop on top of a finite source" pattern that dominates the
	// overlay + music-video corpus: without it the longer input runs
	// to its own EOF and the output is padded with whatever the
	// shorter stream's last frame holds. The runtime captures the
	// PTS at which the shortest stream closes and stops emitting
	// further packets on every other stream feeding the same
	// output, mirroring the per-output-file scope FFmpeg uses.
	Shortest bool `json:"shortest,omitempty"`
	// MaxFileSize caps the encoded output at this many bytes.
	// Mirrors FFmpeg's `-fs SIZE` flag and uses the same
	// enforcement pattern: before each `WritePacket` call the
	// runtime queries `avio_tell(pb)` and, if the current file
	// size has reached the limit, returns EOF and writes the
	// trailer cleanly (so the output remains a valid container).
	// 0 (default) means unlimited. Counted in bytes of the
	// container as written, including muxer overhead — matches
	// FFmpeg's `OutputFile.limit_filesize` semantics in
	// `fftools/ffmpeg_mux.c`. Negative values are rejected by
	// validate().
	MaxFileSize int64 `json:"max_file_size,omitempty"`
	// Metadata is the container-level metadata table written into the
	// output (`-metadata key=value` in FFmpeg). When non-nil it
	// completely replaces any metadata mapped from inputs via
	// `Input.MapMetadata`; when nil and at least one input has
	// MapMetadata=true the merged input metadata is written instead.
	// Per-stream metadata + disposition flags live on `Streams`
	// below (mirrors `-metadata:s:<type>:<index> key=value` and
	// `-disposition:s:<type>:<index> default+forced`).
	Metadata map[string]string `json:"metadata,omitempty"`
	// Streams attaches per-output-stream attributes (metadata,
	// disposition flags) to a specific stream of this output,
	// addressed in the FFmpeg-style `<media-type>:<index>` form.
	// Mirrors `-metadata:s:a:0 language=eng` and
	// `-disposition:s:v:0 default+forced`. The mapping is resolved
	// in the runtime in [pipeline/handlers.go]::handleSink after
	// each AVStream has been registered with the muxer (after
	// AddStream / AddStreamFromInput) and before WriteHeader, by
	// counting streams per media type in the order they were added
	// (matches FFmpeg's `check_stream_specifier` semantics for
	// `s:<type>:<idx>`). Per-stream codec / bitrate overrides are
	// intentionally not exposed here yet — explicit encoder graph
	// nodes already cover that need (see e.g.
	// `testdata/examples/35_abr_ladder.json`); this field is the
	// first PR towards the §6 Wave 1 "per-stream encoder overrides
	// + per-stream metadata" item and ships the metadata +
	// disposition half (the half that unblocks dual-language audio
	// and language-tagged subtitles in real jobs).
	Streams []StreamSpec `json:"streams,omitempty"`
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

// StreamSpec attaches per-output-stream attributes to a single stream
// of an `Output`, addressed in the FFmpeg-style `<media-type>:<index>`
// form. Mirrors FFmpeg's `-metadata:s:<type>:<idx>` and
// `-disposition:s:<type>:<idx>` per-stream specifiers (see
// fftools/ffmpeg_mux_init.c::of_add_metadata + set_dispositions, and
// the StreamSpecifier parser in fftools/cmdutils.c).
type StreamSpec struct {
	// Type is the media-type letter that the index counts within.
	// One of `"v"` (video), `"a"` (audio), `"s"` (subtitle),
	// `"d"` (data). Mirrors the second component of FFmpeg's
	// `s:<type>:<idx>` specifier.
	Type string `json:"type"`
	// Index is the 0-based position within the chosen media type
	// at this output, in the order the streams were added to the
	// muxer (which in turn reflects the order of the inbound
	// edges to the sink node). For example `Type:"a", Index:1`
	// targets the second audio stream of the output.
	Index int `json:"index"`
	// Metadata is applied to the AVStream's AVDictionary via
	// av_dict_set, mirroring `-metadata:s:<type>:<idx> key=value`.
	// The killer use-case is language tagging using the ISO
	// 639-2 three-letter codes (`{"language": "eng"}`,
	// `{"language": "fra"}`, …); other recognised keys include
	// `title`, `comment`, and any container-supported tag.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Disposition is a `+`-separated list of AV_DISPOSITION_*
	// flag names (e.g. `"default"`, `"default+forced"`,
	// `"hearing_impaired"`). Applied via
	// av_opt_set(stream, "disposition", value, 0) — mirrors
	// `-disposition:s:<type>:<idx>` in FFmpeg
	// (fftools/ffmpeg_mux_init.c::set_dispositions). Empty
	// string leaves the muxer's default disposition unchanged.
	Disposition string `json:"disposition,omitempty"`
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
		if inp.StreamLoop < -1 {
			return fmt.Errorf("input %q: invalid stream_loop %d (must be >= -1; -1 = infinite)", inp.ID, inp.StreamLoop)
		}
		if inp.ReadRate < 0 {
			return fmt.Errorf("input %q: invalid read_rate %g (must be >= 0)", inp.ID, inp.ReadRate)
		}
		if inp.ReadRateInitialBurst < 0 {
			return fmt.Errorf("input %q: invalid read_rate_initial_burst %g (must be >= 0)", inp.ID, inp.ReadRateInitialBurst)
		}
		if inp.ReadRateCatchup < 0 {
			return fmt.Errorf("input %q: invalid read_rate_catchup %g (must be >= 0)", inp.ID, inp.ReadRateCatchup)
		}
		if inp.ReadRateCatchup > 0 && inp.ReadRate > 0 && inp.ReadRateCatchup < inp.ReadRate {
			return fmt.Errorf("input %q: read_rate_catchup %g must be >= read_rate %g (mirrors fftools/ffmpeg_demux.c)", inp.ID, inp.ReadRateCatchup, inp.ReadRate)
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
		if out.AudioSync < 0 {
			return fmt.Errorf("output %q: invalid audio_sync %d (must be >= 0)", out.ID, out.AudioSync)
		}
		if out.MaxFileSize < 0 {
			return fmt.Errorf("output %q: invalid max_file_size %d (must be >= 0)", out.ID, out.MaxFileSize)
		}
		for j, ss := range out.Streams {
			switch ss.Type {
			case "v", "a", "s", "d":
			default:
				return fmt.Errorf("output %q streams[%d]: invalid type %q (want v|a|s|d)", out.ID, j, ss.Type)
			}
			if ss.Index < 0 {
				return fmt.Errorf("output %q streams[%d]: invalid index %d (must be >= 0)", out.ID, j, ss.Index)
			}
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
