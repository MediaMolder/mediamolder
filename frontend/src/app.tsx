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
import { MMNode, type MMNodeRunData, INSPECTOR_OPEN_EVENT, URL_BROWSE_EVENT } from './components/MMNode';
import { FileBrowser } from './components/FileBrowser';
import { MMEdge } from './components/MMEdge';
import { RunPanel } from './components/RunPanel';
import { RunDock } from './components/RunDock';
import { ValidatePanel } from './components/ValidatePanel';
import { HelpDialog } from './components/HelpDialog';
import { HardwareDialog } from './components/HardwareDialog';
import { ImportFFmpegDialog } from './components/ImportFFmpegDialog';
import { ExportFFmpegDialog } from './components/ExportFFmpegDialog';
import { AssetManager } from './components/AssetManager';
import { BackendSettingsDialog } from './components/BackendSettingsDialog';
import { Legend } from './components/Legend';
import {
  configToFlow,
  displayUrl,
  flowToConfig,
  materializeImplicitEncoders,
  nextInputTrack,
  EMPTY_URL_PLACEHOLDER,
  INPUT_PREFIX,
  OUTPUT_PREFIX,
  type FlowEdge,
  type FlowEdgeData,
  type FlowNode,
} from './lib/jsonAdapter';
import { MEDIA_FILE_EXTENSIONS } from './lib/mediaExtensions';
import { autoLayout } from './lib/layout';
import { spawnNodeFrom, type PaletteEntry } from './lib/spawn';
import { useJobRun } from './lib/useJobRun';
import { loadBackendSettings, type BackendSettings } from './lib/backendSettings';
import { inferEdgeAttributes, summariseAttributes } from './lib/streamAttrs';
import { fetchCatalog, indexStreams } from './lib/nodeCatalog';
import { fetchEncoderInfo } from './lib/encoderSchema';
import type { Fix, HWAccelProbe, JobConfig, Output, ProbeResponse, StreamType, ValidationIssue, ValidationReport } from './lib/jobTypes';
import { postValidate } from './lib/validate';
import { RTControllerNode } from './components/RTControllerNode';
import { RTControllerInspector } from './components/RTControllerInspector';
import { useRTSnapshot } from './lib/rtSnapshot';

const NODE_TYPES = { mmNode: MMNode, rtController: RTControllerNode };
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
  | { kind: 'file'; name: string; path?: string; handle?: FileSystemFileHandle }
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
  // 'open' | 'save' | null — controls the job-file FileBrowser dialog.
  const [jobBrowserMode, setJobBrowserMode] = useState<'open' | 'save' | null>(null);
  const [validateReport, setValidateReport] = useState<ValidationReport | null>(null);
  const [probeReport, setProbeReport] = useState<ValidationReport | null>(null);
  const [isValidating, setIsValidating] = useState(false);
  const [isProbing, setIsProbing] = useState(false);
  const [showValidatePanel, setShowValidatePanel] = useState(false);
  // null = probe not yet returned (show all options as fallback);
  // HWAccelProbe[] = full probe results from /api/hwaccel
  const [availableHWAccels, setAvailableHWAccels] = useState<HWAccelProbe[] | null>(null);
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

  /* Panel visibility — each is a persisted boolean (default true) so the
   * user gets the full layout on first load but a deliberate hide sticks
   * across reloads. Storage keys are namespaced under mm.view.* so they
   * don't collide with palette / labelMode keys. */
  const useStoredBool = (key: string, def: boolean) => {
    const [v, setV] = useState<boolean>(() => {
      const s = localStorage.getItem(key);
      return s == null ? def : s === '1';
    });
    useEffect(() => {
      localStorage.setItem(key, v ? '1' : '0');
    }, [key, v]);
    return [v, setV] as const;
  };
  const [showPalette, setShowPalette] = useStoredBool('mm.view.palette', true);
  const [showInspector, setShowInspector] = useStoredBool('mm.view.inspector', true);
  const [showMinimap, setShowMinimap] = useStoredBool('mm.view.minimap', true);
  const canvasRef = useRef<HTMLDivElement>(null);
  const rf = useReactFlow();

  /* ---------- Hardware acceleration probe ---------- */
  useEffect(() => {
    fetch('/api/hwaccel')
      .then((r) => (r.ok ? r.json() : []))
      .then((list: HWAccelProbe[]) => {
        setAvailableHWAccels(list);
      })
      .catch(() => setAvailableHWAccels(null));
  }, []);

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
        const srcOnly = new Set(catalog.filter((e) => e.source_only).map((e) => `${e.type}/${e.name}`));
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
            const key = `${def.type}/${lookupName}`;
            const streams = idx.get(key);
            const updates: Record<string, unknown> = {};
            if (streams) updates.streams = streams;
            if (srcOnly.has(key)) updates.sourceOnly = true;
            if (Object.keys(updates).length === 0) return node;
            return { ...node, data: { ...node.data, ...updates } };
          }),
        );
      })
      .catch(() => {
        /* catalog unavailable: fall back to all-pins display */
      });

    // Auto-probe input nodes with real (non-placeholder) file URLs.
    //   • No cache (cachedMtime=-1)  → probe, populate cache (no dirty mark)
    //   • Local file (cachedMtime>0) → probe, check mtime; update + mark dirty if changed
    //   • Network/device (cachedMtime=0, already cached) → skip
    for (const inp of cfg.inputs) {
      const url = inp.url ?? '';
      if (
        !url.trim() ||
        url === EMPTY_URL_PLACEHOLDER ||
        inp.kind === 'lavfi' ||
        inp.kind === 'raw' ||
        inp.kind === 'concat'
      ) continue;
      const cache = cfg.graph?.ui?.probed_inputs?.[inp.id];
      const cachedMtime = cache?.file_mtime ?? -1;
      // Network/device inputs that already have cached data: trust the cache.
      if (cachedMtime === 0) continue;
      const flowId = INPUT_PREFIX + inp.id;
      fetch('/api/probe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: inp.url, ...(inp.format ? { format: inp.format } : {}) }),
      })
        .then((r) => (r.ok ? (r.json() as Promise<ProbeResponse>) : Promise.reject(new Error(`HTTP ${r.status}`))))
        .then((resp) => {
          const newMtime = resp.file_mtime ?? 0;
          // Local file unchanged — cached data is still valid.
          if (cachedMtime > 0 && newMtime > 0 && newMtime === cachedMtime) return;
          const probed = resp.streams;
          const streams = [...new Set(probed.map((s) => s.type as string))];
          const audioCount = probed.filter((s) => s.type === 'audio').length;
          setNodes((ns) =>
            ns.map((node) => {
              if (node.id !== flowId) return node;
              let ref = node.data.ref;
              if (ref.kind === 'input') {
                const trackCount: Record<string, number> = {};
                const rebuiltStreams = probed.map((s) => {
                  const t = s.type as StreamType;
                  const track = trackCount[t as string] ?? 0;
                  trackCount[t as string] = track + 1;
                  return { input_index: 0, type: t, track };
                });
                ref = { kind: 'input', def: { ...ref.def, streams: rebuiltStreams } };
              }
              return {
                ...node,
                data: {
                  ...node.data,
                  ref,
                  probed,
                  probedFileMtime: newMtime,
                  streams,
                  ...(audioCount > 1 ? { audioTrackCount: audioCount } : { audioTrackCount: undefined }),
                },
              };
            }),
          );
          setEdges((es) =>
            es.filter((e) => {
              if (e.source !== flowId || e.sourceHandle == null) return true;
              // Events and file edges are routing annotations, not AV streams
              // — never remove them based on probe results.
              const st = (e.data as { streamType?: string } | null)?.streamType;
              const isSynthetic = (e.data as { synthetic?: boolean } | null)?.synthetic;
              if (st === 'events' || st === 'file' || isSynthetic) return true;
              return streams.includes(e.sourceHandle.split(':')[0]);
            }),
          );
          // Mark dirty only for freshness re-probes (file changed since last save).
          // Initial cache population is transparent to the user.
          if (cachedMtime > 0) setDirty(true);
        })
        .catch(() => {
          /* Probe failed — file unavailable, permission error, etc.
           * Leave any existing cached data in place. */
        });
    }
  }, []);

  /* Mark the graph as having unsaved edits. Loaded examples promote to
   * 'unsaved' so the toolbar stops claiming the user is still on a named
   * example; opened/saved files keep their identity but flip dirty=true so
   * the Graph: slot grows a • marker and Save knows there's something to
   * write back. */
  const markDirty = useCallback(() => {
    setDirty(true);
    setIdentity((cur) => (cur.kind === 'example' || cur.kind === 'empty' ? { kind: 'unsaved' } : cur));
    setValidateReport(null);
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
      // Changes targeting the synthetic __rtc__ node must not reach
      // applyNodeChanges: __rtc__ lives only in the derived
      // decoratedNodesWithRTC array, so applyNodeChanges always returns a
      // new array for __rtc__ changes (the node isn't found), which
      // triggers setNodes → re-render → new decoratedNodesWithRTC → new
      // ReactFlow prop → another dimensions change — an infinite loop.
      const filtered = changes.filter((c) => (c as { id?: string }).id !== '__rtc__');
      if (filtered.length === 0) return;
      setNodes((ns) => applyNodeChanges(filtered, ns) as FlowNode[]);
      if (filtered.some((c) => c.type !== 'select' && c.type !== 'dimensions')) markDirty();
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
  // Handles may carry a track suffix (e.g. "audio:2") — compare base types.
  const baseStreamType = (h: string | null | undefined) => (h ?? '').split(':')[0];
  const isValidConnection = useCallback((c: Connection | FlowEdge) => {
    const src = baseStreamType(c.sourceHandle);
    return src !== '' && src === baseStreamType(c.targetHandle);
  }, []);

  const onConnect = useCallback(
    (c: Connection) => {
      if (!isValidConnection(c)) return;
      // Refuse connections that touch a ghost node — ghosts have no
      // identity in `nodes` state and the resulting edge would be
      // unanchored.
      if (c.source?.startsWith('__ghost__') || c.target?.startsWith('__ghost__')) return;
      // Base stream type from the handle (strip ":N" track suffix).
      const stream = (baseStreamType(c.sourceHandle) as StreamType) || 'video';
      const sourceNode = nodes.find((n) => n.id === c.source);
      markDirty();
      setEdges((es) => {
        // For input nodes: the handle ID encodes the track index directly
        // ("audio:2" → in0:a:2). For single-handle input nodes (probed data
        // not yet available) fall back to auto-increment so a second drag
        // still gets a distinct track.
        let rawFrom = '';
        if (sourceNode?.data.kind === 'input') {
          if (stream === 'events' || stream === 'file') {
            // Events and file edges carry a file-path notification, not a
            // decoded stream. The raw ref is just the input id with no type suffix.
            rawFrom = sourceNode.data.label as string;
          } else {
            const trackStr = (c.sourceHandle ?? '').split(':')[1];
            const letter = stream === 'audio' ? 'a' : stream === 'video' ? 'v' : stream === 'subtitle' ? 's' : 'd';
            if (trackStr !== undefined) {
              // Per-track handle — track is encoded in the handle id.
              rawFrom = `${sourceNode.data.label}:${letter}:${parseInt(trackStr, 10)}`;
            } else {
              // Single-handle fallback: assign the next unused track index.
              const track = nextInputTrack(sourceNode.data.label as string, stream, es);
              rawFrom = `${sourceNode.data.label}:${letter}:${track}`;
            }
          }
        }
        const newEdge: FlowEdge = {
          id: `e-${Date.now()}-${es.length}`,
          type: 'mmEdge',
          source: c.source!,
          target: c.target!,
          sourceHandle: c.sourceHandle ?? undefined,
          targetHandle: c.targetHandle ?? undefined,
          className: `edge-${stream}`,
          data: { streamType: stream, rawFrom, rawTo: '' },
        };
        return addEdge(newEdge, es) as FlowEdge[];
      });
    },
    [isValidConnection, nodes],
  );

  /* ---------- Selection ---------- */
  const onSelectionChange = useCallback((params: OnSelectionChangeParams) => {
    setSelectedId(params.nodes[0]?.id ?? null);
    setSelectedEdgeIds(params.edges.map((e) => e.id));
    // Auto-open the inspector when the RT Controller canvas node is clicked.
    if (params.nodes[0]?.id === '__rtc__') setShowInspector(true);
  }, [setShowInspector]);

  /* ---------- Open Inspector from node button ----------
   * MMNode dispatches mm.inspector.open with the node id when the user
   * clicks the small pencil glyph in its header. We mark just that node
   * as React-Flow-selected (which also drives the Inspector via
   * onSelectionChange) and force-show the Inspector panel if hidden so
   * the click never appears to do nothing. */
  useEffect(() => {
    const handler = (e: Event) => {
      const id = (e as CustomEvent<{ id: string }>).detail?.id;
      if (!id) return;
      setNodes((ns) => ns.map((n) => (n.selected === (n.id === id) ? n : { ...n, selected: n.id === id })));
      setSelectedId(id);
      setShowInspector(true);
    };
    window.addEventListener(INSPECTOR_OPEN_EVENT, handler);
    return () => window.removeEventListener(INSPECTOR_OPEN_EVENT, handler);
  }, [setShowInspector]);

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

  const onProbedData = useCallback((nodeId: string, response: ProbeResponse | undefined) => {
    const probed = response?.streams;
    const fileMtime = response?.file_mtime ?? 0;
    const streams = probed
      ? [...new Set(probed.map((s) => s.type as string))]
      : undefined;
    // Number of distinct audio tracks in the probed result (drives per-track
    // handle rendering in MMNode).
    const audioTrackCount = probed ? probed.filter((s) => s.type === 'audio').length : undefined;
    setNodes((ns) => ns.map((n) => {
      if (n.id !== nodeId) return n;
      // Rebuild def.streams from probe results so the serialised JSON only
      // declares streams the file actually contains. Track indices are
      // assigned per-type in appearance order (first video → track 0, etc.).
      let ref = n.data.ref;
      if (probed !== undefined && ref.kind === 'input') {
        const trackCount: Record<string, number> = {};
        const rebuiltStreams = probed.map((s) => {
          const t = s.type as StreamType;
          const track = trackCount[t as string] ?? 0;
          trackCount[t as string] = track + 1;
          return { input_index: 0, type: t, track };
        });
        ref = { kind: 'input', def: { ...ref.def, streams: rebuiltStreams } };
      }
      return {
        ...n,
        data: {
          ...n.data,
          ref,
          probed,
          probedFileMtime: probed !== undefined ? fileMtime : undefined,
          streams,
          ...(audioTrackCount !== undefined && audioTrackCount > 1
            ? { audioTrackCount }
            : { audioTrackCount: undefined }),
        },
      };
    }));
    // Remove edges whose sourceHandle base type is no longer in the probed
    // stream set. Handles may carry a track suffix ("audio:2") — compare
    // base stream type, not the full handle string.
    if (streams !== undefined) {
      setEdges((es) =>
        es.filter((e) => {
          if (e.source !== nodeId || e.sourceHandle == null) return true;
          // Events and file edges are routing annotations — preserve them
          // regardless of probe results.
          const st = (e.data as { streamType?: string } | null)?.streamType;
          if (st === 'events' || st === 'file') return true;
          const base = e.sourceHandle.split(':')[0];
          return streams.includes(base);
        }),
      );
    }
    markDirty();
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
  const serialiseJob = useCallback(async (): Promise<string> => {
    const out = flowToConfig(
      job.schema_version || '1.2',
      nodes,
      edges,
      job.description,
      job.global_options,
      job.assets,
    );
    // Refresh ffmpeg_cmd from the live export endpoint so it always reflects
    // the current graph state. Clear the field if export fails or produces
    // nothing (e.g. the job uses go_processor or other non-exportable nodes).
    try {
      const r = await fetch('/api/export-cmd', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: out }),
      });
      if (r.ok) {
        const { command } = await r.json() as { command?: string };
        if (command) out.ffmpeg_cmd = command;
      }
    } catch { /* silent: ffmpeg_cmd stays absent */ }
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

  /* Save As… — always prompts for a destination. On success the chosen
   * handle becomes the graph's identity, so subsequent Save calls
   * overwrite silently. */
  const onSaveAs = useCallback(async () => {
    const w = window as FsaWindow;
    if (typeof w.showSaveFilePicker === 'function') {
      try {
        const handle = await w.showSaveFilePicker({
          suggestedName: suggestedFilename(),
          types: [{ description: 'MediaMolder job', accept: { 'application/json': ['.json'] } }],
        });
        const text = await serialiseJob();
        const fh = handle as FileSystemFileHandle & { createWritable: () => Promise<FileSystemWritableFileStream> };
        const writable = await fh.createWritable();
        await writable.write(text);
        await writable.close();
        setIdentity({ kind: 'file', name: handle.name, handle });
        setDirty(false);
        return;
      } catch (err) {
        // AbortError = user cancelled the picker; treat as a no-op.
        if ((err as DOMException)?.name === 'AbortError') return;
        console.error('Save As failed', err);
        // fall through to backend FileBrowser
      }
    }
    // FSA unavailable or failed: open the backend FileBrowser in save mode.
    setJobBrowserMode('save');
  }, [serialiseJob, suggestedFilename]);

  /* Save — try in order:
   * 1. Backend API  (identity.path set via FileBrowser open/save)
   * 2. FSA handle   (identity.handle set via showOpenFilePicker/showSaveFilePicker)
   * 3. Save As      (no known destination yet) */
  const onSave = useCallback(async () => {
    if (identity.kind === 'file' && identity.path) {
      try {
        const text = await serialiseJob();
        const r = await fetch('/api/file', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: identity.path, content: text }),
        });
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        setDirty(false);
        return;
      } catch (err) {
        console.error('Save failed', err);
        // fall through to FSA / Save As
      }
    }
    if (identity.kind === 'file' && identity.handle) {
      try {
        const text = await serialiseJob();
        const fh = identity.handle as FileSystemFileHandle & {
          requestPermission?: (d: { mode: string }) => Promise<string>;
          createWritable: () => Promise<FileSystemWritableFileStream>;
        };
        // Request write permission explicitly — required on Chrome/macOS when
        // the handle came from showOpenFilePicker (read-only by default).
        if (typeof fh.requestPermission === 'function') {
          const perm = await fh.requestPermission({ mode: 'readwrite' });
          if (perm !== 'granted') { await onSaveAs(); return; }
        }
        const writable = await fh.createWritable();
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
   * user can later Save into; falls back to the backend FileBrowser
   * (which gives a full path for silent saves) on browsers without FSA;
   * last resort is <input type=file>. */
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
    // FSA unavailable: use the backend FileBrowser so we get a full server-
    // side path and can subsequently save silently via PUT /api/file.
    setJobBrowserMode('open');
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

  /* ---------- Validate controls ---------- */
  const onValidate = useCallback(async () => {
    const cfg = buildJobRef.current?.() ?? null;
    if (!cfg) return;
    setProbeReport(null); // clear previous probe result when re-running static validation
    setShowValidatePanel(true);
    setIsValidating(true);
    try {
      const r = await postValidate(cfg, false);
      setValidateReport(r);
    } catch (err) {
      setValidateReport({ issues: [{ severity: 'ERROR', code: 'NETWORK_ERROR', message: String(err) }], has_errors: true, has_warnings: false });
    } finally {
      setIsValidating(false);
    }
  }, []);

  const onValidateProbe = useCallback(async () => {
    const cfg = buildJobRef.current?.() ?? null;
    if (!cfg) return;
    setIsProbing(true);
    try {
      const r = await postValidate(cfg, true);
      setProbeReport(r);
    } catch (err) {
      setProbeReport({ issues: [{ severity: 'ERROR', code: 'NETWORK_ERROR', message: String(err) }], has_errors: true, has_warnings: false });
    } finally {
      setIsProbing(false);
    }
  }, []);

  const onApplyFix = useCallback((fix: Fix, _issue: ValidationIssue) => {
    if (fix.insert_filter) {
      const { before_node_id, filter_name, params } = fix.insert_filter;
      setNodes((ns) => {
        const target = ns.find((n) => n.id === before_node_id);
        const pos = target ? { x: target.position.x - 220, y: target.position.y } : { x: 200, y: 200 };
        const existingIds = ns.map((n) => n.id);
        const base = filter_name.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'filter';
        let newId = base;
        let i = 0;
        while (existingIds.includes(newId)) newId = `${base}_${++i}`;
        const newNode: FlowNode = {
          id: newId,
          type: 'mmNode',
          position: pos,
          data: {
            kind: 'filter',
            label: filter_name,
            sublabel: newId,
            ref: { kind: 'node', def: { id: newId, type: 'filter', filter: filter_name, params: params ?? {} } },
          },
        };
        return [...ns, newNode];
      });
      setEdges((es) => {
        // Reroute edges targeting before_node_id to the new filter node.
        // We need the new node id — re-derive it from the current edge state
        // after the setNodes above has queued. Use a local derivation.
        const base = filter_name.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'filter';
        const existingNodeIds = new Set(es.flatMap((e) => [e.source, e.target]));
        let newId = base;
        let i = 0;
        while (existingNodeIds.has(newId)) newId = `${base}_${++i}`;
        const incomingToTarget = es.filter((e) => e.target === before_node_id);
        const rest = es.filter((e) => e.target !== before_node_id);
        const rerouted = incomingToTarget.map((e): FlowEdge => ({
          ...e,
          target: newId,
          id: `${e.id}_via_${newId}`,
          data: { ...(e.data as FlowEdgeData), rawTo: newId },
        }));
        // Infer stream type from first rerouted edge.
        const st: StreamType = (rerouted[0]?.data?.streamType as StreamType | undefined) ?? 'video';
        const bridgeEdge: FlowEdge = {
          id: `${newId}_to_${before_node_id}`,
          source: newId,
          target: before_node_id,
          type: 'mmEdge',
          data: { streamType: st, rawFrom: newId, rawTo: before_node_id },
        };
        return [...rest, ...rerouted, bridgeEdge];
      });
      markDirty();
    } else if (fix.set_output_field) {
      const { output_id, field, value } = fix.set_output_field;
      const flowId = OUTPUT_PREFIX + output_id;
      setNodes((ns) => ns.map((n) => {
        if (n.id !== flowId || n.data.ref.kind !== 'output') return n;
        const def = { ...n.data.ref.def, [field]: value };
        return { ...n, data: { ...n.data, ref: { kind: 'output' as const, def } } };
      }));
      markDirty();
    }
    // Re-run static validation after applying the fix.
    void onValidate();
  }, [markDirty, onValidate]);

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
      job.assets,
    );
  };
  const [backend, setBackend] = useState<BackendSettings | null>(loadBackendSettings);
  const [backendDialogOpen, setBackendDialogOpen] = useState(false);
  const run = useJobRun(() => buildJobRef.current?.() ?? null, backend);
  const [showRunPanel, setShowRunPanel] = useState(false);
  const isRunning = run.status === 'running' || run.status === 'starting';
  const rtSnapshot = useRTSnapshot(isRunning && !!(job.global_options?.realtime));

  const onRun = useCallback(() => {
    setShowValidatePanel(false);
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
  const [showHWDialog, setShowHWDialog] = useState(false);
  const [importFFmpegOpen, setImportFFmpegOpen] = useState(false);
  const [showExportCmd, setShowExportCmd] = useState(false);
  const [showAssetManager, setShowAssetManager] = useState(false);

  /* ---------- Node URL browse (from clicking URL chip on canvas) ---------- */
  const [browseNodeId, setBrowseNodeId] = useState<string | null>(null);
  useEffect(() => {
    const handler = (e: Event) => {
      const id = (e as CustomEvent<{ id: string }>).detail?.id;
      if (!id) return;
      setBrowseNodeId(id);
    };
    window.addEventListener(URL_BROWSE_EVENT, handler);
    return () => window.removeEventListener(URL_BROWSE_EVENT, handler);
  }, []);
  const browseNode = browseNodeId ? nodes.find((n) => n.id === browseNodeId) : null;
  const browseIsInput = browseNode?.data.kind === 'input';
  const browseCurrentUrl =
    browseNode?.data.ref.kind === 'input' ? browseNode.data.ref.def.url
    : browseNode?.data.ref.kind === 'output' ? browseNode.data.ref.def.url
    : '';
  const browseInitialDir = (() => {
    const u = browseCurrentUrl ?? '';
    const last = Math.max(u.lastIndexOf('/'), u.lastIndexOf('\\'));
    return last > 0 ? u.slice(0, last) : undefined;
  })();

  // Pre-fill the filename input with the node's existing URL filename so
  // printf-style patterns (shot-%05d.mp4) are preserved across edits.
  const browseDefaultFilename = (() => {
    if (browseIsInput) return undefined;
    const u = browseCurrentUrl ?? '';
    const last = Math.max(u.lastIndexOf('/'), u.lastIndexOf('\\'));
    const name = last >= 0 ? u.slice(last + 1) : u;
    return name || 'output.mp4';
  })();

  // Segmented outputs (segment_on_metadata set) require a printf-style
  // filename pattern; surface this in the FileBrowser title and hint.
  const browseOutputDef =
    browseNode?.data.ref.kind === 'output'
      ? (browseNode.data.ref.def as Output)
      : null;
  const browseIsSegmented = !!browseOutputDef?.segment_on_metadata;

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
    () => {
      // Identify which graph nodes are HW-accelerated (have NodeDef.device set).
      const hwNodeIds = new Set<string>();
      for (const n of nodes) {
        if (n.data.ref.kind === 'node' && n.data.ref.def.device) {
          hwNodeIds.add(n.id);
        }
      }
      // Build undirected adjacency so round-trip detection is O(edges).
      const neighbors = new Map<string, Set<string>>();
      const addEdge = (a: string, b: string) => {
        if (!neighbors.has(a)) neighbors.set(a, new Set());
        neighbors.get(a)!.add(b);
      };
      for (const e of edges) {
        addEdge(e.source, e.target);
        addEdge(e.target, e.source);
      }

      return nodes.map((n) => {
        const r = runByNode.get(n.id);
        const ref = n.data.ref;
        let hwDevice: string | undefined;
        let hwRoundTrip: boolean | undefined;

        if (ref.kind === 'node') {
          const def = ref.def;
          hwDevice = def.device || undefined;
          // SW filter adjacent to any HW node → implicit round-trip warning.
          if (!hwDevice && (def.type === 'filter' || def.type === 'filter_source' || def.type === 'filter_sink')) {
            for (const nb of neighbors.get(n.id) ?? []) {
              if (hwNodeIds.has(nb)) { hwRoundTrip = true; break; }
            }
          }
        }

        const updates: Record<string, unknown> = {};
        if (r) updates.run = r;
        if (hwDevice !== undefined) updates.hwDevice = hwDevice;
        if (hwRoundTrip !== undefined) updates.hwRoundTrip = hwRoundTrip;
        if (Object.keys(updates).length === 0) return n;
        return { ...n, data: { ...n.data, ...updates } } as FlowNode;
      });
    },
    [nodes, runByNode, edges],
  );

  /* Prefetch encoder schemas for every encoder node in the graph so that
     the edge attribute popover can show rate-control defaults (e.g. CRF 23)
     without waiting for the user to click the node. Each resolved schema
     bumps schemaVersion, which is included in the decoratedEdges memo dep
     array so the edge attrs recompute with the now-cached data. */
  const [schemaVersion, setSchemaVersion] = useState(0);
  useEffect(() => {
    const codecs = new Set<string>();
    for (const n of nodes) {
      const ref = n.data.ref;
      if (ref?.kind === 'node' && ref.def.type === 'encoder') {
        const codec = ref.def.filter ?? String(ref.def.params?.codec ?? '');
        if (codec) codecs.add(codec);
      }
    }
    for (const codec of codecs) {
      fetchEncoderInfo(codec).then(() => setSchemaVersion((v) => v + 1));
    }
  }, [nodes]);

  /* Inject the synthetic Real-Time Controller node whenever realtime mode is
     enabled in the job config — even before the job starts.  When a live
     snapshot is available the node is positioned over the controlled-encoder
     bounding box; otherwise it floats above all non-I/O nodes. */
  const decoratedNodesWithRTC = useMemo<FlowNode[]>(() => {
    if (!job.global_options?.realtime) return decoratedNodes;

    // Always use all non-I/O nodes for positioning so the box doesn't jump
    // when the snapshot first arrives (snapshot-controlled IDs can differ).
    const controlled = decoratedNodes.filter(
      (n) => !n.id.startsWith('__in__') && !n.id.startsWith('__out__'),
    );
    if (controlled.length === 0) return decoratedNodes;
    const FALLBACK_W = 200;
    const RTC_H = 56;
    const RTC_GAP = 24;
    const minX = Math.min(...controlled.map((n) => n.position.x));
    const maxX = Math.max(...controlled.map((n) => n.position.x + ((n.measured?.width as number | undefined) ?? FALLBACK_W)));
    const minY = Math.min(...controlled.map((n) => n.position.y));
    const w = Math.max(200, maxX - minX + 20);
    const rtcNode: FlowNode = {
      id: '__rtc__',
      type: 'rtController',
      position: { x: minX - 10, y: minY - RTC_H - RTC_GAP },
      selected: selectedId === '__rtc__',
      data: {
        kind: 'rtController',
        label: 'Real-Time Controller',
        ref: undefined as unknown as FlowNode['data']['ref'],
        snapshot: rtSnapshot,
      },
      draggable: false,
      deletable: false,
      selectable: true,
      style: { minWidth: w, width: w },
    };
    return [rtcNode, ...decoratedNodes];
  }, [decoratedNodes, rtSnapshot, job.global_options?.realtime, selectedId]);

  /* Compute inferred technical attributes for each edge so MMEdge can render
     a chip showing pix_fmt / size / sample_rate / etc. Recomputes whenever
     the graph topology or any node params change. */
  const decoratedEdges = useMemo<FlowEdge[]>(
    () =>
      edges.map((e) => {
        if ((e.data as { synthetic?: boolean } | null)?.synthetic) return e;
        const attrs = inferEdgeAttributes(nodes, edges, e);
        const summary = summariseAttributes(attrs);
        return {
          ...e,
          data: { ...(e.data ?? {}), attrs, attrSummary: summary },
        } as FlowEdge;
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [nodes, edges, schemaVersion],
  );

  return (
    <div
      className="app-shell"
      data-palette={showPalette ? 'shown' : 'hidden'}
      data-inspector={showInspector ? 'shown' : 'hidden'}
      data-rtc-inspector={showInspector && selectedId === '__rtc__' ? 'shown' : undefined}
    >
      <div className="toolbar">
        <span className="title">MediaMolder</span>

        <button onClick={onClear}>New</button>
        <button onClick={onOpen}>Open…</button>
        <button onClick={() => setImportFFmpegOpen(true)} title="Paste an FFmpeg command line and convert it to a graph">
          FFmpeg -{'>'}
        </button>
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

        <button
          onClick={onSave}
          disabled={!nodes.length || identity.kind === 'example' || (identity.kind === 'file' && !dirty)}
          title={
            identity.kind === 'example'
              ? 'Use Save As… to save a copy — built-in examples cannot be overwritten'
              : identity.kind === 'file'
              ? `Save to ${identity.name}`
              : 'Save to disk…'
          }
        >
          Save
        </button>
        <button onClick={onSaveAs} disabled={!nodes.length} title="Save to a new file…">
          Save As…
        </button>
        <button
          onClick={() => setShowExportCmd(true)}
          disabled={!nodes.length}
          title="Show the current graph as an ffmpeg command line"
        >
          -{'>'}  FFmpeg
        </button>

        <div className="spacer" />

        <button onClick={onAutoLayout} disabled={!nodes.length}>Auto layout</button>
        <div
          className="segmented"
          role="group"
          aria-label="Panel visibility"
          title="Show or hide editor panels."
        >
          <span className="segmented-label">View:</span>
          <button
            type="button"
            aria-pressed={showPalette}
            className={showPalette ? 'segmented-on' : ''}
            onClick={() => setShowPalette((v) => !v)}
            title="Toggle the node palette (left sidebar)"
          >
            Palette
          </button>
          <button
            type="button"
            aria-pressed={showInspector}
            className={showInspector ? 'segmented-on' : ''}
            onClick={() => setShowInspector((v) => !v)}
            title="Toggle the inspector (right sidebar)"
          >
            Inspector
          </button>
          <button
            type="button"
            aria-pressed={showMinimap}
            className={showMinimap ? 'segmented-on' : ''}
            onClick={() => setShowMinimap((v) => !v)}
            title="Toggle the canvas minimap"
          >
            Minimap
          </button>
        </div>
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
        <button
          onClick={onValidate}
          disabled={isValidating || !nodes.length}
          title="Run static validation (no file I/O)">
          Validate{validateReport?.has_errors ? ' ✗' : validateReport && !validateReport.has_errors ? ' ✓' : ''}
        </button>
        <label
          className="toolbar-check"
          title="Enable adaptive real-time mode: dynamically adjusts encoder threads and drops frames to meet fps_target. Saved in global_options.realtime when the graph is saved.">
          <input
            type="checkbox"
            checked={!!(job.global_options?.realtime)}
            onChange={(e) => {
              setJob((j) => ({
                ...j,
                global_options: {
                  ...j.global_options,
                  realtime: e.target.checked || undefined,
                },
              }));
              markDirty();
            }}
            disabled={isRunning}
          />
          Real-time
        </label>
        {isRunning && rtSnapshot && (
          <span className={`rtc-status-pill rtc-status-pill--${rtSnapshot.Status}`} title="Real-Time Controller status">
            {rtSnapshot.Status}&ensp;{rtSnapshot.FPSActual.toFixed(1)}&thinsp;/&thinsp;{rtSnapshot.FPSTarget.toFixed(1)}&thinsp;fps
          </span>
        )}
        {isRunning ? (
          <button className="danger" onClick={onStop}>Stop</button>
        ) : (
          <button className="primary" onClick={onRun} disabled={!nodes.length}>Run</button>
        )}
        <button onClick={() => setShowRunPanel((v) => !v)} disabled={run.status === 'idle'}>
          {showRunPanel ? 'Hide log' : 'Show log'}
        </button>
        <button onClick={() => setHelpOpen(true)} title="Open help (or press ?)">Help</button>
        <button
          onClick={() => setBackendDialogOpen(true)}
          title={backend ? `Remote backend: ${backend.url}` : 'Local execution (click to configure remote backend)'}
          style={backend ? { outline: '1px solid var(--color-primary, #3b82f6)' } : undefined}
        >
          {backend ? 'Remote' : 'Backend'}
        </button>
        <button
          onClick={() => setShowAssetManager(true)}
          title="Manage the asset registry (fonts, ML models, LUTs)"
        >
          Assets{job.assets && Object.keys(job.assets).length > 0 && (
            <span className="toolbar-badge">{Object.keys(job.assets).length}</span>
          )}
        </button>
      </div>

      {showPalette && <Palette hwProbes={availableHWAccels} onHardwareClick={() => setShowHWDialog(true)} />}

      <div
        className="canvas"
        data-label-mode={labelMode}
        ref={canvasRef}
        onDragOver={onDragOver}
        onDrop={onDrop}
      >
        <ReactFlow
          nodes={decoratedNodesWithRTC}
          edges={decoratedEdges}
          nodeTypes={NODE_TYPES}
          edgeTypes={EDGE_TYPES}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          isValidConnection={isValidConnection}
          onSelectionChange={onSelectionChange}
          onNodeClick={(_, node) => {
            if (node.id === '__rtc__') {
              setSelectedId('__rtc__');
              setShowInspector(true);
            }
          }}
          deleteKeyCode={null /* handled manually so inputs aren't hijacked */}
          edgesReconnectable={false}
          fitView
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={16} size={1} color="#2a303a" />
          {showMinimap && (
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
          )}
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
        <div className="canvas-stats" title="Nodes · edges in the current graph">{stats}</div>
      </div>

      {showInspector && (
        selectedId === '__rtc__'
          ? <RTControllerInspector
              snapshot={rtSnapshot}
              globalOptions={job.global_options}
              onGlobalOptionsChange={(update) => {
                setJob((j) => ({ ...j, global_options: { ...(j.global_options ?? {}), ...update } }));
                markDirty();
              }}
            />
          : <Inspector node={selectedNode} nodes={nodes} edges={edges} onChange={onNodeUpdate} onDelete={onNodeDelete} onSelectNode={setSelectedId} onProbedData={onProbedData} hwDevices={job.hardware_devices ?? []} availableHWAccels={availableHWAccels} />
      )}
      <RunDock visible={showRunPanel}>
        <RunPanel run={run} nodeKinds={nodeKinds} onClose={() => setShowRunPanel(false)} />
      </RunDock>
      <RunDock visible={showValidatePanel && validateReport !== null}>
        <ValidatePanel
          report={validateReport ?? { issues: [], has_errors: false, has_warnings: false }}
          probeReport={probeReport}
          isValidating={isValidating}
          isProbing={isProbing}
          onApplyFix={onApplyFix}
          onRunWithProbe={onValidateProbe}
          onClose={() => setShowValidatePanel(false)}
        />
      </RunDock>
      <HelpDialog open={helpOpen} onClose={() => setHelpOpen(false)} />
      <BackendSettingsDialog
        open={backendDialogOpen}
        current={backend}
        onClose={() => setBackendDialogOpen(false)}
        onChange={setBackend}
      />
      <HardwareDialog open={showHWDialog} probes={availableHWAccels} onClose={() => setShowHWDialog(false)} />
      <ImportFFmpegDialog
        open={importFFmpegOpen}
        onClose={() => setImportFFmpegOpen(false)}
        onImported={(cfg) => loadJob(cfg)}
      />
      {showExportCmd && (
        <ExportFFmpegDialog
          config={flowToConfig(
            job.schema_version || '1.2',
            nodes,
            edges,
            job.description,
            job.global_options,
            job.assets,
          )}
          onClose={() => setShowExportCmd(false)}
        />
      )}
      {showAssetManager && (
        <AssetManager
          assets={job.assets ?? {}}
          onChange={(a) =>
            setJob((j) => ({ ...j, assets: Object.keys(a).length > 0 ? a : undefined }))
          }
          onClose={() => setShowAssetManager(false)}
        />
      )}
      {browseNodeId && browseNode && (
        <FileBrowser
          open
          mode={browseIsInput ? 'open' : 'save'}
          title={
            browseIsInput ? 'Choose input file'
            : browseIsSegmented ? 'Set output folder and filename pattern'
            : 'Choose output file'
          }
          filter={browseIsInput ? MEDIA_FILE_EXTENSIONS : undefined}
          warnExtensions={browseIsInput ? undefined : MEDIA_FILE_EXTENSIONS}
          initialPath={browseInitialDir}
          defaultFilename={browseIsInput ? undefined : (browseDefaultFilename ?? 'output.mp4')}
          filenameHint={
            browseIsSegmented
              ? 'Use a printf-style pattern for per-segment files, e.g. shot-%05d.mp4 or clip_%d.mkv'
              : undefined
          }
          onClose={() => setBrowseNodeId(null)}
          onPick={(path) => {
            setBrowseNodeId(null);
            setNodes((ns) =>
              ns.map((n) => {
                if (n.id !== browseNodeId) return n;
                const ref = n.data.ref;
                if (ref.kind === 'input') {
                  return { ...n, data: { ...n.data, sublabel: displayUrl(path), ref: { kind: 'input', def: { ...ref.def, url: path } } } };
                }
                if (ref.kind === 'output') {
                  return { ...n, data: { ...n.data, sublabel: displayUrl(path), ref: { kind: 'output', def: { ...ref.def, url: path } } } };
                }
                return n;
              }),
            );
            markDirty();
          }}
        />
      )}
      {jobBrowserMode && (
        <FileBrowser
          open
          mode={jobBrowserMode}
          title={jobBrowserMode === 'open' ? 'Open job file' : 'Save job file as…'}
          filter="json"
          defaultFilename={jobBrowserMode === 'save' ? suggestedFilename() : undefined}
          initialPath={identity.kind === 'file' ? identity.path ?? undefined : undefined}
          onClose={() => setJobBrowserMode(null)}
          onPick={async (pickedPath) => {
            setJobBrowserMode(null);
            if (jobBrowserMode === 'open') {
              try {
                const r = await fetch(`/api/file?path=${encodeURIComponent(pickedPath)}`);
                if (!r.ok) throw new Error(`HTTP ${r.status}`);
                const text = await r.text();
                const name = pickedPath.split('/').pop() ?? pickedPath;
                loadJob(JSON.parse(text) as JobConfig, { kind: 'file', name, path: pickedPath });
              } catch (err) {
                alert('Could not open file: ' + (err as Error).message);
              }
            } else {
              // save
              try {
                const text = await serialiseJob();
                const r = await fetch('/api/file', {
                  method: 'PUT',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ path: pickedPath, content: text }),
                });
                if (!r.ok) throw new Error(`HTTP ${r.status}`);
                const name = pickedPath.split('/').pop() ?? pickedPath;
                setIdentity({ kind: 'file', name, path: pickedPath });
                setDirty(false);
              } catch (err) {
                alert('Could not save file: ' + (err as Error).message);
              }
            }
          }}
        />
      )}
    </div>
  );
}
