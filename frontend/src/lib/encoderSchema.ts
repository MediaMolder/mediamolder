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
 * (always visible) section of the form. The Inspector shows these four
 * roles up-front for every recognised encoder; everything else lives
 * under "Advanced". Encoders not listed here get a generic mapping
 * (preset = first string option named "preset"; bit_rate = "b";
 * keyframe_interval = "g"; rate_control = "rc" if present, else null).
 */
export interface EncoderUiRoles {
  preset?: string;
  rate_control?: string;
  bit_rate?: string;
  quality?: string; // crf-like
  keyframe_interval?: string;
}

export const ENCODER_UI_ROLES: Record<string, EncoderUiRoles> = {
  libx264:   { preset: 'preset', rate_control: 'rc', bit_rate: 'b', quality: 'crf',  keyframe_interval: 'g' },
  libx265:   { preset: 'preset', rate_control: 'rc', bit_rate: 'b', quality: 'crf',  keyframe_interval: 'g' },
  libsvtav1: { preset: 'preset', rate_control: 'rc', bit_rate: 'b', quality: 'crf',  keyframe_interval: 'g' },
  libvpx_vp9:{ preset: 'deadline', rate_control: 'rc', bit_rate: 'b', quality: 'crf', keyframe_interval: 'g' },
  libaom_av1:{ preset: 'cpu-used', rate_control: 'rc', bit_rate: 'b', quality: 'crf', keyframe_interval: 'g' },
  h264_nvenc:{ preset: 'preset', rate_control: 'rc', bit_rate: 'b', quality: 'cq',   keyframe_interval: 'g' },
  hevc_nvenc:{ preset: 'preset', rate_control: 'rc', bit_rate: 'b', quality: 'cq',   keyframe_interval: 'g' },
  h264_videotoolbox: { preset: undefined, rate_control: undefined, bit_rate: 'b', quality: 'q', keyframe_interval: 'g' },
  hevc_videotoolbox: { preset: undefined, rate_control: undefined, bit_rate: 'b', quality: 'q', keyframe_interval: 'g' },
  aac:       { bit_rate: 'b' },
  libfdk_aac:{ bit_rate: 'b', quality: 'vbr' },
  libopus:   { bit_rate: 'b', quality: 'vbr' },
  libmp3lame:{ bit_rate: 'b', quality: 'q' },
};

/** Resolve the UI roles for an encoder, falling back to generic guesses. */
export function rolesFor(name: string, options: EncoderOption[]): EncoderUiRoles {
  const explicit = ENCODER_UI_ROLES[name];
  if (explicit) return explicit;
  const has = (k: string) => options.some((o) => o.name === k);
  return {
    preset: has('preset') ? 'preset' : undefined,
    rate_control: has('rc') ? 'rc' : undefined,
    bit_rate: has('b') ? 'b' : undefined,
    quality: has('crf') ? 'crf' : has('q') ? 'q' : undefined,
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
