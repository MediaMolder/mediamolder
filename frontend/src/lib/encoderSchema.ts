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
}

export const ENCODER_UI_ROLES: Record<string, EncoderUiRoles> = {
  libx264:   { preset: 'preset', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g' },
  libx265:   { preset: 'preset', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g' },
  libsvtav1: { preset: 'preset', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g' },
  libvpx_vp9:{ preset: 'deadline', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g' },
  libaom_av1:{ preset: 'cpu-used', bit_rate: 'b', crf: 'crf', qp: 'qp', keyframe_interval: 'g' },
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

const cache = new Map<string, Promise<EncoderInfo>>();

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
  // On failure, drop the cached promise so the user can retry.
  p.catch(() => cache.delete(name));
  return p;
}
