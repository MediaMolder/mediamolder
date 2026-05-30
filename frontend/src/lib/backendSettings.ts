// Remote backend configuration, persisted to localStorage.

export interface BackendSettings {
  /** Base URL of the remote server, e.g. "https://my-server:8443". */
  url: string;
  /** Bearer auth token. */
  token: string;
}

const STORAGE_KEY = 'mediamolder_backend';

/** Load saved backend settings, or null if none are configured. */
export function loadBackendSettings(): BackendSettings | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<BackendSettings>;
    if (typeof parsed.url === 'string' && parsed.url.length > 0) {
      return {
        url: parsed.url.replace(/\/$/, ''),
        token: typeof parsed.token === 'string' ? parsed.token : '',
      };
    }
    return null;
  } catch {
    return null;
  }
}

/** Persist backend settings. Pass null to clear. */
export function saveBackendSettings(s: BackendSettings | null): void {
  if (s === null) {
    localStorage.removeItem(STORAGE_KEY);
  } else {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(s));
  }
}
