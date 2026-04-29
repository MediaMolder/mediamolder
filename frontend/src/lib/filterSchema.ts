// Types and fetcher for the GET /api/filters/{name}/options endpoint.
// Mirrors `av.FilterOptionsInfo` in `av/filter_options.go`.
//
// Filter options share the AVOption representation with encoder options,
// so we re-export the EncoderOption{,Enum,Val} types under filter-flavoured
// names to keep call-sites readable.

import type {
  EncoderOption,
  EncoderOptionEnum,
  EncoderOptionVal,
  EncoderOptionType,
} from './encoderSchema';

export type FilterOptionType = EncoderOptionType;
export type FilterOptionEnum = EncoderOptionEnum;
export type FilterOptionVal = EncoderOptionVal;
export type FilterOption = EncoderOption;

export interface FilterOptionsInfo {
  name: string;
  description?: string;
  options: FilterOption[];
}

const cache = new Map<string, Promise<FilterOptionsInfo>>();

/** Fetch (and cache) the option schema for a given filter name. */
export function fetchFilterInfo(name: string): Promise<FilterOptionsInfo> {
  const hit = cache.get(name);
  if (hit) return hit;
  const p = fetch(`/api/filters/${encodeURIComponent(name)}/options`).then(async (r) => {
    if (!r.ok) {
      const body = await r.text();
      throw new Error(body || `HTTP ${r.status}`);
    }
    return (await r.json()) as FilterOptionsInfo;
  });
  cache.set(name, p);
  // Drop the cached promise on failure so the user can retry.
  p.catch(() => cache.delete(name));
  return p;
}
