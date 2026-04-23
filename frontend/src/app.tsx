import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  applyEdgeChanges,
  applyNodeChanges,
  type EdgeChange,
  type NodeChange,
  type OnSelectionChangeParams,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';

import { Palette } from './components/Palette';
import { Inspector } from './components/Inspector';
import { MMNode } from './components/MMNode';
import {
  configToFlow,
  flowToConfig,
  type FlowEdge,
  type FlowNode,
} from './lib/jsonAdapter';
import type { JobConfig } from './lib/jobTypes';

const NODE_TYPES = { mmNode: MMNode };

interface ExampleEntry {
  name: string;
  url: string;
}

export default function App() {
  return (
    <ReactFlowProvider>
      <Editor />
    </ReactFlowProvider>
  );
}

function Editor() {
  const [examples, setExamples] = useState<ExampleEntry[]>([]);
  const [selectedExample, setSelectedExample] = useState<string>('');
  const [job, setJob] = useState<JobConfig | null>(null);
  const [nodes, setNodes] = useState<FlowNode[]>([]);
  const [edges, setEdges] = useState<FlowEdge[]>([]);
  const [selected, setSelected] = useState<FlowNode | null>(null);

  // Load list of bundled examples from backend.
  useEffect(() => {
    fetch('/api/examples')
      .then((r) => (r.ok ? r.json() : []))
      .then((list: ExampleEntry[]) => {
        setExamples(list);
        if (list.length && !selectedExample) {
          setSelectedExample(list[0].url);
        }
      })
      .catch(() => setExamples([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Load selected example.
  useEffect(() => {
    if (!selectedExample) return;
    fetch(selectedExample)
      .then((r) => r.json())
      .then((cfg: JobConfig) => loadJob(cfg))
      .catch((err) => console.error('failed to load example', err));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedExample]);

  const loadJob = useCallback((cfg: JobConfig) => {
    const { nodes: n, edges: e } = configToFlow(cfg);
    setJob(cfg);
    setNodes(n);
    setEdges(e);
    setSelected(null);
  }, []);

  const onNodesChange = useCallback(
    (changes: NodeChange[]) => setNodes((ns) => applyNodeChanges(changes, ns) as FlowNode[]),
    [],
  );
  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) => setEdges((es) => applyEdgeChanges(changes, es) as FlowEdge[]),
    [],
  );

  const onSelectionChange = useCallback((params: OnSelectionChangeParams) => {
    const sel = params.nodes[0] as FlowNode | undefined;
    setSelected(sel ?? null);
  }, []);

  const onExport = useCallback(() => {
    if (!job) return;
    const out = flowToConfig(job.schema_version, nodes, edges, job.description, job.global_options);
    const blob = new Blob([JSON.stringify(out, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'job.json';
    a.click();
    URL.revokeObjectURL(url);
  }, [job, nodes, edges]);

  const onImportClick = useCallback(() => {
    const inp = document.createElement('input');
    inp.type = 'file';
    inp.accept = 'application/json,.json';
    inp.onchange = async () => {
      const file = inp.files?.[0];
      if (!file) return;
      const text = await file.text();
      try {
        loadJob(JSON.parse(text) as JobConfig);
      } catch (err) {
        alert('Invalid JSON: ' + (err as Error).message);
      }
    };
    inp.click();
  }, [loadJob]);

  const stats = useMemo(
    () => `${nodes.length} nodes · ${edges.length} edges`,
    [nodes.length, edges.length],
  );

  return (
    <div className="app-shell">
      <div className="toolbar">
        <span className="title">MediaMolder</span>
        <span style={{ color: 'var(--text-dim)' }}>{stats}</span>

        <div className="spacer" />

        <label style={{ color: 'var(--text-dim)', fontSize: 12 }}>Example:</label>
        <select
          value={selectedExample}
          onChange={(e) => setSelectedExample(e.target.value)}
          disabled={!examples.length}
        >
          {!examples.length && <option value="">(none available)</option>}
          {examples.map((ex) => (
            <option key={ex.url} value={ex.url}>
              {ex.name}
            </option>
          ))}
        </select>

        <button onClick={onImportClick}>Import JSON</button>
        <button onClick={onExport} disabled={!job}>Export JSON</button>
      </div>

      <Palette />

      <div className="canvas">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={NODE_TYPES}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onSelectionChange={onSelectionChange}
          fitView
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={16} size={1} color="#2a303a" />
          <MiniMap pannable zoomable style={{ background: 'var(--panel)' }} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>

      <Inspector node={selected} />
    </div>
  );
}
