import { useEffect, useMemo, useState } from 'react';
import type { PaletteEntry } from '../lib/spawn';

const FALLBACK: PaletteEntry[] = [
  { category: 'Sources', type: 'input', name: 'Input', label: 'Input file', description: 'File or URL source' },
  { category: 'Sinks', type: 'output', name: 'Output', label: 'Output file', description: 'File or URL sink' },
];

interface CatalogEntry extends PaletteEntry {
  subcategory?: string;
  label?: string;
  description?: string;
}

const PER_SUBCATEGORY_VISIBLE = 50;

export function Palette() {
  const [entries, setEntries] = useState<CatalogEntry[]>(FALLBACK);
  const [filter, setFilter] = useState('');
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({
    // Filter subcategories collapsed by default — there are a lot of them.
    'Filters': false,
  });
  const [openSubs, setOpenSubs] = useState<Record<string, boolean>>({});
  const [showAll, setShowAll] = useState<Record<string, boolean>>({});

  useEffect(() => {
    fetch('/api/nodes')
      .then((r) => (r.ok ? r.json() : null))
      .then((list: CatalogEntry[] | null) => {
        if (Array.isArray(list) && list.length) setEntries(list);
      })
      .catch(() => {
        /* keep fallback */
      });
  }, []);

  // Two-level grouping: Category → Subcategory → entries.
  const grouped = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const out: Record<string, Record<string, CatalogEntry[]>> = {};
    for (const e of entries) {
      const hay = `${e.name} ${e.label ?? ''} ${e.description ?? ''}`.toLowerCase();
      if (q && !hay.includes(q)) continue;
      const cat = e.category;
      const sub = e.subcategory ?? '';
      ((out[cat] ||= {})[sub] ||= []).push(e);
    }
    return out;
  }, [entries, filter]);

  const isSearching = filter.trim().length > 0;

  return (
    <aside className="palette">
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
                        <PaletteItem key={`${item.category}/${item.subcategory ?? ''}/${item.type}/${item.name}`} item={item} />
                      ))}
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
    </aside>
  );
}

function PaletteItem({ item }: { item: CatalogEntry }) {
  // Show the friendly label as the primary text; the canonical name as a
  // secondary muted line; the description as a tooltip on hover.
  const display = item.label ?? item.name;
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
      {item.label && item.label !== item.name && (
        <div className="palette-item-name">{item.name}</div>
      )}
    </div>
  );
}
