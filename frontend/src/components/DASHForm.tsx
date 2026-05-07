// DASHForm — typed wizard for `output.dash` (libavformat dashenc.c).
// Renders when `output.format === 'dash'`.  Covers:
//   • Segment / fragment timing
//   • Manifest window control
//   • Filename templates
//   • Adaptation-set spec builder
//   • SegmentTemplate / SegmentTimeline tri-state toggles
//   • Low-latency DASH + HLS dual-pack options
//   • dash_flags multi-checkbox

import type { DASHOptions } from '../lib/jobTypes';

// All token names accepted by the dash_flags AVOption in dashenc.c.
const DASH_FLAG_TOKENS = [
  'default_base_url_override',
  'round_durations',
  'single_file_name',
  'global_sidx',
  'write_prft',
  'allow_media_loss',
] as const;

interface Props {
  dash: DASHOptions;
  onChange: (next: DASHOptions) => void;
}

export function DASHForm({ dash, onChange }: Props) {
  const set = <K extends keyof DASHOptions>(k: K, v: DASHOptions[K]) => {
    const next = { ...dash };
    if (v === undefined || v === '' || (typeof v === 'number' && isNaN(v))) {
      delete next[k];
    } else {
      next[k] = v;
    }
    onChange(next);
  };

  const numField = (k: keyof DASHOptions, label: string, placeholder: string) => {
    const val = dash[k];
    return (
      <>
        <label>{label}</label>
        <input
          type="number"
          value={typeof val === 'number' ? val : ''}
          placeholder={placeholder}
          step="any"
          onChange={(e) => set(k, e.target.value === '' ? undefined : parseFloat(e.target.value) as DASHOptions[typeof k])}
        />
      </>
    );
  };

  const textField = (k: keyof DASHOptions, label: string, placeholder: string) => {
    const val = dash[k];
    return (
      <>
        <label>{label}</label>
        <input
          type="text"
          value={typeof val === 'string' ? val : ''}
          placeholder={placeholder}
          onChange={(e) => set(k, (e.target.value || undefined) as DASHOptions[typeof k])}
        />
      </>
    );
  };

  // Three-state boolean: unset (libavformat default) | true | false.
  const triState = (k: keyof DASHOptions, label: string, hint: string) => {
    const val = dash[k];
    const effective = val === undefined ? '' : val ? 'true' : 'false';
    return (
      <>
        <label>{label}</label>
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 3 }}>{hint}</div>
        <div className="bool-toggle" role="radiogroup" aria-label={label}>
          {(['', 'true', 'false'] as const).map((opt) => (
            <button
              key={opt}
              type="button"
              role="radio"
              aria-checked={effective === opt}
              className={'bool-toggle-opt' + (effective === opt ? ' active default' : '')}
              onClick={() =>
                set(k, opt === '' ? undefined : (opt === 'true') as DASHOptions[typeof k])
              }
            >
              {opt === '' ? '(default)' : opt}
            </button>
          ))}
        </div>
      </>
    );
  };

  const boolField = (k: keyof DASHOptions, label: string) => {
    const val = dash[k];
    return (
      <>
        <label>{label}</label>
        <div className="bool-toggle" role="radiogroup" aria-label={label}>
          {(['true', 'false'] as const).map((opt) => (
            <button
              key={opt}
              type="button"
              role="radio"
              aria-checked={val === (opt === 'true')}
              className={'bool-toggle-opt' + (val === (opt === 'true') ? ' active' : '')}
              onClick={() =>
                set(k, opt === 'true' as unknown as DASHOptions[typeof k])
              }
            >
              {opt}
            </button>
          ))}
          <button
            type="button"
            className="bool-toggle-clear"
            onClick={() => set(k, undefined)}
            disabled={val === undefined}
            title="Clear override"
            aria-label="Clear override"
          >
            ✕
          </button>
        </div>
      </>
    );
  };

  return (
    <>
      <SectionHeader>DASH muxer options</SectionHeader>
      {numField('seg_duration', 'Segment duration (s)', '4')}
      {numField('frag_duration', 'Fragment duration (s)', '(same as seg_duration)')}
      {numField('window_size', 'Manifest window size (0 = all)', '5')}
      {numField('extra_window_size', 'Extra segments kept on disk', '5')}

      {textField('init_seg_name', 'Init segment name template', 'init-stream$RepresentationID$.m4s')}
      {textField('media_seg_name', 'Media segment name template', 'chunk-stream$RepresentationID$-$Number%05d$.m4s')}

      <SectionHeader>Manifest settings</SectionHeader>
      {triState(
        'use_template',
        'Use SegmentTemplate',
        'Emit <SegmentTemplate> instead of <SegmentList>. Recommended (libavformat default: true).',
      )}
      {triState(
        'use_timeline',
        'Use SegmentTimeline',
        'Emit <SegmentTimeline> inside <SegmentTemplate>. Recommended for VoD (libavformat default: true).',
      )}

      <label>Adaptation sets</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 3 }}>
        Manual adaptation-set spec, e.g.{' '}
        <code>id=0,streams=v id=1,streams=a</code>. Leave empty for automatic grouping.
      </div>
      <AdaptationSetsEditor
        value={dash.adaptation_sets ?? ''}
        onChange={(v) => set('adaptation_sets', v || undefined)}
      />

      <SectionHeader>Low-latency &amp; packaging</SectionHeader>
      {boolField('streaming', 'Streaming (progressive fragment writes)')}
      {boolField('ldash', 'Low-latency DASH (LL-DASH)')}
      {boolField('hls_playlist', 'Also write HLS .m3u8 (CMAF dual-pack)')}
      {boolField('single_file', 'Single-file output (SegmentBase)')}

      <SectionHeader>Flags</SectionHeader>
      <FlagsPicker
        tokens={DASH_FLAG_TOKENS as unknown as string[]}
        value={dash.flags ?? []}
        onChange={(f) => set('flags', f.length ? f : undefined)}
      />
    </>
  );
}

/* ---------- Adaptation-sets editor ----------
 * Each adaptation set is a space-separated token block
 * "id=N,streams=v" etc.  Show one row per set. */
function AdaptationSetsEditor({
  value,
  onChange,
}: {
  value: string;
  onChange: (next: string) => void;
}) {
  const sets = value.trim() ? value.trim().split(/\s+/) : [];

  const update = (i: number, v: string) => {
    const next = [...sets];
    next[i] = v;
    onChange(next.filter(Boolean).join(' '));
  };
  const remove = (i: number) => {
    onChange(sets.filter((_, idx) => idx !== i).join(' '));
  };
  const add = () => {
    const newId = sets.length;
    onChange([...sets, `id=${newId},streams=${newId === 0 ? 'v' : 'a'}`].join(' '));
  };

  if (sets.length === 0) {
    return (
      <>
        <div className="empty" style={{ marginTop: 0, marginBottom: 6, fontSize: 11 }}>
          Automatic grouping (one AdaptationSet per codec type).
        </div>
        <button type="button" style={{ fontSize: 11 }} onClick={add}>
          + add adaptation set
        </button>
      </>
    );
  }
  return (
    <>
      {sets.map((s, i) => (
        <div key={i} className="param-row" style={{ gridTemplateColumns: '1fr 28px' }}>
          <input
            value={s}
            placeholder="id=0,streams=v"
            spellCheck={false}
            onChange={(e) => update(i, e.target.value)}
          />
          <button type="button" onClick={() => remove(i)} title="Remove">×</button>
        </div>
      ))}
      <button type="button" style={{ marginTop: 4, fontSize: 11 }} onClick={add}>
        + add adaptation set
      </button>
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
