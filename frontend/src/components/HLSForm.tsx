// HLSForm — typed wizard for `output.hls` (libavformat hlsenc.c).
// Renders when `output.format === 'hls'`.  Covers:
//   • Segment timing & playlist control
//   • Segment-type selector (mpegts / fmp4)
//   • Filename templates
//   • ABR master playlist + variant-stream-map builder
//   • hls_flags multi-checkbox

import type { HLSOptions } from '../lib/jobTypes';

// All token names accepted by the hls_flags AVOption in hlsenc.c.
const HLS_FLAG_TOKENS = [
  'delete_segments',
  'append_list',
  'round_durations',
  'discont_start',
  'split_by_time',
  'program_date_time',
  'second_level_segment_index',
  'second_level_segment_duration',
  'second_level_segment_size',
  'temp_file',
  'independent_segments',
  'iframes_only',
  'single_file',
] as const;

interface Props {
  hls: HLSOptions;
  onChange: (next: HLSOptions) => void;
}

export function HLSForm({ hls, onChange }: Props) {
  const set = <K extends keyof HLSOptions>(k: K, v: HLSOptions[K]) => {
    const next = { ...hls };
    if (v === undefined || v === '' || (typeof v === 'number' && isNaN(v))) {
      delete next[k];
    } else {
      next[k] = v;
    }
    onChange(next);
  };

  const numField = (k: keyof HLSOptions, label: string, placeholder: string) => {
    const val = hls[k];
    return (
      <>
        <label>{label}</label>
        <input
          type="number"
          value={typeof val === 'number' ? val : ''}
          placeholder={placeholder}
          step="any"
          onChange={(e) => set(k, e.target.value === '' ? undefined : parseFloat(e.target.value) as HLSOptions[typeof k])}
        />
      </>
    );
  };

  const textField = (k: keyof HLSOptions, label: string, placeholder: string) => {
    const val = hls[k];
    return (
      <>
        <label>{label}</label>
        <input
          type="text"
          value={typeof val === 'string' ? val : ''}
          placeholder={placeholder}
          onChange={(e) => set(k, (e.target.value || undefined) as HLSOptions[typeof k])}
        />
      </>
    );
  };

  return (
    <>
      <SectionHeader>HLS muxer options</SectionHeader>
      {numField('time', 'Segment duration (s)', '2')}
      {numField('init_time', 'Init segment duration (s)', '(same as time)')}
      {numField('list_size', 'Playlist list size (0 = all)', '5')}
      {numField('start_number', 'Start segment number', '0')}

      <label>Playlist type</label>
      <select
        value={hls.playlist_type ?? ''}
        onChange={(e) => set('playlist_type', (e.target.value || undefined) as HLSOptions['playlist_type'])}
      >
        <option value="">(default)</option>
        <option value="event">event</option>
        <option value="vod">vod</option>
      </select>

      <label>Segment type</label>
      <select
        value={hls.segment_type ?? ''}
        onChange={(e) => set('segment_type', (e.target.value || undefined) as HLSOptions['segment_type'])}
      >
        <option value="">(default — mpegts)</option>
        <option value="mpegts">mpegts</option>
        <option value="fmp4">fmp4 (CMAF)</option>
      </select>

      {textField(
        'segment_filename',
        'Segment filename template',
        'out%03d.ts  or  stream%v/seg%03d.ts',
      )}

      {(hls.segment_type === 'fmp4') && textField(
        'fmp4_init_filename',
        'fMP4 init segment filename',
        'init.mp4',
      )}

      <SectionHeader>ABR / master playlist</SectionHeader>
      {textField('master_pl_name', 'Master playlist filename', 'master.m3u8')}
      <VarStreamMapEditor
        value={hls.var_stream_map ?? ''}
        onChange={(v) => set('var_stream_map', v || undefined)}
      />

      <SectionHeader>Flags</SectionHeader>
      <FlagsPicker
        tokens={HLS_FLAG_TOKENS as unknown as string[]}
        value={hls.flags ?? []}
        onChange={(f) => set('flags', f.length ? f : undefined)}
      />
    </>
  );
}

/* ---------- Variant-stream-map builder ----------
 * var_stream_map is a space-separated list of per-variant specs,
 * e.g. "v:0,a:0 v:1,a:0 v:2,a:1".  Each spec is a comma-separated
 * list of stream-group tokens: v:<idx>, a:<idx>, agroup:<name>,
 * sgroup:<name>, language:<lang>, etc.
 *
 * The UI shows one row per variant with a free-text field for its
 * comma-separated spec.  Add / remove rows, then serialise as the
 * space-separated string that FFmpeg expects. */
function VarStreamMapEditor({
  value,
  onChange,
}: {
  value: string;
  onChange: (next: string) => void;
}) {
  // Parse the current value into an array of variant specs.
  const variants = value.trim() ? value.trim().split(/\s+/) : [];

  const update = (i: number, spec: string) => {
    const next = [...variants];
    next[i] = spec;
    onChange(next.filter(Boolean).join(' '));
  };
  const remove = (i: number) => {
    const next = variants.filter((_, idx) => idx !== i);
    onChange(next.join(' '));
  };
  const add = () => {
    onChange([...variants, 'v:0,a:0'].join(' '));
  };

  return (
    <>
      <label>
        Variant stream map{' '}
        <span style={{ fontWeight: 400, textTransform: 'none', fontSize: 10 }}>
          (space-separated; requires master playlist filename)
        </span>
      </label>
      {variants.length === 0 && (
        <div className="empty" style={{ marginTop: 0, marginBottom: 6, fontSize: 11 }}>
          No variants — single rendition. Add variants for ABR.
        </div>
      )}
      {variants.map((spec, i) => (
        <div key={i} className="param-row" style={{ gridTemplateColumns: '1fr 28px' }}>
          <input
            value={spec}
            placeholder="v:0,a:0"
            spellCheck={false}
            onChange={(e) => update(i, e.target.value)}
          />
          <button type="button" onClick={() => remove(i)} title="Remove variant">×</button>
        </div>
      ))}
      <button type="button" style={{ marginTop: 4, fontSize: 11 }} onClick={add}>
        + add variant
      </button>
      {variants.length > 0 && (
        <div style={{ fontSize: 10, color: 'var(--text-dim)', marginTop: 4 }}>
          Tokens: <code>v:N</code>, <code>a:N</code>, <code>agroup:name</code>,{' '}
          <code>sgroup:name</code>, <code>language:xx</code>
        </div>
      )}
    </>
  );
}

/* ---------- Multi-checkbox flag picker ---------- */
function FlagsPicker({
  tokens,
  value,
  onChange,
}: {
  tokens: string[];
  value: string[];
  onChange: (next: string[]) => void;
}) {
  const active = new Set(value);
  const toggle = (t: string) => {
    const next = active.has(t) ? value.filter((x) => x !== t) : [...value, t];
    onChange(next);
  };
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 8px', marginBottom: 4 }}>
      {tokens.map((t) => (
        <label
          key={t}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 4,
            fontSize: 11,
            color: active.has(t) ? 'var(--text)' : 'var(--text-dim)',
            cursor: 'pointer',
            textTransform: 'none',
            margin: 0,
            letterSpacing: 0,
          }}
        >
          <input
            type="checkbox"
            checked={active.has(t)}
            onChange={() => toggle(t)}
            style={{ width: 'auto', margin: 0 }}
          />
          {t}
        </label>
      ))}
    </div>
  );
}

/* ---------- Visual section header ---------- */
function SectionHeader({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        marginTop: 14,
        marginBottom: 2,
        fontSize: 10,
        fontWeight: 600,
        textTransform: 'uppercase',
        letterSpacing: 0.8,
        color: 'var(--accent, #4f8cff)',
        borderBottom: '1px solid var(--border)',
        paddingBottom: 3,
      }}
    >
      {children}
    </div>
  );
}
