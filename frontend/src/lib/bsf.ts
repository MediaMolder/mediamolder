/* Bitstream-filter chain parser / serialiser.
 *
 * Mirrors libavcodec's `av_bsf_list_parse_str` syntax:
 *
 *     f1[=k=v[:k=v]*][,f2[=k=v[:k=v]*]]*
 *
 * Examples:
 *   "h264_mp4toannexb"
 *   "h264_metadata=video_full_range_flag=1"
 *   "h264_mp4toannexb,h264_metadata=video_full_range_flag=1:level=4"
 *
 * The chain is comma-separated. Each entry is `name` optionally
 * followed by `=k=v:k=v:…`. We do not currently support quoted /
 * escaped values — the underlying av_bsf parser doesn't either
 * (libavcodec/bsf.c::av_bsf_list_parse_str splits on bare `,` `=`
 * `:`). */

export interface BSFEntry {
  name: string;
  params: Record<string, string>;
}

export function parseBSFChain(spec: string): BSFEntry[] {
  const trimmed = spec.trim();
  if (!trimmed) return [];
  return trimmed.split(',').map((part) => parseEntry(part.trim()));
}

function parseEntry(s: string): BSFEntry {
  if (!s) return { name: '', params: {} };
  const eq = s.indexOf('=');
  if (eq < 0) return { name: s, params: {} };
  const name = s.slice(0, eq);
  const rest = s.slice(eq + 1);
  const params: Record<string, string> = {};
  if (rest) {
    for (const kv of rest.split(':')) {
      const i = kv.indexOf('=');
      if (i < 0) {
        // Bare token — keep as a flag with empty value so the user
        // can edit it. Round-trips back as `name=token=`.
        if (kv) params[kv] = '';
      } else {
        params[kv.slice(0, i)] = kv.slice(i + 1);
      }
    }
  }
  return { name, params };
}

export function serializeBSFChain(entries: BSFEntry[]): string {
  return entries
    .filter((e) => e.name.trim() !== '')
    .map((e) => {
      const kvs = Object.entries(e.params)
        .filter(([k]) => k.trim() !== '')
        .map(([k, v]) => `${k}=${v}`)
        .join(':');
      return kvs ? `${e.name}=${kvs}` : e.name;
    })
    .join(',');
}
