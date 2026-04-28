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
  options?: Record<string, unknown>;
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
