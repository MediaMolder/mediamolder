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

import React, { useEffect, useMemo, useState } from 'react';
import type { NodeDef } from '../lib/jobTypes';
import {
  effectivePresetDefault,
  fetchEncoderInfo,
  findOption,
  optionChoices,
  primaryMeta,
  rolesFor,
  type EncoderInfo,
  type EncoderOption,
  type EncoderUiRoles,
  type OptionChoiceList,
} from '../lib/encoderSchema';
import { OptionControl, defaultDisplay } from './controls/OptionControl';
import { parseBitRate } from '../lib/streamAttrs';

interface Props {
  def: NodeDef;
  onChange: (next: NodeDef) => void;
}

// Options promoted to an always-visible "GOP & Frames" section above Advanced.
// Listed in the preferred display order.
const GOP_OPTION_NAMES: string[] = [
  'bf',          // B Frames
  'keyint_min',  // Min Keyframe Interval
  'sc_threshold',// Scene Change Threshold
  'refs',        // Reference Frames
  'b_strategy',  // B-Frame Strategy
];

// Options promoted to an always-visible "Profile / Level" section above Advanced.
const PROFILE_OPTION_NAMES: string[] = [
  'profile',
  'level',
  'tier',
];

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

  // Build the set of primary option names to exclude from all secondary views.
  // Includes everything driven by the rate-control group (b, crf, qp, rc,
  // and the maxrate/minrate hack used for CBR with libx264/libx265).
  const primaryNames = new Set<string>();
  for (const o of [preset, bitRate, crf, qp, keyint, rcEnum]) {
    if (o) primaryNames.add(o.name);
  }
  primaryNames.add('maxrate');
  primaryNames.add('minrate');
  primaryNames.add('bufsize');

  // Effective defaults implied by the current preset + tune (used as
  // placeholder text so the user can see what x264/x265 will actually apply).
  const presetVal = preset ? (getParam(preset.name) || 'medium') : 'medium';
  const tuneOpt   = findOption(info.options, 'tune');
  const tuneVal   = tuneOpt ? (getParam(tuneOpt.name) || undefined) : undefined;
  const getEffectivePlaceholder = (optionName: string): string | undefined =>
    effectivePresetDefault(codec, optionName, presetVal, tuneVal);

  // Split remaining options into GOP, Profile, Raw, and Advanced.
  // Profile options are deduplicated by name: when both a generic
  // AVCodecContext option (e.g. avctx->profile, int -99) and a private
  // codec option (e.g. x264/x265 string "profile") share the same name,
  // keep only the private one — it is the functional setting for these
  // encoders and has the correct string type.
  const gopOptions: EncoderOption[] = [];
  const profileMap = new Map<string, EncoderOption>();
  const raw: EncoderOption[] = [];
  const advanced: EncoderOption[] = [];
  for (const o of info.options) {
    if (primaryNames.has(o.name)) continue;
    if (RAW_OPTION_NAMES.has(o.name)) { raw.push(o); continue; }
    if (GOP_OPTION_NAMES.includes(o.name)) { gopOptions.push(o); continue; }
    if (PROFILE_OPTION_NAMES.includes(o.name)) {
      const existing = profileMap.get(o.name);
      if (!existing || (!existing.is_private && o.is_private)) {
        profileMap.set(o.name, o);
      }
      continue;
    }
    advanced.push(o);
  }
  const profileOptions = [...profileMap.values()]
    .sort((a, b) => PROFILE_OPTION_NAMES.indexOf(a.name) - PROFILE_OPTION_NAMES.indexOf(b.name));
  gopOptions.sort((a, b) => GOP_OPTION_NAMES.indexOf(a.name) - GOP_OPTION_NAMES.indexOf(b.name));

  // Advanced grouping. Cheap and stateless — recompute on every render.
  const groups = groupAdvanced(advanced);

  const hasRateControl = !!(bitRate || crf || qp);

  return (
    <>
      <div className="encoder-form-header" style={{ marginTop: 4, marginBottom: 8 }}>
        <strong>{prettyCodecFormat(info, codec)}</strong>
      </div>

      {preset && (
        <PrimaryRow
          codec={codec}
          option={preset}
          value={getParam(preset.name)}
          onChange={(v) => setParam(preset.name, v)}
          choices={optionChoices(codec, preset.name)}
        />
      )}

      {hasRateControl && (
        <RateControlGroup
          codec={codec}
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
          codec={codec}
          option={keyint}
          value={getParam(keyint.name)}
          onChange={(v) => setParam(keyint.name, v)}
          labelOverride="Keyframe interval (GOP size)"
          effectivePlaceholder={getEffectivePlaceholder(keyint.name)}
        />
      )}

      {!preset && !hasRateControl && !keyint && (
        <div className="empty" style={{ fontSize: 11 }}>
          No primary controls recognised for this encoder. Use the Advanced
          section below to configure it.
        </div>
      )}

      {gopOptions.length > 0 && (
        <PromotedSection title="GOP & Frames">
          {gopOptions.map((o) => (
            <PrimaryRow
              key={o.name}
              codec={codec}
              option={o}
              value={getParam(o.name)}
              onChange={(v) => setParam(o.name, v)}
              labelOverride={prettyLabel(o.name)}
              choices={optionChoices(codec, o.name)}
              effectivePlaceholder={getEffectivePlaceholder(o.name)}
            />
          ))}
        </PromotedSection>
      )}

      {profileOptions.length > 0 && (
        <PromotedSection title="Profile / Level">
          {profileOptions.map((o) => (
            <PrimaryRow
              key={o.name}
              codec={codec}
              option={o}
              value={getParam(o.name)}
              onChange={(v) => setParam(o.name, v)}
              labelOverride={prettyLabel(o.name)}
              choices={optionChoices(codec, o.name)}
              effectivePlaceholder={getEffectivePlaceholder(o.name)}
            />
          ))}
        </PromotedSection>
      )}

      {raw.length > 0 && (
        <RawOptions options={raw} getParam={getParam} setParam={setParam} />
      )}

      {advanced.length > 0 && (
        <AdvancedSection codec={codec} groups={groups} getParam={getParam} setParam={setParam} getEffectivePlaceholder={getEffectivePlaceholder} />
      )}
    </>
  );
}

/* ---------- Rate-control group (mode + per-mode controls) ---------- */

type RateMode = 'bitrate' | 'crf' | 'qp';
type BitrateSubMode = 'vbr' | 'cbr';

function RateControlGroup({
  codec,
  roles,
  bitRate,
  crf,
  qp,
  rcEnum,
  getParam,
  setParam,
}: {
  codec: string;
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
    if (bitRate && bVal !== '') return 'bitrate';
    // Nothing set — use the codec's declared default RC mode.
    return roles.default_rc ?? (bitRate ? 'bitrate' : crf ? 'crf' : 'qp');
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

      {mode === 'crf' && crf && (() => {
        const meta = primaryMeta(codec, crf);
        const display = codecNativeName(codec, crf.name) ?? crf.name;
        return (
          <>
            <label style={{ marginTop: 6 }} title={crf.help}>
              CRF <span className="empty" style={{ fontSize: 10 }}>({display}{meta.rangeHint})</span>
            </label>
            <input
              type="number"
              step={1}
              min={meta.min}
              max={meta.max}
              value={crfVal}
              placeholder={meta.default}
              onChange={(e) => setParam(crf.name, e.target.value)}
            />
          </>
        );
      })()}

      {mode === 'qp' && qp && (() => {
        const meta = primaryMeta(codec, qp);
        const display = codecNativeName(codec, qp.name) ?? qp.name;
        return (
          <>
            <label style={{ marginTop: 6 }} title={qp.help}>
              QP <span className="empty" style={{ fontSize: 10 }}>({display}{meta.rangeHint})</span>
            </label>
            <input
              type="number"
              step={1}
              min={meta.min}
              max={meta.max}
              value={qpVal}
              placeholder={meta.default}
              onChange={(e) => setParam(qp.name, e.target.value)}
            />
          </>
        );
      })()}
    </div>
  );
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

/* ---------- Promoted section (always visible, labelled group) ---------- */
function PromotedSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="encoder-section" style={{ marginTop: 12 }}>
      <div
        style={{
          fontSize: 11,
          fontWeight: 600,
          textTransform: 'uppercase',
          letterSpacing: '0.06em',
          color: 'var(--fg-muted, #888)',
          marginBottom: 4,
        }}
      >
        {title}
      </div>
      {children}
    </div>
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
            <div className="empty" style={{ fontSize: 10, marginTop: 2, marginBottom: 6 }}>
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
  codec,
  groups,
  getParam,
  setParam,
  getEffectivePlaceholder,
}: {
  codec: string;
  groups: AdvancedGroup[];
  getParam: (k: string) => string;
  setParam: (k: string, v: string) => void;
  getEffectivePlaceholder: (name: string) => string | undefined;
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
                <AdvancedRow key={o.name} codec={codec} option={o} value={getParam(o.name)} onChange={(v) => setParam(o.name, v)} effectivePlaceholder={getEffectivePlaceholder(o.name)} />
              ))
            )
          ) : (
            groups.map((g) => (
              <AdvancedGroupView
                key={g.name}
                codec={codec}
                group={g}
                getParam={getParam}
                setParam={setParam}
                getEffectivePlaceholder={getEffectivePlaceholder}
              />
            ))
          )}
        </>
      )}
    </div>
  );
}

function AdvancedGroupView({
  codec,
  group,
  getParam,
  setParam,
  getEffectivePlaceholder,
}: {
  codec: string;
  group: AdvancedGroup;
  getParam: (k: string) => string;
  setParam: (k: string, v: string) => void;
  getEffectivePlaceholder: (name: string) => string | undefined;
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
        <AdvancedRow key={o.name} codec={codec} option={o} value={getParam(o.name)} onChange={(v) => setParam(o.name, v)} effectivePlaceholder={getEffectivePlaceholder(o.name)} />
      ))}
    </div>
  );
}

function AdvancedRow({
  codec,
  option,
  value,
  onChange,
  effectivePlaceholder,
}: {
  codec: string;
  option: EncoderOption;
  value: string;
  onChange: (next: string) => void;
  effectivePlaceholder?: string;
}) {
  const def = effectivePlaceholder ?? defaultDisplay(option);
  const friendly = prettyLabel(option.name);
  const native = codecNativeName(codec, option.name);
  const display = native ?? option.name;
  const help = helpOverride(codec, option.name) ?? option.help;
  const optForControl: EncoderOption = effectivePlaceholder
    ? {
        ...option,
        default: option.type === 'float' || option.type === 'double'
          ? { float: parseFloat(effectivePlaceholder) }
          : option.type === 'string'
          ? { string: effectivePlaceholder }
          : { int: parseInt(effectivePlaceholder, 10) },
      }
    : option;
  return (
    <div style={{ marginTop: 4 }}>
      <label title={help ?? option.name} style={{ fontSize: 11 }}>
        {friendly}
        {friendly !== display && (
          <span className="empty" style={{ fontSize: 10, marginLeft: 4 }}>({display})</span>
        )}
        <span className="empty" style={{ fontSize: 10, marginLeft: 4 }}>
          {option.type}{def ? ` · default ${def}` : ''}
        </span>
      </label>
      <OptionControl option={optForControl} value={value} onChange={onChange} />
      {help && (
        <div className="empty" style={{ fontSize: 10, marginTop: 2, marginBottom: 4 }}>
          {help}
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
  if (n === 'g' || n.includes('keyint') || n.includes('gop') || n.includes('bf') || n === 'b_strategy' || n.includes('frames') || n.includes('refs') || n.includes('intra-refresh') || n.includes('intra_refresh')) return 'GOP & frames';
  return 'Other';
}


function PrimaryRow({
  codec,
  option,
  value,
  onChange,
  labelOverride,
  choices,
  effectivePlaceholder,
}: {
  codec?: string;
  option: EncoderOption;
  value: string;
  onChange: (next: string) => void;
  labelOverride?: string;
  choices?: OptionChoiceList;
  effectivePlaceholder?: string;
}) {
  const label = labelOverride ?? prettyLabel(option.name);
  const def = effectivePlaceholder ?? choices?.default ?? defaultDisplay(option);
  const native = codec ? codecNativeName(codec, option.name) : undefined;
  const display = native ?? option.name;
  const help = codec ? helpOverride(codec, option.name) ?? option.help : option.help;
  const optForControl: EncoderOption = effectivePlaceholder
    ? {
        ...option,
        default: option.type === 'float' || option.type === 'double'
          ? { float: parseFloat(effectivePlaceholder) }
          : option.type === 'string'
          ? { string: effectivePlaceholder }
          : { int: parseInt(effectivePlaceholder, 10) },
      }
    : option;
  return (
    <>
      <label title={help}>
        {label} <span className="empty" style={{ fontSize: 10 }}>({display}{def ? ` · default ${def}` : ''})</span>
      </label>
      {choices ? (
        <select value={value || (choices.default ?? '')} onChange={(e) => onChange(e.target.value)}>
          {!choices.default && <option value="">(default)</option>}
          {choices.choices.map((c) => (
            <option key={c.value} value={c.value}>
              {c.label ?? c.value}
            </option>
          ))}
        </select>
      ) : (
        <OptionControl option={optForControl} value={value} onChange={onChange} />
      )}
      {help && (
        <div className="empty" style={{ fontSize: 10, marginTop: 2, marginBottom: 6 }}>
          {help}
        </div>
      )}
    </>
  );
}

/**
 * Trim FFmpeg's verbose codec long_name down to the two most common
 * names for the underlying coding standard. libavcodec long_names
 * routinely include the implementation prefix and every historical
 * alias for the codec, e.g.
 *
 *   libx264:    "libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10"
 *   libx265:    "libx265 H.265 / HEVC (High Efficiency Video Coding)"
 *   libsvtav1:  "SVT-AV1(Scalable Video Technology for AV1) encoder"
 *   aac:        "AAC (Advanced Audio Coding)"
 *
 * Users want one short, unambiguous "this is the format you'll get"
 * line — so we strip the implementation name when it leads, then
 * keep at most the first two slash-separated tokens. The implementation
 * name is already shown elsewhere (the node heading and the codec field
 * in the params block) so dropping it here removes a redundancy, not
 * information.
 */
function prettyCodecFormat(info: EncoderInfo, codec: string): string {
  let s = (info.long_name || info.name || codec).trim();
  // Strip leading "<codec>" prefix when libavcodec embeds it (libx264,
  // libx265, libfdk_aac all do this).
  const lower = s.toLowerCase();
  const prefix = codec.toLowerCase();
  if (lower.startsWith(prefix + ' ')) s = s.slice(prefix.length + 1).trim();
  else if (lower.startsWith(prefix + '(')) s = s.slice(prefix.length).trim();
  // Drop a trailing " encoder" word that some long_names append.
  s = s.replace(/\s+encoder$/i, '').trim();
  // Keep at most the first two " / "-separated tokens.
  const parts = s.split(/\s*\/\s*/);
  if (parts.length > 2) s = parts.slice(0, 2).join(' / ');
  return s;
}

function prettyLabel(name: string): string {
  // Map common cryptic libav / codec AVOption names to friendly labels.
  switch (name) {
    // Primary controls
    case 'preset':          return 'Preset';
    case 'rc':              return 'Rate control';
    case 'b':               return 'Bit rate';
    case 'crf':             return 'Quality (CRF)';
    case 'cq':              return 'Quality (CQ)';
    case 'q':               return 'Quality (Q)';
    case 'g':               return 'Keyframe interval (frames)';
    case 'vbr':             return 'VBR mode';
    case 'cpu-used':        return 'CPU usage preset';
    case 'deadline':        return 'Deadline';
    // GOP & Frames
    case 'bf':              return 'B Frames';
    case 'keyint_min':      return 'Min Keyframe Interval';
    case 'sc_threshold':    return 'Scene Change Threshold';
    case 'refs':            return 'Reference Frames';
    case 'b_strategy':      return 'B-Frame Strategy';
    case 'intra-refresh':
    case 'intra_refresh':   return 'Periodic Intra Refresh';
    // Profile / Level / Tier
    case 'profile':         return 'Profile';
    case 'level':           return 'Level';
    case 'tier':            return 'Tier';
    // Tuning
    case 'tune':            return 'Tune';
    case 'fastfirstpass':   return 'Fast First Pass';
    case 'slow-firstpass':  return 'Slow First Pass';
    // Rate control extras
    case 'rc-lookahead':    return 'Rate Control Lookahead (frames)';
    case 'mbtree':          return 'MB-Tree Rate Control';
    case 'aq-mode':         return 'Adaptive Quantization Mode';
    case 'aq-strength':     return 'Adaptive Quantization Strength';
    case 'qmin':            return 'Min Quantizer';
    case 'qmax':            return 'Max Quantizer';
    case 'qcomp':           return 'QP Compression';
    case 'qblur':           return 'QP Blur';
    case 'qdiff':           return 'Max QP Step';
    case 'crf_max':
    case 'crf-max':         return 'Max CRF (CRF+VBV cap)';
    case 'vbv-maxrate':     return 'VBV Max Rate (kbps)';
    case 'vbv-bufsize':     return 'VBV Buffer Size (kbit)';
    case 'vbv-init':        return 'VBV Initial Occupancy';
    case 'ipratio':         return 'I/P Frame QP Ratio';
    case 'pbratio':         return 'P/B Frame QP Ratio';
    case 'chroma-qp-offset':
    case 'chromaoffset':    return 'Chroma QP Offset';
    // Psychovisual
    case 'psy':             return 'Psychovisual Optimisation';
    case 'psy-rd':          return 'Psychovisual Rate-Distortion';
    case 'noise-reduction': return 'Noise Reduction';
    // Prediction / Motion
    case 'wpredp':          return 'Weighted Prediction (P-frames)';
    case 'weightb':         return 'Weighted Prediction (B-frames)';
    case 'mixed-refs':      return 'Mixed Reference Frames';
    case 'fast-pskip':      return 'Fast P-Skip';
    case 'b-adapt':
    case 'b_adapt':         return 'Adaptive B-Frame Decision';
    case 'b-bias':
    case 'b_bias':          return 'B-Frame Bias';
    case 'b-pyramid':
    case 'b_pyramid':       return 'B-Frame Pyramid';
    case 'direct-pred':
    case 'directpred':      return 'Direct MV Prediction';
    // Deblock
    case 'deblock':         return 'Deblock Filter (alpha:beta)';
    // Transform
    case '8x8dct':          return '8×8 DCT Transform';
    case 'dct-decimate':
    case 'dct_decimate':    return 'DCT Decimate';
    // Threading / slicing
    case 'threads':         return 'Threads';
    case 'thread_type':     return 'Thread Type';
    case 'slices':          return 'Slices per Frame';
    case 'slice-max-size':  return 'Max Slice Size (bytes)';
    case 'slice-max-mbs':   return 'Max Slice Size (MBs)';
    case 'slice-min-mbs':   return 'Min Slice Size (MBs)';
    case 'sliced-threads':  return 'Sliced Threads';
    // Colour
    case 'pix_fmt':         return 'Pixel Format';
    case 'colorspace':      return 'Colour Space';
    case 'color_range':     return 'Colour Range';
    case 'color_primaries': return 'Colour Primaries';
    case 'color_trc':       return 'Transfer Characteristics';
    // Misc
    case 'aud':             return 'Access Unit Delimiters (Blu-ray)';
    case 'udu_sei':         return 'User-Data Unregistered SEI';
    case 'open-gop':        return 'Open GOP';
    case 'constrained-intra': return 'Constrained Intra Prediction';
    case 'cabac':           return 'CABAC Entropy Coding';
    case 'ssim':            return 'SSIM Metric Logging';
    case 'psnr':            return 'PSNR Metric Logging';
    case 'partitions':      return 'Partitions';
    case 'subme':           return 'Sub-Pixel Motion Estimation';
    case 'me_range':
    case 'merange':         return 'Motion Estimation Range';
    case 'me_method':
    case 'me':              return 'Motion Estimation Method';
    case 'nr':              return 'Noise Reduction';
    case 'trellis':         return 'Trellis Quantization';
    case 'sync-lookahead':  return 'Sync Lookahead';
    case 'rc-refcnt':
    case 'rc_ref':          return 'Rate Control Reference';
    case 'passlogfile':     return 'Two-Pass Logfile';
    default: return name;
  }
}

/**
 * Override libavcodec's terse / FFmpeg-jargon AVOption help string with
 * a clearer, user-facing description for specific (codec, option)
 * pairs. Returns undefined when no override exists (the caller falls
 * back to option.help unchanged). Keyed first by codec, then by the
 * raw AVOption name (NOT the codec-native CLI flag), so the lookup
 * matches what libavutil reports.
 */
function helpOverride(codec: string, optionName: string): string | undefined {
  const c = codec.toLowerCase();
  const map = HELP_OVERRIDES[c];
  return map ? map[optionName] : undefined;
}

const HELP_OVERRIDES: Record<string, Record<string, string>> = {
  libx264: {
    'intra-refresh': "Creates partial Intra Frames at regular intervals. Don't use this unless you know you need it.",
    intra_refresh:   "Creates partial Intra Frames at regular intervals. Don't use this unless you know you need it.",
    refs:            'Reference frames to consider for inter-frame prediction.',
  },
  libx265: {
    'intra-refresh': "Creates partial Intra Frames at regular intervals. Don't use this unless you know you need it.",
    intra_refresh:   "Creates partial Intra Frames at regular intervals. Don't use this unless you know you need it.",
    refs:            'Reference frames to consider for inter-frame prediction.',
  },
};

/**
 * Translate FFmpeg's generic AVCodecContext / AVOption names into the
 * codec-native CLI names that x264 / x265 / svt-av1 / etc. document, so
 * the Inspector reads as if the user were invoking the encoder directly
 * rather than via the FFmpeg CLI. Returns undefined when the option has
 * no codec-native rename (in which case the caller falls back to the
 * AVOption name unchanged). The serialised params dict is unaffected —
 * we still write `b`, `g`, `bf`, ... as keys; only the user-visible
 * label changes.
 */
function codecNativeName(codec: string, optionName: string): string | undefined {
  const c = codec.toLowerCase();
  if (c === 'libx264' || c === 'libx264rgb') {
    const m = X264_NATIVE[optionName];
    if (m) return m;
  }
  if (c === 'libx265') {
    const m = X265_NATIVE[optionName];
    if (m) return m;
  }
  return undefined;
}

// FFmpeg generic AVOption -> x264 CLI flag.
// Source: x264 --fullhelp and libavcodec/libx264.c option mappings.
const X264_NATIVE: Record<string, string> = {
  b:              '--bitrate',
  g:              '--keyint',
  keyint_min:     '--min-keyint',
  bf:             '--bframes',
  refs:           '--ref',
  b_strategy:     '--b-adapt',
  sc_threshold:   '--scenecut',
  qmin:           '--qpmin',
  qmax:           '--qpmax',
  qdiff:          '--qpstep',
  qcomp:          '--qcomp',
  qblur:          '--qblur',
  crf:            '--crf',
  qp:             '--qp',
  maxrate:        '--vbv-maxrate',
  bufsize:        '--vbv-bufsize',
  level:          '--level',
  profile:        '--profile',
  'intra-refresh':'--intra-refresh',
  intra_refresh:  '--intra-refresh',
  trellis:        '--trellis',
  partitions:     '--partitions',
  me_method:      '--me',
  me:             '--me',
  me_range:       '--merange',
  merange:        '--merange',
  subq:           '--subme',
  subme:          '--subme',
  cabac:          '--cabac',
  'aq-mode':      '--aq-mode',
  'aq-strength':  '--aq-strength',
  'rc-lookahead': '--rc-lookahead',
  mbtree:         '--mbtree',
  psy:            '--psy',
  'psy-rd':       '--psy-rd',
  weightb:        '--weightb',
  wpredp:         '--weightp',
  'mixed-refs':   '--mixed-refs',
  'fast-pskip':   '--no-fast-pskip',
  '8x8dct':       '--8x8dct',
  'b-pyramid':    '--b-pyramid',
  'b-bias':       '--b-bias',
  'direct-pred':  '--direct',
  deblock:        '--deblock',
  slices:         '--slices',
  'crf-max':      '--crf-max',
  ipratio:        '--ipratio',
  pbratio:        '--pbratio',
  chromaoffset:   '--chroma-qp-offset',
  'chroma-qp-offset': '--chroma-qp-offset',
  threads:        '--threads',
  'sliced-threads': '--sliced-threads',
};

// FFmpeg generic AVOption -> x265 CLI flag.
// Source: x265 --help and libavcodec/libx265.c.
const X265_NATIVE: Record<string, string> = {
  b:              '--bitrate',
  g:              '--keyint',
  keyint_min:     '--min-keyint',
  bf:             '--bframes',
  refs:           '--ref',
  b_strategy:     '--b-adapt',
  sc_threshold:   '--scenecut',
  qmin:           '--qpmin',
  qmax:           '--qpmax',
  qcomp:          '--qcomp',
  crf:            '--crf',
  qp:             '--qp',
  maxrate:        '--vbv-maxrate',
  bufsize:        '--vbv-bufsize',
  level:          '--level-idc',
  profile:        '--profile',
  tier:           '--high-tier',
  'rc-lookahead': '--rc-lookahead',
  'aq-mode':      '--aq-mode',
  'aq-strength':  '--aq-strength',
  'crf-max':      '--crf-max',
  ipratio:        '--ipratio',
  pbratio:        '--pbratio',
  ssim:           '--ssim',
  psnr:           '--psnr',
  'open-gop':     '--open-gop',
  threads:        '--pools',
  'no-scenecut':  '--no-scenecut',
};
