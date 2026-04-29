// FilterForm — typed Inspector form for graph nodes whose `type` is
// "filter". Loads the option schema for `def.filter` from the backend
// and renders one labelled control per AVOption, with type-aware
// widgets (number inputs with min/max for numerics, bool selects, and
// dropdowns for options that declare named constants — e.g. trim's
// units enum, scale's flags).
//
// Editing model matches EncoderForm: every value is stored as a string
// in `def.params`. Empty string ⇒ remove the key entirely so the
// filter falls back to libavfilter's built-in default.

import { useEffect, useMemo, useState } from 'react';
import type { NodeDef } from '../lib/jobTypes';
import { fetchFilterInfo, type FilterOption, type FilterOptionsInfo } from '../lib/filterSchema';
import { OptionControl, defaultDisplay } from './controls/OptionControl';

interface Props {
  def: NodeDef;
  onChange: (next: NodeDef) => void;
}

export function FilterForm({ def, onChange }: Props) {
  const filter = def.filter ?? '';
  const [info, setInfo] = useState<FilterOptionsInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState('');

  useEffect(() => {
    if (!filter) {
      setInfo(null);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    let cancelled = false;
    fetchFilterInfo(filter)
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
  }, [filter]);

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

  const visible = useMemo(() => {
    if (!info) return [];
    const q = search.trim().toLowerCase();
    if (!q) return info.options;
    return info.options.filter(
      (o) =>
        o.name.toLowerCase().includes(q) ||
        (o.help ?? '').toLowerCase().includes(q),
    );
  }, [info, search]);

  if (!filter) {
    return (
      <div className="empty" style={{ fontSize: 11, marginTop: 6 }}>
        Set the <code>Filter</code> name above to choose a libavfilter filter
        (e.g. <code>scale</code>, <code>trim</code>).
      </div>
    );
  }
  if (loading) {
    return <div className="empty">Loading {filter} options…</div>;
  }
  if (error) {
    return (
      <div className="empty" style={{ color: '#f5b7b1', fontSize: 11 }}>
        Failed to load {filter}: {error}
      </div>
    );
  }
  if (!info) return null;

  return (
    <>
      <div className="encoder-form-header" style={{ marginTop: 4, marginBottom: 8 }}>
        <strong>{info.name}</strong>
        {info.description && (
          <div className="empty" style={{ fontSize: 11, marginTop: 2 }}>
            {info.description}
          </div>
        )}
      </div>

      {info.options.length === 0 ? (
        <div className="empty" style={{ fontSize: 11 }}>
          This filter has no configurable options.
        </div>
      ) : (
        <>
          {info.options.length > 6 && (
            <input
              type="search"
              placeholder="Search options…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              style={{ marginBottom: 8 }}
            />
          )}
          {visible.length === 0 && (
            <div className="empty" style={{ fontSize: 11 }}>
              No options match “{search}”.
            </div>
          )}
          {visible.map((opt) => (
            <OptionRow
              key={opt.name}
              option={opt}
              value={getParam(opt.name)}
              onChange={(v) => setParam(opt.name, v)}
            />
          ))}
        </>
      )}
    </>
  );
}

/* ---------- Single labelled option row ---------- */

function OptionRow({
  option,
  value,
  onChange,
}: {
  option: FilterOption;
  value: string;
  onChange: (next: string) => void;
}) {
  const def = defaultDisplay(option);
  const range = rangeHint(option);
  const meta = [option.type, def && `default ${def}`, range].filter(Boolean).join(' · ');
  return (
    <div style={{ marginBottom: 8 }}>
      <label
        style={{ display: 'block', marginBottom: 2 }}
        title={option.help}
      >
        {option.name}
      </label>
      <OptionControl option={option} value={value} onChange={onChange} />
      {meta && (
        <div className="empty" style={{ fontSize: 10, marginTop: 2 }}>
          {meta}
        </div>
      )}
      {option.help && (
        <div className="empty" style={{ fontSize: 10, marginTop: 2 }}>
          {option.help}
        </div>
      )}
    </div>
  );
}

/** Format the option's numeric range for display, suppressing libav's
 * common -INF / +INF sentinels (INT_MIN, INT_MAX, FLT_MAX, ...). */
function rangeHint(option: FilterOption): string {
  const sane = (n: number | undefined): n is number =>
    typeof n === 'number' && Number.isFinite(n) && Math.abs(n) < 1e15;
  const lo = sane(option.min) ? option.min : undefined;
  const hi = sane(option.max) ? option.max : undefined;
  if (lo !== undefined && hi !== undefined) return `${lo}–${hi}`;
  if (hi !== undefined) return `≤ ${hi}`;
  if (lo !== undefined) return `≥ ${lo}`;
  return '';
}
