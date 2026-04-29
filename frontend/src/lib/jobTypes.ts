// Mirror of pipeline/config.go types. Keep field names in sync.
// Only fields used by the editor are typed; unknown JSON fields are preserved
// via passthrough during round-trip in adapter.ts.

export type StreamType = 'video' | 'audio' | 'subtitle' | 'data';

export interface StreamSelect {
  input_index: number;
  type: StreamType;
  track: number;
}

export interface Input {
  id: string;
  url: string;
  /** How to open the input. "file" (default) probes the URL with
   *  libavformat. "lavfi" routes through the lavfi virtual demuxer
   *  (FFmpeg's `-f lavfi`); the `url` field is then a filtergraph spec
   *  such as `anullsrc=r=48000:cl=stereo` or
   *  `color=black:s=1920x1080:r=30`. */
  kind?: "file" | "lavfi";
  /** Copy this input's container metadata onto outputs that don't set
   *  their own `metadata` (mirrors ffmpeg `-map_metadata IDX`).
   *  Multiple inputs merge in declaration order; last writer wins. */
  map_metadata?: boolean;
  /** Copy this input's chapter table onto outputs that don't set their
   *  own `chapters` (mirrors ffmpeg `-map_chapters IDX`). First input
   *  with map_chapters=true wins. */
  map_chapters?: boolean;
  /** Number of additional times the demuxer rewinds and replays after
   *  EOF. 0 = no loop, N>0 = play N+1 times total, -1 = infinite.
   *  Mirrors ffmpeg `-stream_loop N`. On rewind the runtime captures
   *  the previous iteration's `(max_pts - min_pts)` and adds it to
   *  every subsequent packet so PTS stay monotone. */
  stream_loop?: number;
  /** Per-input timestamp offset in seconds (may be negative). Mirrors
   *  ffmpeg `-itsoffset T`. Positive delays the input on the global
   *  timeline; negative advances it. Composes additively with the
   *  implicit `-ss` ts_offset. */
  itsoffset?: number;
  /** Pace packet reads to (read_rate × realtime). 0 = unpaced;
   *  1.0 mirrors ffmpeg `-re`. Required for live-restream and any
   *  HLS/DASH push that relies on segment walltime equalling
   *  media time. */
  read_rate?: number;
  /** Seconds of media time read unpaced at the start of the input.
   *  Mirrors ffmpeg `-readrate_initial_burst SECS`; defaults to 0.5
   *  when read_rate is non-zero. */
  read_rate_initial_burst?: number;
  /** Multiplier used to recover from a pacing lag (mirrors ffmpeg
   *  `-readrate_catchup`); must be >= read_rate when set. Defaults
   *  to read_rate × 1.05 when unset and read_rate is non-zero. */
  read_rate_catchup?: number;
  streams: StreamSelect[];
  options?: Record<string, unknown>;
}

export interface NodeDef {
  id: string;
  type: string; // "filter" | "encoder" | "source" | "sink" | "go_processor"
  filter?: string;
  processor?: string;
  params?: Record<string, unknown>;
  error_policy?: ErrorPolicy;
}

export interface EdgeDef {
  from: string; // "nodeID:port" or "inputID:v:0"
  to: string;
  type: StreamType;
}

export interface GraphDef {
  nodes: NodeDef[];
  edges: EdgeDef[];
  ui?: GraphUI;
}

export interface GraphUI {
  positions?: Record<string, UIPosition>;
}

export interface UIPosition {
  x: number;
  y: number;
}

export interface Output {
  id: string;
  url: string;
  format?: string;
  codec_video?: string;
  codec_audio?: string;
  codec_subtitle?: string;
  bsf_video?: string;
  bsf_audio?: string;
  /** FourCC overrides for the muxer's per-stream codec_tag. Equivalent to
   *  ffmpeg's -tag:v / -tag:a / -tag:s. Must be exactly 4 ASCII chars when
   *  set. Most commonly used to force HEVC in MP4 to "hvc1" for
   *  QuickTime / Safari / iOS compatibility. */
  codec_tag_video?: string;
  codec_tag_audio?: string;
  codec_tag_subtitle?: string;
  /** Codec-specific options (preset, crf, b, g, ...) attached to the
   *  implicit encoder synthesised by `materializeImplicitEncoders`.
   *  Populated by the backend when parsing FFmpeg command lines via
   *  `compat/ffcli` so that flags like `-crf 22 -preset slow` survive
   *  into the GUI graph as visible encoder params. Ignored when an
   *  explicit encoder node is wired upstream of the matching output
   *  stream. */
  encoder_params_video?: Record<string, unknown>;
  encoder_params_audio?: Record<string, unknown>;
  encoder_params_subtitle?: Record<string, unknown>;
  /** Cap on the number of muxed video / audio packets written to this
   *  output. 0 (or omitted) = unlimited. Mirrors ffmpeg -frames:v /
   *  -frames:a (also -vframes / -aframes); required for extract-frame,
   *  tile-thumbnails and scene-image jobs. */
  max_frames_video?: number;
  max_frames_audio?: number;
  /** Per-output video frame-rate enforcement. Mirrors ffmpeg `-fps_mode`
   *  (and the legacy `-vsync` alias rewritten by compat/ffcli).
   *  - `"passthrough"` / omitted: pass frames through unchanged.
   *  - `"vfr"`: drop frames whose PTS is <= the previously emitted PTS.
   *  - `"cfr"`: renumber PTS at constant 1/framerate intervals, duplicating
   *    into forward gaps and dropping frames that arrive too soon. The
   *    single biggest cure for HLS/DASH player A/V drift.
   *  - `"drop"`: like `vfr` but also drops near-duplicates within half a
   *    frame duration of the previous emission. */
  fps_mode?: "" | "passthrough" | "vfr" | "cfr" | "drop";
  /** Per-output audio resync compensation. Mirrors the legacy ffmpeg
   *  `-async N` flag (removed from the FFmpeg 8.0 CLI in favour of
   *  `-af aresample=async=N`).
   *  - `0` / omitted: no compensation.
   *  - `1`: pad/trim the start so the first sample lands at PTS 0
   *    (rendered as `aresample=async=1:first_pts=0`).
   *  - `N>1`: continuous soft compensation up to `N` samples/sec
   *    (rendered as `aresample=async=N`).
   *  Injected as an aresample filter node in front of the audio
   *  encoder; pure stream-copy outputs are unaffected. */
  audio_sync?: number;
  /** Stop muxing as soon as the shortest input stream feeding this
   *  output ends. Mirrors ffmpeg `-shortest` (per-output scope; see
   *  `fftools/ffmpeg_mux_init.c` sync-queue setup). The runtime
   *  records the PTS at which the first stream closes and stops
   *  emitting on every other stream of the same output. Required for
   *  the `add a music track to a silent clip` / `watermark loop on a
   *  finite source` patterns. */
  shortest?: boolean;
  /** Cap the encoded output at this many bytes. Mirrors ffmpeg `-fs
   *  SIZE`: before each `WritePacket` the runtime queries
   *  `avio_tell` on the muxer's IO context and stops with EOF
   *  (writing a clean trailer) once the limit is reached. 0 =
   *  unlimited. */
  max_file_size?: number;
  /** Container-level metadata key/value pairs (`-metadata key=value`).
   *  Replaces any metadata mapped from inputs via `input.map_metadata`. */
  metadata?: Record<string, string>;
  /** Per-stream metadata + disposition overrides. Mirrors ffmpeg
   *  `-metadata:s:<type>:<idx> key=value` and
   *  `-disposition:s:<type>:<idx> flags`. Each entry addresses one
   *  output stream by media type and 0-based index within that type,
   *  counting in the order streams were added to the muxer (same
   *  convention as FFmpeg's `check_stream_specifier` for
   *  `s:<type>:<idx>`). Per-stream codec/bitrate is intentionally
   *  not exposed here — model it with explicit encoder graph nodes
   *  (see testdata/examples/35_abr_ladder.json). */
  streams?: StreamSpec[];
  /** Explicit chapter table. Replaces any chapters mapped from inputs
   *  via `input.map_chapters`. The container must support chapters
   *  (matroska, mp4, ogg, ffmetadata, ...). */
  chapters?: Chapter[];
  /** Output discriminator. `""` / `"file"` open a single muxer at
   *  `url`; `"tee"` switches to libavformat's built-in tee muxer to
   *  fan one encoded stream out to N targets (`url` / `format` are
   *  ignored when `kind === "tee"`). */
  kind?: '' | 'file' | 'tee';
  /** Slaves for a `kind === "tee"` output. Required in that case;
   *  must be empty otherwise. */
  targets?: TeeTarget[];
  /** Two-pass video encoding bit-field. 0 = single-pass; 1 = analysis
   *  pass (AV_CODEC_FLAG_PASS1); 2 = final pass (AV_CODEC_FLAG_PASS2);
   *  3 = both. Run the job twice (pass=1 then pass=2) against the same
   *  passlogfile prefix. Mirrors FFmpeg `-pass N`. */
  pass?: 0 | 1 | 2 | 3;
  /** Per-stream statistics file prefix for two-pass video encoding.
   *  Rendered as `<prefix>-<stream-idx>.log`. Empty defaults to
   *  `ffmpeg2pass`. Honoured only when `pass !== 0`. Mirrors FFmpeg
   *  `-passlogfile`. */
  passlogfile?: string;
  /** Two-pass EBU R128 loudnorm shuttle. 0 = single-pass; 1 = analysis
   *  (libavfilter writes input_i / input_tp / input_lra / input_thresh
   *  / target_offset to a JSON stats file); 2 = apply (the runtime
   *  reads pass-1 stats and injects measured_I / measured_TP /
   *  measured_LRA / measured_thresh / offset into the same loudnorm
   *  node). Run the job twice (loudnorm_pass=1 then 2) against the
   *  same loudnorm_statsfile prefix. FFmpeg has no flag for this. */
  loudnorm_pass?: 0 | 1 | 2;
  /** Prefix for the per-loudnorm-node JSON stats file rendered as
   *  `<prefix>-<idx>.json`. Empty defaults to `mm-loudnorm`. Honoured
   *  only when `loudnorm_pass !== 0`. */
  loudnorm_statsfile?: string;
  /** FFmpeg `-force_key_frames` spec. Three grammars:
   *  - `expr:EXPR` (libavutil expression per video frame; vars n,
   *    n_forced, prev_forced_n, prev_forced_t, t — canonical idiom
   *    `expr:gte(t,n_forced*2)` for a 2 s GOP),
   *  - `source` (copy keyframes from source),
   *  - comma-separated time list (`3.0,7.5,10.25`).
   *  Required for HLS / DASH segmenters. Honoured on video encoders. */
  force_key_frames?: string;
  options?: Record<string, unknown>;
}

export interface TeeTarget {
  /** Slave URL (file path or scheme). */
  url: string;
  /** Force the slave's container (`f=`). */
  format?: string;
  /** Comma-separated FFmpeg stream specifiers (`v`, `a:0`, ...). */
  select?: string;
  /** Per-slave bitstream-filter chain. */
  bsfs?: string;
  /** Slave-failure policy. */
  onfail?: '' | 'abort' | 'ignore';
  /** Wrap slave in libavformat's `fifo` muxer (extra buffering thread). */
  use_fifo?: boolean;
  /** `;`-separated `key=value` forwarded to the fifo muxer when
   *  `use_fifo` is set. */
  fifo_options?: string;
  /** Free-form additional `[opt=val]` pairs for obscure tee-slave
   *  AVOptions. */
  options?: Record<string, unknown>;
}

export interface StreamSpec {
  /** Media type letter: v=video, a=audio, s=subtitle, d=data. */
  type: 'v' | 'a' | 's' | 'd';
  /** 0-based index within the media type. */
  index: number;
  /** Per-stream key/value metadata (e.g. `language=eng`). */
  metadata?: Record<string, string>;
  /** `+`-separated AV_DISPOSITION_* flag names (e.g.
   *  `default+forced`). Empty leaves the disposition untouched. */
  disposition?: string;
}

export interface Chapter {
  id?: number;
  /** Chapter start in seconds. */
  start: number;
  /** Chapter end in seconds. */
  end: number;
  title?: string;
  metadata?: Record<string, string>;
}

export interface Options {
  threads?: number;
  thread_type?: string;
  hw_accel?: string;
  hw_device?: string;
  realtime?: boolean;
}

export interface ErrorPolicy {
  policy: string;
  max_retries?: number;
  fallback_node?: string;
}

export interface JobConfig {
  schema_version: string;
  description?: string;
  inputs: Input[];
  graph: GraphDef;
  outputs: Output[];
  global_options?: Options;
  /** Preserve original demuxer timestamps end-to-end instead of
   *  rebasing every input to PTS 0. Mirrors ffmpeg's global
   *  `-copyts`: suppresses the demuxer-side ts_offset shift that
   *  normally accompanies `-ss`, and changes the meaning of any
   *  output-side `-ss` / `-to` (in `output.options`) to absolute
   *  timeline values rather than offsets from the input's start.
   *  Required for accurate broadcast / HLS PTS handling. */
  copy_ts?: boolean;
}

/**
 * One stream's worth of probed metadata returned by `POST /api/probe`. The
 * field names match the canonical attribute keys consumed by the edge
 * attribute inference (see `frontend/src/lib/streamAttrs.ts`).
 */
export interface ProbedStream {
  index: number;
  type: StreamType | string;
  codec?: string;
  codec_tag?: string;
  profile?: string;
  level?: number;
  bit_rate?: number;
  bit_depth?: number;
  bits_per_coded_sample?: number;
  bits_per_raw_sample?: number;
  width?: number;
  height?: number;
  pix_fmt?: string;
  frame_rate?: string;
  r_frame_rate?: string;
  sar?: string;
  field_order?: string;
  color_space?: string;
  color_range?: string;
  color_primaries?: string;
  color_transfer?: string;
  sample_rate?: number;
  sample_fmt?: string;
  channels?: number;
  channel_layout?: string;
  duration_sec?: number;
  start_sec?: number;
  time_base_num?: number;
  time_base_den?: number;
}

export interface ProbeResponse {
  url: string;
  streams: ProbedStream[];
}
