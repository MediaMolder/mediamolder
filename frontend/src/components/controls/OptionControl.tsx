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

export interface OptionControlProps {
  option: EncoderOption;
  value: string; // always a string in the params record
  onChange: (next: string) => void;
}

/** Heuristic: decide whether to render this option as an enum-select. */
export function isEnum(option: EncoderOption): boolean {
  return Array.isArray(option.constants) && option.constants.length > 0;
}

export function OptionControl({ option, value, onChange }: OptionControlProps) {
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

/** Render an option's default value for placeholder text. */
export function defaultDisplay(option: EncoderOption): string {
  const d = option.default;
  if (!d) return '';
  if (d.string !== undefined) return d.string;
  if (d.int !== undefined) return String(d.int);
  if (d.float !== undefined) return String(d.float);
  if (d.num_den) return `${d.num_den[0]}/${d.num_den[1]}`;
  return '';
}
