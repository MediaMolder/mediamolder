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
  /** Explicit chapter table. Replaces any chapters mapped from inputs
   *  via `input.map_chapters`. The container must support chapters
   *  (matroska, mp4, ogg, ffmetadata, ...). */
  chapters?: Chapter[];
  options?: Record<string, unknown>;
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
