// useJobRun: starts a pipeline run via /api/run, subscribes to its SSE stream,
// surfaces state/metrics/errors/logs, and exposes a cancel function.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { JobConfig } from './jobTypes';

export type JobStatus = 'idle' | 'starting' | 'running' | 'succeeded' | 'failed' | 'canceled';

export interface NodeMetric {
  NodeID: string;
  Frames: number;
  Errors: number;
  Bytes: number;
  FPS: number;
  Elapsed: number;       // ns
  AvgLatency: number;    // ns
  MaxLatency: number;    // ns
}

export interface MetricsSnapshot {
  State: string;
  Elapsed: number;
  Nodes: NodeMetric[];
}

export interface NodeError {
  node_id: string;
  stage: string;
  error: string;
  time_ms: number;
}

export interface LogEntry {
  time_ms: number;
  message: string;
  level?: 'info' | 'warn' | 'error';
}

export interface RunState {
  jobId: string | null;
  status: JobStatus;
  pipelineState: string;     // last reported pipeline state ("Playing", etc.)
  metrics: MetricsSnapshot | null;
  errors: NodeError[];       // accumulated per-node errors
  logs: LogEntry[];          // bounded log history
  finalError: string | null;
  start: () => Promise<void>;
  cancel: () => Promise<void>;
  reset: () => void;
}

const LOG_CAP = 200;
const ERR_CAP = 50;

export function useJobRun(getConfig: () => JobConfig | null): RunState {
  const [jobId, setJobId] = useState<string | null>(null);
  const [status, setStatus] = useState<JobStatus>('idle');
  const [pipelineState, setPipelineState] = useState<string>('');
  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [errors, setErrors] = useState<NodeError[]>([]);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [finalError, setFinalError] = useState<string | null>(null);
  const esRef = useRef<EventSource | null>(null);

  const closeStream = useCallback(() => {
    esRef.current?.close();
    esRef.current = null;
  }, []);

  const reset = useCallback(() => {
    closeStream();
    setJobId(null);
    setStatus('idle');
    setPipelineState('');
    setMetrics(null);
    setErrors([]);
    setLogs([]);
    setFinalError(null);
  }, [closeStream]);

  const start = useCallback(async () => {
    const cfg = getConfig();
    if (!cfg) return;
    reset();
    setStatus('starting');

    let id: string;
    try {
      const res = await fetch('/api/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(cfg),
      });
      if (!res.ok) {
        const err = await safeError(res);
        setStatus('failed');
        setFinalError(err);
        return;
      }
      const body = (await res.json()) as { job_id: string };
      id = body.job_id;
    } catch (err) {
      setStatus('failed');
      setFinalError((err as Error).message);
      return;
    }

    setJobId(id);
    setStatus('running');

    const es = new EventSource(`/api/events/${id}`);
    esRef.current = es;

    es.addEventListener('state', (ev: MessageEvent) => {
      const data = parseEvent<{ from: string; to: string }>(ev);
      if (data?.to) setPipelineState(data.to);
    });
    es.addEventListener('metrics', (ev: MessageEvent) => {
      const snap = parseEvent<MetricsSnapshot>(ev);
      if (snap) setMetrics(snap);
    });
    es.addEventListener('error', (ev: MessageEvent) => {
      const data = parseEvent<{ node_id: string; stage: string; error: string; time_ms: number }>(ev);
      if (!data) return;
      setErrors((arr) => {
        const next = [...arr, data];
        return next.length > ERR_CAP ? next.slice(next.length - ERR_CAP) : next;
      });
      setLogs((l) => trimLog(l, { time_ms: data.time_ms, message: `[${data.node_id}] ${data.error}`, level: 'error' }));
    });
    es.addEventListener('log', (ev: MessageEvent) => {
      const data = parseEvent<{ message?: string; time_ms: number }>(ev);
      if (data?.message) setLogs((l) => trimLog(l, { time_ms: data.time_ms, message: data.message! }));
    });
    es.addEventListener('done', (ev: MessageEvent) => {
      const data = parseEvent<{ status: JobStatus; error: string }>(ev);
      if (data) {
        setStatus(data.status);
        if (data.error) setFinalError(data.error);
      }
      closeStream();
    });

    es.onerror = () => {
      // Browser will auto-retry; if the job is finished the server closes the
      // stream cleanly which surfaces here too. Keep status unchanged.
    };
  }, [getConfig, reset, closeStream]);

  const cancel = useCallback(async () => {
    if (!jobId) return;
    try {
      await fetch(`/api/cancel/${jobId}`, { method: 'POST' });
    } catch {
      /* ignore */
    }
  }, [jobId]);

  // Cleanup on unmount.
  useEffect(() => () => closeStream(), [closeStream]);

  return {
    jobId,
    status,
    pipelineState,
    metrics,
    errors,
    logs,
    finalError,
    start,
    cancel,
    reset,
  };
}

function parseEvent<T>(ev: MessageEvent): T | null {
  try {
    const wrapped = JSON.parse(ev.data) as { type: string; time_ms: number; data: T };
    // The server wraps payloads in {type, time_ms, data}; surface the inner data
    // for typed listeners but also propagate time_ms by merging when relevant.
    if (wrapped && typeof wrapped === 'object' && 'data' in wrapped) {
      const merged = wrapped.data as unknown as Record<string, unknown>;
      if (merged && typeof merged === 'object' && !('time_ms' in merged)) {
        merged.time_ms = wrapped.time_ms;
      }
      return wrapped.data;
    }
    return null;
  } catch {
    return null;
  }
}

function trimLog(arr: LogEntry[], next: LogEntry): LogEntry[] {
  const out = [...arr, next];
  return out.length > LOG_CAP ? out.slice(out.length - LOG_CAP) : out;
}

async function safeError(res: Response): Promise<string> {
  try {
    const body = await res.json();
    if (body && typeof body === 'object' && 'error' in body) return String((body as { error: string }).error);
  } catch {
    /* fall through */
  }
  return `${res.status} ${res.statusText}`;
}
