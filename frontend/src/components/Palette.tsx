import { useEffect, useMemo, useState } from 'react';
import type { PaletteEntry } from '../lib/spawn';
import {
  NAMING_EVENT,
  displayName,
  readNamingMode,
  registerFriendlyNames,
  writeNamingMode,
  type NamingMode,
} from '../lib/friendlyNames';

const FALLBACK: PaletteEntry[] = [
  { category: 'Sources', type: 'input', name: 'Input', label: 'Input file', description: 'File or URL source', common: true },
  { category: 'Sinks', type: 'output', name: 'Output', label: 'Output file', description: 'File or URL sink', common: true },
];

interface CatalogEntry extends PaletteEntry {
  subcategory?: string;
  label?: string;
  description?: string;
}

const PER_SUBCATEGORY_VISIBLE = 50;

type Scope = 'common' | 'all';
const SCOPE_STORAGE_KEY = 'mm.palette.scope';

function readScope(): Scope {
  if (typeof window === 'undefined') return 'common';
  return window.localStorage.getItem(SCOPE_STORAGE_KEY) === 'all' ? 'all' : 'common';
}

export function Palette() {
  const [entries, setEntries] = useState<CatalogEntry[]>(FALLBACK);
  const [filter, setFilter] = useState('');
  const [scope, setScope] = useState<Scope>(readScope);
  const [naming, setNaming] = useState<NamingMode>(readNamingMode);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({
    // Filter subcategories collapsed by default — there are a lot of them.
    'Filters': false,
  });
  const [openSubs, setOpenSubs] = useState<Record<string, boolean>>({});
  const [showAll, setShowAll] = useState<Record<string, boolean>>({});
  // Per-subcategory override: when scope === 'common', the user can
  // click "Show all in this section" to see every entry in just that
  // subcategory without leaving the curated view elsewhere.
  const [forcedAllSubs, setForcedAllSubs] = useState<Set<string>>(new Set());

  useEffect(() => {
    fetch('/api/nodes')
      .then((r) => (r.ok ? r.json() : null))
      .then((list: CatalogEntry[] | null) => {
        if (Array.isArray(list) && list.length) {
          setEntries(list);
          registerFriendlyNames(list);
        }
      })
      .catch(() => {
        /* keep fallback */
      });
  }, []);

  useEffect(() => {
    if (typeof window !== 'undefined') {
      window.localStorage.setItem(SCOPE_STORAGE_KEY, scope);
    }
  }, [scope]);

  useEffect(() => {
    writeNamingMode(naming);
  }, [naming]);

  // Two-level grouping: Category → Subcategory → entries.
  const grouped = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const out: Record<string, Record<string, CatalogEntry[]>> = {};
    for (const e of entries) {
      // Free-text search — match against canonical name, friendly
      // label, description, AND curated aliases ("h264" → libx264).
      if (q) {
        const hay = [
          e.name,
          e.label ?? '',
          e.description ?? '',
          e.friendly_name ?? '',
          ...(e.aliases ?? []),
        ]
          .join(' ')
          .toLowerCase();
        if (!hay.includes(q)) continue;
      }
      const sub = e.subcategory ?? '';
      const subKey = `${e.category}::${sub}`;
      const forced = forcedAllSubs.has(subKey);
      // Scope filter — Common view hides non-curated entries unless
      // either (a) the user is searching, (b) the user clicked
      // "Show all in this section" for this subcategory.
      if (scope === 'common' && !q && !forced && !e.common) continue;

      ((out[e.category] ||= {})[sub] ||= []).push(e);
    }
    return out;
  }, [entries, filter, scope, forcedAllSubs]);

  const isSearching = filter.trim().length > 0;

  return (
    <aside className="palette">
      <div className="palette-toggles">
        <SegmentedToggle
          label="View"
          value={scope}
          options={[
            { value: 'common', label: 'Common' },
            { value: 'all', label: 'All' },
          ]}
          onChange={setScope}
        />
        <SegmentedToggle
          label="Names"
          value={naming}
          options={[
            { value: 'friendly', label: 'Friendly' },
            { value: 'library', label: 'Library' },
          ]}
          onChange={setNaming}
        />
      </div>

      <input
        className="palette-search"
        type="text"
        placeholder="Search nodes…"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />

      {Object.entries(grouped).length === 0 && (
        <div className="empty" style={{ padding: '8px 0' }}>
          No nodes match "{filter}".
        </div>
      )}

      {Object.entries(grouped).map(([cat, subs]) => {
        const catCollapsed = !!collapsed[cat] && !isSearching;
        const total = Object.values(subs).reduce((s, x) => s + x.length, 0);
        return (
          <section key={cat} className="palette-cat">
            <h3
              onClick={() => setCollapsed((c) => ({ ...c, [cat]: !c[cat] }))}
              title="Click to expand or collapse"
            >
              {catCollapsed ? '▸' : '▾'} {cat}
              <span className="cat-count">({total})</span>
            </h3>
            {!catCollapsed &&
              Object.entries(subs).map(([sub, items]) => {
                const subKey = `${cat}::${sub}`;
                const subOpen = isSearching || (sub === '' ? true : !!openSubs[subKey]);
                const showingAll = !!showAll[subKey];
                const visible = showingAll || isSearching ? items : items.slice(0, PER_SUBCATEGORY_VISIBLE);
                const isForced = forcedAllSubs.has(subKey);
                const showSubExpand = scope === 'common' && !isSearching && !isForced && sub !== '';
                return (
                  <div key={subKey} className="palette-sub">
                    {sub !== '' && (
                      <h4
                        onClick={() => setOpenSubs((c) => ({ ...c, [subKey]: !c[subKey] }))}
                        title="Click to expand or collapse"
                      >
                        {subOpen ? '▾' : '▸'} {sub}
                        <span className="sub-count">({items.length})</span>
                      </h4>
                    )}
                    {subOpen &&
                      visible.map((item) => (
                        <PaletteItem
                          key={`${item.category}/${item.subcategory ?? ''}/${item.type}/${item.name}`}
                          item={item}
                          naming={naming}
                        />
                      ))}
                    {subOpen && showSubExpand && (
                      <button
                        className="palette-more"
                        onClick={() => {
                          setForcedAllSubs((s) => {
                            const next = new Set(s);
                            next.add(subKey);
                            return next;
                          });
                        }}
                        title="Show every entry in this section, leaving other sections curated"
                      >
                        Show all in this section…
                      </button>
                    )}
                    {subOpen && !showingAll && !isSearching && items.length > PER_SUBCATEGORY_VISIBLE && (
                      <button
                        className="palette-more"
                        onClick={() => setShowAll((c) => ({ ...c, [subKey]: true }))}
                      >
                        Show {items.length - PER_SUBCATEGORY_VISIBLE} more…
                      </button>
                    )}
                  </div>
                );
              })}
          </section>
        );
      })}

      {scope === 'common' && !isSearching && (
        <div className="palette-footer-hint">
          Showing common nodes — switch to{' '}
          <button className="link" onClick={() => setScope('all')}>All</button> to see every codec and filter.
        </div>
      )}
    </aside>
  );
}

interface SegmentedToggleProps<T extends string> {
  label: string;
  value: T;
  options: Array<{ value: T; label: string }>;
  onChange: (v: T) => void;
}

function SegmentedToggle<T extends string>({ label, value, options, onChange }: SegmentedToggleProps<T>) {
  return (
    <div className="segmented" role="group" aria-label={label}>
      <span className="segmented-label">{label}:</span>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          className={`segmented-opt ${value === o.value ? 'active' : ''}`}
          onClick={() => onChange(o.value)}
          aria-pressed={value === o.value}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function PaletteItem({ item, naming }: { item: CatalogEntry; naming: NamingMode }) {
  // Heading: friendly label in 'friendly' mode (falls back to label/name);
  // canonical libavcodec/libavfilter name on the muted second line. The
  // tooltip always shows the canonical name + the description so the
  // user can identify a node even when its friendly label is generic.
  const display = displayName({ name: item.name, label: item.label, friendly_name: item.friendly_name }, naming);
  const showSub = display !== item.name;
  return (
    <div
      className="palette-item"
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData('application/x-mm-palette', JSON.stringify(item));
        e.dataTransfer.effectAllowed = 'copy';
      }}
      title={item.description ? `${item.name}\n\n${item.description}` : item.name}
    >
      <div className="palette-item-label">{display}</div>
      {showSub && <div className="palette-item-name">{item.name}</div>}
    </div>
  );
}

// Re-exported for tooling/tests that want the canonical event name.
export { NAMING_EVENT };
