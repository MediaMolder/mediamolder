// Bottom-right floating legend. Always visible so users can identify which
// colour means which stream type at a glance.
export function Legend() {
  return (
    <div className="legend">
      <div className="legend-title">Stream types</div>
      <div className="legend-row"><span className="legend-swatch" style={{ background: 'var(--video)' }} /> Video</div>
      <div className="legend-row"><span className="legend-swatch" style={{ background: 'var(--audio)' }} /> Audio</div>
      <div className="legend-row"><span className="legend-swatch" style={{ background: 'var(--subtitle)' }} /> Subtitle</div>
      <div className="legend-row"><span className="legend-swatch" style={{ background: 'var(--data)' }} /> Data</div>
    </div>
  );
}
