import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  useReactFlow,
  type Connection,
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
import { autoLayout } from './lib/layout';
import { spawnNodeFrom, type PaletteEntry } from './lib/spawn';
import type { JobConfig, StreamType } from './lib/jobTypes';

const NODE_TYPES = { mmNode: MMNode };

interface ExampleEntry {
  name: string;
  url: string;
}

const EMPTY_JOB: JobConfig = {
  schema_version: '1.2',
  inputs: [],
  graph: { nodes: [], edges: [] },
  outputs: [],
};

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
  const [job, setJob] = useState<JobConfig>(EMPTY_JOB);
  const [nodes, setNodes] = useState<FlowNode[]>([]);
  const [edges, setEdges] = useState<FlowEdge[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const canvasRef = useRef<HTMLDivElement>(null);
  const rf = useReactFlow();

  /* ---------- Examples ---------- */
  useEffect(() => {
    fetch('/api/examples')
      .then((r) => (r.ok ? r.json() : []))
      .then((list: ExampleEntry[]) => {
        setExamples(list);
        if (list.length && !selectedExample) setSelectedExample(list[0].url);
      })
      .catch(() => setExamples([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
    setSelectedId(null);
  }, []);

  /* ---------- React Flow change handlers ---------- */
  const onNodesChange = useCallback(
    (changes: NodeChange[]) =>
      setNodes((ns) => applyNodeChanges(changes, ns) as FlowNode[]),
    [],
  );
  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) =>
      setEdges((es) => applyEdgeChanges(changes, es) as FlowEdge[]),
    [],
  );

  /* ---------- Connection (with stream-type validation) ---------- */
  const isValidConnection = useCallback((c: Connection | FlowEdge) => {
    return c.sourceHandle != null && c.sourceHandle === c.targetHandle;
  }, []);

  const onConnect = useCallback(
    (c: Connection) => {
      if (!isValidConnection(c)) return;
      const stream = (c.sourceHandle as StreamType) || 'video';
      setEdges((es) => {
        const newEdge: FlowEdge = {
          id: `e-${Date.now()}-${es.length}`,
          source: c.source!,
          target: c.target!,
          sourceHandle: c.sourceHandle ?? undefined,
          targetHandle: c.targetHandle ?? undefined,
          className: `edge-${stream}`,
          data: { streamType: stream, rawFrom: '', rawTo: '' },
        };
        return addEdge(newEdge, es) as FlowEdge[];
      });
    },
    [isValidConnection],
  );

  /* ---------- Selection ---------- */
  const onSelectionChange = useCallback((params: OnSelectionChangeParams) => {
    setSelectedId(params.nodes[0]?.id ?? null);
  }, []);

  const selectedNode = useMemo(
    () => nodes.find((n) => n.id === selectedId) ?? null,
    [nodes, selectedId],
  );

  /* ---------- Inspector edits ---------- */
  const onNodeUpdate = useCallback((next: FlowNode) => {
    setNodes((ns) => ns.map((n) => (n.id === next.id ? next : n)));
  }, []);

  const onNodeDelete = useCallback((id: string) => {
    setNodes((ns) => ns.filter((n) => n.id !== id));
    setEdges((es) => es.filter((e) => e.source !== id && e.target !== id));
    if (selectedId === id) setSelectedId(null);
  }, [selectedId]);

  /* ---------- Drop palette items ---------- */
  const onDragOver = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer.types.includes('application/x-mm-palette')) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
  }, []);

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      const raw = e.dataTransfer.getData('application/x-mm-palette');
      if (!raw) return;
      e.preventDefault();
      let entry: PaletteEntry;
      try {
        entry = JSON.parse(raw);
      } catch {
        return;
      }
      const position = rf.screenToFlowPosition({ x: e.clientX, y: e.clientY });
      setNodes((ns) => {
        const { flowNode } = spawnNodeFrom(entry, position, ns);
        return [...ns, flowNode];
      });
    },
    [rf],
  );

  /* ---------- Toolbar actions ---------- */
  const onAutoLayout = useCallback(() => {
    setNodes((ns) => autoLayout(ns, edges));
    setTimeout(() => rf.fitView({ duration: 200 }), 0);
  }, [edges, rf]);

  const onExport = useCallback(() => {
    const out = flowToConfig(
      job.schema_version || '1.2',
      nodes,
      edges,
      job.description,
      job.global_options,
    );
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

  const onClear = useCallback(() => {
    if (!confirm('Discard the current graph?')) return;
    loadJob(EMPTY_JOB);
  }, [loadJob]);

  /* ---------- Keyboard delete ---------- */
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) return;
      if ((e.key === 'Backspace' || e.key === 'Delete') && selectedId) {
        e.preventDefault();
        onNodeDelete(selectedId);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [selectedId, onNodeDelete]);

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

        <button onClick={onAutoLayout} disabled={!nodes.length}>Auto layout</button>
        <button onClick={onClear}>New</button>
        <button onClick={onImportClick}>Import</button>
        <button className="primary" onClick={onExport} disabled={!nodes.length}>Export</button>
      </div>

      <Palette />

      <div className="canvas" ref={canvasRef} onDragOver={onDragOver} onDrop={onDrop}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={NODE_TYPES}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          isValidConnection={isValidConnection}
          onSelectionChange={onSelectionChange}
          deleteKeyCode={null /* handled manually so inputs aren't hijacked */}
          fitView
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={16} size={1} color="#2a303a" />
          <MiniMap pannable zoomable style={{ background: 'var(--panel)' }} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>

      <Inspector node={selectedNode} onChange={onNodeUpdate} onDelete={onNodeDelete} />
    </div>
  );
}
