// Validation API client for the MediaMolder GUI.
// Exposes postValidate() for one-shot requests and useAutoValidate() for
// debounced automatic re-validation on every graph change.

import { useEffect, useRef, useState } from 'react';
import type { JobConfig, ValidationReport } from './jobTypes';

/**
 * POST the current config to /api/validate.
 *
 * @param cfg     The pipeline configuration to validate.
 * @param probe   When true, probe inputs (Phase B).  When false, static only.
 */
export async function postValidate(
  cfg: JobConfig,
  probe: boolean,
): Promise<ValidationReport> {
  const url = probe ? '/api/validate' : '/api/validate?no_probe=1';
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cfg),
  });
  if (!r.ok) {
    const text = await r.text().catch(() => `HTTP ${r.status}`);
    throw new Error(text || `HTTP ${r.status}`);
  }
  return r.json() as Promise<ValidationReport>;
}

/**
 * React hook that runs static (no-probe) validation automatically whenever
 * `config` changes, after a debounce delay (default 300 ms).
 *
 * Returns the most-recent ValidationReport (or null if validation has not yet
 * completed), plus an `isValidating` flag.
 */
export function useAutoValidate(
  getConfig: () => JobConfig | null,
  deps: unknown[],
  debounceMs = 300,
): { report: ValidationReport | null; isValidating: boolean } {
  const [report, setReport] = useState<ValidationReport | null>(null);
  const [isValidating, setIsValidating] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const runIdRef = useRef(0);

  useEffect(() => {
    if (timerRef.current !== null) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(async () => {
      const cfg = getConfig();
      if (!cfg) return;
      const runId = ++runIdRef.current;
      setIsValidating(true);
      try {
        const r = await postValidate(cfg, false);
        if (runIdRef.current === runId) {
          setReport(r);
        }
      } catch {
        // Silently ignore auto-validate failures (e.g. network unavailable).
      } finally {
        if (runIdRef.current === runId) {
          setIsValidating(false);
        }
      }
    }, debounceMs);
    return () => {
      if (timerRef.current !== null) clearTimeout(timerRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { report, isValidating };
}
