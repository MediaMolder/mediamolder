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
  type EncoderUiRoles,
} from '../lib/encoderSchema';
import { OptionControl, defaultDisplay } from './controls/OptionControl';
import { parseBitRate } from '../lib/streamAttrs';

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
  const bitRate = findOption(info.options, roles.bit_rate);
  const crf = findOption(info.options, roles.crf);
  const qp = findOption(info.options, roles.qp);
  const keyint = findOption(info.options, roles.keyframe_interval);
  const rcEnum = findOption(info.options, roles.rc_enum);

  // Build the set of primary option names to exclude from the Advanced view.
  // Includes everything driven by the rate-control group (b, crf, qp, rc,
  // and the maxrate/minrate hack used for CBR with libx264/libx265).
  const primaryNames = new Set<string>();
  for (const o of [preset, bitRate, crf, qp, keyint, rcEnum]) {
    if (o) primaryNames.add(o.name);
  }
  primaryNames.add('maxrate');
  primaryNames.add('minrate');
  primaryNames.add('bufsize');

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

  const hasRateControl = !!(bitRate || crf || qp);

  return (
    <>
      <div className="encoder-form-header" style={{ marginTop: 4, marginBottom: 8 }}>
        <strong>{info.long_name || info.name}</strong>
        <div className="empty" style={{ fontSize: 11, marginTop: 2 }}>
          {info.media_type} · {codec}
        </div>
      </div>

      {preset && <PrimaryRow option={preset} value={getParam(preset.name)} onChange={(v) => setParam(preset.name, v)} />}

      {hasRateControl && (
        <RateControlGroup
          roles={roles}
          bitRate={bitRate}
          crf={crf}
          qp={qp}
          rcEnum={rcEnum}
          getParam={getParam}
          setParam={setParam}
        />
      )}

      {keyint && (
        <PrimaryRow
          option={keyint}
          value={getParam(keyint.name)}
          onChange={(v) => setParam(keyint.name, v)}
          labelOverride="Keyframe interval (GOP size)"
        />
      )}

      {!preset && !hasRateControl && !keyint && (
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

/* ---------- Rate-control group (mode + per-mode controls) ---------- */

type RateMode = 'bitrate' | 'crf' | 'qp';
type BitrateSubMode = 'vbr' | 'cbr';

function RateControlGroup({
  roles,
  bitRate,
  crf,
  qp,
  rcEnum,
  getParam,
  setParam,
}: {
  roles: EncoderUiRoles;
  bitRate: EncoderOption | undefined;
  crf: EncoderOption | undefined;
  qp: EncoderOption | undefined;
  rcEnum: EncoderOption | undefined;
  getParam: (k: string) => string;
  setParam: (k: string, v: string) => void;
}) {
  // The "current mode" is held in component state so the user can pick
  // CRF or QP without those modes immediately reverting to Bit rate
  // (which would happen if mode were derived solely from which params
  // are non-empty — switching modes intentionally clears the previous
  // mode's value but leaves the new mode blank for the user to fill).
  // We seed it from the existing params so loading a JobConfig with
  // e.g. `crf` set lands the user on the CRF view.
  const qpVal = qp ? getParam(qp.name) : '';
  const crfVal = crf ? getParam(crf.name) : '';
  const bVal = bitRate ? getParam(bitRate.name) : '';
  const rcEnumVal = rcEnum ? getParam(rcEnum.name) : '';
  const seedMode = (): RateMode => {
    if (rcEnum && rcEnumVal) {
      if (roles.rc_qp && rcEnumVal === roles.rc_qp) return 'qp';
      if (roles.rc_crf && rcEnumVal === roles.rc_crf) return 'crf';
      if (bitRate) return 'bitrate';
    }
    if (qp && qpVal !== '') return 'qp';
    if (crf && crfVal !== '') return 'crf';
    if (bitRate) return 'bitrate';
    if (crf) return 'crf';
    return 'qp';
  };
  const [mode, setModeState] = useState<RateMode>(seedMode);

  // Sub-mode for bitrate: VBR if maxrate not pinned to b; CBR if it is
  // (libx264/libx265 idiom) or if the rc enum is set to a CBR constant.
  const maxrateVal = getParam('maxrate');
  const seedSub = (): BitrateSubMode =>
    (rcEnum && roles.rc_cbr && rcEnumVal === roles.rc_cbr) ||
    (bVal !== '' && maxrateVal !== '' && maxrateVal === bVal)
      ? 'cbr'
      : 'vbr';
  const [sub, setSubState] = useState<BitrateSubMode>(seedSub);

  const setMode = (next: RateMode) => {
    setModeState(next);
    // Clear every other mode's params, then prime the new mode's enum
    // selection (for nvenc-style encoders).
    if (bitRate && next !== 'bitrate') {
      setParam(bitRate.name, '');
      setParam('maxrate', '');
      setParam('minrate', '');
      setParam('bufsize', '');
    }
    if (crf && next !== 'crf') setParam(crf.name, '');
    if (qp && next !== 'qp') setParam(qp.name, '');
    if (rcEnum) {
      const target =
        next === 'crf' ? roles.rc_crf
        : next === 'qp' ? roles.rc_qp
        : roles.rc_vbr; // bitrate defaults to VBR
      if (target !== undefined) setParam(rcEnum.name, target);
    }
  };

  const setBitrateSub = (next: BitrateSubMode) => {
    setSubState(next);
    if (next === 'cbr') {
      // CBR idiom for libx264/libx265: maxrate=minrate=b, bufsize=b.
      // For nvenc, switch the rc enum to its CBR constant.
      const v = bVal || (bitRate?.default?.int ? String(bitRate.default.int) : '');
      if (v) {
        setParam('maxrate', v);
        setParam('minrate', v);
        setParam('bufsize', v);
      }
      if (rcEnum && roles.rc_cbr) setParam(rcEnum.name, roles.rc_cbr);
    } else {
      setParam('maxrate', '');
      setParam('minrate', '');
      setParam('bufsize', '');
      if (rcEnum && roles.rc_vbr) setParam(rcEnum.name, roles.rc_vbr);
    }
  };

  // When the user types a new bitrate while in CBR mode, keep maxrate
  // and friends locked to it.
  const setBitRateValue = (v: string) => {
    if (!bitRate) return;
    setParam(bitRate.name, v);
    if (sub === 'cbr' && v !== '') {
      setParam('maxrate', v);
      setParam('minrate', v);
      setParam('bufsize', v);
    }
  };

  return (
    <div className="rate-control-group">
      <label>Rate control mode</label>
      <select value={mode} onChange={(e) => setMode(e.target.value as RateMode)}>
        {bitRate && <option value="bitrate">Bit rate</option>}
        {crf && <option value="crf">CRF</option>}
        {qp && <option value="qp">QP</option>}
      </select>

      {mode === 'bitrate' && bitRate && (
        <>
          <label style={{ marginTop: 6 }}>Bit rate mode</label>
          <select
            value={sub}
            onChange={(e) => setBitrateSub(e.target.value as BitrateSubMode)}
          >
            <option value="vbr">VBR (variable)</option>
            <option value="cbr">CBR (constant)</option>
          </select>

          <label style={{ marginTop: 6 }} title={bitRate.help}>
            Target bit rate <span className="empty" style={{ fontSize: 10 }}>(kbps)</span>
          </label>
          <input
            type="number"
            min={0}
            step={100}
            value={bpsToKbpsInput(bVal)}
            placeholder={bpsToKbpsInput(defaultDisplay(bitRate)) || '5000'}
            onChange={(e) => setBitRateValue(kbpsInputToBps(e.target.value))}
          />
        </>
      )}

      {mode === 'crf' && crf && (
        <>
          <label style={{ marginTop: 6 }} title={crf.help}>
            CRF <span className="empty" style={{ fontSize: 10 }}>({crf.name}{rangeHint(crf)})</span>
          </label>
          <input
            type="number"
            step={1}
            min={Number.isFinite(crf.min) ? crf.min : undefined}
            max={Number.isFinite(crf.max) ? crf.max : undefined}
            value={crfVal}
            placeholder={defaultDisplay(crf)}
            onChange={(e) => setParam(crf.name, e.target.value)}
          />
        </>
      )}

      {mode === 'qp' && qp && (
        <>
          <label style={{ marginTop: 6 }} title={qp.help}>
            QP <span className="empty" style={{ fontSize: 10 }}>({qp.name}{rangeHint(qp)})</span>
          </label>
          <input
            type="number"
            step={1}
            min={Number.isFinite(qp.min) ? qp.min : undefined}
            max={Number.isFinite(qp.max) ? qp.max : undefined}
            value={qpVal}
            placeholder={defaultDisplay(qp)}
            onChange={(e) => setParam(qp.name, e.target.value)}
          />
        </>
      )}
    </div>
  );
}

function rangeHint(o: EncoderOption): string {
  if (Number.isFinite(o.min) && Number.isFinite(o.max)) return ` · ${o.min}–${o.max}`;
  return '';
}

/** Convert a stored bit-rate param (e.g. "5000000", "5M") into the
 * kbps integer string shown in the input. Empty stays empty. */
function bpsToKbpsInput(stored: string): string {
  if (!stored) return '';
  const bps = parseBitRate(stored);
  if (bps === undefined) return stored; // pass through unparseable values
  return String(Math.round(bps / 1000));
}

/** Convert the kbps the user typed into the bits/s string we persist. */
function kbpsInputToBps(input: string): string {
  const v = input.trim();
  if (!v) return '';
  const n = parseFloat(v);
  if (!Number.isFinite(n) || n < 0) return '';
  return String(Math.round(n * 1000));
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
