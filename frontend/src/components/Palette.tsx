interface PaletteItem {
  category: string;
  label: string;
  hint: string;
}

const ITEMS: PaletteItem[] = [
  { category: 'Sources', label: 'Input', hint: 'File / URL source' },
  { category: 'Filters', label: 'scale', hint: 'Resize video' },
  { category: 'Filters', label: 'fps', hint: 'Change framerate' },
  { category: 'Filters', label: 'crop', hint: 'Crop video' },
  { category: 'Filters', label: 'overlay', hint: 'Composite video' },
  { category: 'Filters', label: 'volume', hint: 'Audio gain' },
  { category: 'Encoders', label: 'libx264', hint: 'H.264 encoder' },
  { category: 'Encoders', label: 'libx265', hint: 'HEVC encoder' },
  { category: 'Encoders', label: 'aac', hint: 'AAC audio encoder' },
  { category: 'Processors', label: 'go_processor', hint: 'Custom Go node' },
  { category: 'Sinks', label: 'Output', hint: 'File / URL sink' },
];

export function Palette() {
  const grouped = ITEMS.reduce<Record<string, PaletteItem[]>>((acc, item) => {
    (acc[item.category] ||= []).push(item);
    return acc;
  }, {});

  return (
    <aside className="palette">
      {Object.entries(grouped).map(([cat, items]) => (
        <section key={cat}>
          <h3>{cat}</h3>
          {items.map((item) => (
            <div
              key={item.label}
              className="palette-item"
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData('application/x-mm-palette', JSON.stringify(item));
                e.dataTransfer.effectAllowed = 'move';
              }}
              title={item.hint}
            >
              {item.label}
            </div>
          ))}
        </section>
      ))}
      <div style={{ marginTop: 16, fontSize: 11, color: 'var(--text-dim)' }}>
        Drag-to-canvas wired in Phase 2.
      </div>
    </aside>
  );
}
