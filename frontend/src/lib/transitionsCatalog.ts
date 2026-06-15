// Singleton in-memory cache for the /api/transitions catalog — the transition
// type names the sequence_editor accepts. The timeline editor's per-clip
// transition picker uses it so the dropdown can never offer a transition the
// backend would reject. Mirrors lib/nodeCatalog.ts.

let cachePromise: Promise<string[]> | null = null;

export function fetchTransitions(): Promise<string[]> {
  if (!cachePromise) {
    cachePromise = fetch('/api/transitions')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((list: string[] | null) => list ?? [])
      .catch((err) => {
        cachePromise = null; // allow retry on failure
        throw err;
      });
  }
  return cachePromise;
}

// Companion cache for /api/audio-transitions — the audio crossfade curve names
// (tri, qsin, …) the sequence_editor accepts. The timeline editor's per-clip
// audio-fade picker uses it.
let audioCachePromise: Promise<string[]> | null = null;

export function fetchAudioTransitions(): Promise<string[]> {
  if (!audioCachePromise) {
    audioCachePromise = fetch('/api/audio-transitions')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((list: string[] | null) => list ?? [])
      .catch((err) => {
        audioCachePromise = null; // allow retry on failure
        throw err;
      });
  }
  return audioCachePromise;
}
