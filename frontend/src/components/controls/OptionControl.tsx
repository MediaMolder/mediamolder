// Inline value control for a single AVOption. Renders the right input
// widget for the option's type and (when present) its enum constants.
//
// Value type contract:
//   - For enum (constants present): value is the *string name* of the
//     selected constant (matching pipeline JSON conventions like
//     {"preset":"slow"} rather than {"preset":3}). Empty string ⇒ unset.
//   - For numeric / string: value is a string (raw user input).
//   - For bool: 'true' | 'false' | '' (unset).

import { useEffect, useState } from 'react';
import type { EncoderOption } from '../../lib/encoderSchema';
import { ExpressionInput } from './ExpressionInput';

export interface OptionControlProps {
  option: EncoderOption;
  value: string; // always a string in the params record
  onChange: (next: string) => void;
  /** Filter name; required for expression-typed options so the
   * ExpressionInput can hit /api/filters/{filter}/eval-expression. */
  filter?: string;
}

/** Heuristic: decide whether to render this option as an enum-select. */
export function isEnum(option: EncoderOption): boolean {
  return Array.isArray(option.constants) && option.constants.length > 0;
}

/** AVOptions whose underlying type is `int` with a 0/1 range and named
 * `on=1` / `off=0` constants are semantically boolean even though
 * libavutil exposes them as `int`. Treat them as bool so the UI never
 * renders 0 / 1 number inputs for yes/no questions. */
function isImplicitBool(option: EncoderOption): boolean {
  if (option.type !== 'int' && option.type !== 'int64') return false;
  if (!Array.isArray(option.constants)) return false;
  if (option.min !== 0 || option.max !== 1) return false;
  const names = new Set(option.constants.map((c) => c.name.toLowerCase()));
  return (
    (names.has('on') && names.has('off')) ||
    (names.has('true') && names.has('false')) ||
    (names.has('yes') && names.has('no')) ||
    (names.has('enabled') && names.has('disabled'))
  );
}

export function OptionControl({ option, value, onChange, filter }: OptionControlProps) {
  // Expression-typed (filter, option) pairs (Wave 5 #20). Falls
  // through to plain text when no filter is supplied (e.g. encoder
  // forms — encoders don't currently have expression options).
  if (option.expression && filter) {
    return (
      <ExpressionInput
        filter={filter}
        option={option.name}
        value={value}
        variables={option.variables}
        onChange={onChange}
        placeholder={defaultDisplay(option)}
      />
    );
  }

  if (option.type === 'bool' || isImplicitBool(option)) {
    return <BoolToggle option={option} value={value} onChange={onChange} />;
  }

  if (isEnum(option)) {
    const defConst =
      option.default?.int !== undefined
        ? option.constants!.find((c) => c.value === option.default!.int)?.name
        : option.default?.string;
    // When the default constant is known, display it as the selected entry
    // rather than a separate "(default: X)" sentinel.  Round-trip: picking
    // the default constant from the list emits "" (unset) so the pipeline
    // keeps the encoder's built-in default.
    const selectValue = (!value && defConst) ? defConst : value;
    const handleChange = (next: string) =>
      onChange(defConst && next === defConst ? '' : next);
    return (
      <select value={selectValue} onChange={(e) => handleChange(e.target.value)}>
        {!defConst && <option value="">(not set)</option>}
        {option.constants!.map((c) => (
          <option key={c.name} value={c.name} title={c.help}>
            {c.name}
          </option>
        ))}
      </select>
    );
  }

  if (
    option.type === 'int' ||
    option.type === 'int64' ||
    option.type === 'uint64' ||
    option.type === 'float' ||
    option.type === 'double'
  ) {
    return <NumericInput option={option} value={value} onChange={onChange} />;
  }

  // Strings, rationals, durations, colors, …: plain text input.
  return (
    <input
      type="text"
      value={value}
      placeholder={defaultDisplay(option)}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

/** Boolean control rendered as a segmented toggle: true / false plus a
 * trailing ✕ that clears the override (back to the libavfilter
 * default). Never displays 0 / 1; serialises the strings 'true' /
 * 'false' into def.params (av_opt_set accepts both forms). */
function BoolToggle({
  option,
  value,
  onChange,
}: {
  option: EncoderOption;
  value: string;
  onChange: (next: string) => void;
}) {
  const def = boolDefault(option);
  const norm = normaliseBool(value);
  const effective = norm || def || '';
  const isDefault = norm === '';
  const cls = (which: 'true' | 'false') => {
    let c = 'bool-toggle-opt';
    if (effective === which) c += ' active';
    if (effective === which && isDefault) c += ' default';
    return c;
  };
  return (
    <div className="bool-toggle" role="radiogroup" aria-label={option.name}>
      <button
        type="button"
        role="radio"
        aria-checked={effective === 'true'}
        className={cls('true')}
        onClick={() => onChange('true')}
        title={`Set ${option.name} = true`}
      >
        true
      </button>
      <button
        type="button"
        role="radio"
        aria-checked={effective === 'false'}
        className={cls('false')}
        onClick={() => onChange('false')}
        title={`Set ${option.name} = false`}
      >
        false
      </button>
      <button
        type="button"
        className="bool-toggle-clear"
        onClick={() => onChange('')}
        disabled={value === ''}
        title={`Clear override (default: ${def ?? 'unset'})`}
        aria-label="Clear override"
      >
        ✕
      </button>
    </div>
  );
}

/** Map any of the strings libavutil accepts for a boolean
 * (`true`/`false`, `on`/`off`, `yes`/`no`, `enabled`/`disabled`,
 * `1`/`0`) to canonical `'true'` / `'false'` / `''`. */
function normaliseBool(value: string): 'true' | 'false' | '' {
  const v = value.trim().toLowerCase();
  if (!v) return '';
  if (v === 'true' || v === 'on' || v === 'yes' || v === 'enabled' || v === '1') return 'true';
  if (v === 'false' || v === 'off' || v === 'no' || v === 'disabled' || v === '0') return 'false';
  return '';
}

function boolDefault(option: EncoderOption): 'true' | 'false' | undefined {
  const d = option.default;
  if (!d) return undefined;
  if (d.string !== undefined) {
    const n = normaliseBool(d.string);
    return n === '' ? undefined : n;
  }
  if (d.int !== undefined) {
    if (d.int === 1) return 'true';
    // 0 and the libavutil 'auto' sentinel -1 both behave as 'false'
    // until the user explicitly opts in.
    if (d.int === 0 || d.int === -1) return 'false';
  }
  return undefined;
}

/** Numeric input with commit-on-blur semantics. Integer-typed
 * AVOptions round fractional input to the nearest valid integer
 * (`Math.round` then clamp to `[min, max]`) so a paste like `2.6`
 * into an int field commits as `3`, not as a libavutil parse error
 * downstream. Float / double options accept integers verbatim and
 * leave fractional input untouched. */
function NumericInput({
  option,
  value,
  onChange,
}: {
  option: EncoderOption;
  value: string;
  onChange: (next: string) => void;
}) {
  const isFloat = option.type === 'float' || option.type === 'double';
  const [local, setLocal] = useState(value);
  useEffect(() => setLocal(value), [value]);

  const commit = () => {
    if (local === value) return;
    if (local.trim() === '') {
      onChange('');
      return;
    }
    const n = Number(local);
    if (!Number.isFinite(n)) {
      // Reject non-numeric input by reverting to the saved value.
      setLocal(value);
      return;
    }
    let v = n;
    if (!isFloat) v = Math.round(v);
    if (Number.isFinite(option.min) && v < (option.min as number)) v = option.min as number;
    if (Number.isFinite(option.max) && v > (option.max as number)) v = option.max as number;
    const next = String(v);
    setLocal(next);
    onChange(next);
  };

  return (
    <input
      type="number"
      step={isFloat ? 'any' : 1}
      min={Number.isFinite(option.min) ? option.min : undefined}
      max={Number.isFinite(option.max) ? option.max : undefined}
      value={local}
      placeholder={defaultDisplay(option)}
      onChange={(e) => setLocal(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          (e.currentTarget as HTMLInputElement).blur();
        }
      }}
    />
  );
}

/** Render an option's default value for placeholder text.
 *  Negative int defaults are FFmpeg sentinels (AV_PROFILE_UNKNOWN=-99,
 *  AV_LEVEL_UNKNOWN=-99, or -1 meaning "let the encoder decide") and are
 *  never useful to display — the effectivePresetDefault table supplies the
 *  real value for preset-controlled options. */
export function defaultDisplay(option: EncoderOption): string {
  const d = option.default;
  if (!d) return '';
  if (d.string !== undefined) return d.string;
  if (d.int !== undefined) {
    // For enum options resolve the integer to the matching constant name.
    if (Array.isArray(option.constants) && option.constants.length > 0) {
      const c = option.constants.find((x) => x.value === d.int);
      if (c) return c.name;
    }
    // For bool/implicit-bool convert 0/−1 → 'false', 1 → 'true'.
    if (option.type === 'bool' || isImplicitBool(option)) {
      if (d.int === 1) return 'true';
      if (d.int === 0 || d.int === -1) return 'false';
    }
    return d.int < 0 ? '' : String(d.int);
  }
  if (d.float !== undefined) return String(d.float);
  if (d.num_den) return `${d.num_den[0]}/${d.num_den[1]}`;
  return '';
}
