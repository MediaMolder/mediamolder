// Bottom-centre floating legend. Always visible so users can identify which
// colour means which stream type at a glance. Laid out horizontally so it
// stays clear of the bottom-right minimap.
export function Legend() {
  return (
    <div className="legend">
      <span className="legend-title">Stream types</span>
      <span className="legend-row"><span className="legend-swatch" style={{ background: 'var(--video)' }} /> Video</span>
      <span className="legend-row"><span className="legend-swatch" style={{ background: 'var(--audio)' }} /> Audio</span>
      <span className="legend-row"><span className="legend-swatch" style={{ background: 'var(--subtitle)' }} /> Subtitle</span>
      <span className="legend-row"><span className="legend-swatch" style={{ background: 'var(--data)' }} /> Data</span>
    </div>
  );
}
