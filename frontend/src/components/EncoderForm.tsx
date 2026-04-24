// EncoderForm — typed Inspector form for graph nodes whose `type` is
// "encoder". Loads the option schema for `params.codec` from the backend
// and renders the four primary controls (preset, rate control,
// bit-rate or quality, keyframe interval) when the encoder advertises
// them. The Advanced section / search / raw-options come in PR3.
//
// Editing model: we store every value in `def.params` as a string (the
// canonical params type is `Record<string, unknown>` but the pipeline
// stringifies values when building FFmpeg args, so strings are the
// safest round-trip). Empty string ⇒ remove the key entirely so the
// encoder uses libav's default.

import { useEffect, useState } from 'react';
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
          No primary controls recognised for this encoder. All options will be
          available under Advanced (coming soon).
        </div>
      )}
    </>
  );
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
