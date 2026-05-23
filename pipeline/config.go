// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the top-level MediaMolder pipeline configuration (JSON schema v1.0).
// It maps 1:1 with the JSON command payload described in the project specification.
type Config struct {
	SchemaVersion string `json:"schema_version"`
	Description   string `json:"description,omitempty"`
	// FfmpegCmd is the equivalent FFmpeg command line for this job.
	// Three rules govern its use:
	//   1. Advisory only: the runtime ignores this field entirely.
	//   2. Auto-populated: convert-cmd and the GUI Import dialog set it to
	//      the original source command; the GUI save action refreshes it via
	//      the export-cmd endpoint on every save.
	//   3. User responsibility: if you edit this JSON manually, you must
	//      also update or remove this field to keep it accurate.
	FfmpegCmd     string   `json:"ffmpeg_cmd,omitempty"`
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
	// StartAtZero modulates `CopyTS`. When `CopyTS` is true the
	// runtime normally suppresses the demuxer-side ts_offset shift
	// that would otherwise rebase the first input PTS to 0
	// (preserving wall-clock timestamps for broadcast / HLS use).
	// `StartAtZero=true` re-enables that shift even under `CopyTS`,
	// so the first kept packet still lands at PTS 0 while later
	// packet timing is left untouched. Mirrors FFmpeg's global
	// `-start_at_zero` flag (see fftools/ffmpeg_demux.c L486 — the
	// `start_at_zero ? 0 : f->start_time_effective` branch). Only
	// meaningful when `CopyTS=true`; rejected by validate() when
	// set without `CopyTS`.
	StartAtZero bool `json:"start_at_zero,omitempty"`
	// FilterComplexThreads sets the upper bound on threads each filter
	// graph may use. Mirrors FFmpeg's global `-filter_complex_threads`
	// flag and the per-filter `-filter_threads`. Written to
	// `AVFilterGraph.nb_threads` after `avfilter_graph_alloc`. 0 leaves
	// libavfilter's default in place (typically `nproc`). Per-node
	// overrides via `NodeDef.Threads` win when set. (Wave 7 #38)
	FilterComplexThreads int `json:"filter_complex_threads,omitempty"`
	// Assets is the named asset registry. Each key is a symbolic asset
	// name; filter params may embed "$asset:<name>" as an option value
	// and the runtime substitutes the resolved filesystem path before
	// constructing the libavfilter graph. Typical use-cases: fonts for
	// drawtext= / subtitles=, RNNoise models for arnndn=, LUT files for
	// lut3d= / haldclut=. (Wave 8 #51)
	Assets map[string]AssetRef `json:"assets,omitempty"`
	// HardwareDevices declares named hardware-acceleration device contexts
	// that nodes may reference via NodeDef.Device. Each entry is opened
	// via av_hwdevice_ctx_create at pipeline start and closed on teardown.
	// Mirrors FFmpeg's global `-init_hw_device type[=name][:device]` flag
	// (fftools/ffmpeg_opt.c::opt_init_hw_device). (Wave 10 #56)
	HardwareDevices []HardwareDevice `json:"hardware_devices,omitempty"`
	// FilterAssetPaths is a list of directories searched when resolving
	// relative model-file paths that appear directly in filter params
	// (e.g. arnndn model=rnnoise.rnnn, sofalizer sofa=file.sofa).
	// Searched in declaration order after the pipeline file's own
	// directory; $asset:<name> references bypass this mechanism.
	// (Wave 11 #66)
	FilterAssetPaths []string `json:"filter_asset_paths,omitempty"`
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
	// "raw" opens an unframed elementary stream — the demuxer is forced
	// via Format (e.g. "rawvideo", "s16le", "image2") and the geometry
	// fields (PixelFormat / VideoSize / FrameRate for video; SampleRate
	// / Channels for audio) are required because the bytestream itself
	// carries no headers. "concat" opens a list of media segments via
	// libavformat's concat demuxer (FFmpeg `-f concat`); see ConcatList.
	Kind    string         `json:"kind,omitempty"`
	Streams []StreamSelect `json:"streams"`
	Options map[string]any `json:"options,omitempty"`
	// Format forces the libavformat demuxer instead of letting
	// avformat_open_input probe the URL. Mirrors FFmpeg's per-input
	// `-f FMT` flag. Required when Kind="raw" (e.g. "rawvideo",
	// "s16le", "image2"); optional otherwise — common values include
	// "mpegts", "concat", "image2", "alaw". Validated lazily at
	// open time via libavformat's `av_find_input_format`; an unknown
	// name surfaces as a clear "unknown input format" error.
	Format string `json:"format,omitempty"`
	// FrameRate sets the input frame rate for unframed/raw video and
	// for the image2 demuxer's PNG/JPEG sequences. Mirrors FFmpeg's
	// per-input `-framerate FPS` (or the legacy `-r FPS` before `-i`).
	// Pushed to the demuxer as the AVOption `framerate`. Ignored by
	// container-aware demuxers (mp4/mov/mkv/ts) which carry their own
	// timing. Negative values are rejected by validate(); 0 means
	// "unset" (the demuxer falls back to its compiled default — 25 for
	// rawvideo, 1/5 for image2).
	FrameRate float64 `json:"framerate,omitempty"`
	// PixelFormat names the planar layout of unframed/raw video frames
	// (e.g. "yuv420p", "rgb24", "nv12"). Mirrors FFmpeg's per-input
	// `-pix_fmt FMT` (or `-pixel_format` for some demuxers). Pushed to
	// the demuxer as `pixel_format`. Required when Kind="raw" and
	// Format names a video raw demuxer; ignored otherwise.
	PixelFormat string `json:"pixel_format,omitempty"`
	// VideoSize is the WxH frame size of unframed/raw video, or one of
	// libavutil's named presets ("hd720", "vga", "ntsc", …). Mirrors
	// FFmpeg's per-input `-video_size SIZE` (or `-s SIZE` before `-i`).
	// Pushed to the demuxer as `video_size`. Required when Kind="raw"
	// and Format names a video raw demuxer; ignored otherwise.
	VideoSize string `json:"video_size,omitempty"`
	// SampleRate is the sampling rate (Hz) of unframed/raw PCM audio.
	// Mirrors FFmpeg's per-input `-ar RATE` (or `-sample_rate`). Pushed
	// to the demuxer as `sample_rate`. Required when Kind="raw" and
	// Format names a PCM audio raw demuxer (s16le, f32le, …).
	SampleRate int `json:"sample_rate,omitempty"`
	// Channels is the channel count of unframed/raw PCM audio. Mirrors
	// FFmpeg's per-input `-ac N` (or `-channels`). Pushed to the
	// demuxer as `channels`. Required when Kind="raw" and Format names
	// a PCM audio raw demuxer.
	Channels int `json:"channels,omitempty"`
	// SampleFormat optionally pins the libavutil sample format for
	// audio raw demuxers that accept it (rare; most PCM demuxers
	// hard-code the format from their name). Mirrors FFmpeg's
	// `-sample_fmt FMT` on the input side. Pushed to the demuxer as
	// `sample_fmt`. Empty = let the demuxer choose.
	SampleFormat string `json:"sample_fmt,omitempty"`
	// ConcatList is the in-config concat playlist used when Kind="concat".
	// Each entry mirrors a `file '…'` block of FFmpeg's concat-demuxer
	// listfile grammar (libavformat/concatdec.c) — the runtime serialises
	// the slice into a temp file and points avformat_open_input at it
	// with Format="concat" forced. Letting the editor describe the
	// playlist directly avoids the chicken-and-egg "I need a sidecar
	// .txt file under version control" problem most concat workflows
	// hit. When ConcatList is empty and Kind="concat", URL is treated
	// as a path to an existing concat listfile.
	ConcatList []ConcatEntry `json:"concat_list,omitempty"`
	// AccurateSeek selects between accurate (decode-and-discard until
	// PTS reaches the seek target — the FFmpeg default) and fast
	// (snap to the nearest keyframe) seeking when -ss is set on the
	// input. Mirrors FFmpeg's `-accurate_seek` / `-noaccurate_seek`.
	// Pointer-typed so an unset value resolves to the FFmpeg default
	// (true); only honoured when input timing has a `ss` value.
	AccurateSeek *bool `json:"accurate_seek,omitempty"`
	// SeekTimestamp, when true, interprets the `-ss` value as an
	// absolute container timestamp rather than as an offset from the
	// container's start_time. Mirrors FFmpeg's `-seek_timestamp 1`.
	// Required for inputs whose first PTS is non-zero (MPEG-TS
	// captures, ad-spliced segments) when the user wants to seek to a
	// specific wall-clock PTS rather than a position in the file.
	SeekTimestamp bool `json:"seek_timestamp,omitempty"`
	// ThreadQueueSize sets the demuxer's input packet queue depth in
	// frames. Mirrors FFmpeg's per-input `-thread_queue_size N`.
	// Pushed to the demuxer as `thread_queue_size`. Larger values
	// (default 8) buffer more packets when the upstream demuxer is
	// faster than the downstream consumer — required for high-bitrate
	// live captures (SDI, NDI, RTMP) where bursty I/O would otherwise
	// stall the pipeline. Negative values rejected by validate().
	ThreadQueueSize int `json:"thread_queue_size,omitempty"`
	// ProtocolWhitelist restricts which libavformat protocols this
	// input may dereference. Mirrors FFmpeg's `-protocol_whitelist
	// "p1,p2,…"`. Joined with commas and pushed to the demuxer as
	// `protocol_whitelist`. Empty = libavformat default
	// ("file,crypto,data" plus whatever the build enables); set to
	// e.g. ["file","tcp","tls","https"] for network inputs that load
	// remote subtitles or playlists, or to ["file"] to forbid network
	// access entirely. Names are not validated up-front; libavformat
	// rejects unknowns at open time.
	ProtocolWhitelist []string `json:"protocol_whitelist,omitempty"`
	// PatternType selects the image-sequence matcher used by the
	// image2 demuxer when URL contains a wildcard. Mirrors FFmpeg's
	// `-pattern_type {none|sequence|glob|glob_sequence}`. Defaults
	// (empty) to libavformat's "glob_sequence" autodetect. Common
	// settings: "glob" for `*.png`, "sequence" for `frame_%04d.png`.
	// Pushed to the demuxer as `pattern_type`. Ignored when the
	// demuxer is not image2.
	PatternType string `json:"pattern_type,omitempty"`
	// SubtitleCharenc forces the source character encoding when decoding
	// text-subtitle streams from this input. Mirrors FFmpeg's per-input
	// `-sub_charenc CODE` flag (parsed in fftools/ffmpeg_opt.c, stored
	// on the per-decoder AVOption AVCodecContext.sub_charenc and used
	// by libavcodec/decode.c L860 to drive iconv from CODE to UTF-8
	// before the text-subtitle decoder sees the packet). Setting this
	// on an input whose subtitle stream is bitmap (PGS, DVB, DVD,
	// ...) is rejected at decoder-open time because the conversion
	// is meaningless on graphics frames (mirrors libavcodec/decode.c
	// L2014-2023 forcing sub_charenc_mode=DO_NOTHING for non-text
	// codecs). Common values: "WINDOWS-1251", "ISO-8859-2", "GBK".
	SubtitleCharenc string `json:"subtitle_charenc,omitempty"`
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
	// HWAccel names the hardware-acceleration method to use when
	// decoding streams from this input. Mirrors FFmpeg's per-input
	// `-hwaccel METHOD` flag (e.g. "cuda", "vaapi", "qsv",
	// "videotoolbox", "none"). When non-empty the pipeline opens the
	// decoder via av.OpenHWDecoder instead of the software path,
	// using the HardwareDevice named by HWAccelDevice (or the first
	// matching device in Config.HardwareDevices when HWAccelDevice
	// is empty). Empty = software decoding (the default). (Wave 10 #59)
	HWAccel string `json:"hwaccel,omitempty"`
	// HWAccelDevice names a HardwareDevice entry (from
	// Config.HardwareDevices) whose opened AVHWDeviceContext is used
	// for hardware-accelerated decoding of this input. Mirrors
	// FFmpeg's per-input `-hwaccel_device DEV` flag. Empty = use the
	// first Config.HardwareDevices entry whose Type matches HWAccel,
	// or let libavcodec pick (if no matching entry exists). Ignored
	// when HWAccel is empty. (Wave 10 #59)
	HWAccelDevice string `json:"hwaccel_device,omitempty"`
	// HWAccelOutputFormat pins the pixel format of frames produced by
	// the hardware decoder, controlling whether frames stay in GPU
	// memory ("cuda", "vaapi_vld", "nv12", …) or are automatically
	// transferred to system RAM ("yuv420p", "nv12"). Mirrors FFmpeg's
	// per-input `-hwaccel_output_format FMT` flag. Empty = let
	// libavcodec choose (frames remain in GPU memory when HWAccel is
	// set, which is usually what you want for zero-copy filter chains).
	// Ignored when HWAccel is empty. (Wave 10 #59)
	HWAccelOutputFormat string `json:"hwaccel_output_format,omitempty"`
}

// ConcatEntry is one row of the libavformat concat-demuxer playlist.
// Mirrors the `file '…' [duration X] [inpoint X] [outpoint X]
// [file_packet_metadata K=V]` grammar parsed by
// libavformat/concatdec.c. The runtime serialises a slice of
// ConcatEntry into a temp listfile when Input.Kind="concat" and
// ConcatList is non-empty, so the editor never has to ship a sidecar
// .txt file alongside the JSON.
type ConcatEntry struct {
	// File is the URL or path of the segment. Quoted with single
	// quotes in the serialised listfile (concatdec.c expects POSIX
	// shell quoting); a literal apostrophe must be escaped as `'\''`
	// upstream — the runtime emits the file path verbatim under
	// single quotes and rejects entries containing one.
	File string `json:"file"`
	// Duration optionally pins the segment's reported duration in
	// seconds. Mirrors the listfile `duration` directive. Required
	// for streamed (pipe:) segments because the demuxer cannot
	// probe their length; ignored for seekable file segments unless
	// the user wants to override the container's reported duration.
	Duration float64 `json:"duration,omitempty"`
	// InPoint optionally trims the segment to start at this PTS
	// (seconds from the segment's start). Mirrors the listfile
	// `inpoint` directive.
	InPoint float64 `json:"inpoint,omitempty"`
	// OutPoint optionally trims the segment to stop at this PTS
	// (seconds from the segment's start). Mirrors the listfile
	// `outpoint` directive. Must be > InPoint when both are set.
	OutPoint float64 `json:"outpoint,omitempty"`
	// Metadata adds key=value pairs as `file_packet_metadata`
	// directives, attached to every packet emitted from this
	// segment via AV_PKT_DATA_STRINGS_METADATA.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// StreamSelect selects a specific stream from an input.
//
// Mirrors FFmpeg's `-map [-]input_file_id[:program_id][:stream_specifier][?]`
// grammar (fftools/ffmpeg_opt.c::map_manual). The runtime resolves the
// selection list against the demuxed input by walking entries in
// declaration order: each non-Negate entry adds matching streams to
// the selection, each Negate entry removes them. `Optional` makes a
// missing match a silent skip rather than a fatal error.
type StreamSelect struct {
	InputIndex int    `json:"input_index"`
	Type       string `json:"type"`  // "video", "audio", "subtitle", "data", "attachment"
	Track      int    `json:"track"` // zero-based track number within the type
	// All, when true, selects every stream of `Type` rather than the
	// single track at `Track`. Mirrors FFmpeg's no-index form
	// (`-map 0:v` selects all video streams; `-map 0:v:0` selects
	// only the first). When `All` is true `Track` is ignored.
	All bool `json:"all,omitempty"`
	// Optional, when true, makes a missing match (no stream of the
	// requested type / index / program in the input) a silent skip
	// instead of a fatal error. Mirrors FFmpeg's `?` suffix
	// (`-map 0:s?` = include subtitles if present, no error if
	// absent). The canonical "include subtitles if present" recipe
	// that today requires per-job branching.
	Optional bool `json:"optional,omitempty"`
	// Negate, when true, removes matching streams from the
	// selection that prior entries added (rather than adding them).
	// Mirrors FFmpeg's leading `-` (`-map -0:s` = drop every
	// subtitle from the default selection). Order matters: a
	// Negate entry only affects entries that come before it.
	Negate bool `json:"negate,omitempty"`
	// Program, when > 0, restricts the match to streams that
	// belong to the input's program with this `id` (NOT array
	// index). Mirrors FFmpeg's `-map 0:p:N[:stream_specifier]`
	// (per `cmdutils.c::check_stream_specifier` the `p:N` token
	// is matched against `AVProgram.id`). Required for any
	// MPEG-TS broadcast input where multiple programs share the
	// same transport stream. `0` (default) means program-agnostic
	// — match against every stream regardless of program
	// membership.
	Program int `json:"program,omitempty"`
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
	// ProbedInputs caches stream-probe results keyed by input id. It is
	// written by the GUI when saving a job so that reopening it restores
	// track handles and stream info without re-probing the source file.
	// The pipeline runtime never reads or modifies this field.
	ProbedInputs map[string]json.RawMessage `json:"probed_inputs,omitempty"`
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
	// Threads, when > 0, sets the per-graph thread cap written to
	// `AVFilterGraph.nb_threads` for this filter's compiled graph.
	// Only meaningful for `Type == "filter"`. Mirrors FFmpeg's
	// per-filter `-filter_threads`. Wins over the pipeline-wide
	// `Config.FilterComplexThreads`. (Wave 7 #38)
	Threads int `json:"threads,omitempty"`
	// OutputMediaType, when set, declares the media type produced on the
	// node's outbound pads. Required for cross-media-type filters where the
	// output type cannot be inferred from the inbound edge type — e.g.
	// `showwavespic` / `showspectrumpic` / `showvolume` (audio in, video
	// out) or `concat=v=1:a=1` (mixed in, mixed out). The pipeline
	// validator checks every outbound edge type matches this field; the
	// runtime forces such nodes through the complex-filter-graph path so
	// the buffersink media type is set correctly. Valid values: "video",
	// "audio", "subtitle", "data". (Wave 7 #37)
	OutputMediaType string `json:"output_media_type,omitempty"`
	// Device, when set, names an entry in Config.HardwareDevices whose
	// opened av.HWDeviceContext is used for hardware-accelerated
	// encode / decode / filter on this node. Mirrors the per-stream device
	// binding that FFmpeg expresses via `-init_hw_device` + per-codec
	// AVOption (hwaccel_device). (Wave 10 #56)
	Device string `json:"device,omitempty"`
	// AutoMapHW, when true on a filter node that also has Device set,
	// opts the node into the hardware filter auto-mapping pass
	// (expandHWFilterMappings). The pass promotes the software filter
	// name to its hardware equivalent for the declared device type
	// (e.g. "scale" on a "cuda" device → "scale_cuda"), and inserts
	// synthetic "hwupload" / "hwdownload" nodes on any video edge
	// that crosses a device boundary.
	//
	// This is an explicit per-node opt-in so the user retains full
	// control: a node with Device set but AutoMapHW=false keeps its
	// software filter name and receives no format-conversion splices.
	// Nodes that already name a hardware filter (e.g. "scale_cuda")
	// should leave AutoMapHW=false (or omit it) — the pass only
	// operates on names listed in the hw_filter_map table and is a
	// no-op for hardware filter names not in that table. (Wave 10 #58)
	AutoMapHW bool `json:"auto_map_hw,omitempty"`
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
	BSFSubtitle   string `json:"bsf_subtitle,omitempty"`
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
	// MuxDelay caps how far the muxer is allowed to buffer ahead of
	// the latest demux/decode timestamp before flushing, in seconds.
	// Mirrors FFmpeg's `-muxdelay SECONDS` (per-output float; see
	// fftools/ffmpeg_mux_init.c L3447 — `oc->max_delay = (int)(o->mux_max_delay
	// * AV_TIME_BASE)` writes the value into AVFormatContext.max_delay
	// in microseconds). FFmpeg's default is 0.7 s; 0 leaves the muxer
	// default unchanged. Negative values are rejected by validate().
	MuxDelay float64 `json:"muxdelay,omitempty"`
	// MuxPreload pre-rolls the initial demux-decode delay window, in
	// seconds. Mirrors FFmpeg's `-muxpreload SECONDS` (per-output
	// float; see fftools/ffmpeg_mux_init.c L3444 —
	// `av_dict_set_int(&mux->opts, "preload", o->mux_preload * AV_TIME_BASE, 0)`).
	// Most muxers ignore this; the historically dominant consumer is
	// the MPEG-PS muxer (`libavformat/mpegenc.c`'s `preload`
	// AVOption). 0 leaves the muxer default unchanged. Negative
	// values are rejected by validate().
	MuxPreload float64 `json:"muxpreload,omitempty"`
	// AvoidNegativeTS controls libavformat's automatic timestamp
	// shift policy at the muxer boundary. Mirrors FFmpeg's
	// `-avoid_negative_ts` (and the AVFormatContext AVOption of the
	// same name; see libavformat/options_table.h L95-99). One of:
	//   ""                  — leave libavformat's default
	//                        (`auto`) unchanged.
	//   "auto"              — shift only when the target muxer
	//                        requires non-negative timestamps
	//                        (`AVFMT_AVOID_NEG_TS_AUTO`, the
	//                        FFmpeg default).
	//   "disabled"          — do not shift; pass timestamps through
	//                        as received (`AVFMT_AVOID_NEG_TS_DISABLED`).
	//   "make_non_negative" — shift just enough to keep all
	//                        timestamps >= 0
	//                        (`AVFMT_AVOID_NEG_TS_MAKE_NON_NEGATIVE`).
	//   "make_zero"         — shift so the first timestamp is exactly
	//                        0 (`AVFMT_AVOID_NEG_TS_MAKE_ZERO`).
	// Required for clean MP4/MOV writes when input PTS are negative
	// (typical with `-ss` + `-copyts`). Validated against the enum
	// at config-load.
	AvoidNegativeTS string `json:"avoid_negative_ts,omitempty"`
	// DisableVideo, DisableAudio, DisableSubtitle, DisableData drop
	// every inbound edge of the corresponding media type at this
	// output's sink, so no stream of that type is added to the muxer.
	// Mirror FFmpeg's `-vn` / `-an` / `-sn` / `-dn` flags scoped to a
	// single `-i`/`-`/`OUT.ext` block (see fftools/ffmpeg_opt.c
	// L1977/2078/2115/2187 — `OPT_OUTPUT` aliases of the
	// per-OutputFile `video_disable`/`audio_disable`/
	// `subtitle_disable`/`data_disable` toggles). The implicit-encoder
	// pass and stream-copy wiring are both suppressed for the dropped
	// type. Edges are filtered in `BuildDef` before
	// `expandImplicitEncoders` runs so no encoder context is ever
	// created for the disabled type. validate() refuses an output
	// whose four flags are all set (zero streams = invalid muxer).
	DisableVideo    bool `json:"vn,omitempty"`
	DisableAudio    bool `json:"an,omitempty"`
	DisableSubtitle bool `json:"sn,omitempty"`
	DisableData     bool `json:"dn,omitempty"`
	// Realtime, when non-nil, overrides the global Phase 7 pre-roll
	// settings for this output only (e.g. an audio-only HLS variant
	// can set PrebufferDurationSeconds: 1.0 while video variants keep
	// the global 4.0 s default).
	Realtime *RealtimeOutputOptions `json:"realtime,omitempty"`
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
	Chapters []Chapter `json:"chapters,omitempty"`
	// CoverArt is the path to an image file (JPEG, PNG, or any format
	// decodable by libavformat) that is embedded as cover art into the
	// output container. Materialised as an AVMEDIA_TYPE_VIDEO stream
	// with AV_DISPOSITION_ATTACHED_PIC before avformat_write_header,
	// mirroring `-i cover.jpg -map 1:v -c:v:1 copy -disposition:v:1
	// attached_pic`. Supported containers (validated by
	// validateCoverArt): mp4, m4a, mov, ipod, mp3, mkv / matroska.
	// Wave 11 #64.
	CoverArt string `json:"cover_art,omitempty"`
	// Attachments lists files muxed into the container as
	// `AVMEDIA_TYPE_ATTACHMENT` streams (matroska / mkv / webm only).
	// Mirrors the FFmpeg `-attach FILE` CLI: each entry's content
	// is loaded once and copied into the stream's
	// `codecpar->extradata`; `filename` (the basename, by default)
	// and `mimetype` are written as stream-metadata entries. Use for
	// embedding fonts (TrueType / OpenType for soft subtitles),
	// cover art, or chapter sidecars. Wave 6 #31.
	Attachments []Attachment   `json:"attachments,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
	// Kind selects the output discriminator. `""` (default) and
	// `"file"` open a single muxer at `URL` (the historical
	// behaviour). `"tee"` switches the runtime to libavformat's
	// built-in tee muxer: `URL` and `Format` are ignored, and
	// `Targets` is required. Encoding happens once; the tee muxer
	// fans the encoded packet stream out to every target with no
	// re-encoding (FFmpeg `-f tee "[f=mp4]a.mp4|[f=hls]b.m3u8"`).
	// Per-target metadata / disposition is not directly supported
	// by libavformat (slaves clone the parent context); use
	// `Output.Metadata` / `Output.Streams` for values shared by
	// every target.
	Kind string `json:"kind,omitempty"`
	// Targets is the list of tee slaves. Required when
	// `Kind == "tee"`; must be empty otherwise. The runtime
	// builds the FFmpeg slaves URL by joining each target with
	// `|` and prepending each with its `[opt=val:opt=val]` block,
	// then opens the tee muxer once via libavformat. Mirrors the
	// `[options]url|[options]url` grammar parsed by
	// `libavformat/tee_common.c::ff_tee_parse_slave_options`.
	Targets []TeeTarget `json:"targets,omitempty"`
	// Pass enables two-pass video encoding for the implicit (or
	// upstream) video encoder feeding this output. Bit-field that
	// mirrors FFmpeg's `-pass N`: 1 = analysis pass
	// (AV_CODEC_FLAG_PASS1, encoder writes statistics), 2 = final
	// pass (AV_CODEC_FLAG_PASS2, encoder consumes the previous
	// pass's statistics), 3 = both bits set (rare; some codecs
	// support single-pass rate control fed pass-1 stats). 0
	// (default) is single-pass. The job is run twice by the
	// caller \u2014 once with Pass=1, once with Pass=2 \u2014 against the
	// same `PassLogFile` prefix; only video encoders honour this
	// field. Mirrors fftools/ffmpeg_mux_init.c (the `do_pass`
	// branch around line 700).
	Pass int `json:"pass,omitempty"`
	// PassLogFile is the per-stream statistics file prefix used by
	// two-pass video encoding. The runtime renders the actual file
	// path as `<prefix>-<stream_idx>.log` where `<stream_idx>` is
	// the global index of the video stream within the run (mirrors
	// FFmpeg's `<prefix>-<ost_idx>.log` naming in
	// fftools/ffmpeg_mux_init.c). Empty defaults to FFmpeg's
	// `ffmpeg2pass`. Honoured only when `Pass != 0`. For
	// libx264 / libvvenc the runtime translates this into the
	// `stats` AVOption on the encoder; for libx265 into
	// `x265-stats`; for any other encoder the runtime opens the
	// file directly and feeds AVCodecContext.stats_in / stats_out
	// (mirrors the three-way switch in fftools/ffmpeg_mux_init.c).
	PassLogFile string `json:"passlogfile,omitempty"`
	// LoudnormPass selects the role of this output in a two-pass
	// EBU R128 loudness-normalization shuttle. 0 = single-pass
	// (default; `loudnorm` runs in measurement-only mode without
	// the `measured_*` feed-forward, exactly as FFmpeg's
	// `-af loudnorm` behaves with no `print_format`). 1 = analysis
	// pass — the runtime walks the graph for any filter node with
	// `filter == "loudnorm"`, sets `print_format=json` and
	// `stats_file=<LoudnormStatsFile>-<idx>.json` on it, and on
	// uninit the loudnorm filter writes the EBU R128 measurements
	// (input_i, input_tp, input_lra, input_thresh, target_offset)
	// to that JSON file via `avpriv_fopen_utf8` — the same code
	// path FFmpeg's `print_format=json:stats_file=…` uses
	// (libavfilter/af_loudnorm.c::uninit). 2 = apply pass — the
	// runtime reads the JSON file written by pass 1 and injects
	// `measured_I` / `measured_TP` / `measured_LRA` /
	// `measured_thresh` / `offset` parameters into the same
	// loudnorm filter node, so the second invocation produces a
	// linearly-scaled output that hits the configured `I` / `TP`
	// / `LRA` targets exactly. The job is run twice by the caller
	// (pass 1 then pass 2) against the same `LoudnormStatsFile`
	// prefix. This is the orchestration primitive FFmpeg itself
	// has no flag for — every documented two-pass loudnorm
	// recipe wires it by hand via stderr-scraping. Mirrors the
	// shape of `Pass` for video two-pass encoding.
	LoudnormPass int `json:"loudnorm_pass,omitempty"`
	// LoudnormStatsFile is the prefix the loudnorm shuttle uses to
	// build the per-loudnorm-node JSON stats file path
	// (`<prefix>-<idx>.json` where `<idx>` is the per-run
	// loudnorm-node ordinal so multiple loudnorm filters in one
	// job get unique stats files). Empty defaults to
	// `mm-loudnorm`. Honoured only when `LoudnormPass != 0`.
	LoudnormStatsFile string `json:"loudnorm_statsfile,omitempty"`
	// ForceKeyFrames mirrors FFmpeg's `-force_key_frames SPEC`
	// flag. When non-empty the runtime forces an IDR keyframe on
	// every video frame matching SPEC by setting
	// `frame.pict_type = AV_PICTURE_TYPE_I` before
	// `enc.SendFrame` (mirrors fftools/ffmpeg_enc.c::forced_kf_apply
	// line 738). Required for HLS / DASH segmenters: without
	// keyframes at segment boundaries the segmenter silently
	// produces broken playlists.
	//
	// Supported grammars (parsed by parseForceKeyFrames):
	//   - "expr:EXPR" — libavutil expression evaluated per video
	//     frame; vars n / n_forced / prev_forced_n /
	//     prev_forced_t / t (mirrors ffmpeg.h:557-561). Canonical
	//     idiom: `expr:gte(t,n_forced*2)` for a 2 s GOP.
	//   - "source" — copy keyframes from the source (matcher fires
	//     when the upstream frame's pict_type is I).
	//   - comma-separated time list — float seconds, e.g.
	//     "3.0,7.5,10.25". HH:MM:SS form deferred.
	ForceKeyFrames string `json:"force_key_frames,omitempty"`
	// HLS carries typed HLS muxer options applied to the output
	// when `Format == "hls"`. Promoted from the generic `Options`
	// AVDict bag so jobs can express segment timing, playlist
	// type, fMP4/CMAF mode, master-playlist generation, and
	// variant-stream binding without raw AVOption strings.
	// Mirrors `libavformat/hlsenc.c`'s AVOption table; values are
	// rendered into the AVDictionary passed to
	// `avformat_write_header`. Conflicting keys in `Options` lose
	// to the typed field. Validated only when `Format == "hls"`.
	HLS *HLSOptions `json:"hls,omitempty"`
	// DASH carries typed DASH muxer options applied to the output
	// when `Format == "dash"`. Same promotion model as `HLS`,
	// against `libavformat/dashenc.c`'s AVOption table.
	DASH *DASHOptions `json:"dash,omitempty"`
	// Color carries per-stream color metadata (range, primaries,
	// transfer, matrix, chroma_location) written onto every video
	// stream of this output's codecpar before WriteHeader. Values
	// are the canonical libavutil names (`av_color_*_name`):
	// `"tv"`/`"pc"`, `"bt709"`/`"bt2020"`/`"smpte170m"`,
	// `"bt709"`/`"smpte2084"` (PQ) / `"arib-std-b67"` (HLG),
	// `"bt709"`/`"bt2020nc"`, `"left"`/`"center"`/.... Empty fields
	// are left unchanged. Mirrors FFmpeg `-color_range` /
	// `-color_primaries` / `-color_trc` / `-colorspace` /
	// `-chroma_sample_location`.
	Color *ColorMetadata `json:"color,omitempty"`
	// HDR carries SMPTE ST 2086 mastering display + CTA-861.3
	// content-light-level metadata attached to every video stream's
	// codecpar.coded_side_data before WriteHeader (the muxer then
	// writes the corresponding `mdcv` / `clli` boxes for mp4/mov,
	// `MasteringMetadata` / `MaxCLL`/`MaxFALL` for matroska/webm,
	// SEI passthrough for mpegts). Mirrors FFmpeg
	// `-mastering_display_metadata` / `-content_light_level`.
	// Validation requires Color.Transfer ∈ {"smpte2084","arib-std-b67"}
	// and a HDR-capable codec/container combination.
	HDR *HDRMetadata `json:"hdr,omitempty"`

	// SAR / DAR set the output video stream's sample / display aspect
	// ratio (mirrors FFmpeg's `setsar=A:B` / `setdar=A:B` filters and
	// the legacy `-aspect A:B` shorthand). Accepted forms: "A:B",
	// "A/B", or a decimal float ("1.5"). Empty means "leave the
	// encoder default unchanged". At most one may be set per output
	// (mutually exclusive: setsar and setdar both compute SAR but
	// take different inputs). The runtime resolves DAR -> SAR using
	// the encoder's resolved width/height (SAR_num/SAR_den =
	// (DAR_num*H) / (DAR_den*W)). Universally requested for legacy
	// SD content (DV-PAL 720x576 @ 4:3 needs SAR 16:15; NTSC 720x480
	// @ 4:3 needs SAR 8:9).
	SAR string `json:"sar,omitempty"`
	DAR string `json:"dar,omitempty"`

	// EncoderTimeBase chooses the encoder's `AVCodecContext.time_base`
	// directly, mirroring FFmpeg's per-stream `-enc_time_base` flag
	// (fftools/ffmpeg_mux_init.c L1391-1417). Accepted forms:
	//   ""         – leave the av-layer default (1/framerate, or the
	//                buffersink TB when the encoder is fed by a graph).
	//   "demux"    – inherit the upstream demuxer/decoder TB
	//                (ENC_TIME_BASE_DEMUX sentinel; resolved at
	//                runtime once the source TB is known).
	//   "filter"   – inherit the buffersink TB
	//                (ENC_TIME_BASE_FILTER sentinel).
	//   "N/D"      – an explicit rational, parsed by av_parse_ratio.
	// Rejected on subtitle outputs (libavcodec ignores it; FFmpeg
	// logs the same warning at fftools/ffmpeg_mux_init.c L1394).
	EncoderTimeBase string `json:"encoder_time_base,omitempty"`

	// FieldOrder stamps the encoder's `AVCodecContext.field_order`
	// (libavcodec/defs.h::AVFieldOrder) so the muxer knows whether
	// to write progressive or interlaced metadata into the
	// container. Mirrors FFmpeg `-field_order`. Accepted values:
	//   ""             – leave the encoder default
	//                    (AV_FIELD_UNKNOWN).
	//   "progressive"  – AV_FIELD_PROGRESSIVE.
	//   "tt" / "bb"    – top/bottom field coded AND displayed first.
	//   "tb" / "bt"    – top/bottom coded first, opposite displayed
	//                    first (mixed-parity streams; rare).
	// FieldOrder=="progressive" with InterlacedEncode=true is
	// rejected at validate time (the combination is nonsensical and
	// the encoder would produce garbage metadata).
	FieldOrder string `json:"field_order,omitempty"`

	// InterlacedEncode toggles the AV_CODEC_FLAG_INTERLACED_DCT |
	// AV_CODEC_FLAG_INTERLACED_ME bits on the encoder context
	// (avcodec.h L310 / L331), telling the encoder to use
	// interlaced motion estimation and DCT layout. Required for
	// broadcast-grade SDI workflows that round-trip 1080i/29.97
	// or 1080i/25 through libx264 / libxavs2 / mpeg2video. Pair
	// with FieldOrder ∈ {"tt","bb","tb","bt"}.
	InterlacedEncode bool `json:"interlaced_encode,omitempty"`
}

// ColorMetadata is the typed projection of FFmpeg's per-stream color
// AVOptions (`color_range`, `color_primaries`, `color_trc`,
// `colorspace`, `chroma_sample_location`). Values must be one of the
// canonical names parsed by `av_color_*_from_name`; unknown names are
// rejected at validation time so a typo never silently writes
// `AVCOL_*_UNSPECIFIED`. Empty strings = "do not set" (preserve the
// muxer's default of inheriting from the encoder / input).
type ColorMetadata struct {
	Range          string `json:"range,omitempty"`
	Primaries      string `json:"primaries,omitempty"`
	Transfer       string `json:"transfer,omitempty"`
	Space          string `json:"space,omitempty"`
	ChromaLocation string `json:"chroma_location,omitempty"`
}

// HDRMetadata holds optional SMPTE ST 2086 mastering display and
// CTA-861.3 content-light-level metadata. Either block may be nil.
//
// MasteringDisplay primaries / white point use the standard 0.00002
// (1/50000) chromaticity units (the encoding HEVC/AV1 SEI use); luminance
// values use 0.0001 cd/m^2 units (i.e. nits × 10000). Setting any of
// the six DisplayPrimariesXY fields requires all six (the validator
// enforces this); WhitePoint is similarly all-or-nothing. The
// canonical Rec.2020 + D65 mastering display for HDR10:
//
//	DisplayPrimariesRX/Y = 35400/8500 (1.0,0.85*0)
//	... (see docs/color-hdr.md for the full encoding table).
//
// ContentLightLevel.MaxCLL is the per-frame peak luminance over the
// whole stream; MaxFALL is the per-frame frame-average maximum.
// 0/0 is treated as "not present" (the side data is not attached).
type HDRMetadata struct {
	MasteringDisplay  *MasteringDisplayMetadata  `json:"mastering_display,omitempty"`
	ContentLightLevel *ContentLightLevelMetadata `json:"content_light_level,omitempty"`
	// DoVi carries the stream-level Dolby Vision configuration
	// record (`AVDOVIDecoderConfigurationRecord`) muxed via
	// `AV_PKT_DATA_DOVI_CONF`. Wave 6 #35.
	DoVi *DoViMetadata `json:"dovi,omitempty"`
}

// DoViMetadata is the stream-level Dolby Vision configuration record
// (libavutil/dovi_meta.h::AVDOVIDecoderConfigurationRecord). Validator
// restricts to hevc/av1 video codecs and mp4/mov/matroska containers
// (the only muxers that carry the dvcC/dvvC/BlockAddIDExtraData
// payload). Wave 6 #35.
type DoViMetadata struct {
	// Profile is the canonical DV profile: 4 (HEVC dual-layer),
	// 5 (HEVC single-layer, BL-incompatible), 7 (HEVC dual,
	// BL-compatible), 8 (HEVC single-layer, BL-compatible),
	// 9 (AVC), 10 (AV1). Profile 0 is rejected (means "unset").
	Profile uint8 `json:"profile"`
	// Level is the DV bitstream level (0..13).
	Level uint8 `json:"level"`
	// RPUPresent advertises that RPU NAL units (NAL type 62 in
	// HEVC, OBU type 6 in AV1) are present in the bitstream. The
	// muxer copies them through verbatim under `-c:v copy`. Default
	// true (mirrors FFmpeg passthrough behaviour for DV streams).
	RPUPresent *bool `json:"rpu_present,omitempty"`
	// ELPresent flags an enhancement-layer track (profile 4 / 7).
	ELPresent bool `json:"el_present,omitempty"`
	// BLPresent flags a base-layer track (default true).
	BLPresent *bool `json:"bl_present,omitempty"`
	// BLCompatibilityID for profile 8 / 10 streams: 0 = none,
	// 1 = HDR10, 2 = SDR/BT.709, 4 = HLG. Determines the
	// cross-compatibility hint a non-DV decoder reads.
	BLCompatibilityID uint8 `json:"bl_compatibility_id,omitempty"`
}

// MasteringDisplayMetadata mirrors AVMasteringDisplayMetadata's wire
// layout. See HDRMetadata for unit conventions. Fields default to 0;
// HasPrimaries / HasLuminance gate which side-data flags are set.
type MasteringDisplayMetadata struct {
	DisplayPrimariesRX int `json:"display_primaries_rx,omitempty"`
	DisplayPrimariesRY int `json:"display_primaries_ry,omitempty"`
	DisplayPrimariesGX int `json:"display_primaries_gx,omitempty"`
	DisplayPrimariesGY int `json:"display_primaries_gy,omitempty"`
	DisplayPrimariesBX int `json:"display_primaries_bx,omitempty"`
	DisplayPrimariesBY int `json:"display_primaries_by,omitempty"`
	WhitePointX        int `json:"white_point_x,omitempty"`
	WhitePointY        int `json:"white_point_y,omitempty"`
	MinLuminance       int `json:"min_luminance,omitempty"`
	MaxLuminance       int `json:"max_luminance,omitempty"`
}

// ContentLightLevelMetadata mirrors AVContentLightMetadata.
type ContentLightLevelMetadata struct {
	MaxCLL  uint32 `json:"max_cll,omitempty"`
	MaxFALL uint32 `json:"max_fall,omitempty"`
}

// HLSOptions promotes the most-asked HLS muxer AVOptions
// (`libavformat/hlsenc.c`) to typed fields on `Output`. Only
// applied when `Output.Format == "hls"`. Zero / empty fields are
// omitted from the AVDictionary so libavformat's defaults apply.
//
// `Output.Options` remains the escape hatch for niche options
// (`hls_enc`, `hls_key_info_file`, `hls_subtitle_path`, …); on key
// collision the typed field wins.
type HLSOptions struct {
	// Time is the target segment duration in seconds (HLS
	// `hls_time`). FFmpeg default is 2.0. Forwarded as a string
	// with up to six decimals.
	Time float64 `json:"time,omitempty"`
	// InitTime is the initialisation segment duration in seconds
	// (HLS `hls_init_time`). 0 leaves the muxer default.
	InitTime float64 `json:"init_time,omitempty"`
	// ListSize caps the number of entries kept in the playlist
	// (HLS `hls_list_size`). 0 keeps every segment.
	ListSize int `json:"list_size,omitempty"`
	// PlaylistType is `hls_playlist_type` — `""` (live, default),
	// `"event"`, or `"vod"`. Setting `"vod"` writes EXT-X-ENDLIST
	// on close.
	PlaylistType string `json:"playlist_type,omitempty"`
	// SegmentType is `hls_segment_type` — `""` / `"mpegts"`
	// (default) or `"fmp4"` (CMAF). When `"fmp4"`,
	// `FMP4InitFilename` controls the init file name.
	SegmentType string `json:"segment_type,omitempty"`
	// SegmentFilename is the printf-style template for segment
	// files (HLS `hls_segment_filename`). May contain `%d` /
	// `%v` for variant-stream substitution.
	SegmentFilename string `json:"segment_filename,omitempty"`
	// FMP4InitFilename is the init segment file name when
	// `SegmentType == "fmp4"` (HLS `hls_fmp4_init_filename`).
	// Default `init.mp4`.
	FMP4InitFilename string `json:"fmp4_init_filename,omitempty"`
	// StartNumber is the first sequence number in the playlist
	// (HLS `start_number`). 0 starts from 0; FFmpeg's default
	// is also 0.
	StartNumber int `json:"start_number,omitempty"`
	// MasterPlName triggers master-playlist generation (HLS
	// `master_pl_name`). Empty = single-rendition playlist only.
	// Required for ABR ladders (combine with `VarStreamMap`).
	MasterPlName string `json:"master_pl_name,omitempty"`
	// VarStreamMap is the variant-stream mapping string (HLS
	// `var_stream_map`), e.g.
	// `"v:0,a:0 v:1,a:0 v:2,a:0"`. Required when
	// `MasterPlName` is set and the output has more than one
	// video / audio rendition (the standard ABR pattern).
	VarStreamMap string `json:"var_stream_map,omitempty"`
	// Flags is a list of `hls_flags` token names (e.g.
	// `["delete_segments", "independent_segments",
	// "program_date_time"]`). Joined with `+` before being
	// written to the AVDictionary, matching libavutil's
	// `AV_OPT_TYPE_FLAGS` parser.
	Flags []string `json:"flags,omitempty"`
}

// DASHOptions promotes the most-asked DASH muxer AVOptions
// (`libavformat/dashenc.c`) to typed fields on `Output`. Only
// applied when `Output.Format == "dash"`. Zero / empty fields are
// omitted from the AVDictionary so libavformat's defaults apply.
//
// `Output.Options` remains the escape hatch for niche options;
// on key collision the typed field wins.
type DASHOptions struct {
	// SegDuration is the target segment duration in seconds (DASH
	// `seg_duration`). FFmpeg default is 5.0.
	SegDuration float64 `json:"seg_duration,omitempty"`
	// FragDuration is the target fragment duration in seconds
	// (DASH `frag_duration`). 0 leaves segments unfragmented.
	FragDuration float64 `json:"frag_duration,omitempty"`
	// WindowSize is the maximum number of segments kept in the
	// manifest (DASH `window_size`). 0 keeps every segment.
	WindowSize int `json:"window_size,omitempty"`
	// ExtraWindowSize is the number of segments retained on disk
	// past `WindowSize` before deletion (DASH
	// `extra_window_size`). 0 leaves the muxer default (5).
	ExtraWindowSize int `json:"extra_window_size,omitempty"`
	// InitSegName is the init segment file-name template (DASH
	// `init_seg_name`). Empty leaves the muxer default
	// `init-stream$RepresentationID$.$ext$`.
	InitSegName string `json:"init_seg_name,omitempty"`
	// MediaSegName is the media segment file-name template
	// (DASH `media_seg_name`). Empty leaves the muxer default
	// `chunk-stream$RepresentationID$-$Number%05d$.$ext$`.
	MediaSegName string `json:"media_seg_name,omitempty"`
	// SingleFile stores every representation in one file with a
	// SegmentBase (DASH `single_file`). Defaults to false
	// (templated per-segment files).
	SingleFile bool `json:"single_file,omitempty"`
	// UseTemplate emits `<SegmentTemplate>` in the manifest
	// (DASH `use_template`). Pointer so unset = inherit
	// libavformat's default (true). false explicitly disables.
	UseTemplate *bool `json:"use_template,omitempty"`
	// UseTimeline emits `<SegmentTimeline>` (DASH
	// `use_timeline`). Pointer so unset = inherit libavformat's
	// default (true). false explicitly disables.
	UseTimeline *bool `json:"use_timeline,omitempty"`
	// Streaming enables low-latency streaming output with
	// progressive fragment writes (DASH `streaming`).
	Streaming bool `json:"streaming,omitempty"`
	// AdaptationSets is the manual adaptation-set spec (DASH
	// `adaptation_sets`), e.g.
	// `"id=0,streams=v id=1,streams=a"`. Required for any DASH
	// ABR ladder that mixes multiple video bitrates with one
	// audio rendition.
	AdaptationSets string `json:"adaptation_sets,omitempty"`
	// HLSPlaylist also generates HLS .m3u8 playlists alongside
	// the DASH manifest (DASH `hls_playlist`). The CMAF
	// dual-pack delivery mode.
	HLSPlaylist bool `json:"hls_playlist,omitempty"`
	// LDash enables low-latency DASH (DASH `ldash`).
	LDash bool `json:"ldash,omitempty"`
	// Flags is a list of `dash_flags` token names. Joined with
	// `+` before being written to the AVDictionary.
	Flags []string `json:"flags,omitempty"`
}

// TeeTarget describes one slave of a `Kind == "tee"` Output. It
// becomes one `[opt=val:opt=val]url` clause in the slaves URL passed
// to libavformat's tee muxer. Mirrors `libavformat/tee.c::open_slave`
// (the AVOption set on each tee slave context).
type TeeTarget struct {
	// URL is the slave's output URL (file path or scheme). Required.
	// `:`, `]`, `\`, and `|` inside the URL are escaped automatically
	// when the slaves string is built (matches `av_get_token`'s
	// backslash-escape grammar in libavutil/avstring.c).
	URL string `json:"url"`
	// Format forces the slave's container format (the tee muxer's
	// `f=` AVOption). Usually required since auto-detection from
	// `URL` may fail (e.g. when writing through a pipe).
	Format string `json:"format,omitempty"`
	// Select is the tee muxer's `select=` AVOption — a comma-
	// separated list of FFmpeg stream specifiers that picks which
	// streams from the parent context this slave receives
	// (e.g. `"v"`, `"a:0"`, `"v,a:0"`). Empty means all streams.
	Select string `json:"select,omitempty"`
	// BSFs is the tee muxer's `bsfs=` AVOption — a per-slave
	// bitstream-filter chain (e.g. `"h264_mp4toannexb"`). Use the
	// FFmpeg per-stream form (`bsfs/v=...`) by passing it through
	// `Options` when stream-type-specific chains are needed.
	BSFs string `json:"bsfs,omitempty"`
	// OnFail is the tee muxer's `onfail=` AVOption — slave-failure
	// policy. `""` / `"abort"` (default) propagate the error; the
	// other accepted value is `"ignore"` which closes the failed
	// slave and continues writing to the rest.
	OnFail string `json:"onfail,omitempty"`
	// UseFifo wraps the slave in libavformat's `fifo` muxer (the
	// tee muxer's `use_fifo` AVOption). Adds an extra buffering
	// thread; required for slaves that must absorb downstream
	// stalls without blocking the encode loop.
	UseFifo bool `json:"use_fifo,omitempty"`
	// FifoOptions is the tee muxer's `fifo_options` AVOption,
	// forwarded as a `;`-separated `key=value` string to the fifo
	// muxer when `UseFifo` is true. Ignored otherwise.
	FifoOptions string `json:"fifo_options,omitempty"`
	// Options is a free-form bag of additional `[opt=val:opt=val]`
	// pairs prepended to the slave's `[...]` block. Accepted as an
	// escape hatch for obscure tee-slave AVOptions
	// (e.g. `use_hardware_acceleration`) that are not promoted to
	// typed fields.
	Options map[string]any `json:"options,omitempty"`
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

// Attachment is one file muxed into the output container as an
// `AVMEDIA_TYPE_ATTACHMENT` stream (Wave 6 #31). Mirrors FFmpeg's
// `-attach FILE` plus the optional `-metadata:s:t mimetype=…`
// pairing. Container support is matroska / mkv / webm only — other
// muxers reject attachment streams at WriteHeader time.
type Attachment struct {
	// Path is the local filesystem path of the file to embed.
	// Required. The file is read once at openSink time.
	Path string `json:"path"`
	// Filename is the value written to the stream's `filename`
	// metadata key (and, for matroska, the FileName element).
	// When empty the basename of `Path` is used.
	Filename string `json:"filename,omitempty"`
	// MimeType is the value written to the stream's `mimetype`
	// metadata key (matroska FileMimeType). Optional but strongly
	// recommended for fonts (`application/x-truetype-font`,
	// `application/vnd.ms-opentype`) and cover art (`image/png`,
	// `image/jpeg`).
	MimeType string `json:"mimetype,omitempty"`
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
	// Encoder, when non-nil, overlays per-stream encoder choices on
	// the matching synthetic encoder graph node produced by
	// expandImplicitEncoders. Lets callers spell ABR ladders inline
	// (one Output, multiple video edges, distinct -b:v / -crf per
	// stream) without authoring explicit encoder nodes. Mirrors
	// FFmpeg's per-stream encoder option specifier
	// `-<key>:<type>:<idx>` (e.g. `-b:v:0 5M -b:v:1 2.5M`,
	// fftools/ffmpeg_opt.c). Wave 6 #30.
	Encoder *EncoderOverride `json:"encoder,omitempty"`
}

// EncoderOverride is a sparse per-stream override applied on top of
// Output.CodecVideo/Audio/Subtitle and Output.EncoderParamsVideo/
// Audio/Subtitle. Empty Codec leaves the output-level codec choice
// in place; non-empty Codec replaces it for the matching stream.
// Options keys overwrite same-named entries in the output-level
// EncoderParams map for the matching stream only. Wave 6 #30.
type EncoderOverride struct {
	Codec   string         `json:"codec,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// HardwareDevice declares a named hardware-acceleration device context that
// can be referenced by name from encoder, decoder, and filter nodes via
// NodeDef.Device. Mirrors FFmpeg's `-init_hw_device type[=name][:device]`
// (fftools/ffmpeg_opt.c::opt_init_hw_device). The name is a user-chosen
// label (e.g. "gpu0"); type is one of "cuda", "vaapi", "qsv",
// "videotoolbox"; device is the OS-level device specifier (e.g.
// "/dev/dri/renderD128", "0", or "" for the first available);
// options are forwarded as AVDictionary entries to av_hwdevice_ctx_create.
// (Wave 10 #56)
type HardwareDevice struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Device  string         `json:"device,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// Options holds global pipeline options.
type Options struct {
	Threads        int    `json:"threads,omitempty"`
	ThreadType     string `json:"thread_type,omitempty"` // "frame", "slice", "frame+slice", "" = auto
	HardwareAccel  string `json:"hw_accel,omitempty"`
	HardwareDevice string `json:"hw_device,omitempty"`
	Realtime       bool   `json:"realtime,omitempty"`

	// Phase 6 — adaptive encoder preset stepping.
	//
	// HighestQualityPreset: the slowest (highest quality) preset the
	//   controller is allowed to use. The controller may step freely to
	//   any faster preset when needed to keep up with real time. If
	//   unset, the encoder's initial preset is used as the quality bound.
	// PresetGroupStep: when non-nil, overrides the default true setting
	//   for the group-quorum step rule (steps every eligible video encoder
	//   together when enough are simultaneously behind).
	// TargetFPS: graph-level real-time fps target; 0 = derive from
	//   per-node fps_target params.
	// EncoderInputBufferFrames: per-encoder input channel capacity in
	//   frames; 0 = pipeline default (~8). The recommended realtime
	//   value is 96 (~4 s @ 24 fps) so a preset close+reopen does not
	//   stall upstream filters during the transition.
	HighestQualityPreset     string  `json:"highest_quality_preset,omitempty"`
	PresetGroupStep          *bool   `json:"preset_group_step,omitempty"`
	TargetFPS                float64 `json:"target_fps,omitempty"`
	EncoderInputBufferFrames int     `json:"encoder_input_buffer_frames,omitempty"`

	// Phase 7 — real-time output buffering & readiness signal.
	//
	// PrebufferDurationSeconds: target per-output preroll fill (seconds)
	//   before the muxer is allowed to write. Default 4.0 s for video
	//   outputs (1.0 s for audio-only outputs). 0 disables prerolling.
	// PrebufferMaxSeconds: hard cap on the preroll buffer; once exceeded
	//   the oldest packet is dropped (oldest-drop ring). Defaults to
	//   2 × PrebufferDurationSeconds.
	// Per-output overrides live on Output.Realtime.
	PrebufferDurationSeconds float64 `json:"prebuffer_duration_seconds,omitempty"`
	PrebufferMaxSeconds      float64 `json:"prebuffer_max_seconds,omitempty"`

	// ReadRate is a global default applied to every input whose own
	// read_rate field is zero. Paces demuxer reads to
	// (ReadRate × realtime); 0 = unpaced; 1.0 mirrors ffmpeg -re.
	// Set to 1.0 together with realtime:true for live-restream jobs so
	// the demuxer doesn't race ahead of the pipeline clock.
	ReadRate float64 `json:"read_rate,omitempty"`
	// ReadRateInitialBurst is the global default for the per-input
	// read_rate_initial_burst field; see Input.ReadRateInitialBurst.
	ReadRateInitialBurst float64 `json:"read_rate_initial_burst,omitempty"`
	// ReadRateCatchup is the global default for the per-input
	// read_rate_catchup field; see Input.ReadRateCatchup.
	ReadRateCatchup float64 `json:"read_rate_catchup,omitempty"`

	// RealtimeLogPath, when non-empty, enables a per-tick debug log written
	// by the real-time adaptive controller. Each line is a JSON object
	// containing the full NodePerfSnapshot for every node, the controller's
	// internal cool-down counters, and any decisions made that tick.
	// Useful for post-hoc diagnosis of performance anomalies.
	// The file is created (or truncated) when the pipeline starts.
	// Only active when realtime:true. Example: "/tmp/rt_debug.jsonl".
	RealtimeLogPath string `json:"realtime_log_path,omitempty"`
}

// RealtimeOutputOptions holds per-output Phase 7 pre-roll overrides.
// When a field is zero the corresponding global default applies.
type RealtimeOutputOptions struct {
	PrebufferDurationSeconds float64 `json:"prebuffer_duration_seconds,omitempty"`
	PrebufferMaxSeconds      float64 `json:"prebuffer_max_seconds,omitempty"`
}

// ErrorPolicy defines how a node handles errors.
type ErrorPolicy struct {
	Policy       string `json:"policy"` // "abort", "skip", "retry", "fallback"
	MaxRetries   int    `json:"max_retries,omitempty"`
	FallbackNode string `json:"fallback_node,omitempty"`
}

// AssetRef is a named media-asset entry in Config.Assets. The map key
// is a symbolic name used in filter params as "$asset:<name>"; the
// runtime resolves it to an absolute filesystem path before building
// the libavfilter graph. This keeps pipeline JSON machine-agnostic:
// fonts, ML model files, and LUTs live at different absolute paths on
// each workstation.
type AssetRef struct {
	// Path is the filesystem path of the asset file (absolute or
	// relative). Relative paths are resolved left-to-right against the
	// working directory and then against each directory listed in the
	// MEDIAMOLDER_ASSET_PATH environment variable (colon-separated on
	// POSIX, semicolon-separated on Windows). Required; must be
	// non-empty.
	Path string `json:"path"`
	// Kind classifies the asset for GUI presentation.
	// Accepted values: "font" (TrueType/OpenType for drawtext=/
	// subtitles=), "model" (ML model file for arnndn=, YOLO …),
	// "lut" (.cube/.3dl/.m3d for lut3d=/haldclut=), "other".
	Kind string `json:"kind"`
	// Desc is an optional human-readable label shown in the GUI.
	Desc string `json:"desc,omitempty"`
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
	cfg, err := ParseConfig(data)
	if err != nil {
		return nil, err
	}
	// Validate filter model paths relative to the pipeline file's directory.
	// (Wave 11 #66)
	if err := validateFilterModelPaths(cfg, filepath.Dir(path)); err != nil {
		return nil, err
	}
	return cfg, nil
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
		// Wave 7 #36a: a graph may stand entirely on filter_source
		// nodes (e.g. testsrc → encoder → file) with no top-level
		// demuxer inputs. Permit that, but require at least one
		// filter_source node to make the intent explicit.
		hasFilterSource := false
		for _, n := range cfg.Graph.Nodes {
			if n.Type == "filter_source" {
				hasFilterSource = true
				break
			}
		}
		if !hasFilterSource {
			return fmt.Errorf("config must have at least one input")
		}
	}
	if len(cfg.Outputs) == 0 {
		// Wave 7 #36d: a pipeline whose terminal nodes are all
		// filter_sink (nullsink/anullsink, optionally chained behind
		// a side-effect filter such as ebur128 or
		// ametadata=mode=print) needs no top-level muxer output.
		hasFilterSink := false
		for _, n := range cfg.Graph.Nodes {
			if n.Type == "filter_sink" {
				hasFilterSink = true
				break
			}
		}
		if !hasFilterSink {
			return fmt.Errorf("config must have at least one output")
		}
	}
	if cfg.StartAtZero && !cfg.CopyTS {
		return fmt.Errorf("start_at_zero requires copy_ts=true (it modulates -copyts behaviour; see fftools/ffmpeg_demux.c)")
	}
	// Pre-build hw device name set for use by both input and node validation.
	hwDeviceNames := make(map[string]bool, len(cfg.HardwareDevices))
	for _, hd := range cfg.HardwareDevices {
		if hd.Name != "" {
			hwDeviceNames[hd.Name] = true
		}
	}
	// All input IDs must be unique.
	seen := map[string]bool{}
	// Propagate global_options read_rate defaults to any input that has
	// no per-input override set. This lets `global_options.read_rate: 1.0`
	// pace every input without repeating the field on each input block.
	if cfg.GlobalOptions.ReadRate != 0 {
		for i := range cfg.Inputs {
			if cfg.Inputs[i].ReadRate == 0 {
				cfg.Inputs[i].ReadRate = cfg.GlobalOptions.ReadRate
			}
			if cfg.Inputs[i].ReadRateInitialBurst == 0 && cfg.GlobalOptions.ReadRateInitialBurst != 0 {
				cfg.Inputs[i].ReadRateInitialBurst = cfg.GlobalOptions.ReadRateInitialBurst
			}
			if cfg.Inputs[i].ReadRateCatchup == 0 && cfg.GlobalOptions.ReadRateCatchup != 0 {
				cfg.Inputs[i].ReadRateCatchup = cfg.GlobalOptions.ReadRateCatchup
			}
		}
	}
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
		switch inp.Kind {
		case "", "file", "lavfi", "raw", "concat":
		default:
			return fmt.Errorf("input %q: invalid kind %q (want \"file\", \"lavfi\", \"raw\" or \"concat\")", inp.ID, inp.Kind)
		}
		if err := validateInputDemuxerFields(inp); err != nil {
			return err
		}
		for j, s := range inp.Streams {
			if s.Type == "" {
				return fmt.Errorf("input %q streams[%d] missing type", inp.ID, j)
			}
			switch s.Type {
			case "video", "audio", "subtitle", "data", "attachment":
			default:
				return fmt.Errorf("input %q streams[%d]: invalid type %q (want video|audio|subtitle|data|attachment)", inp.ID, j, s.Type)
			}
			if !s.All && s.Track < 0 {
				return fmt.Errorf("input %q streams[%d]: negative track %d (use all=true for the no-index form)", inp.ID, j, s.Track)
			}
			if s.Program < 0 {
				return fmt.Errorf("input %q streams[%d]: invalid program %d (must be >= 0; 0 = unset)", inp.ID, j, s.Program)
			}
			if s.Negate && s.Optional {
				return fmt.Errorf("input %q streams[%d]: optional and negate are mutually exclusive (mirrors FFmpeg's `-map -0:s?` parse error)", inp.ID, j)
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
		// Validate HWAccel fields (Wave 10 #59).
		if inp.HWAccelDevice != "" && inp.HWAccel == "" {
			return fmt.Errorf("input %q: hwaccel_device requires hwaccel to be set", inp.ID)
		}
		if inp.HWAccelOutputFormat != "" && inp.HWAccel == "" {
			return fmt.Errorf("input %q: hwaccel_output_format requires hwaccel to be set", inp.ID)
		}
		if inp.HWAccelDevice != "" && !hwDeviceNames[inp.HWAccelDevice] {
			return fmt.Errorf("input %q: hwaccel_device %q does not match any hardware_devices entry", inp.ID, inp.HWAccelDevice)
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
		if out.MuxDelay < 0 {
			return fmt.Errorf("output %q: invalid muxdelay %g (must be >= 0)", out.ID, out.MuxDelay)
		}
		if out.MuxPreload < 0 {
			return fmt.Errorf("output %q: invalid muxpreload %g (must be >= 0)", out.ID, out.MuxPreload)
		}
		switch out.AvoidNegativeTS {
		case "", "auto", "disabled", "make_non_negative", "make_zero":
		default:
			return fmt.Errorf("output %q: invalid avoid_negative_ts %q (want auto|disabled|make_non_negative|make_zero)", out.ID, out.AvoidNegativeTS)
		}
		if out.DisableVideo && out.DisableAudio && out.DisableSubtitle && out.DisableData {
			return fmt.Errorf("output %q: vn/an/sn/dn all set — output would have no streams", out.ID)
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
		switch out.Kind {
		case "", "file":
			if len(out.Targets) > 0 {
				return fmt.Errorf("output %q: targets is only valid when kind=\"tee\" (have kind=%q)", out.ID, out.Kind)
			}
		case "tee":
			if len(out.Targets) == 0 {
				return fmt.Errorf("output %q: kind=\"tee\" requires at least one entry in targets", out.ID)
			}
			for j, t := range out.Targets {
				if t.URL == "" {
					return fmt.Errorf("output %q targets[%d]: missing url", out.ID, j)
				}
				switch t.OnFail {
				case "", "abort", "ignore":
				default:
					return fmt.Errorf("output %q targets[%d]: invalid onfail %q (want abort|ignore)", out.ID, j, t.OnFail)
				}
			}
		default:
			return fmt.Errorf("output %q: invalid kind %q (want \"\"|file|tee)", out.ID, out.Kind)
		}
		if out.Pass < 0 || out.Pass > 3 {
			return fmt.Errorf("output %q: invalid pass %d (want 0|1|2|3)", out.ID, out.Pass)
		}
		if out.Pass == 0 && out.PassLogFile != "" {
			return fmt.Errorf("output %q: passlogfile is only valid when pass != 0", out.ID)
		}
		if out.LoudnormPass < 0 || out.LoudnormPass > 2 {
			return fmt.Errorf("output %q: invalid loudnorm_pass %d (want 0|1|2)", out.ID, out.LoudnormPass)
		}
		if out.LoudnormPass == 0 && out.LoudnormStatsFile != "" {
			return fmt.Errorf("output %q: loudnorm_statsfile is only valid when loudnorm_pass != 0", out.ID)
		}
		if out.ForceKeyFrames != "" {
			if _, err := parseForceKeyFrames(out.ForceKeyFrames); err != nil {
				return fmt.Errorf("output %q: %w", out.ID, err)
			}
		}
		if out.HLS != nil {
			if out.Format != "" && out.Format != "hls" {
				return fmt.Errorf("output %q: hls options only valid when format=\"hls\" (have format=%q)", out.ID, out.Format)
			}
			if out.HLS.Time < 0 {
				return fmt.Errorf("output %q: invalid hls.time %g (must be >= 0)", out.ID, out.HLS.Time)
			}
			if out.HLS.InitTime < 0 {
				return fmt.Errorf("output %q: invalid hls.init_time %g (must be >= 0)", out.ID, out.HLS.InitTime)
			}
			if out.HLS.ListSize < 0 {
				return fmt.Errorf("output %q: invalid hls.list_size %d (must be >= 0)", out.ID, out.HLS.ListSize)
			}
			if out.HLS.StartNumber < 0 {
				return fmt.Errorf("output %q: invalid hls.start_number %d (must be >= 0)", out.ID, out.HLS.StartNumber)
			}
			switch out.HLS.PlaylistType {
			case "", "event", "vod":
			default:
				return fmt.Errorf("output %q: invalid hls.playlist_type %q (want event|vod)", out.ID, out.HLS.PlaylistType)
			}
			switch out.HLS.SegmentType {
			case "", "mpegts", "fmp4":
			default:
				return fmt.Errorf("output %q: invalid hls.segment_type %q (want mpegts|fmp4)", out.ID, out.HLS.SegmentType)
			}
			if out.HLS.VarStreamMap != "" && out.HLS.MasterPlName == "" {
				return fmt.Errorf("output %q: hls.var_stream_map requires hls.master_pl_name", out.ID)
			}
		}
		if out.DASH != nil {
			if out.Format != "" && out.Format != "dash" {
				return fmt.Errorf("output %q: dash options only valid when format=\"dash\" (have format=%q)", out.ID, out.Format)
			}
			if out.DASH.SegDuration < 0 {
				return fmt.Errorf("output %q: invalid dash.seg_duration %g (must be >= 0)", out.ID, out.DASH.SegDuration)
			}
			if out.DASH.FragDuration < 0 {
				return fmt.Errorf("output %q: invalid dash.frag_duration %g (must be >= 0)", out.ID, out.DASH.FragDuration)
			}
			if out.DASH.WindowSize < 0 {
				return fmt.Errorf("output %q: invalid dash.window_size %d (must be >= 0)", out.ID, out.DASH.WindowSize)
			}
			if out.DASH.ExtraWindowSize < 0 {
				return fmt.Errorf("output %q: invalid dash.extra_window_size %d (must be >= 0)", out.ID, out.DASH.ExtraWindowSize)
			}
		}
		if err := validateColorHDR(out); err != nil {
			return err
		}
		if err := validateAspect(out); err != nil {
			return err
		}
		if err := validateEncoderTiming(out); err != nil {
			return err
		}
		if err := validateAttachments(out); err != nil {
			return err
		}
		if err := validateCoverArt(out); err != nil {
			return err
		}
	}
	// At most one non-zero loudnorm_pass across the whole run — a
	// single job invocation maps to one shuttle pass (mirrors the
	// hand-rolled FFmpeg recipe: one ffmpeg run = pass 1 OR pass 2).
	{
		seen := 0
		for _, out := range cfg.Outputs {
			if out.LoudnormPass == 0 {
				continue
			}
			if seen != 0 && seen != out.LoudnormPass {
				return fmt.Errorf("conflicting loudnorm_pass values across outputs (got %d and %d) — a single run can carry only one pass", seen, out.LoudnormPass)
			}
			seen = out.LoudnormPass
		}
	}
	// Edge types must be valid.
	validTypes := map[string]bool{
		"video": true, "audio": true, "subtitle": true, "data": true,
		"attachment": true, "metadata": true, "events": true,
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
	// Wave 7 #42: reject filters whose libavfilter implementation is
	// not compiled into this build (e.g. zscale without --enable-libzimg).
	if err := validateFilterAvailability(cfg); err != nil {
		return err
	}
	// Wave 7 #36e: enforce security perimeter for `movie` / `amovie`
	// filter_source nodes (filename required + sanitised; well-formed
	// protocol_whitelist).
	if err := validateMovieFilterParams(cfg); err != nil {
		return err
	}
	// Wave 7 #37: enforce that filter nodes naming a known cross-media-type
	// filter (showwavespic, showspectrumpic, showvolume, ...) have outbound
	// edge types matching the filter's produced media type.
	if err := validateCrossMediaTypeFilters(cfg); err != nil {
		return err
	}
	// Validate metadata_reader / metadata_writer nodes (Wave 2 #11).
	// Reader requires params.source = input id; writer requires
	// params.target = output id. Optional params: section
	// ("global"|"chapters", default "global"). Reader+writer pairs
	// are connected by edges of type "metadata".
	inputIDs := make(map[string]bool, len(cfg.Inputs))
	for _, in := range cfg.Inputs {
		inputIDs[in.ID] = true
	}
	outputIDs := make(map[string]bool, len(cfg.Outputs))
	for _, out := range cfg.Outputs {
		outputIDs[out.ID] = true
	}
	for i, node := range cfg.Graph.Nodes {
		switch node.Type {
		case "metadata_reader":
			src := paramStringConfig(node.Params, "source")
			if src == "" {
				return fmt.Errorf("node[%d] %q: metadata_reader requires params.source (input id)", i, node.ID)
			}
			if !inputIDs[src] {
				return fmt.Errorf("node[%d] %q: metadata_reader params.source %q does not match any input", i, node.ID, src)
			}
			if sec := paramStringConfig(node.Params, "section"); sec != "" && sec != "global" && sec != "chapters" {
				return fmt.Errorf("node[%d] %q: metadata_reader params.section must be \"global\" or \"chapters\" (got %q)", i, node.ID, sec)
			}
		case "metadata_writer":
			tgt := paramStringConfig(node.Params, "target")
			if tgt == "" {
				return fmt.Errorf("node[%d] %q: metadata_writer requires params.target (output id)", i, node.ID)
			}
			if !outputIDs[tgt] {
				return fmt.Errorf("node[%d] %q: metadata_writer params.target %q does not match any output", i, node.ID, tgt)
			}
			if sec := paramStringConfig(node.Params, "section"); sec != "" && sec != "global" && sec != "chapters" {
				return fmt.Errorf("node[%d] %q: metadata_writer params.section must be \"global\" or \"chapters\" (got %q)", i, node.ID, sec)
			}
		}
	}
	// Validate assets.
	validKinds := map[string]bool{"font": true, "model": true, "lut": true, "other": true}
	for name, ref := range cfg.Assets {
		if name == "" {
			return fmt.Errorf("assets: empty key is not allowed")
		}
		if ref.Path == "" {
			return fmt.Errorf("assets[%q]: path must not be empty", name)
		}
		if !validKinds[ref.Kind] {
			return fmt.Errorf("assets[%q]: kind %q is not valid; must be \"font\", \"model\", \"lut\", or \"other\"", name, ref.Kind)
		}
	}
	// Validate hardware_devices (Wave 10 #56).
	hwDeviceNamesSeen := make(map[string]bool, len(cfg.HardwareDevices))
	for i, hd := range cfg.HardwareDevices {
		if hd.Name == "" {
			return fmt.Errorf("hardware_devices[%d]: name must not be empty", i)
		}
		if hwDeviceNamesSeen[hd.Name] {
			return fmt.Errorf("duplicate hardware_devices name %q", hd.Name)
		}
		hwDeviceNamesSeen[hd.Name] = true
		if hd.Type == "" {
			return fmt.Errorf("hardware_devices[%d] %q: type must not be empty", i, hd.Name)
		}
	}
	// Validate that NodeDef.Device references a declared hardware_device name.
	// Also validate AutoMapHW constraints (Wave 10 #58).
	for i, node := range cfg.Graph.Nodes {
		if node.Device != "" && !hwDeviceNames[node.Device] {
			return fmt.Errorf("node[%d] %q: device %q does not match any hardware_devices entry", i, node.ID, node.Device)
		}
		if node.AutoMapHW {
			if node.Type != "filter" {
				return fmt.Errorf("node[%d] %q: auto_map_hw is only valid on filter nodes (type is %q)", i, node.ID, node.Type)
			}
			if node.Device == "" {
				return fmt.Errorf("node[%d] %q: auto_map_hw requires device to be set", i, node.ID)
			}
			// Look up the device type to give an early hint about unsupported combos.
			var devType string
			for _, hd := range cfg.HardwareDevices {
				if hd.Name == node.Device {
					devType = strings.ToLower(hd.Type)
					break
				}
			}
			if devType != "" {
				alts := HWFilterAlts()
				if devAlts, ok := alts[node.Filter]; ok {
					if _, ok := devAlts[devType]; !ok {
						return fmt.Errorf("node[%d] %q: auto_map_hw has no hardware equivalent for filter %q on device type %q (supported device types: %s)",
							i, node.ID, node.Filter, devType, joinedKeys(devAlts))
					}
				}
				// If the filter itself is not in the table the pass is a no-op;
				// no error — the user may have set AutoMapHW speculatively on a
				// filter that never needed mapping, and that is harmless.
			}
		}
	}

	// Validate highest_quality_preset when realtime mode is on.
	// It must name a preset that exists on at least one known codec ladder.
	if cfg.GlobalOptions.Realtime && cfg.GlobalOptions.HighestQualityPreset != "" {
		found := false
		for _, codecName := range []string{"libx264", "libx265", "libsvtav1"} {
			ladder, ok := LadderFor(codecName)
			if !ok {
				continue
			}
			if ladder.IndexOf(cfg.GlobalOptions.HighestQualityPreset) >= 0 {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf(
				"global_options: highest_quality_preset %q is not a recognised preset on any known codec ladder",
				cfg.GlobalOptions.HighestQualityPreset,
			)
		}
	}

	return nil
}

// paramStringConfig is a local helper for validation; the runtime
// uses paramString in handlers.go (same shape).
func paramStringConfig(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// joinedKeys returns the sorted keys of m joined by ", " for error messages.
func joinedKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
