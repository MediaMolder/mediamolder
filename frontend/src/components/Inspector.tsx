import type { FlowNode } from '../lib/jsonAdapter';

interface Props {
  node: FlowNode | null;
}

export function Inspector({ node }: Props) {
  if (!node) {
    return (
      <div className="inspector">
        <h3>Inspector</h3>
        <div className="empty">Select a node to view its properties.</div>
      </div>
    );
  }
  const ref = node.data.ref;
  const json = JSON.stringify(
    ref.kind === 'node' ? ref.def : ref.def,
    null,
    2,
  );
  return (
    <div className="inspector">
      <h3>{node.data.label}</h3>
      <div className="mm-node-type" style={{ marginBottom: 8 }}>{node.data.kind}</div>
      <label>Definition (read-only in MVP)</label>
      <textarea readOnly value={json} style={{ minHeight: 280 }} />
    </div>
  );
}
