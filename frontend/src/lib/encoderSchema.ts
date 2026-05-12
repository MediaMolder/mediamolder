// Types and fetcher for the GET /api/encoders/{name}/options endpoint.
// Mirrors `av.EncoderInfo` in `av/options.go`.

export type EncoderOptionType =
  | 'flags'
  | 'int'
  | 'int64'
  | 'uint64'
  | 'bool'
  | 'float'
  | 'double'
  | 'string'
  | 'rational'
  | 'binary'
  | 'dict'
  | 'duration'
  | 'color'
  | 'channel_layout'
  | 'pix_fmt'
  | 'sample_fmt'
  | 'image_size'
  | 'unknown';

export interface EncoderOptionEnum {
  name: string;
  help?: string;
  value: number;
}

export interface EncoderOptionVal {
  int?: number;
  float?: number;
  string?: string;
  num_den?: [number, number];
}

export interface EncoderOption {
  name: string;
  help?: string;
  type: EncoderOptionType;
  unit?: string;
  min?: number;
  max?: number;
  default?: EncoderOptionVal;
  constants?: EncoderOptionEnum[];
  is_private: boolean;

  // AVOption.flags bitmask + decoded bits (Wave 5 #19). Mirrors
  // av.EncoderOption in av/options.go.
  flags?: number;
  is_encoding_param?: boolean;
  is_decoding_param?: boolean;
  is_audio_param?: boolean;
  is_video_param?: boolean;
  is_subtitle_param?: boolean;
  is_export?: boolean;
  is_read_only?: boolean;
  is_bsf_param?: boolean;
  is_runtime_param?: boolean;
  is_filtering_param?: boolean;
  is_deprecated?: boolean;
  is_child_consts?: boolean;

  // Curated by the gui layer (not by libavutil) for (filter, option)
  // pairs whose value is parsed by the libavutil expression
  // evaluator. When `expression` is true the GUI renders the
  // syntax-highlighted ExpressionInput; `variables` lists the
  // identifiers the expression may reference.
  expression?: boolean;
  variables?: string[];

  // Longer prose explanation injected from private_local/nodes.csv by
  // the GUI handler (applyEncoderExtendedHelp). Shown behind a "?"
  // button in the Inspector so the compact help line stays visible.
  extended_help?: string;
}

export interface EncoderInfo {
  name: string;
  long_name?: string;
  media_type: string;
  options: EncoderOption[];
}

/** Per-encoder mapping that promotes specific options to the "common"
 * (always visible) section of the form. The Inspector uses these roles
 * to render the four primary controls (preset, rate-control,
 * keyframe interval) and to drive the rate-control mode switch
 * (Bit rate / CRF / QP). Encoders not listed here get a generic
 * mapping based on conventional libav option names.
 *
 * `rc_enum` (and the rc_* constants) describe encoders whose rate
 * control is selected via an AVOption enum (notably the *_nvenc family
 * with its `rc` option). For libx264/libx265/libsvtav1 the mode is
 * implied by which of `b` / `crf` / `qp` is set, so rc_enum is
 * unset. CBR for those encoders is expressed by setting `maxrate` and
 * `minrate` equal to `b` (handled in the form).
 */
export interface EncoderUiRoles {
  preset?: string;
  bit_rate?: string;          // typically "b"
  crf?: string;               // CRF-style constant rate factor (libx264/x265: "crf"; nvenc: "cq")
  qp?: string;                // constant quantizer (libx264/x265: "qp"; nvenc: "qp")
  keyframe_interval?: string; // typically "g"

  // Optional enum-driven rate-control selector (e.g. nvenc's `rc`).
  rc_enum?: string;
  rc_vbr?: string;
  rc_cbr?: string;
  rc_crf?: string;            // enum constant that means "use CRF/CQ"
  rc_qp?: string;             // enum constant that means "use constant QP"
  /** Default RC mode shown when no params are set. Defaults to 'bitrate'
   *  when omitted for backward compat with generic encoders, but should be
   *  set explicitly for any encoder whose library default is CRF/QP. */
  default_rc?: 'bitrate' | 'crf' | 'qp';
}

export const ENCODER_UI_ROLES: Record<string, EncoderUiRoles> = {
  libx264:   { preset: 'preset', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g', default_rc: 'crf' },
  libx265:   { preset: 'preset', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g', default_rc: 'crf' },
  libsvtav1: { preset: 'preset', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g', default_rc: 'crf' },
  libvpx_vp9:{ preset: 'deadline', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g', default_rc: 'crf' },
  libaom_av1:{ preset: 'cpu-used', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g', default_rc: 'crf' },
  h264_nvenc:{
    preset: 'preset', bit_rate: 'b', crf: 'cq', qp: 'qp', keyframe_interval: 'g',
    rc_enum: 'rc', rc_vbr: 'vbr', rc_cbr: 'cbr', rc_crf: 'vbr', rc_qp: 'constqp',
  },
  hevc_nvenc:{
    preset: 'preset', bit_rate: 'b', crf: 'cq', qp: 'qp', keyframe_interval: 'g',
    rc_enum: 'rc', rc_vbr: 'vbr', rc_cbr: 'cbr', rc_crf: 'vbr', rc_qp: 'constqp',
  },
  h264_videotoolbox: { bit_rate: 'b', qp: 'q', keyframe_interval: 'g' },
  hevc_videotoolbox: { bit_rate: 'b', qp: 'q', keyframe_interval: 'g' },
  aac:       { bit_rate: 'b' },
  libfdk_aac:{ bit_rate: 'b' },
  libopus:   { bit_rate: 'b' },
  libmp3lame:{ bit_rate: 'b', qp: 'q' },
};

/** Resolve the UI roles for an encoder, falling back to generic guesses. */
export function rolesFor(name: string, options: EncoderOption[]): EncoderUiRoles {
  const explicit = ENCODER_UI_ROLES[name];
  if (explicit) return explicit;
  const has = (k: string) => options.some((o) => o.name === k);
  return {
    preset: has('preset') ? 'preset' : undefined,
    bit_rate: has('b') ? 'b' : undefined,
    crf: has('crf') ? 'crf' : has('cq') ? 'cq' : undefined,
    qp: has('qp') ? 'qp' : has('q') ? 'q' : undefined,
    keyframe_interval: has('g') ? 'g' : undefined,
  };
}

/** Find a single option by name. */
export function findOption(options: EncoderOption[], name: string | undefined): EncoderOption | undefined {
  if (!name) return undefined;
  return options.find((o) => o.name === name);
}

/* -------------------------------------------------------------------------- *
 * Sentinel handling for primary AVOptions.
 *
 * Several libavcodec encoders expose `crf` / `qp` (and other primary
 * controls) as "AVOption defaults you should not actually use": the option's
 * default_val and min are both -1, and max is FLT_MAX. For example libx265:
 *
 *   { "crf", ..., AV_OPT_TYPE_FLOAT, { .dbl = -1 }, -1, FLT_MAX, VE }
 *
 * The encoder treats -1 as "unset → fall back to my built-in default" (28
 * for x265, 23 for x264, ...). The metadata is correct in libav but a
 * dreadful UI, so we override display values per encoder + option.
 *
 * The runtime is unaffected — the user typing nothing means nothing is sent
 * and libavcodec applies its real default. These overrides are *only* shown
 * as placeholder text and as the input's HTML5 min/max bounds.
 * -------------------------------------------------------------------------- */

export interface PrimaryOverride {
  /** Placeholder shown when libav reports the sentinel default (-1). */
  default?: string;
  /** Sensible numeric range for the HTML5 input + range hint. */
  min?: number;
  max?: number;
}

const ENCODER_PRIMARY_OVERRIDES: Record<string, Record<string, PrimaryOverride>> = {
  libx264:    { crf: { default: '23', min: 0, max: 51 }, qp: { default: '23', min: 0, max: 69 } },
  libx265:    { crf: { default: '28', min: 0, max: 51 }, qp: { default: '28', min: 0, max: 51 } },
  libsvtav1:  { crf: { default: '35', min: 1, max: 63 }, qp: { default: '50', min: 1, max: 63 } },
  libvpx_vp9: { crf: { default: '32', min: 0, max: 63 }, qp: { default: '32', min: 0, max: 63 } },
  libaom_av1: { crf: { default: '23', min: 0, max: 63 }, qp: { default: '23', min: 0, max: 63 } },
  // *_nvenc: cq default is 0 ("disabled"); leave alone — not a sentinel.
};

/** True when the AVOption looks like an unset sentinel (-1 / -1 / FLT_MAX). */
export function isSentinelOption(opt: EncoderOption): boolean {
  const d = opt.default;
  const sentinelDefault = !!d && (d.int === -1 || d.float === -1);
  const sentinelMin = opt.min === -1;
  const sentinelMax = typeof opt.max === 'number' && Math.abs(opt.max) > 1e9;
  // We require BOTH the default and at least one of the range bounds to be
  // a sentinel before suppressing them, so that genuine -1 defaults (rare
  // but valid) on bounded ranges still display normally.
  return sentinelDefault && (sentinelMin || sentinelMax);
}

/**
 * Compute the displayable default, HTML5 input min/max, and rangeHint for
 * a primary encoder option. Falls back to the AVOption's own metadata when
 * no override is registered and the values are sane.
 */
export function primaryMeta(
  codec: string,
  opt: EncoderOption,
): { default: string; min?: number; max?: number; rangeHint: string } {
  const override = ENCODER_PRIMARY_OVERRIDES[codec]?.[opt.name];
  const sane = (n: number | undefined): boolean =>
    typeof n === 'number' && Number.isFinite(n) && Math.abs(n) < 1e9;
  const sentinel = isSentinelOption(opt);

  let def = '';
  if (opt.default) {
    if (opt.default.string !== undefined) def = opt.default.string;
    else if (opt.default.int !== undefined) def = String(opt.default.int);
    else if (opt.default.float !== undefined) def = String(opt.default.float);
    else if (opt.default.num_den) def = `${opt.default.num_den[0]}/${opt.default.num_den[1]}`;
  }
  if (override?.default !== undefined && (sentinel || def === '' || def === '-1' || def === '-1.0')) {
    def = override.default;
  } else if (sentinel) {
    def = '';
  }

  const minVal = override?.min ?? (sane(opt.min) && opt.min !== -1 ? opt.min : undefined);
  const maxVal = override?.max ?? (sane(opt.max) ? opt.max : undefined);

  let rangeHint = '';
  if (minVal !== undefined && maxVal !== undefined) rangeHint = ` · ${minVal}–${maxVal}`;
  else if (maxVal !== undefined) rangeHint = ` · ≤ ${maxVal}`;
  else if (minVal !== undefined) rangeHint = ` · ≥ ${minVal}`;

  return { default: def, min: minVal, max: maxVal, rangeHint };
}

/* -------------------------------------------------------------------------- *
 * String-typed AVOption choice tables.
 *
 * libavcodec exposes some encoder options (notably libx264/libx265 `preset`,
 * and libvpx-vp9 `deadline`) as AV_OPT_TYPE_STRING with no AVOption
 * constants attached, so isEnum() can't surface them as a dropdown. The
 * accepted values are nevertheless a fixed enum baked into the codec. List
 * them here so the GUI can render a <select> instead of a free-form input.
 *
 * Entries with `constants` already on the AVOption (e.g. nvenc presets,
 * cpu-used integers) are NOT listed — OptionControl already enums them.
 * -------------------------------------------------------------------------- */

export interface OptionChoiceList {
  /** Built-in default written by the codec when no value is supplied. */
  default?: string;
  /** Ordered choices to render in the dropdown. */
  choices: { value: string; label?: string }[];
}

const X264_X265_PRESETS: OptionChoiceList = {
  default: 'medium',
  choices: [
    { value: 'ultrafast' },
    { value: 'superfast' },
    { value: 'veryfast' },
    { value: 'faster' },
    { value: 'fast' },
    { value: 'medium' },
    { value: 'slow' },
    { value: 'slower' },
    { value: 'veryslow' },
    { value: 'placebo' },
  ],
};

/** H.264 (x264) profile choices. Overrides all conflicting settings when set.
 *  Profiles narrow the allowed feature set; choose the least restrictive
 *  profile your target decoder supports. */
const X264_PROFILES: OptionChoiceList = {
  default: 'main',
  choices: [
    { value: 'baseline', label: 'Baseline — no B-frames, no CABAC, progressive only' },
    { value: 'main',     label: 'Main — no 8×8 DCT, no lossless' },
    { value: 'high',     label: 'High — no lossless' },
    { value: 'high10',   label: 'High 10 — 8–10-bit depth, no lossless' },
    { value: 'high422',  label: 'High 4:2:2 — 8–10-bit, 4:2:0 or 4:2:2, no lossless' },
    { value: 'high444',  label: 'High 4:4:4 — 8–10-bit, 4:2:0 / 4:2:2 / 4:4:4' },
  ],
};

/** H.265 (x265) profile choices.
 *  Note: only applied when encoding 8-bit frames (libavcodec limitation). */
const X265_PROFILES: OptionChoiceList = {
  default: 'main',
  choices: [
    { value: 'main',             label: 'Main — 8-bit, 4:2:0' },
    { value: 'main10',           label: 'Main 10 — 8–10-bit, 4:2:0' },
    { value: 'mainstillpicture', label: 'Main Still Picture — single still image' },
  ],
};

/** H.264 (x264) level choices. Values are the integer encoding FFmpeg uses
 *  internally (10 = Level 1, 31 = Level 3.1, …). */
const H264_LEVELS: OptionChoiceList = {
  choices: [
    { value: '10', label: '1'   },
    { value: '11', label: '1.1' },
    { value: '12', label: '1.2' },
    { value: '13', label: '1.3' },
    { value: '20', label: '2'   },
    { value: '21', label: '2.1' },
    { value: '22', label: '2.2' },
    { value: '30', label: '3'   },
    { value: '31', label: '3.1' },
    { value: '32', label: '3.2' },
    { value: '40', label: '4'   },
    { value: '41', label: '4.1' },
    { value: '42', label: '4.2' },
    { value: '50', label: '5'   },
    { value: '51', label: '5.1' },
    { value: '52', label: '5.2' },
    { value: '60', label: '6'   },
    { value: '61', label: '6.1' },
    { value: '62', label: '6.2' },
  ],
};

/** H.265 (x265) level choices. Uses the same integer-encoding convention. */
const H265_LEVELS: OptionChoiceList = {
  choices: [
    { value: '10', label: '1'   },
    { value: '20', label: '2'   },
    { value: '21', label: '2.1' },
    { value: '30', label: '3'   },
    { value: '31', label: '3.1' },
    { value: '40', label: '4'   },
    { value: '41', label: '4.1' },
    { value: '50', label: '5'   },
    { value: '51', label: '5.1' },
    { value: '52', label: '5.2' },
    { value: '60', label: '6'   },
    { value: '61', label: '6.1' },
    { value: '62', label: '6.2' },
  ],
};

export const ENCODER_OPTION_CHOICES: Record<string, Record<string, OptionChoiceList>> = {
  libx264: { preset: X264_X265_PRESETS, profile: X264_PROFILES, level: H264_LEVELS },
  libx265: { preset: X264_X265_PRESETS, profile: X265_PROFILES, level: H265_LEVELS },
  libvpx_vp9: {
    deadline: {
      default: 'good',
      choices: [
        { value: 'best',     label: 'best (slowest)' },
        { value: 'good',     label: 'good (default)' },
        { value: 'realtime', label: 'realtime (fastest)' },
      ],
    },
  },
};

/** Return the choice list registered for `(codec, optionName)`, if any. */
export function optionChoices(codec: string, optionName: string): OptionChoiceList | undefined {
  return ENCODER_OPTION_CHOICES[codec]?.[optionName];
}

/* -------------------------------------------------------------------------- *
 * Preset / tune effective-value tables.
 *
 * libavcodec sets all preset-dependent options (refs, bf, sc_threshold,
 * b_strategy, rc-lookahead, aq-mode, aq-strength, mbtree …) to -1 as a
 * sentinel meaning "let the codec library decide after applying the preset".
 * These tables record the actual value each option receives, derived directly
 * from the encoder C source:
 *   x264  —  common/base.c  x264_param_default  +  param_apply_preset/tune
 *   x265  —  source/common/param.cpp  x265_param_default  +  equivalent fns
 *
 * Only options that are exposed as named AVOptions by libavcodec are listed;
 * x265 parameters that can only be reached via x265-params are omitted.
 * -------------------------------------------------------------------------- */
type PresetRow    = Record<string, string>;
type PresetMatrix = Record<string, PresetRow>;

export const CODEC_PRESET_EFFECTIVE_VALUES: Record<string, PresetMatrix> = {
  // x264 —— source: common/base.c
  // Columns: refs  bf  sc_threshold  b_strategy  rc-lookahead  aq-mode  aq-strength  mbtree
  // (b_strategy: 0=none, 1=fast, 2=trellis)  (aq-mode: 0=off, 1=variance, 2=autovariance)
  libx264: {
    ultrafast: { refs: '1',  bf: '0',  sc_threshold: '0',  b_strategy: '0', 'rc-lookahead': '0',  'aq-mode': '0', 'aq-strength': '0.0', mbtree: '0' },
    superfast: { refs: '1',  bf: '3',  sc_threshold: '40', b_strategy: '1', 'rc-lookahead': '0',  'aq-mode': '1', 'aq-strength': '1.0', mbtree: '0' },
    veryfast:  { refs: '1',  bf: '3',  sc_threshold: '40', b_strategy: '1', 'rc-lookahead': '10', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
    faster:    { refs: '2',  bf: '3',  sc_threshold: '40', b_strategy: '1', 'rc-lookahead': '20', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
    fast:      { refs: '2',  bf: '3',  sc_threshold: '40', b_strategy: '1', 'rc-lookahead': '30', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
    medium:    { refs: '3',  bf: '3',  sc_threshold: '40', b_strategy: '1', 'rc-lookahead': '40', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
    slow:      { refs: '5',  bf: '3',  sc_threshold: '40', b_strategy: '1', 'rc-lookahead': '50', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
    slower:    { refs: '8',  bf: '3',  sc_threshold: '40', b_strategy: '2', 'rc-lookahead': '60', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
    veryslow:  { refs: '16', bf: '8',  sc_threshold: '40', b_strategy: '2', 'rc-lookahead': '60', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
    placebo:   { refs: '16', bf: '16', sc_threshold: '40', b_strategy: '2', 'rc-lookahead': '60', 'aq-mode': '1', 'aq-strength': '1.0', mbtree: '1' },
  },
  // x265 —— source: source/common/param.cpp
  // Only refs (maxNumReferences) and bf (bframes) are exposed as generic
  // lavc AVOptions that libavcodec wires through to x265.  All other preset-
  // controlled parameters require x265-params.
  libx265: {
    ultrafast: { refs: '1', bf: '3' },
    superfast: { refs: '1', bf: '3' },
    veryfast:  { refs: '2', bf: '4' },
    faster:    { refs: '2', bf: '4' },
    fast:      { refs: '3', bf: '4' },
    medium:    { refs: '3', bf: '4' },
    slow:      { refs: '4', bf: '4' },
    slower:    { refs: '5', bf: '8' },
    veryslow:  { refs: '5', bf: '8' },
    placebo:   { refs: '5', bf: '8' },
  },
};

/** Codec-wide effective defaults for options whose AVOption `default` is a
 *  sentinel (-1 or 12 from the generic AVCodecContext `g` option) but whose
 *  actual encoder-library default is a well-defined, source-confirmed value
 *  that does not vary by preset.  Checked as a last resort in
 *  `effectivePresetDefault` when no preset-specific row matches.
 *
 *  Sources:
 *   libx264 / libx265 — x264_param_default() / x265_param_default() in
 *     common/base.c and source/common/param.cpp set i_keyint_max /
 *     keyframeMax = 250; the wrapper FFCodecDefault { "g", "-1" } prevents
 *     the generic AVOption default (12) from reaching the encoder.
 *   libvpx-vp9 / libaom-av1 — the wrappers have FFCodecDefault { "g", "-1" };
 *     with gop_size=-1 the condition `gop_size >= 0` in vpx_init / av1_init is
 *     false, so kf_max_dist is left at the library default of 9999 (no forced
 *     periodic keyframes — scene-cut-based only). */
export const CODEC_STATIC_DEFAULTS: Record<string, PresetRow> = {
  libx264:       { g: '250' },
  libx265:       { g: '250' },
  'libvpx-vp9':  { g: '9999' },
  'libaom-av1':  { g: '9999' },
};

/** Per-tune overrides applied on top of the preset value.
 *  Only includes deterministic overrides where the result is a fixed value
 *  regardless of the preset (e.g. zerolatency forces bf=0).  Tunes that
 *  multiply an existing value (e.g. x264 animation doubles refs) are omitted
 *  because the result depends on the preset in a non-trivial way. */
export const CODEC_TUNE_EFFECTIVE_VALUES: Record<string, Record<string, PresetRow>> = {
  libx264: {
    zerolatency: { bf: '0', 'rc-lookahead': '0', mbtree: '0' },
    grain:       { 'aq-strength': '0.5' },
    psnr:        { 'aq-mode': '0' },
    ssim:        { 'aq-mode': '2' },
  },
  libx265: {
    zerolatency: { bf: '0' },
    psnr:        { 'aq-strength': '0.0' },
  },
};

/** Returns the effective placeholder value an encoder will apply for
 *  `optionName` given the current `preset` and `tune`, or `undefined` when
 *  the codec/option combination is not in the table. */
export function effectivePresetDefault(
  codec: string,
  optionName: string,
  preset: string,
  tune?: string,
): string | undefined {
  const matrix = CODEC_PRESET_EFFECTIVE_VALUES[codec];
  // Fall back to 'medium' row when the preset name is absent (e.g. empty).
  const row = matrix?.[preset] ?? matrix?.['medium'];
  let val = row?.[optionName] ?? CODEC_STATIC_DEFAULTS[codec]?.[optionName];
  if (tune) {
    const tuneOverride = CODEC_TUNE_EFFECTIVE_VALUES[codec]?.[tune]?.[optionName];
    if (tuneOverride !== undefined) val = tuneOverride;
  }
  return val;
}

const cache = new Map<string, Promise<EncoderInfo>>();
// Resolved values stored separately for synchronous access.
const resolvedCache = new Map<string, EncoderInfo>();

/** Synchronously return the cached EncoderInfo if it has already resolved, or undefined. */
export function getEncoderInfoSync(name: string): EncoderInfo | undefined {
  return resolvedCache.get(name);
}

/** Fetch (and cache) the encoder option schema for a given encoder name. */
export function fetchEncoderInfo(name: string): Promise<EncoderInfo> {
  const hit = cache.get(name);
  if (hit) return hit;
  const p = fetch(`/api/encoders/${encodeURIComponent(name)}/options`).then(async (r) => {
    if (!r.ok) {
      const body = await r.text();
      throw new Error(body || `HTTP ${r.status}`);
    }
    return (await r.json()) as EncoderInfo;
  });
  cache.set(name, p);
  p.then((info) => resolvedCache.set(name, info));
  // On failure, drop the cached promise so the user can retry.
  p.catch(() => cache.delete(name));
  return p;
}
