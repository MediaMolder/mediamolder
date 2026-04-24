// Singleton in-memory cache for the /api/nodes catalog. Used by the
// editor to resolve which media-type pins each node should expose when a
// graph is loaded from JSON (the JSON itself doesn't carry pin
// metadata — it's a property of the underlying libavfilter / encoder /
// processor and must be looked up).
//
// The Palette component fetches /api/nodes on mount; this module shares
// the same response so we don't issue a duplicate request.

import type { PaletteEntry } from './spawn';

let cachePromise: Promise<PaletteEntry[]> | null = null;

export function fetchCatalog(): Promise<PaletteEntry[]> {
  if (!cachePromise) {
    cachePromise = fetch('/api/nodes')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((list: PaletteEntry[] | null) => list ?? [])
      .catch((err) => {
        cachePromise = null; // allow retry on failure
        throw err;
      });
  }
  return cachePromise;
}

/** Build a map keyed by `${type}/${name}` → streams[] for fast lookup. */
export function indexStreams(catalog: PaletteEntry[]): Map<string, string[]> {
  const m = new Map<string, string[]>();
  for (const e of catalog) {
    if (!e.streams || e.streams.length === 0) continue;
    m.set(`${e.type}/${e.name}`, e.streams);
  }
  return m;
}
