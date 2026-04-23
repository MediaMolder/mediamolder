import { useEffect, useMemo, useState } from 'react';
import type { PaletteEntry } from '../lib/spawn';

const FALLBACK: PaletteEntry[] = [
  { category: 'Sources', type: 'input', name: 'Input', description: 'File or URL source' },
  { category: 'Sinks', type: 'output', name: 'Output', description: 'File or URL sink' },
];

interface CatalogEntry {
  category: string;
  type: string;
  name: string;
  description?: string;
  streams?: string[];
  num_inputs?: number;
  num_outputs?: number;
}

export function Palette() {
  const [entries, setEntries] = useState<PaletteEntry[]>(FALLBACK);
  const [filter, setFilter] = useState('');
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

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

  const grouped = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const out: Record<string, PaletteEntry[]> = {};
    for (const e of entries) {
      if (q && !e.name.toLowerCase().includes(q) && !(e.description ?? '').toLowerCase().includes(q)) continue;
      (out[e.category] ||= []).push(e);
    }
    return out;
  }, [entries, filter]);

  return (
    <aside className="palette">
      <input
        className="palette-search"
        type="text"
        placeholder="Search nodes…"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />
      {Object.entries(grouped).map(([cat, items]) => {
        const isCollapsed = collapsed[cat];
        return (
          <section key={cat}>
            <h3
              onClick={() => setCollapsed((c) => ({ ...c, [cat]: !c[cat] }))}
              style={{ cursor: 'pointer', userSelect: 'none' }}
            >
              {isCollapsed ? '▸' : '▾'} {cat}{' '}
              <span style={{ color: 'var(--text-dim)', fontWeight: 400 }}>({items.length})</span>
            </h3>
            {!isCollapsed &&
              items.slice(0, 200).map((item) => (
                <div
                  key={`${item.category}/${item.type}/${item.name}`}
                  className="palette-item"
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData('application/x-mm-palette', JSON.stringify(item));
                    e.dataTransfer.effectAllowed = 'copy';
                  }}
                  title={item.description || item.name}
                >
                  {item.name}
                </div>
              ))}
            {!isCollapsed && items.length > 200 && (
              <div style={{ color: 'var(--text-dim)', fontSize: 11, padding: '4px 0' }}>
                {items.length - 200} more — use search to narrow.
              </div>
            )}
          </section>
        );
      })}
    </aside>
  );
}
