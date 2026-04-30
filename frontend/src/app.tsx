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
import { MMNode, type MMNodeRunData } from './components/MMNode';
import { MMEdge } from './components/MMEdge';
import { RunPanel } from './components/RunPanel';
import { RunDock } from './components/RunDock';
import { HelpDialog } from './components/HelpDialog';
import { ImportFFmpegDialog } from './components/ImportFFmpegDialog';
import { Legend } from './components/Legend';
import {
  configToFlow,
  flowToConfig,
  materializeImplicitEncoders,
  type FlowEdge,
  type FlowNode,
} from './lib/jsonAdapter';
import { autoLayout } from './lib/layout';
import { spawnNodeFrom, type PaletteEntry } from './lib/spawn';
import { useJobRun } from './lib/useJobRun';
import { inferEdgeAttributes, summariseAttributes } from './lib/streamAttrs';
import { fetchCatalog, indexStreams } from './lib/nodeCatalog';
import type { JobConfig, StreamType } from './lib/jobTypes';

const NODE_TYPES = { mmNode: MMNode };
const EDGE_TYPES = { mmEdge: MMEdge };

interface ExampleEntry {
  name: string;
  url: string;
}

/* Identity of the graph currently being edited. Drives the "Graph: …"
 * toolbar slot and the Save/Save As… behaviour.
 *  - empty:   the blank starter graph (just opened the GUI, or chose New)
 *  - example: loaded from /examples — read-only origin, edits flip to 'unsaved'
 *  - file:    opened from disk or successfully Saved/Save-As’d to disk;
 *             carries an optional FileSystemFileHandle so subsequent Save
 *             writes back to the same path silently
 *  - unsaved: user edits to an example, or a fresh New graph that has had
 *             nodes added — there is no on-disk anchor yet */
type GraphIdentity =
  | { kind: 'empty' }
  | { kind: 'example'; url: string; name: string }
  | { kind: 'file'; name: string; handle?: FileSystemFileHandle }
  | { kind: 'unsaved' };

/* File System Access API — feature-detected at call time. The TS lib does
 * not yet include these globals across all targets, so we cast through
 * Window. Fallbacks (anchor download, <input type=file>) cover Firefox /
 * Safari / older Edge. */
interface FsaWindow extends Window {
  showOpenFilePicker?: (opts?: unknown) => Promise<FileSystemFileHandle[]>;
  showSaveFilePicker?: (opts?: unknown) => Promise<FileSystemFileHandle>;
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
  const [identity, setIdentity] = useState<GraphIdentity>({ kind: 'empty' });
  const [dirty, setDirty] = useState(false);
  const [job, setJob] = useState<JobConfig>(EMPTY_JOB);
  const [nodes, setNodes] = useState<FlowNode[]>([]);
  const [edges, setEdges] = useState<FlowEdge[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedEdgeIds, setSelectedEdgeIds] = useState<string[]>([]);
  // Node label density (persisted). 'verbose' shows the full heading +
  // sublabel (file path / node id) on every node; 'compact' hides the
  // sublabel so dense graphs stay readable. Connections are unaffected.
  type LabelMode = 'verbose' | 'compact';
  const [labelMode, setLabelMode] = useState<LabelMode>(() => {
    const v = localStorage.getItem('mm.labelMode');
    return v === 'compact' ? 'compact' : 'verbose';
  });
  useEffect(() => {
    localStorage.setItem('mm.labelMode', labelMode);
  }, [labelMode]);
  const canvasRef = useRef<HTMLDivElement>(null);
  const rf = useReactFlow();

  /* ---------- Examples ---------- */
  useEffect(() => {
    fetch('/api/examples')
      .then((r) => (r.ok ? r.json() : []))
      .then((list: ExampleEntry[]) => {
        setExamples(list);
        // On first paint, auto-load the first example so the canvas isn't
        // blank for new users. Only do this if we're still on the empty
        // starter graph — never clobber a graph the user has touched.
        if (list.length && identity.kind === 'empty') {
          loadExample(list[0]);
        }
      })
      .catch(() => setExamples([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadExample = useCallback((ex: ExampleEntry) => {
    fetch(ex.url)
      .then((r) => r.json())
      .then((cfg: JobConfig) => loadJob(cfg, { kind: 'example', url: ex.url, name: ex.name }))
      .catch((err) => console.error('failed to load example', err));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadJob = useCallback((cfg: JobConfig, nextIdentity: GraphIdentity = { kind: 'unsaved' }) => {
    // Promote implicit encoders (direct source→sink edges) into real
    // editable graph.nodes[] entries so the user can configure
    // libx264/aac like any other encoder. Mirrors the runtime fallback
    // in pipeline.expandImplicitEncoders.
    cfg = materializeImplicitEncoders(cfg);
    const { nodes: n, edges: e } = configToFlow(cfg);
    setJob(cfg);
    setNodes(n);
    setEdges(e);
    setSelectedId(null);
    setIdentity(nextIdentity);
    setDirty(false);
    // Resolve handle media types for filter / encoder / processor nodes
    // from the live /api/nodes catalog (the JobConfig itself doesn't
    // carry pin metadata). Done asynchronously so loadJob stays
    // synchronous; the pins simply re-render once the catalog arrives.
    fetchCatalog()
      .then((catalog) => {
        const idx = indexStreams(catalog);
        setNodes((cur) =>
          cur.map((node) => {
            if (node.data.ref.kind !== 'node') return node;
            const def = node.data.ref.def;
            const lookupName =
              def.type === 'filter'
                ? (def.filter ?? '')
                : def.type === 'encoder'
                  ? String(def.params?.codec ?? '')
                  : def.type === 'go_processor'
                    ? (def.processor ?? '')
                    : '';
            const streams = idx.get(`${def.type}/${lookupName}`);
            if (!streams) return node;
            return { ...node, data: { ...node.data, streams } };
          }),
        );
      })
      .catch(() => {
        /* catalog unavailable: fall back to all-pins display */
      });
  }, []);

  /* Mark the graph as having unsaved edits. Loaded examples promote to
   * 'unsaved' so the toolbar stops claiming the user is still on a named
   * example; opened/saved files keep their identity but flip dirty=true so
   * the Graph: slot grows a • marker and Save knows there's something to
   * write back. */
  const markDirty = useCallback(() => {
    setDirty(true);
    setIdentity((cur) => (cur.kind === 'example' || cur.kind === 'empty' ? { kind: 'unsaved' } : cur));
  }, []);

  /* ---------- React Flow change handlers ----------
   * Ghost nodes/edges live only in the derived `expandedNodes`/
   * `expandedEdges` arrays — never in `nodes`/`edges` state — so
   * applyNodeChanges/applyEdgeChanges naturally ignore changes
   * targeting ghost ids (the underlying node simply isn't there).
   * Position-only changes (drag, select) are not user-meaningful edits
   * for dirty tracking, but layout is part of the saved JSON, so we do
   * mark dirty for any change other than pure selection. */
  const onNodesChange = useCallback(
    (changes: NodeChange[]) => {
      setNodes((ns) => applyNodeChanges(changes, ns) as FlowNode[]);
      if (changes.some((c) => c.type !== 'select' && c.type !== 'dimensions')) markDirty();
    },
    [markDirty],
  );
  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) => {
      setEdges((es) => applyEdgeChanges(changes, es) as FlowEdge[]);
      if (changes.some((c) => c.type !== 'select')) markDirty();
    },
    [markDirty],
  );

  /* ---------- Connection (with stream-type validation) ---------- */
  const isValidConnection = useCallback((c: Connection | FlowEdge) => {
    return c.sourceHandle != null && c.sourceHandle === c.targetHandle;
  }, []);

  const onConnect = useCallback(
    (c: Connection) => {
      if (!isValidConnection(c)) return;
      // Refuse connections that touch a ghost node — ghosts have no
      // identity in `nodes` state and the resulting edge would be
      // unanchored.
      if (c.source?.startsWith('__ghost__') || c.target?.startsWith('__ghost__')) return;
      const stream = (c.sourceHandle as StreamType) || 'video';
      markDirty();
      setEdges((es) => {
        const newEdge: FlowEdge = {
          id: `e-${Date.now()}-${es.length}`,
          type: 'mmEdge',
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
    setSelectedEdgeIds(params.edges.map((e) => e.id));
  }, []);

  const selectedNode = useMemo(
    () => nodes.find((n) => n.id === selectedId) ?? null,
    [nodes, selectedId],
  );

  /* ---------- Inspector edits ---------- */
  const onNodeUpdate = useCallback((next: FlowNode) => {
    setNodes((ns) => ns.map((n) => (n.id === next.id ? next : n)));
    markDirty();
    // markDirty is stable (no deps), so leaving it out of deps is fine
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onNodeDelete = useCallback((id: string) => {
    setNodes((ns) => ns.filter((n) => n.id !== id));
    setEdges((es) => es.filter((e) => e.source !== id && e.target !== id));
    if (selectedId === id) setSelectedId(null);
    markDirty();
  }, [selectedId, markDirty]);

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
      markDirty();
    },
    [rf, markDirty],
  );

  /* ---------- Toolbar actions ---------- */
  const onAutoLayout = useCallback(() => {
    setNodes((ns) => autoLayout(ns, edges));
    setTimeout(() => rf.fitView({ duration: 200 }), 0);
  }, [edges, rf]);

  /* Serialise the current graph to JSON text. Pure function over the
   * editor state; used by both Save and Save As… paths. */
  const serialiseJob = useCallback((): string => {
    const out = flowToConfig(
      job.schema_version || '1.2',
      nodes,
      edges,
      job.description,
      job.global_options,
    );
    return JSON.stringify(out, null, 2);
  }, [job, nodes, edges]);

  /* Default filename suggestion for Save As… / download fallback. Derived
   * from the current identity so a Save-As of an opened file pre-fills
   * the same name, and a Save-As of an example pre-fills the example's
   * basename. */
  const suggestedFilename = useCallback((): string => {
    if (identity.kind === 'file') return identity.name;
    if (identity.kind === 'example') return identity.name + '.json';
    return 'job.json';
  }, [identity]);

  /* Browser-level fallback when the File System Access API is unavailable
   * (Firefox, Safari): trigger a download. We can't learn where it ended
   * up, so identity stays as 'unsaved' — calling this "saved" would lie. */
  const downloadJob = useCallback((text: string, filename: string) => {
    const blob = new Blob([text], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  }, []);

  /* Save As… — always prompts for a destination. On success the chosen
   * handle becomes the graph's identity, so subsequent Save calls
   * overwrite silently. */
  const onSaveAs = useCallback(async () => {
    const text = serialiseJob();
    const w = window as FsaWindow;
    if (typeof w.showSaveFilePicker === 'function') {
      try {
        const handle = await w.showSaveFilePicker({
          suggestedName: suggestedFilename(),
          types: [{ description: 'MediaMolder job', accept: { 'application/json': ['.json'] } }],
        });
        const writable = await (handle as FileSystemFileHandle & { createWritable: () => Promise<FileSystemWritableFileStream> }).createWritable();
        await writable.write(text);
        await writable.close();
        setIdentity({ kind: 'file', name: handle.name, handle });
        setDirty(false);
        return;
      } catch (err) {
        // AbortError = user cancelled the picker; treat as a no-op.
        if ((err as DOMException)?.name === 'AbortError') return;
        console.error('Save As failed', err);
        // fall through to download fallback
      }
    }
    downloadJob(text, suggestedFilename());
  }, [serialiseJob, suggestedFilename, downloadJob]);

  /* Save — if we have a remembered file handle, overwrite it silently;
   * otherwise behave like Save As…. */
  const onSave = useCallback(async () => {
    if (identity.kind === 'file' && identity.handle) {
      try {
        const text = serialiseJob();
        const writable = await (identity.handle as FileSystemFileHandle & { createWritable: () => Promise<FileSystemWritableFileStream> }).createWritable();
        await writable.write(text);
        await writable.close();
        setDirty(false);
        return;
      } catch (err) {
        console.error('Save failed', err);
        // fall through to Save As so the user can pick a new path
      }
    }
    await onSaveAs();
  }, [identity, serialiseJob, onSaveAs]);

  /* Open — prefers File System Access API so we capture a handle the
   * user can later Save into; falls back to <input type=file> on
   * browsers without FSA. */
  const onOpen = useCallback(async () => {
    const w = window as FsaWindow;
    if (typeof w.showOpenFilePicker === 'function') {
      try {
        const [handle] = await w.showOpenFilePicker({
          types: [{ description: 'MediaMolder job', accept: { 'application/json': ['.json'] } }],
          multiple: false,
        });
        const file = await (handle as FileSystemFileHandle & { getFile: () => Promise<File> }).getFile();
        const text = await file.text();
        loadJob(JSON.parse(text) as JobConfig, { kind: 'file', name: handle.name, handle });
        return;
      } catch (err) {
        if ((err as DOMException)?.name === 'AbortError') return;
        if (err instanceof SyntaxError) {
          alert('Invalid JSON: ' + err.message);
          return;
        }
        console.error('Open failed, falling back', err);
      }
    }
    const inp = document.createElement('input');
    inp.type = 'file';
    inp.accept = 'application/json,.json';
    inp.onchange = async () => {
      const file = inp.files?.[0];
      if (!file) return;
      const text = await file.text();
      try {
        loadJob(JSON.parse(text) as JobConfig, { kind: 'file', name: file.name });
      } catch (err) {
        alert('Invalid JSON: ' + (err as Error).message);
      }
    };
    inp.click();
  }, [loadJob]);

  const onClear = useCallback(() => {
    if (dirty && !confirm('Discard the current graph?')) return;
    loadJob(EMPTY_JOB, { kind: 'empty' });
  }, [loadJob, dirty]);

  /* ---------- Keyboard delete ---------- */
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) return;
      if (e.key === 'Escape') {
        setHelpOpen(false);
        return;
      }
      if (e.key === '?' || (e.shiftKey && e.key === '/')) {
        e.preventDefault();
        setHelpOpen(true);
        return;
      }
      if ((e.key === 'Backspace' || e.key === 'Delete')) {
        if (selectedEdgeIds.length > 0) {
          e.preventDefault();
          setEdges((es) => es.filter((edge) => !selectedEdgeIds.includes(edge.id)));
          setSelectedEdgeIds([]);
          return;
        }
        if (selectedId) {
          e.preventDefault();
          onNodeDelete(selectedId);
        }
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [selectedId, selectedEdgeIds, onNodeDelete]);

  const stats = useMemo(
    () => `${nodes.length} nodes · ${edges.length} edges`,
    [nodes.length, edges.length],
  );

  /* ---------- Run controls (Phase 3) ---------- */
  const buildJobRef = useRef<() => JobConfig | null>(() => null);
  buildJobRef.current = () => {
    if (!nodes.length) return null;
    return flowToConfig(
      job.schema_version || '1.2',
      nodes,
      edges,
      job.description,
      job.global_options,
    );
  };
  const run = useJobRun(() => buildJobRef.current?.() ?? null);
  const [showRunPanel, setShowRunPanel] = useState(false);
  const isRunning = run.status === 'running' || run.status === 'starting';

  const onRun = useCallback(() => {
    setShowRunPanel(true);
    void run.start();
  }, [run]);
  const onStop = useCallback(() => {
    void run.cancel();
  }, [run]);

  /* Per-node media kind, derived from edge stream types. A node that
     touches only one stream type (video/audio/subtitle/data) reports
     that type; mixed nodes (e.g. demuxer sources) report ''. The run
     panel uses this to label per-node throughput correctly (FPS for
     video, packets/s for everything else). */
  const nodeKinds = useMemo<Map<string, '' | 'video' | 'audio' | 'subtitle' | 'data'>>(() => {
    const m = new Map<string, Set<string>>();
    const add = (id: string, t?: string) => {
      if (!id || !t) return;
      let s = m.get(id);
      if (!s) {
        s = new Set();
        m.set(id, s);
      }
      s.add(t);
    };
    for (const e of edges) {
      const t = (e.data?.streamType ?? e.sourceHandle ?? '') as string;
      add(e.source, t);
      add(e.target, t);
    }
    const out = new Map<string, '' | 'video' | 'audio' | 'subtitle' | 'data'>();
    for (const [id, s] of m) {
      if (s.size !== 1) {
        out.set(id, '');
        continue;
      }
      const only = [...s][0];
      if (only === 'video' || only === 'audio' || only === 'subtitle' || only === 'data') {
        out.set(id, only);
      } else {
        out.set(id, '');
      }
    }
    return out;
  }, [edges]);

  /* ---------- Help dialog ---------- */
  const [helpOpen, setHelpOpen] = useState(false);
  const [importFFmpegOpen, setImportFFmpegOpen] = useState(false);

  /* Merge live metrics + errors into node data so MMNode can render badges. */
  const runByNode = useMemo(() => {
    const map = new Map<string, MMNodeRunData>();
    for (const m of run.metrics?.Nodes ?? []) {
      map.set(m.NodeID, { frames: m.Frames, fps: m.FPS, errors: m.Errors });
    }
    for (const e of run.errors) {
      const cur = map.get(e.node_id) ?? {};
      cur.hasError = true;
      map.set(e.node_id, cur);
    }
    return map;
  }, [run.metrics, run.errors]);

  const decoratedNodes = useMemo<FlowNode[]>(
    () =>
      nodes.map((n) => {
        const r = runByNode.get(n.id);
        if (!r) return n;
        return { ...n, data: { ...n.data, run: r } } as FlowNode;
      }),
    [nodes, runByNode],
  );

  /* Compute inferred technical attributes for each edge so MMEdge can render
     a chip showing pix_fmt / size / sample_rate / etc. Recomputes whenever
     the graph topology or any node params change. */
  const decoratedEdges = useMemo<FlowEdge[]>(
    () =>
      edges.map((e) => {
        const attrs = inferEdgeAttributes(nodes, edges, e);
        const summary = summariseAttributes(attrs);
        return {
          ...e,
          data: { ...(e.data ?? {}), attrs, attrSummary: summary },
        } as FlowEdge;
      }),
    [nodes, edges],
  );

  return (
    <div className="app-shell">
      <div className="toolbar">
        <span className="title">MediaMolder</span>
        <span style={{ color: 'var(--text-dim)' }}>{stats}</span>

        <div className="spacer" />

        <label style={{ color: 'var(--text-dim)', fontSize: 12 }}>Graph:</label>
        <select
          value={identity.kind === 'example' ? identity.url : '__current__'}
          onChange={(e) => {
            const v = e.target.value;
            if (v === '__current__') return;
            const ex = examples.find((x) => x.url === v);
            if (!ex) return;
            if (dirty && !confirm('Discard the current graph?')) return;
            loadExample(ex);
          }}
          disabled={!examples.length}
          title="Switch to a built-in example. Discards unsaved changes."
        >
          {identity.kind !== 'example' && (
            <option value="__current__" disabled>
              {identity.kind === 'file'
                ? identity.name + (dirty ? ' \u2022' : '')
                : identity.kind === 'empty'
                  ? '(empty)'
                  : '<not saved>'}
            </option>
          )}
          {!examples.length && <option value="">(none available)</option>}
          {examples.map((ex) => (
            <option key={ex.url} value={ex.url}>
              {ex.name}
            </option>
          ))}
        </select>

        <button onClick={onAutoLayout} disabled={!nodes.length}>Auto layout</button>
        <div
          className="segmented"
          role="radiogroup"
          aria-label="Node label density"
          title="Choose how much detail each node shows. Connections are unaffected."
        >
          <span className="segmented-label">Labels:</span>
          <button
            type="button"
            role="radio"
            aria-checked={labelMode === 'verbose'}
            className={labelMode === 'verbose' ? 'segmented-on' : ''}
            onClick={() => setLabelMode('verbose')}
          >
            Verbose
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={labelMode === 'compact'}
            className={labelMode === 'compact' ? 'segmented-on' : ''}
            onClick={() => setLabelMode('compact')}
          >
            Compact
          </button>
        </div>
        <button onClick={onClear}>New</button>
        <button onClick={onOpen}>Open…</button>
        <button onClick={() => setImportFFmpegOpen(true)} title="Paste an FFmpeg command line and convert it to a graph">
          Import FFmpeg…
        </button>
        <button
          onClick={onSave}
          disabled={!nodes.length || (identity.kind === 'file' && !dirty)}
          title={identity.kind === 'file' ? `Save to ${identity.name}` : 'Save to disk…'}
        >
          Save
        </button>
        <button onClick={onSaveAs} disabled={!nodes.length} title="Save to a new file…">
          Save As…
        </button>
        {isRunning ? (
          <button className="danger" onClick={onStop}>Stop</button>
        ) : (
          <button className="primary" onClick={onRun} disabled={!nodes.length}>Run</button>
        )}
        <button onClick={() => setShowRunPanel((v) => !v)} disabled={run.status === 'idle'}>
          {showRunPanel ? 'Hide log' : 'Show log'}
        </button>
        <button onClick={() => setHelpOpen(true)} title="Open help (or press ?)">Help</button>
      </div>

      <Palette />

      <div
        className="canvas"
        data-label-mode={labelMode}
        ref={canvasRef}
        onDragOver={onDragOver}
        onDrop={onDrop}
      >
        <ReactFlow
          nodes={decoratedNodes}
          edges={decoratedEdges}
          nodeTypes={NODE_TYPES}
          edgeTypes={EDGE_TYPES}
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
          <MiniMap
            pannable
            zoomable
            nodeColor={(n) => {
              switch (n.type) {
                case 'source':   return '#4f8cff33';
                case 'filter':   return '#2dd4bf33';
                case 'encoder':  return '#a78bfa33';
                case 'sink':     return '#f59e0b33';
                default:         return '#ffffff18';
              }
            }}
            nodeStrokeColor={(n) => {
              switch (n.type) {
                case 'source':   return '#4f8cff';
                case 'filter':   return '#2dd4bf';
                case 'encoder':  return '#a78bfa';
                case 'sink':     return '#f59e0b';
                default:         return '#4a5060';
              }
            }}
            nodeStrokeWidth={2}
            maskColor="rgba(15,17,21,0.65)"
            className="mm-minimap"
          />
          <Controls showInteractive={false} className="mm-controls" />
        </ReactFlow>
        {nodes.length === 0 && (
          <div className="canvas-onboarding">
            <h2>Build your first pipeline</h2>
            <ol>
              <li>Pick an example from the toolbar dropdown, <em>or</em></li>
              <li>Drag a <strong>Source</strong> node (Input file) from the palette on the left onto this canvas.</li>
              <li>Add <strong>Filters</strong>, <strong>Encoders</strong> or <strong>Processors</strong>, then add a <strong>Sink</strong> (Output file).</li>
              <li>Connect matching coloured handles — see the legend on the bottom.</li>
              <li>Click <strong>Run</strong> to execute and watch progress live.</li>
            </ol>
            <p className="hint">Need more help? Press <kbd>?</kbd> or click the <strong>Help</strong> button in the toolbar.</p>
          </div>
        )}
        <Legend />
      </div>

      <Inspector node={selectedNode} nodes={nodes} edges={edges} onChange={onNodeUpdate} onDelete={onNodeDelete} />
      <RunDock visible={showRunPanel}>
        <RunPanel run={run} nodeKinds={nodeKinds} onClose={() => setShowRunPanel(false)} />
      </RunDock>
      <HelpDialog open={helpOpen} onClose={() => setHelpOpen(false)} />
      <ImportFFmpegDialog
        open={importFFmpegOpen}
        onClose={() => setImportFFmpegOpen(false)}
        onImported={(cfg) => loadJob(cfg)}
      />
    </div>
  );
}
