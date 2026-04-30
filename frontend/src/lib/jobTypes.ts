// Mirror of pipeline/config.go types. Keep field names in sync.
// Only fields used by the editor are typed; unknown JSON fields are preserved
// via passthrough during round-trip in adapter.ts.

export type StreamType = 'video' | 'audio' | 'subtitle' | 'data' | 'metadata';

export interface StreamSelect {
  input_index: number;
  type: StreamType;
  track: number;
  all?: boolean;
  optional?: boolean;
  negate?: boolean;
  program?: number;
}

export interface Input {
  id: string;
  url: string;
  /** How to open the input. "file" (default) probes the URL with
   *  libavformat. "lavfi" routes through the lavfi virtual demuxer
   *  (FFmpeg's `-f lavfi`); the `url` field is then a filtergraph spec
   *  such as `anullsrc=r=48000:cl=stereo` or
   *  `color=black:s=1920x1080:r=30`. "raw" forces a rawvideo / PCM
   *  demuxer (requires `format` + geometry/audio params). "concat"
   *  opens an inline `concat_list` (or an existing listfile pointed
   *  to by `url`) via libavformat's concat demuxer. */
  kind?: 'file' | 'lavfi' | 'raw' | 'concat';
  /** Force libavformat demuxer name (mirrors ffmpeg `-f FMT`).
   *  Required when `kind="raw"`. Common values: rawvideo, s16le,
   *  image2, mpegts, concat. */
  format?: string;
  /** Frame rate for unframed/raw video and image2 sequences. Pushed
   *  to the demuxer as `framerate`. Mirrors ffmpeg `-framerate`. */
  framerate?: number;
  /** Planar layout of unframed/raw video frames (mirrors `-pix_fmt`).
   *  Required when `kind="raw"` and `format=rawvideo`. */
  pixel_format?: string;
  /** Frame size of unframed/raw video as WxH or a libavutil named
   *  preset (hd720/vga/ntsc/...). Mirrors `-video_size`/`-s`. */
  video_size?: string;
  /** Sample rate (Hz) of unframed/raw PCM audio (mirrors `-ar`).
   *  Required when `kind="raw"` + format names a PCM demuxer. */
  sample_rate?: number;
  /** Channel count of unframed/raw PCM audio (mirrors `-ac`).
   *  Required when `kind="raw"` + format names a PCM demuxer. */
  channels?: number;
  /** Optional libavutil sample format for audio raw demuxers that
   *  accept it (input-side `-sample_fmt`). */
  sample_fmt?: string;
  /** In-config concat playlist used when `kind="concat"`. */
  concat_list?: ConcatEntry[];
  /** Accurate (decode-and-discard until target PTS) vs fast
   *  (snap-to-keyframe) seeking when `-ss` is set. Default true. */
  accurate_seek?: boolean;
  /** When true, interpret `-ss` as an absolute container timestamp
   *  rather than an offset from start_time. */
  seek_timestamp?: boolean;
  /** Demuxer input packet queue depth in frames. */
  thread_queue_size?: number;
  /** Restrict which libavformat protocols this input may dereference. */
  protocol_whitelist?: string[];
  /** Image-sequence matcher used by image2 demuxer. */
  pattern_type?: '' | 'none' | 'sequence' | 'glob' | 'glob_sequence';
  subtitle_charenc?: string;
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

export interface ConcatEntry {
  file: string;
  duration?: number;
  inpoint?: number;
  outpoint?: number;
  metadata?: Record<string, string>;
}

export interface NodeDef {
  id: string;
  type: string; // "filter" | "encoder" | "source" | "sink" | "go_processor" | "metadata_reader" | "metadata_writer"
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
  bsf_subtitle?: string;
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
  /** Maximum demux-decode delay the muxer is allowed to buffer
   *  ahead, in seconds. Mirrors ffmpeg `-muxdelay` (per-output
   *  float; `fftools/ffmpeg_mux_init.c` L3447 — written as
   *  `oc->max_delay = muxdelay * AV_TIME_BASE`). FFmpeg default is
   *  0.7s; 0 leaves the muxer default unchanged. */
  muxdelay?: number;
  /** Initial demux-decode delay (pre-roll), in seconds. Mirrors
   *  ffmpeg `-muxpreload` (per-output float; emitted into the
   *  muxer AVDict as `preload = muxpreload * AV_TIME_BASE`). Most
   *  muxers ignore this; the historic consumer is `mpegenc.c`. */
  muxpreload?: number;
  /** Libavformat's automatic timestamp-shift policy at the muxer
   *  (`AVFormatContext.avoid_negative_ts`). Mirrors ffmpeg
   *  `-avoid_negative_ts`. Required for clean MP4/MOV writes when
   *  input PTS are negative (typical with `-ss` + `-copyts`). */
  avoid_negative_ts?: '' | 'auto' | 'disabled' | 'make_non_negative' | 'make_zero';
  /** Drop every video stream from this output's muxer. Mirrors ffmpeg
   *  `-vn` (OPT_OUTPUT). Filtered before implicit-encoder expansion. */
  vn?: boolean;
  /** Drop every audio stream from this output's muxer. Mirrors ffmpeg `-an`. */
  an?: boolean;
  /** Drop every subtitle stream from this output's muxer. Mirrors ffmpeg `-sn`. */
  sn?: boolean;
  /** Drop every data stream from this output's muxer. Mirrors ffmpeg `-dn`. */
  dn?: boolean;
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
  /** Typed HLS muxer options (only valid when `format === 'hls'`).
   *  Promoted from the generic `options` AVDict bag; on key
   *  collision the typed field wins. Mirrors libavformat/hlsenc.c. */
  hls?: HLSOptions;
  /** Typed DASH muxer options (only valid when `format === 'dash'`).
   *  Promoted from the generic `options` AVDict bag; on key
   *  collision the typed field wins. Mirrors libavformat/dashenc.c. */
  dash?: DASHOptions;
  /** Per-stream color metadata (range, primaries, transfer, matrix,
   *  chroma_location). Names are libavutil canonical
   *  (`av_color_*_name`). Empty fields are left unchanged on the
   *  output stream. Mirrors ffmpeg `-color_range` / `-color_primaries`
   *  / `-color_trc` / `-colorspace` / `-chroma_sample_location`. */
  color?: ColorMetadata;
  /** SMPTE ST 2086 mastering-display + CTA-861.3 content-light-level
   *  (HDR10) metadata attached to every video stream's
   *  codecpar.coded_side_data before WriteHeader. Requires an
   *  HDR-capable codec (hevc/av1/vp9 or copy) and container
   *  (mp4/mov/matroska/webm/mpegts). Mirrors ffmpeg
   *  `-mastering_display_metadata` / `-content_light_level`. */
  hdr?: HDRMetadata;
  /** Output sample aspect ratio (`setsar` shorthand / FFmpeg per-encoder
   *  SAR). Forms: `A:B`, `A/B`, or decimal. Mutually exclusive with
   *  `dar`. */
  sar?: string;
  /** Output display aspect ratio (`setdar` shorthand / FFmpeg `-aspect`).
   *  Forms: `A:B`, `A/B`, or decimal. Resolved to SAR using the
   *  encoder's width/height: SAR = (DAR_num*H)/(DAR_den*W). Mutually
   *  exclusive with `sar`. */
  dar?: string;
  options?: Record<string, unknown>;
}

export interface ColorMetadata {
  range?: string;
  primaries?: string;
  transfer?: string;
  space?: string;
  chroma_location?: string;
}

export interface HDRMetadata {
  mastering_display?: MasteringDisplayMetadata;
  content_light_level?: ContentLightLevelMetadata;
}

export interface MasteringDisplayMetadata {
  /** Chromaticity coords in 1/50000 units (HEVC/AV1 SEI encoding). */
  display_primaries_rx?: number;
  display_primaries_ry?: number;
  display_primaries_gx?: number;
  display_primaries_gy?: number;
  display_primaries_bx?: number;
  display_primaries_by?: number;
  white_point_x?: number;
  white_point_y?: number;
  /** Luminance in 1/10000 cd/m^2 (i.e. nits * 10000). */
  min_luminance?: number;
  max_luminance?: number;
}

export interface ContentLightLevelMetadata {
  /** Per-frame peak luminance over the whole stream (cd/m^2). */
  max_cll?: number;
  /** Per-frame frame-average maximum luminance (cd/m^2). */
  max_fall?: number;
}

export interface HLSOptions {
  /** Target segment duration, seconds (`hls_time`). */
  time?: number;
  /** Init segment duration, seconds (`hls_init_time`). */
  init_time?: number;
  /** Maximum entries kept in the playlist (`hls_list_size`); 0 = all. */
  list_size?: number;
  /** `hls_playlist_type`. `vod` writes EXT-X-ENDLIST on close. */
  playlist_type?: '' | 'event' | 'vod';
  /** `hls_segment_type`. `fmp4` selects CMAF-style fragmented MP4. */
  segment_type?: '' | 'mpegts' | 'fmp4';
  /** Printf-style template for segment files (`hls_segment_filename`). */
  segment_filename?: string;
  /** Init segment file name when `segment_type === 'fmp4'`
   *  (`hls_fmp4_init_filename`). */
  fmp4_init_filename?: string;
  /** First sequence number in the playlist (`start_number`). */
  start_number?: number;
  /** Master playlist filename (`master_pl_name`); required for ABR. */
  master_pl_name?: string;
  /** Variant-stream mapping (`var_stream_map`),
   *  e.g. `'v:0,a:0 v:1,a:0'`. Requires `master_pl_name`. */
  var_stream_map?: string;
  /** `hls_flags` token names; joined with `+` before being passed
   *  to libavformat. */
  flags?: string[];
}

export interface DASHOptions {
  /** Target segment duration, seconds (`seg_duration`). */
  seg_duration?: number;
  /** Target fragment duration, seconds (`frag_duration`). */
  frag_duration?: number;
  /** Maximum segments kept in the manifest (`window_size`); 0 = all. */
  window_size?: number;
  /** Extra segments retained on disk past `window_size`
   *  (`extra_window_size`). */
  extra_window_size?: number;
  /** Init segment file-name template (`init_seg_name`). */
  init_seg_name?: string;
  /** Media segment file-name template (`media_seg_name`). */
  media_seg_name?: string;
  /** SegmentBase single-file output (`single_file`). */
  single_file?: boolean;
  /** Emit `<SegmentTemplate>` (`use_template`); unset = libavformat
   *  default (true). */
  use_template?: boolean;
  /** Emit `<SegmentTimeline>` (`use_timeline`); unset = libavformat
   *  default (true). */
  use_timeline?: boolean;
  /** Low-latency progressive fragment writes (`streaming`). */
  streaming?: boolean;
  /** Manual adaptation-set spec (`adaptation_sets`),
   *  e.g. `'id=0,streams=v id=1,streams=a'`. */
  adaptation_sets?: string;
  /** Also emit HLS .m3u8 playlists alongside the DASH manifest
   *  (`hls_playlist`); the CMAF dual-pack mode. */
  hls_playlist?: boolean;
  /** Enable low-latency DASH (`ldash`). */
  ldash?: boolean;
  /** `dash_flags` token names; joined with `+` before being passed
   *  to libavformat. */
  flags?: string[];
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
  /** Modulates `copy_ts`. When true, re-enables the demuxer-side
   *  ts_offset shift even under `copy_ts` so the first kept packet
   *  still anchors at PTS 0 while later timing is preserved.
   *  Mirrors ffmpeg's global `-start_at_zero`
   *  (`fftools/ffmpeg_demux.c` L486). Requires `copy_ts=true`. */
  start_at_zero?: boolean;
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
