// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

import { useEffect, useMemo, useState } from 'react';
import { fetchTransitions } from '../lib/transitionsCatalog';

/* A wide, buffered table editor for the sequence_editor's timeline. Rows map
 * 1:1 to a track's clips[]; columns map to the JSON fields. Edits are made on a
 * local working copy; Apply commits it through onApply, Cancel discards.
 *
 * Phase 1 edits the first track only (multi-track tabs are a later phase); any
 * additional tracks are preserved untouched on Apply. */

type Json = Record<string, unknown>;

interface Transition extends Json {
  type?: string;
  duration?: number;
}
interface Clip extends Json {
  media_id?: string;
  input_id?: string;
  url?: string;
  source_in?: number;
  source_out?: number;
  timeline_in?: number;
  transition?: Transition;
}
interface Track extends Json {
  id?: string;
  type?: string;
  clips?: Clip[];
}

interface Props {
  params: Record<string, unknown>;
  inputIds: string[];
  onApply: (next: Record<string, unknown>) => void;
  onCancel: () => void;
}

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v ?? null)) as T;
const round3 = (n: number) => Math.round(n * 1000) / 1000;
const numStr = (v: unknown): string => (typeof v === 'number' && Number.isFinite(v) ? String(v) : '');
const clipSpan = (c: Clip): number | undefined =>
  typeof c.source_in === 'number' && typeof c.source_out === 'number'
    ? c.source_out - c.source_in
    : undefined;

interface Issue {
  row: number;
  severity: 'error' | 'warn';
  msg: string;
}

function validate(clips: Clip[], inputIds: string[], names: string[]): Issue[] {
  const out: Issue[] = [];
  const known = new Set(names);
  clips.forEach((c, i) => {
    const row = i + 1;
    const media = c.media_id || c.input_id || c.url;
    if (!media) {
      out.push({ row, severity: 'error', msg: `clip ${row}: no media selected` });
    } else if (c.media_id && inputIds.length > 0 && !inputIds.includes(c.media_id)) {
      out.push({ row, severity: 'error', msg: `clip ${row}: media "${c.media_id}" is not a job input` });
    }
    const sp = clipSpan(c);
    if (sp !== undefined && sp <= 0) {
      out.push({ row, severity: 'error', msg: `clip ${row}: source_out must be greater than source_in` });
    }
    const t = c.transition;
    if (t && t.type) {
      if (names.length > 0 && !known.has(t.type)) {
        out.push({ row, severity: 'error', msg: `clip ${row}: unknown transition "${t.type}"` });
      }
      if (typeof t.duration !== 'number' || t.duration <= 0) {
        out.push({ row, severity: 'warn', msg: `clip ${row}: transition has no positive duration` });
      } else if (sp !== undefined && t.duration > sp) {
        out.push({ row, severity: 'warn', msg: `clip ${row}: transition ${t.duration}s is longer than the clip span ${round3(sp)}s` });
      }
    }
  });
  for (let i = 1; i < clips.length; i++) {
    const prev = clips[i - 1].timeline_in ?? 0;
    const cur = clips[i].timeline_in ?? 0;
    if (cur < prev) {
      out.push({ row: i + 1, severity: 'warn', msg: `clip ${i + 1}: timeline_in moves backwards` });
    }
  }
  return out;
}

export function TimelineEditorDialog({ params, inputIds, onApply, onCancel }: Props) {
  const [tracks, setTracks] = useState<Track[]>(() => {
    const t = Array.isArray(params.tracks) ? clone(params.tracks as Track[]) : [];
    return t.length > 0 ? t : [{ id: 'V1', type: 'video', clips: [] }];
  });
  const [names, setNames] = useState<string[]>([]);
  const trackIdx = 0; // Phase 1: first track

  useEffect(() => {
    let alive = true;
    fetchTransitions()
      .then((n) => { if (alive) setNames(n); })
      .catch(() => { /* leave dropdown with raw values */ });
    return () => { alive = false; };
  }, []);

  const track = tracks[trackIdx] ?? {};
  const clips: Clip[] = Array.isArray(track.clips) ? track.clips : [];

  const setClips = (next: Clip[]) => {
    const nt = clone(tracks);
    nt[trackIdx] = { ...nt[trackIdx], clips: next };
    setTracks(nt);
  };
  const patchClip = (i: number, patch: (c: Clip) => Clip) => {
    const next = clips.slice();
    next[i] = patch({ ...next[i] });
    setClips(next);
  };

  const setMedia = (i: number, id: string) => patchClip(i, (c) => {
    delete c.input_id;
    delete c.url;
    c.media_id = id;
    return c;
  });
  const setUrl = (i: number, url: string) => patchClip(i, (c) => {
    delete c.input_id;
    delete c.media_id;
    c.url = url;
    return c;
  });
  const setNum = (i: number, key: 'source_in' | 'source_out' | 'timeline_in', raw: string) =>
    patchClip(i, (c) => {
      if (raw === '') delete c[key];
      else c[key] = Number(raw);
      return c;
    });
  const setTransType = (i: number, type: string) => patchClip(i, (c) => {
    if (!type) delete c.transition;
    else c.transition = { ...(c.transition ?? {}), type, duration: c.transition?.duration ?? 0.5 };
    return c;
  });
  const setTransDur = (i: number, raw: string) => patchClip(i, (c) => {
    if (c.transition) c.transition = { ...c.transition, duration: raw === '' ? undefined : Number(raw) };
    return c;
  });

  const addClip = () => {
    const last = clips[clips.length - 1];
    const c: Clip = inputIds.length > 0
      ? { media_id: inputIds[0], source_in: 0, source_out: 5, timeline_in: 0 }
      : { url: '', source_in: 0, source_out: 5, timeline_in: 0 };
    if (last) {
      const sp = clipSpan(last) ?? 0;
      const dur = last.transition?.duration ?? 0;
      c.timeline_in = round3((last.timeline_in ?? 0) + sp - dur);
    }
    setClips([...clips, c]);
  };
  const duplicateClip = (i: number) => {
    const next = clips.slice();
    next.splice(i + 1, 0, clone(clips[i]));
    setClips(next);
  };
  const removeClip = (i: number) => setClips(clips.filter((_, j) => j !== i));

  // Recompute every timeline_in back-to-back, accounting for transition overlap
  // (source_out = source_in + span + transition.duration; the next clip starts
  // transition.duration before the previous one's content ends).
  const chainTimeline = () => {
    const next = clips.slice();
    let t = next[0]?.timeline_in ?? 0;
    for (let i = 0; i < next.length; i++) {
      next[i] = { ...next[i], timeline_in: round3(t) };
      t += (clipSpan(next[i]) ?? 0) - (next[i].transition?.duration ?? 0);
    }
    setClips(next);
  };

  const issues = useMemo(() => validate(clips, inputIds, names), [clips, inputIds, names]);
  const hasErrors = issues.some((x) => x.severity === 'error');
  const totalDur = useMemo(
    () => clips.reduce((m, c) => Math.max(m, (c.timeline_in ?? 0) + (clipSpan(c) ?? 0)), 0),
    [clips],
  );

  const fmt = (params.format ?? {}) as Json;
  const fmtSummary = [
    fmt.width && fmt.height ? `${fmt.width}×${fmt.height}` : null,
    typeof fmt.frame_rate === 'number' ? `${fmt.frame_rate} fps` : null,
    typeof fmt.pix_fmt === 'string' ? fmt.pix_fmt : null,
  ].filter(Boolean).join(' · ');

  const apply = () => onApply({ ...params, tracks });

  const cell: React.CSSProperties = { padding: '3px 6px', borderBottom: '1px solid var(--border)' };
  const numInput: React.CSSProperties = { width: 84 };

  return (
    <div className="dialog-overlay" onClick={onCancel}>
      <div
        className="dialog"
        style={{ width: 'min(1080px, 95vw)', maxHeight: '88vh', display: 'flex', flexDirection: 'column' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="dialog-header">
          <h3>
            Sequence Timeline
            <span style={{ fontWeight: 400, color: 'var(--text-dim)', marginLeft: 8, fontSize: 12 }}>
              {clips.length} clip{clips.length === 1 ? '' : 's'} · {round3(totalDur)} s{fmtSummary ? ` · ${fmtSummary}` : ''}
            </span>
          </h3>
          <button onClick={onCancel}>×</button>
        </div>

        {tracks.length > 1 && (
          <div style={{ padding: '6px 16px 0', fontSize: 11, color: 'var(--text-dim)' }}>
            Editing track <strong>{track.id ?? `#${trackIdx + 1}`}</strong>. This job has {tracks.length} tracks;
            the others are preserved unchanged (multi-track editing is coming next).
          </div>
        )}

        <div style={{ overflow: 'auto', padding: '8px 16px', flex: 1 }}>
          <table style={{ borderCollapse: 'collapse', width: '100%', fontSize: 12 }}>
            <thead>
              <tr style={{ textAlign: 'left', color: 'var(--text-dim)' }}>
                <th style={cell}>#</th>
                <th style={cell}>Media</th>
                <th style={cell}>Src In</th>
                <th style={cell}>Src Out</th>
                <th style={cell}>Span</th>
                <th style={cell}>Timeline In</th>
                <th style={cell}>Transition →</th>
                <th style={cell}>Dur</th>
                <th style={cell}></th>
              </tr>
            </thead>
            <tbody>
              {clips.length === 0 && (
                <tr>
                  <td style={{ ...cell, color: 'var(--text-dim)' }} colSpan={9}>No clips yet — add one below.</td>
                </tr>
              )}
              {clips.map((c, i) => {
                const sp = clipSpan(c);
                const media = c.media_id ?? c.input_id ?? '';
                return (
                  <tr key={i}>
                    <td style={{ ...cell, color: 'var(--text-dim)' }}>{i + 1}</td>
                    <td style={cell}>
                      {inputIds.length > 0 ? (
                        <select value={media} onChange={(e) => setMedia(i, e.target.value)} style={{ width: 130 }}>
                          {media && !inputIds.includes(media) && <option value={media}>{media} (missing)</option>}
                          {inputIds.map((id) => <option key={id} value={id}>{id}</option>)}
                        </select>
                      ) : (
                        <input
                          type="text"
                          placeholder="/path or url"
                          value={typeof c.url === 'string' ? c.url : ''}
                          onChange={(e) => setUrl(i, e.target.value)}
                          style={{ width: 180 }}
                        />
                      )}
                    </td>
                    <td style={cell}>
                      <input type="number" step="0.001" style={numInput} value={numStr(c.source_in)} onChange={(e) => setNum(i, 'source_in', e.target.value)} />
                    </td>
                    <td style={cell}>
                      <input type="number" step="0.001" style={numInput} value={numStr(c.source_out)} onChange={(e) => setNum(i, 'source_out', e.target.value)} />
                    </td>
                    <td style={{ ...cell, color: sp !== undefined && sp <= 0 ? 'var(--danger, #d66)' : 'var(--text-dim)' }}>
                      {sp !== undefined ? round3(sp) : '—'}
                    </td>
                    <td style={cell}>
                      <input type="number" step="0.001" style={numInput} value={numStr(c.timeline_in)} onChange={(e) => setNum(i, 'timeline_in', e.target.value)} />
                    </td>
                    <td style={cell}>
                      <select value={c.transition?.type ?? ''} onChange={(e) => setTransType(i, e.target.value)} style={{ width: 130 }}>
                        <option value="">— none —</option>
                        {c.transition?.type && !names.includes(c.transition.type) && (
                          <option value={c.transition.type}>{c.transition.type} (?)</option>
                        )}
                        {names.map((n) => <option key={n} value={n}>{n}</option>)}
                      </select>
                    </td>
                    <td style={cell}>
                      <input
                        type="number"
                        step="0.001"
                        style={numInput}
                        disabled={!c.transition?.type}
                        value={numStr(c.transition?.duration)}
                        onChange={(e) => setTransDur(i, e.target.value)}
                      />
                    </td>
                    <td style={cell}>
                      <button className="mini-btn" title="Duplicate clip" onClick={() => duplicateClip(i)}>⎘</button>{' '}
                      <button className="mini-btn" title="Remove clip" onClick={() => removeClip(i)}>✕</button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        <div style={{ padding: '6px 16px', borderTop: '1px solid var(--border)', fontSize: 11, minHeight: 20 }}>
          {issues.length === 0 ? (
            <span style={{ color: 'var(--text-dim)' }}>No issues · total {round3(totalDur)} s</span>
          ) : (
            <span style={{ color: hasErrors ? 'var(--danger, #d66)' : 'var(--warn, #c90)' }}>
              {issues.slice(0, 3).map((x) => x.msg).join('  ·  ')}
              {issues.length > 3 ? `  · +${issues.length - 3} more` : ''}
            </span>
          )}
        </div>

        <div className="dialog-footer" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div style={{ display: 'flex', gap: 6 }}>
            <button onClick={addClip}>+ Clip</button>
            <button onClick={chainTimeline} title="Recompute every timeline_in back-to-back, including transition overlap">Chain timeline ⟂</button>
          </div>
          <div style={{ display: 'flex', gap: 6 }}>
            <button onClick={onCancel}>Cancel</button>
            <button onClick={apply} disabled={hasErrors} title={hasErrors ? 'Fix the errors above to apply' : 'Apply changes to the node'}>
              Apply
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
