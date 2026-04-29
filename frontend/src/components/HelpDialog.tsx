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
          <h4>1. Build a media processing graph (pipeline)</h4>
          <ol>
            <li>
              <strong>Drag processing libraries from the palette</strong> on the left 
			  onto the canvas to add nodes to your media processing graph. Use the
              search box or expand a category (Sources, Filters, Encoders, Processors,
              Sinks). Hover over any library to read its full description.
            </li>
            <li>
              <strong>Connect nodes</strong> by dragging from a coloured handle on one
              node's right edge to a matching handle on another node's left edge. Handle
              colour identifies the stream type (see legend at the bottom center).
              Connections are colour-coded by stream type but carry no inline
              label. Hover (or click to pin) any connection to open a popover
              listing every technical attribute MediaMolder can infer for the
              stream (size, pix_fmt, frame rate, color space, codec profile,
              bit rate, sample rate, channel layout, …)
              and which node established each value.
            </li>
            <li>
              <strong>Configure each node</strong> by selecting it. The Inspector panel 
			  on the right shows its properties. Input and Output nodes have a 
			  <em>Browse…</em> button for picking files from your local machine.
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
              Each node displays live packet counts and FPS. Nodes that report errors get
              a red border.
            </li>
            <li>
              The <strong>Run panel</strong> (bottom-right when "Show log" is on) lists
              per-node metrics, errors, and log entries. <strong>Stop</strong> cancels
              the run cleanly.
            </li>
          </ol>

          <h4>Edge attribute popover</h4>
          <p>
            Hover any connection — or click it to pin the popover open — to
            see every known technical attribute for that stream
            (<code>width</code>, <code>height</code>, <code>pix_fmt</code>,
            <code>frame_rate</code>, <code>color_space</code>,
            <code>color_range</code>, <code>bit_depth</code>,
            <code>sample_rate</code>, <code>channel_layout</code>,
            <code>sample_fmt</code>, <code>codec</code>,
            <code>profile</code>, <code>bit_rate</code>, …).
            The values are inferred by walking upstream from the edge: the closest
            node whose parameters constrain a given attribute wins. Pass-through
            nodes (e.g. <code>setpts</code>, <code>drawtext</code>) leave the
            attribute unchanged so it propagates from earlier in the chain.
            Attributes that no upstream node has set are simply omitted — the
            popover never guesses.
          </p>
          <p>
            Click <strong>Get properties</strong> on any Input node in the
            Inspector to probe the source file with libavformat. The probed
            stream metadata (codec, pix_fmt, frame rate, sample rate, channel
            layout, …) is then used as the seed for downstream attribute
            inference, so every connection in the graph displays accurate
            technical properties without you having to type them.
          </p>

          <h4>Editing connections</h4>
          <ul>
            <li>
              <strong>Create:</strong> drag from a coloured handle on one node's
              right edge to a matching handle on another node's left edge.
              Stream types must match (video→video, audio→audio, …).
            </li>
            <li>
              <strong>Select:</strong> click a connection. The selected edge is
              drawn thicker with a glow so it's easy to see.
            </li>
            <li>
              <strong>Delete:</strong> select an edge and press
              <kbd>Backspace</kbd> or <kbd>Delete</kbd>, or click the
              <strong>Delete</strong> button in the edge popover. Multiple
              edges can be selected (drag-select on empty canvas, or
              shift-click) and deleted at once.
            </li>
            <li>
              <strong>Re-route:</strong> grab the endpoint of an existing
              connection and drag it to a different handle. Drop it on empty
              canvas to discard the connection.
            </li>
          </ul>

          <h4>Configuring encoders</h4>
          <p>
            Selecting an <strong>Encoder</strong> node loads its option schema
            from libavcodec and renders the most common controls — Preset,
            Rate control, Quality (CRF/CQ/Q) or Bit rate (whichever applies),
            and Keyframe interval — as typed inputs with the codec's own
            ranges, defaults, and named values (e.g. <code>preset</code>{' '}
            offers a dropdown of <code>ultrafast</code>, <code>fast</code>,
            <code>medium</code>, …). Leaving a field blank uses libav's
            default.
          </p>
          <p>
            Below the primary controls, a <strong>Raw options</strong>{' '}
            section surfaces codec-native parameter strings (
            <code>x264-params</code>, <code>x265-params</code>,
            <code>svtav1-params</code>, <code>aom-params</code>,
            <code>vpx-params</code>) for power users who want to pass
            <code>key=value:key=value</code> blobs through verbatim. Every
            other option lives under the <strong>Advanced</strong>{' '}
            collapsible, grouped (Threading, Quality, Color, Motion, Profile/
            Level, GOP &amp; frames, Other) and filterable through a
            search box that matches against option name and help text.
          </p>

          <h4>Stream-type colour legend</h4>
          <ul className="legend-list">
            <li><span className="legend-swatch" style={{ background: 'var(--video)' }} /> Video</li>
            <li><span className="legend-swatch" style={{ background: 'var(--audio)' }} /> Audio</li>
            <li><span className="legend-swatch" style={{ background: 'var(--subtitle)' }} /> Subtitle</li>
            <li><span className="legend-swatch" style={{ background: 'var(--data)' }} /> Data</li>
          </ul>

          <h4>Keyboard shortcuts</h4>
          <ul>
            <li><kbd>Backspace</kbd> / <kbd>Delete</kbd> — remove the selected node or connection</li>
            <li><kbd>?</kbd> or the <strong>Help</strong> toolbar button — open this help dialog</li>
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
