// Inline value control for a single AVOption. Renders the right input
// widget for the option's type and (when present) its enum constants.
//
// Value type contract:
//   - For enum (constants present): value is the *string name* of the
//     selected constant (matching pipeline JSON conventions like
//     {"preset":"slow"} rather than {"preset":3}). Empty string ⇒ unset.
//   - For numeric / string: value is a string (raw user input).
//   - For bool: 'true' | 'false' | '' (unset).

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

  if (isEnum(option)) {
    return (
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">(default)</option>
        {option.constants!.map((c) => (
          <option key={c.name} value={c.name} title={c.help}>
            {c.name}
          </option>
        ))}
      </select>
    );
  }

  if (option.type === 'bool') {
    return (
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">(default)</option>
        <option value="true">true</option>
        <option value="false">false</option>
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
    const isFloat = option.type === 'float' || option.type === 'double';
    return (
      <input
        type="number"
        step={isFloat ? 'any' : 1}
        min={Number.isFinite(option.min) ? option.min : undefined}
        max={Number.isFinite(option.max) ? option.max : undefined}
        value={value}
        placeholder={defaultDisplay(option)}
        onChange={(e) => onChange(e.target.value)}
      />
    );
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

/** Render an option's default value for placeholder text.
 *  Negative int defaults are FFmpeg sentinels (AV_PROFILE_UNKNOWN=-99,
 *  AV_LEVEL_UNKNOWN=-99, or -1 meaning "let the encoder decide") and are
 *  never useful to display — the effectivePresetDefault table supplies the
 *  real value for preset-controlled options. */
export function defaultDisplay(option: EncoderOption): string {
  const d = option.default;
  if (!d) return '';
  if (d.string !== undefined) return d.string;
  if (d.int !== undefined) return d.int < 0 ? '' : String(d.int);
  if (d.float !== undefined) return String(d.float);
  if (d.num_den) return `${d.num_den[0]}/${d.num_den[1]}`;
  return '';
}
