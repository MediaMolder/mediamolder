// Naming-mode helper used across the palette, the inspector, and the
// graph node renderer (MMNode). The user toggles between two display
// modes:
//
//   - 'friendly' — show the curated friendly label when the backend
//     emitted one (e.g. "x264" instead of "libx264", "Resize" instead
//     of "scale"). Falls back to the raw label/name when there is no
//     curated alias.
//   - 'library' — always show the canonical libavcodec / libavfilter
//     name. This is the historical behaviour and is preferred by
//     power users who already think in FFmpeg terms.
//
// The mode persists in localStorage under `mm.palette.naming` so it
// survives reloads. Components subscribe to changes via the
// `useNamingMode` hook, which listens for a custom
// `mm.palette.naming.changed` event dispatched by the Palette toggle.
import { useEffect, useState } from 'react';

export type NamingMode = 'friendly' | 'library';

export const NAMING_STORAGE_KEY = 'mm.palette.naming';
export const NAMING_EVENT = 'mm.palette.naming.changed';

export interface NameableEntry {
  name: string;
  label?: string;
  friendly_name?: string;
}

export function readNamingMode(): NamingMode {
  if (typeof window === 'undefined') return 'friendly';
  const v = window.localStorage.getItem(NAMING_STORAGE_KEY);
  return v === 'library' ? 'library' : 'friendly';
}

export function writeNamingMode(mode: NamingMode): void {
  if (typeof window === 'undefined') return;
  window.localStorage.setItem(NAMING_STORAGE_KEY, mode);
  window.dispatchEvent(new CustomEvent(NAMING_EVENT, { detail: mode }));
}

/**
 * Module-level lookup table populated by the Palette as it fetches
 * `/api/nodes`. Consulted by `nodeDisplayLabel` (and friends) so
 * graphs loaded from JSON can show curated friendly labels even
 * though the JobConfig itself only carries canonical libav names.
 */
const friendlyByName = new Map<string, string>();

export function registerFriendlyNames(entries: Array<{ name: string; friendly_name?: string }>): void {
  for (const e of entries) {
    if (e.friendly_name) friendlyByName.set(e.name, e.friendly_name);
  }
}

export function lookupFriendlyName(name: string | undefined): string | undefined {
  if (!name) return undefined;
  return friendlyByName.get(name);
}

// Static display labels for common hardware-accelerated filter names.
// Falls back to lookupFriendlyName (palette registry) and then to a
// simple pretty-print (drop the vendor suffix, title-case the rest).
const FILTER_LABEL: Record<string, string> = {
  scale_cuda:            'Scale (CUDA)',
  scale_vaapi:           'Scale (VAAPI)',
  scale_qsv:             'Scale (QSV)',
  scale_videotoolbox:    'Scale (VideoToolbox)',
  scale_amf:             'Scale (AMF)',
  yadif_cuda:            'Deinterlace (CUDA)',
  yadif_vaapi:           'Deinterlace (VAAPI)',
  overlay_cuda:          'Overlay (CUDA)',
  overlay_vaapi:         'Overlay (VAAPI)',
  overlay_qsv:           'Overlay (QSV)',
  overlay_amf:           'Overlay (AMF)',
  tonemap_vaapi:         'Tone Map (VAAPI)',
  tonemap_opencl:        'Tone Map (OpenCL)',
  deinterlace_vaapi:     'Deinterlace (VAAPI)',
  denoise_vaapi:         'Denoise (VAAPI)',
  sharpen_vaapi:         'Sharpen (VAAPI)',
  procamp_vaapi:         'ProAmp (VAAPI)',
  hwupload_cuda:         'HW Upload (CUDA)',
  hwdownload:            'HW Download',
  hwupload:              'HW Upload',
  hwmap:                 'HW Map',
  transpose_vaapi:       'Transpose (VAAPI)',
  transpose_cuda:        'Transpose (CUDA)',
  transpose_qsv:         'Transpose (QSV)',
  rotate_vaapi:          'Rotate (VAAPI)',
  vpp_qsv:               'Video Post-Processing (QSV)',
  vpp_amf:               'Video Post-Processing (AMF)',
};

/**
 * Return a human-friendly label for a hardware-accelerated filter name.
 * Checks the static label table first, then the palette registry, and
 * finally falls back to a prettified version of the raw name.
 */
export function friendlyFilterName(name: string): string {
  const stat = FILTER_LABEL[name];
  if (stat) return stat;
  const reg = lookupFriendlyName(name);
  if (reg) return reg;
  // Pretty-print: replace underscores, capitalise first word.
  const words = name.replace(/_/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * Return the human-friendly heading for an entry given the active
 * naming mode. In 'library' mode (or when no friendly alias exists)
 * this falls back to entry.label and finally entry.name so callers
 * never get an empty string.
 */
export function displayName(e: NameableEntry, mode: NamingMode): string {
  if (mode === 'friendly' && e.friendly_name) return e.friendly_name;
  return e.label || e.name;
}

/**
 * React hook that subscribes to naming-mode changes. Returns the
 * current mode, re-rendering the calling component whenever the
 * Palette toggle (or any other writer of `writeNamingMode`) updates
 * the value.
 */
export function useNamingMode(): NamingMode {
  const [mode, setMode] = useState<NamingMode>(readNamingMode);
  useEffect(() => {
    const onChange = (ev: Event) => {
      const detail = (ev as CustomEvent<NamingMode>).detail;
      setMode(detail === 'library' ? 'library' : 'friendly');
    };
    const onStorage = (ev: StorageEvent) => {
      if (ev.key === NAMING_STORAGE_KEY) setMode(readNamingMode());
    };
    window.addEventListener(NAMING_EVENT, onChange);
    window.addEventListener('storage', onStorage);
    return () => {
      window.removeEventListener(NAMING_EVENT, onChange);
      window.removeEventListener('storage', onStorage);
    };
  }, []);
  return mode;
}
