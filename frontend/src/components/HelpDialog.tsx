interface Props {
  open: boolean;
  onClose: () => void;
}

export function HelpDialog({ open, onClose }: Props) {
  if (!open) return null;
  return (
    <div className="dialog-overlay" onClick={onClose}>
      <div className="dialog dialog-help" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-header">
          <h3>How to use MediaMolder</h3>
          <button onClick={onClose}>×</button>
        </div>

        <div className="help-content">
          <h4>1. Build a pipeline</h4>
          <ol>
            <li>
              <strong>Drag from the palette</strong> on the left onto the canvas. Use the
              search box or expand a category (Sources, Filters, Encoders, Processors,
              Sinks). Hover any palette item to read its full description.
            </li>
            <li>
              <strong>Connect nodes</strong> by dragging from a coloured handle on one
              node's right edge to a matching handle on another node's left edge. Handle
              colour identifies the stream type (see legend in the bottom-right corner).
            </li>
            <li>
              <strong>Configure each node</strong> by selecting it. The Inspector on the
              right shows its properties. Input and Output nodes have a <em>Browse…</em>
              button for picking files from your local machine.
            </li>
          </ol>

          <h4>2. Load examples or import existing JSON</h4>
          <ul>
            <li>
              Pick a sample from the <strong>Example</strong> dropdown to learn from
              working pipelines.
            </li>
            <li>
              Use <strong>Import</strong> to load a job JSON file from disk. Use{' '}
              <strong>Export</strong> to save the current graph as JSON (compatible with
              <code>mediamolder run job.json</code>).
            </li>
            <li>
              <strong>Auto layout</strong> rearranges nodes left-to-right using dagre.
            </li>
          </ul>

          <h4>3. Run and monitor</h4>
          <ol>
            <li>
              Click <strong>Run</strong>. The graph is sent to the backend and a live
              event stream begins.
            </li>
            <li>
              Each node displays live frame counts and FPS. Nodes that report errors get
              a red border.
            </li>
            <li>
              The <strong>Run panel</strong> (bottom-right when "Show log" is on) lists
              per-node metrics, errors, and log entries. <strong>Stop</strong> cancels
              the run cleanly.
            </li>
          </ol>

          <h4>Stream-type colour legend</h4>
          <ul className="legend-list">
            <li><span className="legend-swatch" style={{ background: 'var(--video)' }} /> Video</li>
            <li><span className="legend-swatch" style={{ background: 'var(--audio)' }} /> Audio</li>
            <li><span className="legend-swatch" style={{ background: 'var(--subtitle)' }} /> Subtitle</li>
            <li><span className="legend-swatch" style={{ background: 'var(--data)' }} /> Data</li>
          </ul>

          <h4>Keyboard shortcuts</h4>
          <ul>
            <li><kbd>Backspace</kbd> / <kbd>Delete</kbd> — remove the selected node</li>
            <li><kbd>?</kbd> — open this help dialog</li>
            <li><kbd>Esc</kbd> — close any open dialog</li>
          </ul>

          <h4>Tips</h4>
          <ul>
            <li>
              Connections are validated by stream type. If a drag won't "stick", the
              source and target handles are different colours.
            </li>
            <li>
              Node positions are saved into the exported JSON under{' '}
              <code>graph.ui.positions</code> (schema v1.2). The runtime ignores this
              block — it's metadata for the editor.
            </li>
            <li>
              Multi-input filters (overlay, split, ...) are not yet draggable from the
              palette — open the JSON editor or import an example to use them.
            </li>
          </ul>

          <p className="hint">
            For the full developer guide and HTTP API reference, see{' '}
            <code>docs/gui.md</code>.
          </p>
        </div>

        <div className="dialog-footer">
          <div className="spacer" />
          <button className="primary" onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}
