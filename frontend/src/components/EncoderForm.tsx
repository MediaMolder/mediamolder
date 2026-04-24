// EncoderForm — typed Inspector form for graph nodes whose `type` is
// "encoder". Loads the option schema for `params.codec` from the backend
// and renders:
//   1. The four primary controls (preset, rate control,
//      bit-rate or quality, keyframe interval) when the encoder advertises
//      them, in their own always-visible block.
//   2. A Raw options section for codec-specific param-string escape hatches
//      (x264-opts, x265-params, svtav1-params, ...). These are shown above
//      Advanced because they are common power-user knobs.
//   3. An Advanced collapsible listing every remaining option, grouped by a
//      lightweight heuristic (Threading / Quality / Color / Motion /
//      Profile/Level / General). A search box at the top filters the
//      Advanced list by option name + help substring.
//
// Editing model: we store every value in `def.params` as a string (the
// canonical params type is `Record<string, unknown>` but the pipeline
// stringifies values when building FFmpeg args, so strings are the
// safest round-trip). Empty string ⇒ remove the key entirely so the
// encoder uses libav's default.

import { useEffect, useMemo, useState } from 'react';
import type { NodeDef } from '../lib/jobTypes';
import {
  fetchEncoderInfo,
  findOption,
  rolesFor,
  type EncoderInfo,
  type EncoderOption,
} from '../lib/encoderSchema';
import { OptionControl, defaultDisplay } from './controls/OptionControl';

interface Props {
  def: NodeDef;
  onChange: (next: NodeDef) => void;
}

// Names of well-known param-string escape hatches surfaced as Raw options.
const RAW_OPTION_NAMES = new Set([
  'x264-params',
  'x264opts',
  'x264-opts',
  'x265-params',
  'svtav1-params',
  'vpx-params',
  'aom-params',
]);

export function EncoderForm({ def, onChange }: Props) {
  const codec = String(def.params?.codec ?? '');
  const [info, setInfo] = useState<EncoderInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!codec) {
      setInfo(null);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    let cancelled = false;
    fetchEncoderInfo(codec)
      .then((i) => {
        if (!cancelled) setInfo(i);
      })
      .catch((e: Error) => {
        if (!cancelled) {
          setError(e.message);
          setInfo(null);
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [codec]);

  const setParam = (key: string, value: string) => {
    const next = { ...(def.params ?? {}) };
    if (value === '') delete next[key];
    else next[key] = value;
    onChange({ ...def, params: next });
  };

  const getParam = (key: string): string => {
    const v = def.params?.[key];
    return v === undefined || v === null ? '' : String(v);
  };

  if (!codec) {
    return (
      <>
        <label>Codec</label>
        <div className="empty" style={{ fontSize: 11 }}>
          Set <code>codec</code> in Params to choose an encoder (e.g. <code>libx264</code>).
        </div>
      </>
    );
  }

  if (loading) {
    return <div className="empty">Loading {codec} options…</div>;
  }
  if (error) {
    return (
      <div className="empty" style={{ color: '#f5b7b1', fontSize: 11 }}>
        Failed to load {codec}: {error}
      </div>
    );
  }
  if (!info) return null;

  const roles = rolesFor(codec, info.options);
  const preset = findOption(info.options, roles.preset);
  const rc = findOption(info.options, roles.rate_control);
  const bitRate = findOption(info.options, roles.bit_rate);
  const quality = findOption(info.options, roles.quality);
  const keyint = findOption(info.options, roles.keyframe_interval);
  const rcValue = rc ? getParam(rc.name) : '';

  // Decide whether to surface the bitrate or quality field as the
  // primary "rate" control. If the encoder distinguishes via an `rc`
  // option whose name suggests CRF / VBR / CBR, honour it; otherwise
  // show whichever the user has already set, falling back to bitrate.
  const showQuality = quality !== undefined && (
    !rc || /crf|vbr|qp|cqp|constqp|q$/i.test(rcValue) || (!!quality && getParam(quality.name) !== '')
  );

  // Build the set of primary option names to exclude from the Advanced view.
  const primaryNames = new Set<string>();
  for (const o of [preset, rc, keyint, showQuality ? quality : bitRate]) {
    if (o) primaryNames.add(o.name);
  }

  // Split remaining options into Raw vs Advanced.
  const raw: EncoderOption[] = [];
  const advanced: EncoderOption[] = [];
  for (const o of info.options) {
    if (primaryNames.has(o.name)) continue;
    if (RAW_OPTION_NAMES.has(o.name)) raw.push(o);
    else advanced.push(o);
  }

  // Advanced grouping. Cheap and stateless — recompute on every render.
  const groups = groupAdvanced(advanced);

  return (
    <>
      <div className="encoder-form-header" style={{ marginTop: 4, marginBottom: 8 }}>
        <strong>{info.long_name || info.name}</strong>
        <div className="empty" style={{ fontSize: 11, marginTop: 2 }}>
          {info.media_type} · {codec}
        </div>
      </div>

      {preset && <PrimaryRow option={preset} value={getParam(preset.name)} onChange={(v) => setParam(preset.name, v)} />}
      {rc && <PrimaryRow option={rc} value={rcValue} onChange={(v) => setParam(rc.name, v)} />}
      {showQuality && quality && (
        <PrimaryRow
          option={quality}
          value={getParam(quality.name)}
          onChange={(v) => setParam(quality.name, v)}
          labelOverride="Quality"
        />
      )}
      {!showQuality && bitRate && (
        <PrimaryRow
          option={bitRate}
          value={getParam(bitRate.name)}
          onChange={(v) => setParam(bitRate.name, v)}
          labelOverride="Bit rate"
        />
      )}
      {keyint && (
        <PrimaryRow
          option={keyint}
          value={getParam(keyint.name)}
          onChange={(v) => setParam(keyint.name, v)}
          labelOverride="Keyframe interval"
        />
      )}

      {!preset && !rc && !bitRate && !quality && !keyint && (
        <div className="empty" style={{ fontSize: 11 }}>
          No primary controls recognised for this encoder. Use the Advanced
          section below to configure it.
        </div>
      )}

      {raw.length > 0 && (
        <RawOptions options={raw} getParam={getParam} setParam={setParam} />
      )}

      {advanced.length > 0 && (
        <AdvancedSection groups={groups} getParam={getParam} setParam={setParam} />
      )}
    </>
  );
}

/* ---------- Raw options (param-string escape hatch) ---------- */
function RawOptions({
  options,
  getParam,
  setParam,
}: {
  options: EncoderOption[];
  getParam: (k: string) => string;
  setParam: (k: string, v: string) => void;
}) {
  return (
    <div className="encoder-section" style={{ marginTop: 12 }}>
      <label style={{ marginTop: 0 }}>Raw options</label>
      <div className="empty" style={{ fontSize: 10, marginTop: -2, marginBottom: 6 }}>
        Codec-native parameter strings — passed through verbatim.
      </div>
      {options.map((o) => (
        <div key={o.name}>
          <label title={o.help}>
            {o.name}
          </label>
          <textarea
            value={getParam(o.name)}
            placeholder={defaultDisplay(o) || 'key=value:key=value'}
            onChange={(e) => setParam(o.name, e.target.value)}
            rows={2}
            style={{ width: '100%', fontFamily: 'var(--mono, monospace)', fontSize: 11 }}
          />
          {o.help && (
            <div className="empty" style={{ fontSize: 10, marginTop: -4, marginBottom: 6 }}>
              {o.help}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

/* ---------- Advanced section (collapsible, searchable, grouped) ---------- */
function AdvancedSection({
  groups,
  getParam,
  setParam,
}: {
  groups: AdvancedGroup[];
  getParam: (k: string) => string;
  setParam: (k: string, v: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');

  // Filter when there's a query; otherwise show grouped layout.
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return null;
    const out: EncoderOption[] = [];
    for (const g of groups) {
      for (const o of g.options) {
        if (o.name.toLowerCase().includes(q) || (o.help && o.help.toLowerCase().includes(q))) {
          out.push(o);
        }
      }
    }
    return out;
  }, [groups, query]);

  return (
    <div className="encoder-section" style={{ marginTop: 14 }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        style={{ width: '100%', textAlign: 'left' }}
      >
        {open ? '▼' : '▶'} Advanced ({groups.reduce((n, g) => n + g.options.length, 0)} options)
      </button>
      {open && (
        <>
          <input
            type="search"
            value={query}
            placeholder="Search options…"
            onChange={(e) => setQuery(e.target.value)}
            style={{ width: '100%', marginTop: 6, marginBottom: 6 }}
          />
          {filtered ? (
            filtered.length === 0 ? (
              <div className="empty" style={{ fontSize: 11 }}>No options match “{query}”.</div>
            ) : (
              filtered.map((o) => (
                <AdvancedRow key={o.name} option={o} value={getParam(o.name)} onChange={(v) => setParam(o.name, v)} />
              ))
            )
          ) : (
            groups.map((g) => (
              <AdvancedGroupView
                key={g.name}
                group={g}
                getParam={getParam}
                setParam={setParam}
              />
            ))
          )}
        </>
      )}
    </div>
  );
}

function AdvancedGroupView({
  group,
  getParam,
  setParam,
}: {
  group: AdvancedGroup;
  getParam: (k: string) => string;
  setParam: (k: string, v: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div style={{ marginTop: 6 }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        style={{ width: '100%', textAlign: 'left', fontSize: 11 }}
      >
        {open ? '▾' : '▸'} {group.name} ({group.options.length})
      </button>
      {open && group.options.map((o) => (
        <AdvancedRow key={o.name} option={o} value={getParam(o.name)} onChange={(v) => setParam(o.name, v)} />
      ))}
    </div>
  );
}

function AdvancedRow({
  option,
  value,
  onChange,
}: {
  option: EncoderOption;
  value: string;
  onChange: (next: string) => void;
}) {
  const def = defaultDisplay(option);
  return (
    <div style={{ marginTop: 4 }}>
      <label title={option.help} style={{ fontSize: 11 }}>
        {option.name}
        <span className="empty" style={{ fontSize: 10, marginLeft: 4 }}>
          {option.type}{def ? ` · default ${def}` : ''}
        </span>
      </label>
      <OptionControl option={option} value={value} onChange={onChange} />
      {option.help && (
        <div className="empty" style={{ fontSize: 10, marginTop: -4, marginBottom: 4 }}>
          {option.help}
        </div>
      )}
    </div>
  );
}

/* ---------- Advanced grouping heuristic ---------- */
interface AdvancedGroup {
  name: string;
  options: EncoderOption[];
}

function groupAdvanced(opts: EncoderOption[]): AdvancedGroup[] {
  const buckets: Record<string, EncoderOption[]> = {
    Threading: [],
    Quality: [],
    Color: [],
    Motion: [],
    'Profile / Level': [],
    'GOP & frames': [],
    Other: [],
  };
  for (const o of opts) {
    buckets[bucketFor(o)].push(o);
  }
  // Preserve a stable order; drop empty buckets.
  const order = ['GOP & frames', 'Quality', 'Profile / Level', 'Color', 'Motion', 'Threading', 'Other'];
  return order
    .filter((n) => buckets[n].length > 0)
    .map((n) => ({ name: n, options: buckets[n] }));
}

function bucketFor(o: EncoderOption): string {
  const n = o.name.toLowerCase();
  const help = (o.help ?? '').toLowerCase();
  if (n.includes('thread') || n === 'slices') return 'Threading';
  if (n.includes('qmin') || n.includes('qmax') || n.includes('qcomp') || n.startsWith('q') || n.includes('quant') || n.includes('aq') || help.includes('quantizer')) return 'Quality';
  if (n.includes('color') || n.includes('chroma') || n.includes('matrix') || n.includes('primaries') || n.includes('transfer') || n.includes('range') || n === 'pix_fmt') return 'Color';
  if (n.includes('mv') || n.includes('motion') || n.includes('me_') || n.startsWith('me') || n.includes('subq') || n.includes('refs')) return 'Motion';
  if (n.includes('profile') || n.includes('level') || n.includes('tier')) return 'Profile / Level';
  if (n === 'g' || n.includes('keyint') || n.includes('gop') || n.includes('bf') || n === 'b_strategy' || n.includes('frames') || n.includes('refs')) return 'GOP & frames';
  return 'Other';
}


function PrimaryRow({
  option,
  value,
  onChange,
  labelOverride,
}: {
  option: EncoderOption;
  value: string;
  onChange: (next: string) => void;
  labelOverride?: string;
}) {
  const label = labelOverride ?? prettyLabel(option.name);
  const def = defaultDisplay(option);
  return (
    <>
      <label title={option.help}>
        {label} <span className="empty" style={{ fontSize: 10 }}>({option.name}{def ? ` · default ${def}` : ''})</span>
      </label>
      <OptionControl option={option} value={value} onChange={onChange} />
      {option.help && (
        <div className="empty" style={{ fontSize: 10, marginTop: -4, marginBottom: 6 }}>
          {option.help}
        </div>
      )}
    </>
  );
}

function prettyLabel(name: string): string {
  // Map common cryptic libav option names to friendly labels.
  switch (name) {
    case 'preset': return 'Preset';
    case 'rc': return 'Rate control';
    case 'b': return 'Bit rate';
    case 'crf': return 'Quality (CRF)';
    case 'cq': return 'Quality (CQ)';
    case 'q': return 'Quality (Q)';
    case 'g': return 'Keyframe interval (frames)';
    case 'vbr': return 'VBR mode';
    case 'cpu-used': return 'CPU usage preset';
    case 'deadline': return 'Deadline';
    default: return name;
  }
}
