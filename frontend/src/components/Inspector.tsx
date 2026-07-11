import { Fragment, useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import type { FlowEdge, FlowNode } from '../lib/jsonAdapter';
import { displayUrl, nodeDisplayLabel, nodeDisplaySublabel } from '../lib/jsonAdapter';
import { displayName, lookupFriendlyName, useNamingMode } from '../lib/friendlyNames';
import type { Chapter, EncoderOverride, HWAccelProbe, HardwareDevice, Input, NodeDef, Output, ProbeResponse, ProbedStream, StreamSpec } from '../lib/jobTypes';
import { type BSFEntry, parseBSFChain, serializeBSFChain } from '../lib/bsf';
import { MEDIA_FILE_EXTENSIONS } from '../lib/mediaExtensions';
import { FileBrowser, type BrowseMode } from './FileBrowser';
import { EncoderForm } from './EncoderForm';
import { FilterForm } from './FilterForm';
import { HLSForm } from './HLSForm';
import { DASHForm } from './DASHForm';
import { TeeForm } from './TeeForm';
import { TimelineEditorDialog } from './TimelineEditorDialog';
import { describeKind } from './MMNode';

interface Props {
  node: FlowNode | null;
  /** Full node array, so the output form can resolve its upstream encoder. */
  nodes: FlowNode[];
  /** Full edge array, used to walk back from the output to the encoder. */
  edges: FlowEdge[];
  onChange: (next: FlowNode) => void;
  onDelete: (id: string) => void;
  /** Switch the canvas selection to a different node id. Used by the
   *  Wave 8 #45 multi-output tab strip so the user can flip between
   *  outputs without going back to the canvas. */
  onSelectNode?: (id: string) => void;
  /** Called when probe results arrive for an input node.  Kept separate from
   *  onChange so the caller can use a functional setNodes update and avoid
   *  overwriting concurrent URL changes made by the same event batch. */
  onProbedData: (nodeId: string, response: ProbeResponse | undefined) => void;
  /** Named hardware-acceleration device contexts available in the current
   *  job config. Passed to NodeForm to populate the device picker. (Wave 10 #60) */
  hwDevices?: HardwareDevice[];
  /** Accelerator probe results from GET /api/hwaccel. null = probe not yet
   *  returned; show all options as a fallback. The richer HWAccelProbe type
   *  carries SW format lists and codec enumerations used to annotate the
   *  dropdown and show a capability summary. */
  availableHWAccels?: HWAccelProbe[] | null;
}

export function Inspector({ node, nodes, edges, onChange, onDelete, onSelectNode, onProbedData, hwDevices = [], availableHWAccels = null }: Props) {
  if (!node) {
    return (
      <div className="inspector">
        <h3>Inspector</h3>
        <div className="empty">Select a node to view its properties.</div>
      </div>
    );
  }

  const ref = node.data.ref;
  const naming = useNamingMode();
  const friendly =
    node.data.friendlyName ?? lookupFriendlyName(node.data.label);
  const heading = displayName(
    { name: node.data.label, friendly_name: friendly },
    naming,
  );

  if (node.data.implicit) {
    return (
      <div className="inspector">
        <div className="inspector-header">
          <h3>{heading}</h3>
        </div>
        <div className="mm-node-type" style={{ marginBottom: 12 }}>
          {describeKind(node.data.kind, node.data.streams ?? [])} (implicit)
        </div>
        {node.data.sublabel && (
          <div style={{ fontSize: 12, color: 'var(--text-dim)', marginBottom: 12 }}>
            {node.data.sublabel}
          </div>
        )}
        <p style={{ fontSize: 12, color: 'var(--text-dim)', lineHeight: 1.5 }}>
          This stage is auto-generated from the surrounding inputs and outputs.
          The runtime instantiates it on your behalf — to change it, edit the
          input or output it belongs to.
        </p>
      </div>
    );
  }

  return (
    <div className="inspector">
      <div className="inspector-header">
        <h3>{heading}</h3>
        <button className="danger" onClick={() => onDelete(node.id)}>Delete</button>
      </div>
      <div className="mm-node-type" style={{ marginBottom: 12 }}>
        {describeKind(node.data.kind, node.data.streams ?? [])}
      </div>

      {ref.kind === 'input' && isDeviceInput(ref.def) && (
        <DeviceInputForm
          def={ref.def}
          probed={node.data.probed}
          onChange={(def) => onChange(updateRef(node, { kind: 'input', def }, def.id, displayUrl(def.url)))}
          onProbed={(resp) => onProbedData(node.id, resp)}
        />
      )}
      {ref.kind === 'input' && !isDeviceInput(ref.def) && (
        <InputForm
          def={ref.def}
          probed={node.data.probed}
          onChange={(def) => onChange(updateRef(node, { kind: 'input', def }, def.id, displayUrl(def.url)))}
          onProbed={(resp) => onProbedData(node.id, resp)}
          hwDevices={hwDevices}
          availableHWAccels={availableHWAccels}
        />
      )}
      {ref.kind === 'output' && (
        <>
          <OutputTabs
            nodes={nodes}
            currentId={node.id}
            onSelectNode={onSelectNode}
          />
          <OutputForm
            def={ref.def}
            upstreamCodecs={resolveUpstreamCodecs(nodes, edges, node.id)}
            onChange={(def) => onChange(updateRef(node, { kind: 'output', def }, def.id, displayUrl(def.url)))}
          />
        </>
      )}
      {ref.kind === 'node' && (
        <NodeForm
          def={ref.def}
          streams={node.data.streams}
          padHints={resolveUpstreamPad(nodes, edges, node.id)}
          hwDevices={hwDevices}
          inputIds={nodes.filter((n) => n.data.kind === 'input').map((n) => n.data.label)}
          onChange={(def) =>
            onChange(updateRef(node, { kind: 'node', def }, nodeDisplayLabel(def), nodeDisplaySublabel(def)))
          }
        />
      )}
    </div>
  );
}

function updateRef(node: FlowNode, ref: FlowNode['data']['ref'], label: string, sublabel: string): FlowNode {
  return {
    ...node,
    data: { ...node.data, ref, label, sublabel },
  };
}

/* ---------- Device-input helpers ---------- */
const DEVICE_FORMATS = new Set(['dshow', 'avfoundation', 'v4l2', 'gdigrab', 'decklink']);

// COVER_ART_FORMATS is the set of libavformat muxer names that support
// AV_DISPOSITION_ATTACHED_PIC cover art embedding (Wave 11 #64).
const COVER_ART_FORMATS = new Set(['mp4', 'm4a', 'mov', 'ipod', 'mp3', 'mkv', 'matroska']);

function isDeviceInput(def: Input): boolean {
  return !!(def.format && DEVICE_FORMATS.has(def.format));
}

/* ---------- Network-input helpers (Wave 11 #67) ---------- */

/** Extract the lower-cased scheme from a URL (token before "://"), or "" if none. */
function urlScheme(url: string): string {
  const m = /^([a-zA-Z][a-zA-Z0-9+\-.]*):\/\//.exec(url);
  return m ? m[1].toLowerCase() : '';
}

/** True for live-network schemes that may need protocol-specific AVOptions. */
function isNetworkInput(url: string): boolean {
  switch (urlScheme(url)) {
    case 'rtsp': case 'rtsps':
    case 'rtmp': case 'rtmps': case 'rtmpe': case 'rtmpt': case 'rtmpte':
    case 'srt':
    case 'rist':
    case 'rtp':
      return true;
  }
  return false;
}

const SCHEME_LABEL: Record<string, string> = {
  rtsp: 'RTSP', rtsps: 'RTSPS',
  rtmp: 'RTMP', rtmps: 'RTMPS', rtmpe: 'RTMPE', rtmpt: 'RTMPT', rtmpte: 'RTMPTE',
  srt: 'SRT',
  rist: 'RIST',
  rtp: 'RTP',
};

/** A small coloured badge showing the URL protocol. */
function SchemeBadge({ url }: { url: string }) {
  const scheme = urlScheme(url);
  const label = SCHEME_LABEL[scheme];
  if (!label) return null;
  return (
    <span
      className="url-scheme-badge"
      title={`Network protocol: ${label}`}
      style={{
        display: 'inline-block',
        fontSize: 10,
        fontFamily: 'monospace',
        fontWeight: 700,
        padding: '1px 5px',
        borderRadius: 3,
        marginLeft: 4,
        verticalAlign: 'middle',
        background: 'var(--accent-muted, #1e3a5f)',
        color: 'var(--accent, #4ea8de)',
        border: '1px solid var(--accent, #4ea8de)',
        letterSpacing: '0.04em',
        userSelect: 'none',
      }}
    >
      {label}
    </span>
  );
}

/** Protocol-specific option controls that write into Input.Options (AVDict passthrough). */
function NetworkInputSection({
  url,
  options,
  onChange,
}: {
  url: string;
  options: Record<string, unknown> | undefined;
  onChange: (opts: Record<string, unknown> | undefined) => void;
}) {
  const scheme = urlScheme(url);
  const setOpt = (key: string, value: string) => onChange(setDeviceOption(options, key, value));
  const getOpt = (key: string) => String(options?.[key] ?? '');

  if (scheme === 'rtsp' || scheme === 'rtsps') {
    return (
      <>
        <label style={{ marginTop: 10 }}>RTSP transport</label>
        <select
          value={getOpt('rtsp_transport')}
          onChange={(e) => setOpt('rtsp_transport', e.target.value)}
        >
          <option value="">Default (UDP)</option>
          <option value="tcp">TCP (recommended for firewalled networks)</option>
          <option value="udp">UDP</option>
          <option value="udp_multicast">UDP multicast</option>
          <option value="http">HTTP tunnelling</option>
        </select>
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
          <code>-rtsp_transport</code> — TCP is more reliable across NAT/firewalls.
        </div>
        <label>Socket timeout (µs)</label>
        <input
          type="number"
          min="0"
          value={getOpt('stimeout')}
          onChange={(e) => setOpt('stimeout', e.target.value)}
          placeholder="e.g. 5000000 (5 s)"
        />
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
          <code>-stimeout</code> — RTSP socket timeout in microseconds. 0 = no timeout.
        </div>
      </>
    );
  }

  if (scheme === 'srt') {
    return (
      <>
        <label style={{ marginTop: 10 }}>SRT mode</label>
        <select
          value={getOpt('mode')}
          onChange={(e) => setOpt('mode', e.target.value)}
        >
          <option value="">Default (caller)</option>
          <option value="caller">Caller — connect to a remote host</option>
          <option value="listener">Listener — wait for incoming connection</option>
          <option value="rendezvous">Rendezvous — symmetric hole-punch</option>
        </select>
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
          <code>-mode</code> — SRT connection role.
        </div>
        <label>Listen timeout (µs)</label>
        <input
          type="number"
          min="0"
          value={getOpt('listen_timeout')}
          onChange={(e) => setOpt('listen_timeout', e.target.value)}
          placeholder="e.g. 30000000 (30 s)"
        />
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
          <code>-listen_timeout</code> — Required in listener mode to prevent indefinite blocking.
          0 = no timeout.
        </div>
      </>
    );
  }

  if (scheme === 'rtmp' || scheme === 'rtmps' || scheme === 'rtmpe' ||
      scheme === 'rtmpt' || scheme === 'rtmpte') {
    return (
      <>
        <label style={{ marginTop: 10 }}>Connection timeout (µs)</label>
        <input
          type="number"
          min="0"
          value={getOpt('timeout')}
          onChange={(e) => setOpt('timeout', e.target.value)}
          placeholder="e.g. 10000000 (10 s)"
        />
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
          <code>-timeout</code> — Network I/O timeout in microseconds. 0 = no timeout.
        </div>
      </>
    );
  }

  // RIST, RTP, and other network schemes: no specialised controls — AVDict
  // passthrough via the generic Options dict in TimingFields handles them.
  return null;
}

type DeviceType = 'video' | 'audio' | 'screen';

interface DeviceEntry {
  name: string;
  description: string;
}

/** Mutate a copy of `options`, setting or deleting `key`. Returns undefined when the map is empty. */
function setDeviceOption(
  options: Record<string, unknown> | undefined,
  key: string,
  value: string,
): Record<string, unknown> | undefined {
  const next: Record<string, unknown> = { ...(options ?? {}) };
  if (value.trim() === '') {
    delete next[key];
  } else {
    next[key] = value;
  }
  return Object.keys(next).length === 0 ? undefined : next;
}

/** Build the libavdevice URL specifier from a device name, type, and format. */
function buildDeviceUrl(name: string, type: DeviceType, format: string, devices: DeviceEntry[]): string {
  switch (format) {
    case 'dshow':
      return type === 'audio' ? `audio="${name}"` : `video="${name}"`;
    case 'gdigrab':
      return name || 'desktop';
    case 'v4l2':
      return name;
    case 'avfoundation': {
      // URL is the device index in the enumerated list, or "none:<idx>" for audio-only.
      const idx = devices.findIndex((d) => d.name === name);
      const idxStr = idx >= 0 ? String(idx) : name;
      return type === 'audio' ? `none:${idxStr}` : idxStr;
    }
    default:
      return name;
  }
}

/** Extract the raw device name from a pre-built libavdevice URL. */
function extractDeviceName(url: string, format: string): string {
  if (!url) return '';
  if (format === 'dshow') {
    const m = /^(?:video|audio)="(.*)"$/.exec(url);
    return m ? m[1] : url;
  }
  return url;
}

/* ---------- Device input form (Wave 11 #63) ----------
 * Shown instead of InputForm when Input.format is a libavdevice demuxer name.
 * Provides:
 *   • Device type dropdown (video / audio / screen) where format allows it.
 *   • Async device-name combobox populated from GET /api/devices?format=<fmt>.
 *   • Raw URL field — auto-populated by the picker, also freely editable.
 *   • Common capture knobs: framerate, video_size, pixel_format, sample_rate.
 *   • "Test connection" probe button (passes format + options to /api/probe). */
function DeviceInputForm({
  def,
  probed,
  onChange,
  onProbed,
}: {
  def: Input;
  probed?: ProbedStream[];
  onChange: (next: Input) => void;
  onProbed: (next: ProbeResponse | undefined) => void;
}) {
  const format = def.format ?? 'dshow';

  const availableTypes: DeviceType[] =
    format === 'gdigrab' ? ['screen'] :
    format === 'v4l2'    ? ['video']  :
    ['video', 'audio']; // dshow, avfoundation, decklink

  const inferType = (): DeviceType => {
    if (availableTypes.length === 1) return availableTypes[0];
    if (def.url.startsWith('audio=') || def.url.startsWith('none:')) return 'audio';
    return 'video';
  };

  const [deviceType, setDeviceType] = useState<DeviceType>(inferType);
  const [devices, setDevices] = useState<DeviceEntry[]>([]);
  const [loadingDevices, setLoadingDevices] = useState(false);
  const [deviceError, setDeviceError] = useState<string | null>(null);
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState<string | null>(null);

  useEffect(() => {
    setLoadingDevices(true);
    setDeviceError(null);
    fetch(`/api/devices?format=${encodeURIComponent(format)}`)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((d: DeviceEntry[]) => setDevices(d ?? []))
      .catch((e: Error) => setDeviceError(e.message))
      .finally(() => setLoadingDevices(false));
  }, [format]);

  const runProbe = async () => {
    if (!def.url) { setProbeError('Select a device first.'); return; }
    setProbing(true);
    setProbeError(null);
    try {
      const r = await fetch('/api/probe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: def.url, format: def.format, options: def.options }),
      });
      if (!r.ok) throw new Error((await r.text()) || `HTTP ${r.status}`);
      const resp = (await r.json()) as ProbeResponse;
      onProbed(resp);
    } catch (err) {
      setProbeError((err as Error).message);
      onProbed(undefined);
    } finally {
      setProbing(false);
    }
  };

  const listId = `device-names-${format}`;
  const currentName = extractDeviceName(def.url, format);

  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <label>Format</label>
      <div className="inspector-canonical" style={{ marginBottom: 8 }}>
        <code>{format}</code>
      </div>
      {availableTypes.length > 1 && (
        <>
          <label>Device type</label>
          <select
            value={deviceType}
            onChange={(e) => {
              const t = e.target.value as DeviceType;
              setDeviceType(t);
              if (currentName) onChange({ ...def, url: buildDeviceUrl(currentName, t, format, devices) });
            }}
          >
            {availableTypes.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        </>
      )}
      <label style={{ marginTop: 8 }}>Device</label>
      {loadingDevices && (
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>Listing devices…</div>
      )}
      {deviceError && <div className="probe-error">{deviceError}</div>}
      {/* key=format resets the uncontrolled input when the format changes */}
      <input
        key={format}
        list={listId}
        defaultValue={currentName}
        placeholder={devices.length === 0 && !loadingDevices ? 'No devices found — type a name' : 'Select or type device name…'}
        onBlur={(e) => {
          const name = e.target.value.trim();
          if (!name) return;
          const url = buildDeviceUrl(name, deviceType, format, devices);
          onChange({ ...def, url });
          if (probed) onProbed(undefined);
        }}
      />
      <datalist id={listId}>
        {devices.map((d) => (
          <option key={d.name} value={d.name}>{d.description || d.name}</option>
        ))}
      </datalist>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 8 }}>
        {format === 'dshow'        && 'Device name as reported by Windows (e.g. "Integrated Camera").'}
        {format === 'avfoundation' && 'Device index from the enumerated list.'}
        {format === 'v4l2'         && 'Device node path (e.g. /dev/video0).'}
        {format === 'gdigrab'      && 'Window title, or leave empty for the full desktop.'}
        {format === 'decklink'     && 'Blackmagic DeckLink device name.'}
      </div>
      <label>Device URL</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
        Full specifier sent to libavdevice — auto-populated above, or edit directly.
      </div>
      <input
        value={def.url}
        onChange={(e) => {
          onChange({ ...def, url: e.target.value });
          if (probed) onProbed(undefined);
        }}
      />
      <div className="probe-actions">
        {def.url ? (
          <button onClick={runProbe} disabled={probing}>
            {probing ? 'Testing…' : 'Test connection'}
          </button>
        ) : (
          <div className="empty" style={{ fontSize: 11 }}>
            Select a device above to test the connection.
          </div>
        )}
        {probed && (
          <button className="link-btn" onClick={() => onProbed(undefined)} title="Discard probed metadata">
            Clear
          </button>
        )}
      </div>
      {probeError && <div className="probe-error">{probeError}</div>}
      {probed && <ProbedStreamsView streams={probed} />}
      <label style={{ marginTop: 12 }}>Capture options</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
        Common knobs passed to the demuxer as AVOptions. Leave empty to use device defaults.
      </div>
      <Field
        label="Frame rate"
        value={String(def.options?.['framerate'] ?? '')}
        onChange={(v) => onChange({ ...def, options: setDeviceOption(def.options, 'framerate', v) })}
      />
      <Field
        label="Video size (e.g. 1280x720)"
        value={String(def.options?.['video_size'] ?? '')}
        onChange={(v) => onChange({ ...def, options: setDeviceOption(def.options, 'video_size', v) })}
      />
      <Field
        label="Pixel format"
        value={String(def.options?.['pixel_format'] ?? '')}
        onChange={(v) => onChange({ ...def, options: setDeviceOption(def.options, 'pixel_format', v) })}
      />
      <Field
        label="Sample rate (Hz)"
        value={String(def.options?.['sample_rate'] ?? '')}
        onChange={(v) => onChange({ ...def, options: setDeviceOption(def.options, 'sample_rate', v) })}
      />
    </>
  );
}

/* ---------- Input form ---------- */

// All hardware accelerator options in display order. Filtered at render time
// by the availableHWAccels list returned from GET /api/hwaccel.
const HW_ACCELS: Array<{ value: string; label: string }> = [
  { value: 'cuda',         label: 'cuda (NVIDIA NVDEC)' },
  { value: 'vaapi',        label: 'vaapi (Intel/AMD VA-API)' },
  { value: 'videotoolbox', label: 'videotoolbox (Apple)' },
  { value: 'qsv',          label: 'qsv (Intel Quick Sync)' },
  { value: 'd3d11va',      label: 'd3d11va (Windows Direct3D 11)' },
  { value: 'dxva2',        label: 'dxva2 (Windows DXVA2)' },
  { value: 'auto',         label: 'auto' },
];

function InputForm({
  def,
  probed,
  onChange,
  onProbed,
  hwDevices = [],
  availableHWAccels = null,
}: {
  def: Input;
  probed?: ProbedStream[];
  onChange: (next: Input) => void;
  onProbed: (next: ProbeResponse | undefined) => void;
  hwDevices?: HardwareDevice[];
  /** null = probe not yet returned; show all options. HWAccelProbe[] =
   *  full probe results including SW formats and codec lists. */
  availableHWAccels?: HWAccelProbe[] | null;
}) {
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState<string | null>(null);

  const probeUrl = async (url: string) => {
    setProbing(true);
    setProbeError(null);
    try {
      const r = await fetch('/api/probe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url, options: def.options }),
      });
      if (!r.ok) {
        const body = await r.text();
        throw new Error(body || `HTTP ${r.status}`);
      }
      const resp = (await r.json()) as ProbeResponse;
      onProbed(resp);
    } catch (err) {
      setProbeError((err as Error).message);
      onProbed(undefined);
    } finally {
      setProbing(false);
    }
  };

  const runProbe = () => {
    if (!def.url) { setProbeError('Set a URL first.'); return; }
    probeUrl(def.url);
  };

  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <FileField
        label="URL"
        value={def.url}
        mode="open"
        filter={MEDIA_FILE_EXTENSIONS}
        onChange={(v) => {
          onChange({ ...def, url: v });
          // Stale once the URL changes.
          if (probed) onProbed(undefined);
        }}
        onBrowsePick={(path) => {
          onChange({ ...def, url: path });
          probeUrl(path);
        }}
      />
      {/* Wave 11 #67: show protocol badge when URL is a network scheme. */}
      {isNetworkInput(def.url) && (
        <div style={{ marginTop: -4, marginBottom: 4, fontSize: 11, color: 'var(--text-dim)' }}>
          <SchemeBadge url={def.url} />
          {' '}Network stream
        </div>
      )}
      <div className="probe-actions">
        {def.url && def.url.trim() ? (
          <button onClick={runProbe} disabled={probing}>
            {probing ? 'Probing…' : 'Get properties'}
          </button>
        ) : (
          <div className="empty" style={{ fontSize: 11 }}>
            Set a URL above to probe the file's technical properties.
          </div>
        )}
        {probed && (
          <button className="link-btn" onClick={() => onProbed(undefined)} title="Discard probed metadata">
            Clear
          </button>
        )}
      </div>
      {probeError && <div className="probe-error">{probeError}</div>}
      {probed && <ProbedStreamsView streams={probed} />}
      {/* Wave 11 #67: network-protocol specific option controls. */}
      {isNetworkInput(def.url) && (
        <NetworkInputSection
          url={def.url}
          options={def.options}
          onChange={(opts) => onChange({ ...def, options: opts })}
        />
      )}
      <label style={{ marginTop: 12 }}>Subtitle charset</label>
      <input
        list="sub-charenc-list"
        value={def.subtitle_charenc ?? ''}
        onChange={(e) => onChange({ ...def, subtitle_charenc: e.target.value || undefined })}
        placeholder="UTF-8 (default)"
      />
      <datalist id="sub-charenc-list">
        {SUBTITLE_CHARSETS.map((c) => <option key={c} value={c} />)}
      </datalist>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
        Character encoding for text subtitle streams (<code>-sub_charenc</code>).
        Leave empty for the UTF-8 default. Applies to SRT, ASS, SSA; ignored
        for bitmap subtitles (PGS, DVB).
      </div>
      {/* Wave 10 #59: per-input hardware-accelerated decoding. */}
      <div style={{ marginTop: 12, marginBottom: 4 }}>
        <label>HW decode accelerator</label>
        {(() => {
          // null → probe not yet returned → show everything as fallback.
          // HWAccelProbe[] → filter to confirmed-available types.
          const probes = availableHWAccels;
          const availableTypes = probes?.filter((p) => p.available).map((p) => p.type) ?? null;
          const filtered = availableTypes === null
            ? HW_ACCELS
            : HW_ACCELS.filter((a) =>
                a.value === 'auto'
                  ? availableTypes.length > 0
                  : availableTypes.includes(a.value),
              );
          // If the job was authored on a different machine, preserve the
          // current value as a disabled option so it's visible but not selectable.
          const currentMissing =
            def.hwaccel &&
            availableTypes !== null &&
            !filtered.some((a) => a.value === def.hwaccel);
          return (
            <select
              value={def.hwaccel ?? ''}
              onChange={(e) => onChange({ ...def, hwaccel: e.target.value || undefined })}
              style={{ display: 'block', width: '100%', marginTop: 4, marginBottom: 4 }}
            >
              <option value="">(none — software decode)</option>
              {currentMissing && (
                <option value={def.hwaccel} disabled>
                  {def.hwaccel} (not available on this machine)
                </option>
              )}
              {filtered.map((a) => (
                <option key={a.value} value={a.value}>{a.label}</option>
              ))}
            </select>
          );
        })()}
        {availableHWAccels !== null && availableHWAccels.length === 0 && (
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
            No hardware accelerators detected on this machine.
          </div>
        )}
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -2, marginBottom: 6 }}>
          <code>-hwaccel</code> — selects the hardware decode API for this input.
        </div>

        {/* Inline stream-level HW decode scope hint. */}
        {(() => {
          if (!def.hwaccel || !probed || probed.length === 0) return null;
          const probe = availableHWAccels?.find((p) => p.type === def.hwaccel);
          // When probe data is available use its codec list; otherwise assume
          // video-only acceleration (true for all shipping hwaccel backends).
          const hwDecoderNames = probe?.codecs
            ?.filter((c) => c.role === 'decode')
            .map((c) => c.name.toLowerCase()) ?? null;

          const videoStreams = probed.filter((s) => s.type === 'video');
          const audioStreams = probed.filter((s) => s.type === 'audio');

          const willAccelerate = (s: ProbedStream) => {
            if (!hwDecoderNames) return s.type === 'video'; // fallback: video only
            return hwDecoderNames.some(
              (n) => s.codec && (n === s.codec.toLowerCase() || n.startsWith(s.codec.toLowerCase())),
            );
          };

          const hwVideo = videoStreams.filter(willAccelerate);
          const hwAudio = audioStreams.filter(willAccelerate);
          const swVideo = videoStreams.filter((s) => !willAccelerate(s));
          const swAudio = audioStreams.filter((s) => !willAccelerate(s));

          const parts: string[] = [];
          if (hwVideo.length > 0) {
            const tag = hwVideo.map((s) => s.codec ?? 'video').join(', ');
            parts.push(`video (${tag})`);
          }
          if (hwAudio.length > 0) {
            const tag = hwAudio.map((s) => s.codec ?? 'audio').join(', ');
            parts.push(`audio (${tag})`);
          }
          const swParts: string[] = [];
          if (swVideo.length > 0) swParts.push('video');
          if (swAudio.length > 0) swParts.push('audio');

          return (
            <div style={{ fontSize: 11, marginBottom: 6 }}>
              {parts.length > 0 ? (
                <span>
                  <span style={{ color: 'var(--accent, #4caf50)' }}>HW decode:</span>{' '}
                  {parts.join(', ')}
                </span>
              ) : (
                <span style={{ color: 'var(--text-dim)' }}>
                  No streams in this input match a supported HW decoder.
                </span>
              )}
              {swParts.length > 0 && parts.length > 0 && (
                <span style={{ color: 'var(--text-dim)' }}>
                  {' '}· SW fallback: {swParts.join(', ')}
                </span>
              )}
            </div>
          );
        })()}

        {/* Capability summary for the selected accelerator. */}
        {(() => {
          if (!def.hwaccel || def.hwaccel === 'auto' || !availableHWAccels) return null;
          const probe = availableHWAccels.find((p) => p.type === def.hwaccel);
          if (!probe?.available) return null;
          const decoders = probe.codecs?.filter((c) => c.role === 'decode') ?? [];
          const encoders = probe.codecs?.filter((c) => c.role === 'encode') ?? [];
          const hasCaps = decoders.length > 0 || encoders.length > 0 ||
                          (probe.sw_formats?.length ?? 0) > 0 ||
                          probe.max_width || probe.cuda_arch;
          if (!hasCaps) return null;
          // Helper: render codec names with per-codec notes as a footnote block.
          const CodecList = ({ list, label }: { list: typeof decoders; label: string }) => {
            if (list.length === 0) return null;
            const noted = list.filter((c) => c.note);
            return (
              <div>
                <strong>{label}:</strong>{' '}
                {list.map((c) => c.name).join(', ')}
                {noted.length > 0 && (
                  <div style={{ color: 'var(--text-dim)', paddingLeft: 8, marginTop: 2 }}>
                    {noted.map((c) => (
                      <div key={c.name}>{c.name}: {c.note}</div>
                    ))}
                  </div>
                )}
              </div>
            );
          };
          return (
            <div style={{
              fontSize: 11,
              background: 'var(--panel-bg, rgba(255,255,255,0.04))',
              border: '1px solid var(--border, rgba(255,255,255,0.1))',
              borderRadius: 4,
              padding: '6px 8px',
              marginBottom: 8,
              lineHeight: 1.6,
            }}>
              {probe.cuda_arch && (
                <div>
                  <strong>Architecture:</strong>{' '}
                  {probe.cuda_arch}{probe.cuda_sm ? ` (SM ${probe.cuda_sm})` : ''}
                </div>
              )}
              <CodecList list={decoders} label="Decoders" />
              <CodecList list={encoders} label="Encoders" />
              {(probe.sw_formats?.length ?? 0) > 0 && (
                <div>
                  <strong>SW formats:</strong>{' '}
                  {probe.sw_formats!.join(', ')}
                </div>
              )}
              {(probe.max_width ?? 0) > 0 && (
                <div>
                  <strong>Max resolution:</strong>{' '}
                  {probe.max_width}×{probe.max_height}
                </div>
              )}
            </div>
          );
        })()}

        {def.hwaccel && (
          <>
            <label>HW device</label>
            <select
              value={def.hwaccel_device ?? ''}
              onChange={(e) => onChange({ ...def, hwaccel_device: e.target.value || undefined })}
              style={{ display: 'block', width: '100%', marginTop: 4, marginBottom: 4 }}
            >
              <option value="">(auto)</option>
              {hwDevices.map((d) => (
                <option key={d.name} value={d.name}>{d.name} [{d.type}]</option>
              ))}
            </select>
            <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -2, marginBottom: 6 }}>
              <code>-hwaccel_device</code> — reuse a named device context from{' '}
              <code>hardware_devices</code>, or leave empty for a transient context.
            </div>

            <label>HW output pixel format</label>
            <input
              value={def.hwaccel_output_format ?? ''}
              onChange={(e) => onChange({ ...def, hwaccel_output_format: e.target.value || undefined })}
              placeholder="e.g. cuda, nv12, yuv420p (empty = transfer to RAM)"
            />
            <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
              <code>-hwaccel_output_format</code> — keep frames on the GPU (e.g.{' '}
              <code>cuda</code>) for zero-copy filter chains, or use a software
              format to transfer automatically.
            </div>
          </>
        )}
      </div>

      <TimingFields
        kind="input"
        options={def.options}
        onChange={(opts) => onChange({ ...def, options: opts })}
      />
      <label style={{ marginTop: 12 }}>Seek &amp; loop</label>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        <input
          type="checkbox"
          id="in-accurate-seek"
          checked={def.accurate_seek ?? false}
          onChange={(e) => onChange({ ...def, accurate_seek: e.target.checked || undefined })}
        />
        <label htmlFor="in-accurate-seek" style={{ margin: 0, fontSize: 12 }}>
          Accurate seek (<code>-accurate_seek</code>)
        </label>
      </div>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 6 }}>
        Decode from nearest keyframe to reach the exact <code>-ss</code> position.
        Slower but frame-accurate. Keyframe seek is the default for stream copy.
      </div>
      <label style={{ marginTop: 4 }}>Loop count (<code>-stream_loop</code>)</label>
      <input
        type="number"
        min="-1"
        value={def.stream_loop ?? ''}
        placeholder="-1 = infinite, 0 = no loop, N = N extra plays"
        onChange={(e) => {
          const v = e.target.value.trim();
          onChange({ ...def, stream_loop: v === '' ? undefined : parseInt(v, 10) });
        }}
      />
      <label style={{ marginTop: 4 }}>Input timestamp offset (<code>-itsoffset</code>)</label>
      <input
        type="number"
        step="any"
        value={def.itsoffset ?? ''}
        placeholder="Seconds; positive = delay this input"
        onChange={(e) => {
          const v = e.target.value.trim();
          onChange({ ...def, itsoffset: v === '' ? undefined : parseFloat(v) });
        }}
      />
      <label style={{ marginTop: 12 }}>Metadata &amp; chapters</label>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
        <input
          type="checkbox"
          id="in-map-metadata"
          checked={def.map_metadata ?? true}
          onChange={(e) => onChange({ ...def, map_metadata: e.target.checked })}
        />
        <label htmlFor="in-map-metadata" style={{ margin: 0, fontSize: 12 }}>
          Map metadata from this input (<code>-map_metadata</code>)
        </label>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
        <input
          type="checkbox"
          id="in-map-chapters"
          checked={def.map_chapters ?? true}
          onChange={(e) => onChange({ ...def, map_chapters: e.target.checked })}
        />
        <label htmlFor="in-map-chapters" style={{ margin: 0, fontSize: 12 }}>
          Map chapters from this input (<code>-map_chapters</code>)
        </label>
      </div>
    </>
  );
}

/** Read-only summary of probed streams. Drives the canvas edge attribute
 *  chips via FlowNodeData.probed (see lib/streamAttrs.ts). */
function ProbedStreamsView({ streams }: { streams: ProbedStream[] }) {
  if (!streams.length) return <div className="empty">No streams reported.</div>;
  return (
    <div className="probed-streams">
      <label style={{ marginTop: 12 }}>Probed streams</label>
      {streams.map((s) => (
        <div key={s.index} className="probed-stream">
          <div className="probed-stream-head">
            <span className={`stream-pill stream-${s.type}`}>{s.type}</span>
            <span className="probed-stream-idx">#{s.index}</span>
            {s.codec && <span className="probed-stream-codec">{s.codec}</span>}
            {s.codec_tag && <span className="probed-stream-codec">{s.codec_tag}</span>}
          </div>
          <dl className="probed-stream-attrs">
            {/* Common */}
            {s.bit_rate ? <Pair k="bit_rate" v={formatBitRate(s.bit_rate)} /> : null}
            {s.profile && <Pair k="profile" v={s.profile + (s.level ? `@L${formatLevel(s.profile, s.level)}` : '')} />}
            {s.bit_depth ? <Pair k="bit_depth" v={`${s.bit_depth} bit`} /> : null}
            {/* Video */}
            {s.width && s.height && <Pair k="size" v={`${s.width}×${s.height}`} />}
            {s.sar && s.sar !== '1:1' && <Pair k="sar" v={s.sar} />}
            {s.pix_fmt && <Pair k="pix_fmt" v={s.pix_fmt} />}
            {s.frame_rate && <Pair k="frame_rate" v={`${s.frame_rate} fps`} />}
            {s.r_frame_rate && <Pair k="r_frame_rate" v={`${s.r_frame_rate} fps`} />}
            {s.field_order && s.field_order !== 'progressive' && <Pair k="field_order" v={s.field_order} />}
            {s.color_space && <Pair k="color_space" v={s.color_space} />}
            {s.color_range && <Pair k="color_range" v={s.color_range} />}
            {s.color_primaries && <Pair k="color_primaries" v={s.color_primaries} />}
            {s.color_transfer && <Pair k="color_transfer" v={s.color_transfer} />}
            {/* Audio */}
            {s.sample_rate ? <Pair k="sample_rate" v={`${s.sample_rate} Hz`} /> : null}
            {s.sample_fmt && <Pair k="sample_fmt" v={s.sample_fmt} />}
            {s.channel_layout && <Pair k="channels" v={`${s.channels ?? '?'} (${s.channel_layout})`} />}
            {/* Timing */}
            {s.duration_sec ? <Pair k="duration" v={formatDuration(s.duration_sec)} /> : null}
            {s.start_sec ? <Pair k="start" v={`${s.start_sec.toFixed(3)} s`} /> : null}
          </dl>
        </div>
      ))}
    </div>
  );
}

function formatBitRate(bps: number): string {
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(2)} Mbps`;
  if (bps >= 1_000) return `${(bps / 1_000).toFixed(0)} kbps`;
  return `${bps} bps`;
}

function formatDuration(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec - h * 3600 - m * 60;
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${s.toFixed(2).padStart(5, '0')}`;
  return `${m}:${s.toFixed(2).padStart(5, '0')} (${sec.toFixed(2)} s)`;
}

// H.264 levels are reported as integers like 41 (=> 4.1). Other codecs use
// the raw value. Render H.264/HEVC profiles with a decimal level.
function formatLevel(profile: string, level: number): string {
  const p = profile.toLowerCase();
  if (p.includes('h.264') || p.includes('avc') || level >= 10) {
    return `${Math.floor(level / 10)}.${level % 10}`;
  }
  return String(level);
}

function Pair({ k, v }: { k: string; v: string }) {
  return (
    <>
      <dt>{k}</dt>
      <dd>{v}</dd>
    </>
  );
}

/* ---------- Output form ---------- */
interface UpstreamCodecs {
  video?: { codec: string; sourceLabel: string };
  audio?: { codec: string; sourceLabel: string };
  subtitle?: { codec: string; sourceLabel: string };
}

function OutputForm({
  def,
  upstreamCodecs,
  onChange,
}: {
  def: Output;
  upstreamCodecs: UpstreamCodecs;
  onChange: (next: Output) => void;
}) {
  // The codec actually used by each stream is whatever encoder is wired
  // upstream of this output in the graph. The legacy codec_video / codec_audio
  // / codec_subtitle fields on Output are only used when no upstream encoder
  // is present (the implicit-encoder case), so prefer the resolved upstream
  // value and fall back to the explicit field, then to "(default)".
  const effVideo = upstreamCodecs.video?.codec || def.codec_video || '';
  const effAudio = upstreamCodecs.audio?.codec || def.codec_audio || '';
  const effSubtitle = upstreamCodecs.subtitle?.codec || def.codec_subtitle || '';

  // When the video stream is HEVC and codec_tag_video is still unset, default
  // it to "hvc1" so the resulting MP4 plays in QuickTime/Safari/iOS without
  // the user having to know about the hev1/hvc1 distinction.
  useEffect(() => {
    if (def.codec_tag_video) return;
    if (!isHEVC(effVideo)) return;
    onChange({ ...def, codec_tag_video: 'hvc1' });
    // Only react to changes in the upstream codec or the current tag value;
    // intentionally skip onChange / def in deps to avoid a feedback loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [effVideo, def.codec_tag_video]);

  const isTee = def.kind === 'tee';
  const [subtitleMode, setSubtitleMode] = useState<'soft-mux' | 'burn-in'>('soft-mux');
  const compatWarn = subtitleMode === 'soft-mux'
    ? subtitleCompatWarning(effSubtitle, def.format)
    : null;

  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />

      {/* Output kind: file (default) or tee (multi-target) */}
      <label>Output kind</label>
      <select
        value={def.kind ?? ''}
        onChange={(e) => {
          const k = e.target.value as Output['kind'];
          onChange({ ...def, kind: k || undefined });
        }}
      >
        <option value="">(default — file)</option>
        <option value="file">file</option>
        <option value="tee">tee (multi-output)</option>
      </select>

      {/* Regular file output: URL, format, codecs, timing, BSFs */}
      {!isTee && (
        <>
          <FileField
            label="URL"
            value={def.url}
            mode="save"
            defaultFilename={def.segment_on_metadata ? 'out/shot-%05d.mp4' : 'output.mp4'}
            onChange={(v) => onChange({ ...def, url: v })}
          />
          {def.segment_on_metadata && !/%[0-9]*d/.test(def.url) && (
            <div style={{ fontSize: 11, color: 'var(--warn, #f59e0b)', marginBottom: 4 }}>
              ⚠ URL must contain a printf integer verb (e.g. <code>%05d</code>) for per-segment numbering.
            </div>
          )}
          <Field label="Format" value={def.format ?? ''} onChange={(v) => onChange({ ...def, format: v || undefined })} />

          {/* Segment splitting ------------------------------------------------------------ */}
          <label style={{ marginTop: 12 }}>Segment splitting</label>
          <Field
            label="Split on metadata key"
            value={def.segment_on_metadata ?? ''}
            placeholder="e.g. scene_change"
            onChange={(v) => onChange({ ...def, segment_on_metadata: v || undefined })}
          />
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
            When set, the output closes and re-opens at the next video keyframe each time an
            upstream processor emits an event with this key set. The URL must contain{' '}
            <code>%05d</code> (or similar) for per-segment numbering.{' '}
            Example: key <code>scene_change</code>, URL <code>out/shot-%05d.mp4</code>.
          </div>
          <Field
            label="Segment format"
            value={def.segment_format ?? ''}
            placeholder="(from URL extension)"
            onChange={(v) => onChange({ ...def, segment_format: v || undefined })}
          />
          {/* ----------------------------------------------------------------------------- */}
          <CodecRow
            label="Codec (video)"
            upstream={upstreamCodecs.video}
            explicit={def.codec_video}
            onClear={() => onChange({ ...def, codec_video: undefined })}
            onEdit={(v) => onChange({ ...def, codec_video: v || undefined })}
          />
          <CodecRow
            label="Codec (audio)"
            upstream={upstreamCodecs.audio}
            explicit={def.codec_audio}
            onClear={() => onChange({ ...def, codec_audio: undefined })}
            onEdit={(v) => onChange({ ...def, codec_audio: v || undefined })}
          />
          {/* Subtitle rendering mode --------------------------------------------------------- */}
          <label style={{ marginTop: 10 }}>Subtitle rendering</label>
          <select
            value={subtitleMode}
            onChange={(e) => setSubtitleMode(e.target.value as 'soft-mux' | 'burn-in')}
            style={{ background: 'var(--panel-2)', color: 'var(--text)', border: '1px solid var(--border)', borderRadius: 4, padding: '5px 7px', fontSize: 12, width: '100%' }}
          >
            <option value="soft-mux">Soft-mux (subtitle stream in output)</option>
            <option value="burn-in">Burn-in (rendered via filter into video)</option>
          </select>
          {subtitleMode === 'burn-in' && (
            <div className="subtitle-burnin-hint">
              Add a <code>subtitles=</code> (SRT / ASS) or <code>ass=</code>{' '}
              (styled ASS) filter node to the graph and connect it to the video
              stream. No separate subtitle stream is written to the output.
            </div>
          )}
          {subtitleMode === 'soft-mux' && (
            <>
              <CodecRow
                label="Codec (subtitle)"
                upstream={upstreamCodecs.subtitle}
                explicit={def.codec_subtitle}
                onClear={() => onChange({ ...def, codec_subtitle: undefined })}
                onEdit={(v) => onChange({ ...def, codec_subtitle: v || undefined })}
              />
              {compatWarn && (
                <div className="subtitle-compat-warn">⚠ {compatWarn}</div>
              )}
            </>
          )}
          {/* ----------------------------------------------------------------------------- */}
          <TagField
            label="Codec tag (video)"
            value={def.codec_tag_video ?? ''}
            suggestions={tagsForVideo(effVideo)}
            onChange={(v) => onChange({ ...def, codec_tag_video: v || undefined })}
          />
          <TagField
            label="Codec tag (audio)"
            value={def.codec_tag_audio ?? ''}
            suggestions={tagsForAudio(effAudio)}
            onChange={(v) => onChange({ ...def, codec_tag_audio: v || undefined })}
          />
          {subtitleMode === 'soft-mux' && (
            <TagField
              label="Codec tag (subtitle)"
              value={def.codec_tag_subtitle ?? ''}
              suggestions={tagsForSubtitle(effSubtitle)}
              onChange={(v) => onChange({ ...def, codec_tag_subtitle: v || undefined })}
            />
          )}
          <TimingFields
            kind="output"
            options={def.options}
            onChange={(opts) => onChange({ ...def, options: opts })}
          />
          <label style={{ marginTop: 12 }}>Stream control</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
            <input
              type="checkbox"
              id="out-shortest"
              checked={def.shortest ?? false}
              onChange={(e) => onChange({ ...def, shortest: e.target.checked || undefined })}
            />
            <label htmlFor="out-shortest" style={{ margin: 0, fontSize: 12 }}>
              Stop at shortest stream (<code>-shortest</code>)
            </label>
          </div>
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 6 }}>
            Closes the output when the first stream finishes. Recommended for stream-copy trim
            jobs where keyframe alignment may make the video track end before audio.
          </div>
          <label style={{ marginTop: 4 }}>FPS mode (<code>-fps_mode</code>)</label>
          <select
            value={def.fps_mode ?? ''}
            onChange={(e) =>
              onChange({ ...def, fps_mode: (e.target.value as typeof def.fps_mode) || undefined })
            }
          >
            <option value="">Default</option>
            <option value="passthrough">passthrough — copy timestamps as-is</option>
            <option value="vfr">vfr — variable frame rate</option>
            <option value="cfr">cfr — constant frame rate (drop/dup frames)</option>
            <option value="drop">drop — drop frames to match container rate</option>
          </select>
          <label style={{ marginTop: 4 }}>Avoid negative timestamps (<code>-avoid_negative_ts</code>)</label>
          <select
            value={def.avoid_negative_ts ?? ''}
            onChange={(e) =>
              onChange({
                ...def,
                avoid_negative_ts:
                  (e.target.value as typeof def.avoid_negative_ts) || undefined,
              })
            }
          >
            <option value="">Default (auto)</option>
            <option value="auto">auto — let muxer decide</option>
            <option value="disabled">disabled — allow negative timestamps</option>
            <option value="make_non_negative">make_non_negative — shift to ≥ 0</option>
            <option value="make_zero">make_zero — shift so first timestamp is 0</option>
          </select>
          <BSFEditor
            label="Bitstream filters (video)"
            kind="video"
            spec={def.bsf_video}
            onChange={(s) => onChange({ ...def, bsf_video: s })}
          />
          <BSFEditor
            label="Bitstream filters (audio)"
            kind="audio"
            spec={def.bsf_audio}
            onChange={(s) => onChange({ ...def, bsf_audio: s })}
          />
          {subtitleMode === 'soft-mux' && (
            <BSFEditor
              label="Bitstream filters (subtitle)"
              kind="subtitle"
              spec={def.bsf_subtitle}
              onChange={(s) => onChange({ ...def, bsf_subtitle: s })}
            />
          )}
          {/* Cover art — shown only for containers that support AV_DISPOSITION_ATTACHED_PIC */}
          {COVER_ART_FORMATS.has(def.format ?? '') && (
            <FileField
              label="Cover art"
              value={def.cover_art ?? ''}
              mode="open"
              filter="image/jpeg,image/png,image/webp,image/*"
              onChange={(v) => onChange({ ...def, cover_art: v || undefined })}
            />
          )}
        </>
      )}

      {/* HLS wizard — shown when format is hls */}
      {!isTee && def.format === 'hls' && (
        <HLSForm
          hls={def.hls ?? {}}
          onChange={(h) => onChange({ ...def, hls: h })}
        />
      )}

      {/* DASH wizard — shown when format is dash */}
      {!isTee && def.format === 'dash' && (
        <DASHForm
          dash={def.dash ?? {}}
          onChange={(d) => onChange({ ...def, dash: d })}
        />
      )}

      {/* Tee multi-output target list */}
      {isTee && (
        <TeeForm
          targets={def.targets ?? []}
          onChange={(t) => onChange({ ...def, targets: t })}
        />
      )}

      <MetadataEditor
        label="Container metadata"
        hint={<>Per-output container tags (<code>-metadata key=value</code>): <code>title</code>, <code>artist</code>, <code>comment</code>, <code>genre</code>, <code>date</code>, …</>}
        metadata={def.metadata}
        onChange={(m) => onChange({ ...def, metadata: m })}
      />
      <ChaptersEditor
        chapters={def.chapters}
        onChange={(c) => onChange({ ...def, chapters: c })}
      />
      <StreamsEditor
        streams={def.streams}
        onChange={(streams) => onChange({ ...def, streams })}
      />
    </>
  );
}

/* ---------- Codec row: read-only when an upstream encoder is wired ---------- */
function CodecRow({
  label,
  upstream,
  explicit,
  onClear,
  onEdit,
}: {
  label: string;
  upstream: { codec: string; sourceLabel: string } | undefined;
  explicit: string | undefined;
  onClear: () => void;
  onEdit: (v: string) => void;
}) {
  // When the graph has an encoder feeding this output for this stream type,
  // the encoder's codec is what actually gets used: show it read-only and
  // tell the user where to edit it. Otherwise fall back to the legacy
  // editable text field (for users who want to declare a codec on the
  // sink for the implicit-encoder case).
  if (upstream) {
    return (
      <>
        <label>{label}</label>
        <div className="readonly-codec" title={`Set on encoder node "${upstream.sourceLabel}"`}>
          <span>{upstream.codec}</span>
          <small style={{ marginLeft: 8, color: 'var(--text-dim)' }}>
            from {upstream.sourceLabel}
          </small>
          {explicit && explicit !== upstream.codec && (
            <button
              type="button"
              style={{ marginLeft: 8, fontSize: 11 }}
              onClick={onClear}
              title={`Override "${explicit}" set on this output is ignored while the upstream encoder is wired. Click to clear it.`}
            >
              clear override ({explicit})
            </button>
          )}
        </div>
      </>
    );
  }
  return <Field label={label} value={explicit ?? ''} onChange={onEdit} />;
}

/** Walk back through the graph from an output's flow-node id to find the
 *  encoder feeding each stream type. Stops at the first encoder hit, or
 *  returns undefined for a given stream type if no encoder is reachable
 *  (e.g. a stream-copy node forwards demuxer packets directly). */
function resolveUpstreamCodecs(
  nodes: FlowNode[],
  edges: FlowEdge[],
  outputFlowId: string,
): UpstreamCodecs {
  const result: UpstreamCodecs = {};
  const types: Array<'video' | 'audio' | 'subtitle'> = ['video', 'audio', 'subtitle'];
  const nodeById = new Map(nodes.map((n) => [n.id, n]));
  for (const t of types) {
    let currentId = outputFlowId;
    const visited = new Set<string>();
    // Bounded walk to avoid pathological graphs.
    for (let hops = 0; hops < 32; hops++) {
      if (visited.has(currentId)) break;
      visited.add(currentId);
      const incoming = edges.find(
        (e) => e.target === currentId && e.data?.streamType === t,
      );
      if (!incoming) break;
      const src = nodeById.get(incoming.source);
      if (!src) break;
      if (src.data.ref.kind === 'node') {
        const def = src.data.ref.def;
        if (def.type === 'encoder') {
          const codec = def.params?.codec;
          if (typeof codec === 'string' && codec.length > 0) {
            result[t] = { codec, sourceLabel: src.data.label || src.id };
          }
          break;
        }
        if (def.type === 'copy' || def.type === 'smartcopy') {
          // Stream copy / smart copy: the muxer writes the inbound codec_id
          // straight through (smartcopy keeps the source codec too). Nothing
          // to resolve — leave undefined.
          break;
        }
      }
      currentId = src.id;
    }
  }
  return result;
}

/** Walk the edge graph upstream from a filter node and extract numeric
 * pad hints from the first input node whose probed metadata is available.
 * These are forwarded to the expression eval endpoint as variable bindings
 * so previews show context-aware (not all-zero) results. */
function resolveUpstreamPad(
  nodes: FlowNode[],
  edges: FlowEdge[],
  filterId: string,
): Record<string, number> {
  const nodeById = new Map(nodes.map((n) => [n.id, n]));
  let currentId = filterId;
  const visited = new Set<string>();
  for (let hops = 0; hops < 32; hops++) {
    if (visited.has(currentId)) break;
    visited.add(currentId);
    const incoming = edges.find((e) => e.target === currentId);
    if (!incoming) break;
    const src = nodeById.get(incoming.source);
    if (!src) break;
    if (src.data.ref.kind === 'input' && src.data.probed && src.data.probed.length > 0) {
      const hints: Record<string, number> = {};
      const video = src.data.probed.find((s) => s.type === 'video');
      if (video) {
        if (video.width) {
          hints.w = video.width; hints.iw = video.width;
          hints.in_w = video.width; hints.main_w = video.width; hints.W = video.width;
        }
        if (video.height) {
          hints.h = video.height; hints.ih = video.height;
          hints.in_h = video.height; hints.main_h = video.height; hints.H = video.height;
        }
        if (video.frame_rate) {
          const fps = parsePadFps(video.frame_rate);
          if (fps !== null) { hints.r = fps; hints.FR = fps; }
        }
        if (video.sar) {
          const sarVal = parsePadRatio(video.sar);
          if (sarVal !== null) hints.sar = sarVal;
        }
      }
      const audio = src.data.probed.find((s) => s.type === 'audio');
      if (audio) {
        if (audio.sample_rate) hints.sr = audio.sample_rate;
        if (audio.channels) hints.nb_channels = audio.channels;
      }
      if (Object.keys(hints).length > 0) return hints;
    }
    currentId = src.id;
  }
  return {};
}

function parsePadFps(fps: string): number | null {
  const m = /^(\d+)(?:\/(\d+))?$/.exec(fps.trim());
  if (!m) return null;
  const n = parseInt(m[1]);
  const d = m[2] ? parseInt(m[2]) : 1;
  return d === 0 ? null : n / d;
}

function parsePadRatio(r: string): number | null {
  const m = /^(\d+):(\d+)$/.exec(r.trim());
  if (!m) return null;
  const d = parseInt(m[2]);
  return d === 0 ? null : parseInt(m[1]) / d;
}

// Curated FourCC suggestions for the muxer's per-stream codec_tag override.
// Free text is still accepted; these only populate the datalist drop-down.
// Values come from MOV/MP4's stsd tables in libavformat
// (ff_codec_movvideo_tags / ff_codec_movaudio_tags / ff_codec_movsubtitle_tags)
// plus a few common AVI / Matroska FourCCs. When changing these maps, keep
// entries to exactly 4 ASCII chars - the backend rejects anything else.
//
// Each map's key is a normalized codec name (lowercased, leading "lib" and
// trailing version digits stripped where appropriate). Lookups fall back to
// the union of all values when the codec is unknown / empty so the user
// still gets a useful drop-down.

const VIDEO_TAGS_BY_CODEC: Record<string, string[]> = {
  // H.264
  h264: ['avc1', 'avc3'],
  libx264: ['avc1', 'avc3'],
  h264_videotoolbox: ['avc1', 'avc3'],
  h264_nvenc: ['avc1', 'avc3'],
  h264_qsv: ['avc1', 'avc3'],
  h264_vaapi: ['avc1', 'avc3'],
  h264_amf: ['avc1', 'avc3'],
  // HEVC - hvc1 first so it is the default suggestion.
  hevc: ['hvc1', 'hev1'],
  h265: ['hvc1', 'hev1'],
  libx265: ['hvc1', 'hev1'],
  hevc_videotoolbox: ['hvc1', 'hev1'],
  hevc_nvenc: ['hvc1', 'hev1'],
  hevc_qsv: ['hvc1', 'hev1'],
  hevc_vaapi: ['hvc1', 'hev1'],
  hevc_amf: ['hvc1', 'hev1'],
  // AV1
  av1: ['av01'],
  libaom_av1: ['av01'],
  libsvtav1: ['av01'],
  librav1e: ['av01'],
  av1_nvenc: ['av01'],
  av1_qsv: ['av01'],
  // VP9 / VP8
  vp9: ['vp09'],
  libvpx_vp9: ['vp09'],
  vp8: ['vp08'],
  libvpx: ['vp08'],
  // MPEG-4 Part 2
  mpeg4: ['mp4v', 'XVID', 'DIVX'],
  // Motion JPEG
  mjpeg: ['jpeg', 'mjpa', 'mjpb'],
  // ProRes
  prores: ['apch', 'apcn', 'apcs', 'apco', 'ap4h', 'ap4x'],
  prores_ks: ['apch', 'apcn', 'apcs', 'apco', 'ap4h', 'ap4x'],
  prores_videotoolbox: ['apch', 'apcn', 'apcs', 'apco', 'ap4h', 'ap4x'],
  // DNxHD / DNxHR
  dnxhd: ['AVdn'],
  // VVC / H.266
  vvc: ['vvc1', 'vvi1'],
  libvvenc: ['vvc1', 'vvi1'],
};

const AUDIO_TAGS_BY_CODEC: Record<string, string[]> = {
  aac: ['mp4a'],
  aac_at: ['mp4a'],
  libfdk_aac: ['mp4a'],
  mp3: ['.mp3', 'mp4a'],
  libmp3lame: ['.mp3', 'mp4a'],
  ac3: ['ac-3'],
  eac3: ['ec-3'],
  opus: ['Opus', 'opus'],
  libopus: ['Opus', 'opus'],
  flac: ['fLaC'],
  alac: ['alac'],
  pcm_s16le: ['sowt', 'lpcm'],
  pcm_s16be: ['twos', 'lpcm'],
  pcm_s24le: ['in24', 'lpcm'],
  pcm_s32le: ['in32', 'lpcm'],
  pcm_f32le: ['fl32', 'lpcm'],
  pcm_f64le: ['fl64', 'lpcm'],
  pcm_mulaw: ['ulaw'],
  pcm_alaw: ['alaw'],
};

const SUBTITLE_TAGS_BY_CODEC: Record<string, string[]> = {
  mov_text: ['tx3g'],
  webvtt: ['wvtt'],
  eia_608: ['c608'],
  eia_708: ['c708'],
};

// Known-compatible subtitle codec ↔ container combinations.
// Key = normalised codec name; value = format substrings that accept it.
// Unlisted codecs are not validated.
const SUBTITLE_CODEC_FORMATS: Record<string, string[]> = {
  mov_text:          ['mp4', 'mov', 'm4a', 'm4v', 'ipod'],
  webvtt:            ['webm', 'mkv', 'matroska', 'mp4', 'hls'],
  ass:               ['mkv', 'matroska'],
  ssa:               ['mkv', 'matroska'],
  srt:               ['mkv', 'matroska'],
  subrip:            ['mkv', 'matroska'],
  dvd_subtitle:      ['mp4', 'mov', 'mkv', 'matroska', 'vob'],
  dvdsub:            ['mp4', 'mov', 'mkv', 'matroska', 'vob'],
  hdmv_pgs_subtitle: ['mkv', 'matroska'],
};

// Common character-encoding names for the subtitle_charenc picker.
const SUBTITLE_CHARSETS = [
  'UTF-8', 'UTF-16LE', 'UTF-16BE',
  'ISO-8859-1', 'ISO-8859-2', 'ISO-8859-5', 'ISO-8859-15',
  'Windows-1250', 'Windows-1251', 'Windows-1252', 'Windows-1254',
  'Shift_JIS', 'GB18030', 'GBK', 'Big5',
  'KOI8-R', 'KOI8-U', 'EUC-JP', 'EUC-KR',
];

function normalizeCodec(name: string | undefined | null): string {
  return (name ?? '').trim().toLowerCase().replace(/-/g, '_');
}

function isHEVC(name: string | undefined | null): boolean {
  const n = normalizeCodec(name);
  return n === 'hevc' || n === 'h265' || n.startsWith('libx265') || n.startsWith('hevc_');
}

function lookupTags(map: Record<string, string[]>, codec: string | undefined): string[] {
  const n = normalizeCodec(codec);
  if (n && map[n]) return map[n];
  // Unknown / unset codec: show every tag we know about so the drop-down
  // is still useful.
  if (!n) {
    const all = new Set<string>();
    for (const list of Object.values(map)) for (const t of list) all.add(t);
    return Array.from(all);
  }
  return [];
}

function tagsForVideo(codec: string | undefined): string[] {
  return lookupTags(VIDEO_TAGS_BY_CODEC, codec);
}
function tagsForAudio(codec: string | undefined): string[] {
  return lookupTags(AUDIO_TAGS_BY_CODEC, codec);
}
function tagsForSubtitle(codec: string | undefined): string[] {
  return lookupTags(SUBTITLE_TAGS_BY_CODEC, codec);
}

function subtitleCompatWarning(codec: string, format: string | undefined): string | null {
  if (!codec || !format) return null;
  const compat = SUBTITLE_CODEC_FORMATS[normalizeCodec(codec)];
  if (!compat) return null;
  const fmt = format.toLowerCase().trim();
  if (compat.some((c) => fmt.includes(c) || c.includes(fmt))) return null;
  return `"${codec}" may not be compatible with "${format}" containers. Compatible: ${compat.join(', ')}.`;
}

// Toggle a single AV_DISPOSITION_* flag in a '+'-separated disposition string.
function hasDispFlag(disposition: string, flag: string): boolean {
  return disposition.split('+').map((f) => f.trim()).includes(flag);
}

function toggleDispFlag(disposition: string, flag: string, on: boolean): string {
  const flags = disposition.split('+').map((f) => f.trim()).filter(Boolean);
  const idx = flags.indexOf(flag);
  if (on && idx === -1) flags.push(flag);
  if (!on && idx !== -1) flags.splice(idx, 1);
  return flags.join('+');
}

/* ---------- 4-char FourCC field with datalist suggestions ---------- */
function TagField({
  label,
  value,
  suggestions,
  onChange,
}: {
  label: string;
  value: string;
  suggestions: string[];
  onChange: (v: string) => void;
}) {
  const [local, setLocal] = useState(value);
  useEffect(() => setLocal(value), [value]);
  // Stable id so multiple fields don't share a single datalist.
  const listId = `codec-tag-${label.replace(/[^a-z0-9]/gi, '-').toLowerCase()}`;
  const invalid = local.length > 0 && local.length !== 4;
  // When the codec maps to a single tag we surface it as the placeholder so
  // the user can see what the muxer would write by default.
  const placeholder = suggestions.length === 1 ? suggestions[0] : '(default)';
  return (
    <>
      <label>{label}</label>
      <input
        list={listId}
        value={local}
        maxLength={4}
        placeholder={placeholder}
        spellCheck={false}
        autoComplete="off"
        // Visual hint when the value isn't a valid 4-char FourCC.
        style={invalid ? { outline: '1px solid var(--mm-error, #c33)' } : undefined}
        onChange={(e) => setLocal(e.target.value)}
        onBlur={() => {
          if (local !== value) onChange(local);
        }}
      />
      <datalist id={listId}>
        {suggestions.map((s) => (
          <option key={s} value={s} />
        ))}
      </datalist>
    </>
  );
}

/* ---------- Graph node form ---------- */
function NodeForm({ def, onChange, streams, padHints, hwDevices = [], inputIds = [] }: { def: NodeDef; onChange: (next: NodeDef) => void; streams?: string[]; padHints?: Record<string, number>; hwDevices?: HardwareDevice[]; inputIds?: string[] }) {
  const isFilter =
    def.type === 'filter' || def.type === 'filter_source' || def.type === 'filter_sink';
  // Show the device picker only for hardware-accelerated filters (scale_cuda,
  // yadif_cuda, scale_vaapi, libplacebo, etc.) and hardware encoders.
  // Software-only filters (split, volume, crop, …) and software encoders
  // (libx264, libopus, aac, …) have no use for a device context.
  // Always show when a device is already set so existing config is visible.
  const isHWFilter = isFilter && (() => {
    if (def.device) return true; // already configured — always show
    const f = String(def.filter ?? '');
    return /_(cuda|vaapi|qsv|vulkan|opencl|videotoolbox|metal|amf|rkmpp|v4l2m2m)$/.test(f)
      || f === 'libplacebo';
  })();
  const isHWEncoder = def.type === 'encoder' && (() => {
    if (def.device) return true; // already configured — always show
    const codec = String(def.params?.codec ?? '');
    return /_(nvenc|qsv|vaapi|videotoolbox|amf|vulkan|v4l2m2m|mmal|rkmpp)$/.test(codec);
  })();
  const showDevicePicker = isHWFilter || isHWEncoder;
  return (
    <>
      {isFilter ? (
        <FilterAdvanced def={def} onChange={onChange} />
      ) : (
        <>
          <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
          <Field label="Type" value={def.type} onChange={(v) => onChange({ ...def, type: v })} />
        </>
      )}
      {def.type === 'go_processor' && (
        <Field
          label="Processor"
          value={def.processor ?? ''}
          onChange={(v) => onChange({ ...def, processor: v || undefined })}
        />
      )}
      {showDevicePicker && (
        <div style={{ marginBottom: 12 }}>
          <label>Hardware device</label>
          <select
            value={def.device ?? ''}
            onChange={(e) => onChange({ ...def, device: e.target.value || undefined })}
            style={{ display: 'block', width: '100%', marginTop: 4, marginBottom: 4 }}
          >
            <option value="">(none — software)</option>
            {hwDevices.map((d) => (
              <option key={d.name} value={d.name}>{d.name} [{d.type}]</option>
            ))}
          </select>
          {hwDevices.length === 0 && (
            <div style={{ fontSize: 11, color: 'var(--text-dim)' }}>
              No hardware devices defined. Add entries under{' '}
              <code>hardware_devices</code> in your job config.
            </div>
          )}
          {isFilter && (
            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 6, marginTop: 6, cursor: def.device ? 'pointer' : 'not-allowed', opacity: def.device ? 1 : 0.5 }}>
              <input
                type="checkbox"
                checked={def.auto_map_hw ?? false}
                disabled={!def.device}
                onChange={(e) => onChange({ ...def, auto_map_hw: e.target.checked || undefined })}
                style={{ marginTop: 2, flexShrink: 0 }}
              />
              <span>
                Auto-map to hardware filter (<code>auto_map_hw</code>)
                <div style={{ fontSize: 11, color: 'var(--text-dim)', fontWeight: 'normal', marginTop: 2 }}>
                  Promotes the sw filter name (e.g. <code>scale</code>) to its hw
                  equivalent (e.g. <code>scale_cuda</code>) and inserts
                  hwupload/hwdownload at device boundaries. Requires a device.
                </div>
              </span>
            </label>
          )}
        </div>
      )}
      {def.type === 'encoder' && <EncoderForm def={def} onChange={onChange} />}
      {def.type === 'smartcopy' && (
        <SmartCopyForm
          def={def}
          onChange={onChange}
          isAudio={!!streams?.includes('audio') && !streams?.includes('video')}
        />
      )}
      {isFilter && <FilterForm def={def} onChange={onChange} padHints={padHints} />}
      {def.type !== 'encoder' && !isFilter && def.type === 'go_processor' && (
        <GoProcessorParams processorName={def.processor} params={def.params ?? {}} inputIds={inputIds} onChange={(p) => onChange({ ...def, params: p })} />
      )}
      {def.type !== 'encoder' && def.type !== 'smartcopy' && !isFilter && def.type !== 'go_processor' && (
        <ParamsEditor params={def.params ?? {}} onChange={(p) => onChange({ ...def, params: p })} />
      )}
    </>
  );
}

/* ---------- Smart-copy (frame-accurate trim) node form ---------- */

// Reserved keys that are not boundary-encoder AVOptions.
const SMARTCOPY_RESERVED = new Set([
  'smartcopy_encoder',
  'smartcopy_global_header',
  'smartcopy_start_us',
  'smartcopy_end_us',
  'codec',
]);

// SmartAudioCopyForm is the properties panel for an audio smartcopy node.
// PCM boundary slicing has no encoder — there are no tunable parameters — so the
// panel just explains the behaviour and points to the output's Timing section.
function SmartAudioCopyForm() {
  return (
    <div
      style={{
        fontSize: 11,
        color: 'var(--text-dim)',
        background: 'var(--surface-2, rgba(127,127,127,0.08))',
        border: '1px solid var(--border, rgba(127,127,127,0.2))',
        borderRadius: 6,
        padding: '8px 10px',
        margin: '4px 0 12px',
        lineHeight: 1.45,
      }}
    >
      <b>Smart copy (audio)</b> trims sample-accurately: interior packets are
      copied verbatim and only the boundary packets are byte-sliced at the exact
      sample. The result is lossless with a byte-identical interior. Set the trim
      window (<code>Start</code>/<code>Duration</code>/<code>End</code>) on the
      connected <b>output</b>’s Timing section.
      <div style={{ marginTop: 6 }}>
        <b>PCM only.</b> Compressed audio (AAC/FLAC/Opus/…) is not supported —
        use a <code>codec_audio</code> encoder for a sample-accurate re-encode,
        or a plain audio <b>Copy</b> node for a packet-accurate (~21&nbsp;ms)
        lossless copy. There are no tunable parameters for this node.
      </div>
    </div>
  );
}

function SmartCopyForm({ def, onChange, isAudio = false }: { def: NodeDef; onChange: (next: NodeDef) => void; isAudio?: boolean }) {
  if (isAudio) return <SmartAudioCopyForm />;
  const params = def.params ?? {};
  const encName = (params.smartcopy_encoder as string | undefined)?.trim() || 'libx264';
  const globalHeader = params.smartcopy_global_header;

  // Edit a smartcopy-specific param directly (merges into def.params).
  const setSmartParam = (key: string, value: unknown) => {
    const next: Record<string, unknown> = { ...params };
    if (value === '' || value === undefined) delete next[key];
    else next[key] = value;
    onChange({ ...def, params: next });
  };

  // Shim NodeDef so EncoderForm can render the full boundary-encoder UI
  // (rate control, preset, tune, profile/level, raw params) against the
  // chosen encoder. The smartcopy-specific keys are hidden from the shim.
  const shimParams: Record<string, unknown> = { codec: encName };
  for (const [k, v] of Object.entries(params)) {
    if (SMARTCOPY_RESERVED.has(k)) continue;
    shimParams[k] = v;
  }
  const shimDef: NodeDef = { ...def, type: 'encoder', params: shimParams };

  // Map EncoderForm changes back onto the smartcopy node: keep every quality
  // param it set (minus the shim `codec`) and re-attach the smartcopy keys.
  const handleEncoderChange = (next: NodeDef) => {
    const merged: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(next.params ?? {})) {
      if (k === 'codec') continue;
      merged[k] = v;
    }
    if (params.smartcopy_encoder !== undefined) merged.smartcopy_encoder = params.smartcopy_encoder;
    if (params.smartcopy_global_header !== undefined) merged.smartcopy_global_header = params.smartcopy_global_header;
    onChange({ ...def, params: merged });
  };

  return (
    <>
      <div
        style={{
          fontSize: 11,
          color: 'var(--text-dim)',
          background: 'var(--surface-2, rgba(127,127,127,0.08))',
          border: '1px solid var(--border, rgba(127,127,127,0.2))',
          borderRadius: 6,
          padding: '8px 10px',
          margin: '4px 0 12px',
          lineHeight: 1.45,
        }}
      >
        <b>Smart copy</b> re-encodes only the GOPs the trim cut points land in and
        copies every whole interior GOP byte-for-byte. Set the trim window
        (<code>Start</code>/<code>Duration</code>/<code>End</code>) on the connected{' '}
        <b>output</b>’s Timing section. Source and target video parameters
        (codec, size, frame rate, pixel format, SAR, profile) are identical; the
        controls below tune only the re-encoded boundary GOPs.
      </div>

      <Field
        label="Boundary encoder"
        value={(params.smartcopy_encoder as string | undefined) ?? ''}
        placeholder="auto from source (e.g. libx264)"
        onChange={(v) => setSmartParam('smartcopy_encoder', v.trim())}
      />

      <EncoderForm def={shimDef} onChange={handleEncoderChange} />

      <label
        style={{ display: 'flex', alignItems: 'flex-start', gap: 6, marginTop: 10, cursor: 'pointer' }}
      >
        <input
          type="checkbox"
          checked={globalHeader === undefined ? true : globalHeader !== false}
          onChange={(e) => setSmartParam('smartcopy_global_header', e.target.checked)}
          style={{ marginTop: 2, flexShrink: 0 }}
        />
        <span>
          Global header (<code>smartcopy_global_header</code>)
          <div style={{ fontSize: 11, color: 'var(--text-dim)', fontWeight: 'normal', marginTop: 2 }}>
            Place codec parameter sets in the container header (avcC/hvcC) rather
            than in-band. Required for MP4/MOV; leave on unless the container
            needs in-band parameter sets (e.g. MPEG-TS).
          </div>
        </span>
      </label>
    </>
  );
}

/* ---------- Advanced collapsible for filter nodes ----------
 * Filter, filter_source, and filter_sink graph nodes hide their three
 * structural identifiers (id, type, filter) by default — those are
 * properties *of* the node, not properties to edit. The collapse
 * surfaces them when the user genuinely needs to rename, swap the
 * underlying libavfilter, or read the raw type. Filter swap is
 * destructive (resets every option) so it lives behind a confirm. */
function FilterAdvanced({
  def,
  onChange,
}: {
  def: NodeDef;
  onChange: (next: NodeDef) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div style={{ marginBottom: 12 }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        style={{
          background: 'transparent',
          color: 'var(--text-dim)',
          border: 'none',
          padding: 0,
          fontSize: 11,
          cursor: 'pointer',
        }}
        title="Show node id, type, and filter swap"
      >
        {open ? '▾' : '▸'} Advanced
      </button>
      {open && (
        <div style={{ marginTop: 6, paddingLeft: 8, borderLeft: '1px solid var(--border)' }}>
          <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
          <label>Type</label>
          <div className="inspector-canonical" style={{ marginBottom: 8 }}>{def.type}</div>
          <label>Filter</label>
          <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginBottom: 4 }}>
            <code style={{ flex: 1, fontSize: 12 }}>{def.filter ?? '(unset)'}</code>
            <button
              type="button"
              onClick={() => {
                const cur = def.filter ?? '';
                const next = window.prompt(
                  'Replace filter with another libavfilter name?\n\nNote: this discards every option you have set on this node.',
                  cur,
                );
                if (next === null) return;
                const trimmed = next.trim();
                if (trimmed === '' || trimmed === cur) return;
                onChange({ ...def, filter: trimmed, params: undefined });
              }}
            >
              Replace…
            </button>
          </div>
          <div style={{ fontSize: 11, color: 'var(--text-dim)' }}>
            Replacing the filter clears every option on this node.
          </div>
        </div>
      )}
    </div>
  );
}

/* ---------- Params editor (key/value rows) ---------- */
/* ---------- Go-processor params editor ----------
 * Renders known file-path params (output_file) as a FileField with a
 * Save browse dialog; all other params fall through to ParamsEditor. */
function GoProcessorParams({
  processorName,
  params,
  inputIds = [],
  onChange,
}: {
  processorName?: string;
  params: Record<string, unknown>;
  inputIds?: string[];
  onChange: (next: Record<string, unknown>) => void;
}) {
  const isSceneChange = processorName === 'scene_change' ||
    processorName?.startsWith('scene_change_');

  if (isSceneChange) {
    return <SceneChangeParams processorName={processorName!} params={params} onChange={onChange} />;
  }

  if (processorName?.startsWith('twelvelabs_')) {
    return <TwelveLabsParams processorName={processorName} params={params} onChange={onChange} />;
  }

  if (processorName === 'sequence_editor') {
    return <SequenceEditorParams params={params} inputIds={inputIds} onChange={onChange} />;
  }

  if (processorName === 'whisper_stt') {
    return <WhisperSTTParams params={params} onChange={onChange} />;
  }

  if (processorName === 'raw_decode') {
    return <RawDecodeParams params={params} onChange={onChange} />;
  }

  if (processorName === 'face_detect') {
    return <FaceDetectParams params={params} onChange={onChange} />;
  }

  if (processorName === 'metadata_file_writer') {
    const outputFile = typeof params['output_file'] === 'string' ? params['output_file'] : '';
    // inner_processor is preserved for backward-compat round-trip if present,
    // but is no longer shown in the Inspector — connect via an "events" edge.
    const KNOWN: ReadonlySet<string> = new Set(['output_file', 'inner_processor']);
    const restParams = Object.fromEntries(Object.entries(params).filter(([k]) => !KNOWN.has(k)));
    const set = (key: string, value: unknown) => {
      const next = { ...params };
      if (value !== '' && value !== undefined) next[key] = value; else delete next[key];
      onChange(next);
    };
    return (
      <>
        <FileField
          label="output_file"
          value={outputFile}
          mode="save"
          filter=".jsonl"
          defaultFilename="output.jsonl"
          placeholder="/path/to/output.jsonl"
          onChange={(val) => set('output_file', val)}
        />
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 8 }}>
          Wire an <strong>events</strong> edge from an upstream go_processor to route its events here.
        </div>
        <ParamsEditor params={restParams} onChange={(next) => onChange({ output_file: outputFile || undefined, ...next })} />
      </>
    );
  }

  const FILE_PARAM_KEYS: ReadonlySet<string> = new Set(['output_file']);
  const fileEntries = Object.entries(params).filter(([k]) => FILE_PARAM_KEYS.has(k));
  const restParams = Object.fromEntries(Object.entries(params).filter(([k]) => !FILE_PARAM_KEYS.has(k)));

  return (
    <>
      {fileEntries.map(([k, v]) => (
        <FileField
          key={k}
          label={k}
          value={typeof v === 'string' ? v : ''}
          mode="save"
          filter=".jsonl"
          defaultFilename="output.jsonl"
          onChange={(val) => onChange({ ...params, [k]: val })}
        />
      ))}
      <ParamsEditor params={restParams} onChange={(next) => onChange({ ...fileEntries.reduce<Record<string, unknown>>((acc, [k, v]) => { acc[k] = v; return acc; }, {}), ...next })} />
    </>
  );
}

/* ---------- RawDecodeParams ----------
 * Inspector body for the raw_decode go_processor: a single camera-RAW input
 * (browsed with an open dialog filtered to RAW extensions) plus a read-only
 * summary of the fixed deterministic develop. Fetches /api/raw-capabilities to
 * warn when this binary lacks LibRaw. Unknown keys fall through to ParamsEditor. */
function RawDecodeParams({
  params,
  onChange,
}: {
  params: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const [capable, setCapable] = useState<boolean | null>(null);
  useEffect(() => {
    let alive = true;
    fetch('/api/raw-capabilities')
      .then((r) => r.json())
      .then((d: { capable?: boolean }) => { if (alive) setCapable(Boolean(d.capable)); })
      .catch(() => { if (alive) setCapable(null); });
    return () => { alive = false; };
  }, []);

  const input = typeof params['input'] === 'string' ? (params['input'] as string) : '';
  const KNOWN: ReadonlySet<string> = new Set(['input']);
  const restParams = Object.fromEntries(Object.entries(params).filter(([k]) => !KNOWN.has(k)));
  const setInput = (val: string) => {
    const next = { ...params };
    if (val) next['input'] = val; else delete next['input'];
    onChange(next);
  };

  return (
    <>
      {capable === false && (
        <div style={{ fontSize: 11, color: '#f59e0b', marginBottom: 8 }}>
          This binary has no LibRaw — RAW develop will fail. Build with{' '}
          <code>make build-gui-libraw</code> (after <code>scripts/bundle-libraw.sh</code>).
        </div>
      )}
      <FileField
        label="input"
        value={input}
        mode="open"
        filter="nef,cr2,cr3,arw,raf,orf,rw2,pef,srw,dng"
        placeholder="/path/to/photo.dng"
        onChange={setInput}
      />
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 8 }}>
        Develops a camera-RAW file to a full-resolution 8-bit sRGB frame. Fixed deterministic
        develop: camera white balance, sRGB, AHD demosaic, no auto-brightness; orientation is left
        to downstream nodes. A FrameSource — no graph inputs needed.
      </div>
      <ParamsEditor params={restParams} onChange={(next) => onChange({ ...(input ? { input } : {}), ...next })} />
    </>
  );
}

/* ---------- WhisperSTTParams ----------
 * Inspector body for the whisper_stt go_processor: model + transcription
 * options + an optional sidecar transcript. Unknown keys fall through to the
 * generic ParamsEditor. Mirrors the scene-detector panel conventions. */
function WhisperSTTParams({
  params,
  onChange,
}: {
  params: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const set = (key: string, value: unknown) => {
    const next = { ...params };
    const blank = value === '' || value === undefined || value === null || value === false;
    if (blank) delete next[key];
    else next[key] = value;
    onChange(next);
  };
  const str = (key: string, fallback = ''): string =>
    typeof params[key] === 'string' ? (params[key] as string) : fallback;
  const num = (key: string, fallback: number): number =>
    typeof params[key] === 'number' ? (params[key] as number) : fallback;

  const language = str('language', 'auto');
  const task = str('task', 'transcribe');
  const beamSize = num('beam_size', 0);
  const threads = num('threads', 0);
  const wordTimestamps = params['word_timestamps'] === true;
  const outputFormat = str('output_format', 'srt');

  const EXT: Record<string, string> = { srt: '.srt', vtt: '.vtt', json: '.json', txt: '.txt' };
  const ext = EXT[outputFormat] ?? '.srt';

  const KNOWN = new Set([
    'model', 'language', 'task', 'beam_size', 'word_timestamps',
    'threads', 'initial_prompt', 'output_file', 'output_format',
  ]);
  const known = Object.fromEntries(Object.entries(params).filter(([k]) => KNOWN.has(k)));
  const overflow = Object.fromEntries(Object.entries(params).filter(([k]) => !KNOWN.has(k)));

  const hint = (text: string) => (
    <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 8 }}>{text}</div>
  );

  const setInt = (key: string, raw: string) => {
    const v = parseInt(raw, 10);
    set(key, Number.isNaN(v) ? undefined : v);
  };

  return (
    <>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 8 }}>
        <strong style={{ color: 'var(--text)' }}>Whisper speech-to-text</strong> — local, offline
        transcription via whisper.cpp. Audio passes through unchanged; each segment emits an event,
        and an optional transcript file is written. Requires a <code>with_whisper</code> build.
      </div>

      <FileField
        label="model"
        value={str('model')}
        mode="open"
        filter=".bin,.gguf"
        placeholder="/path/to/ggml-base.en.bin"
        onChange={(val) => set('model', val)}
      />
      {hint('Required. Path to a ggml/gguf Whisper model.')}

      <label style={{ marginTop: 8 }}>Language</label>
      <input
        type="text"
        value={language}
        placeholder="auto"
        onChange={(e) => set('language', e.target.value)}
      />
      {hint('Source language hint; "auto" detects, or an ISO code like "en".')}

      <label style={{ marginTop: 8 }}>Task</label>
      <select value={task} onChange={(e) => set('task', e.target.value)}>
        <option value="transcribe">Transcribe</option>
        <option value="translate">Translate to English</option>
      </select>
      {hint('Transcribe in the source language, or translate to English.')}

      <label style={{ marginTop: 8 }}>Beam size</label>
      <input
        type="number"
        min={0}
        step={1}
        value={beamSize}
        onChange={(e) => setInt('beam_size', e.target.value)}
      />
      {hint('0 or 1 = greedy (fast); >1 = beam search (slower, often better).')}

      <label style={{ display: 'flex', alignItems: 'flex-start', gap: 6, marginTop: 8, cursor: 'pointer' }}>
        <input
          type="checkbox"
          checked={wordTimestamps}
          style={{ marginTop: 2, flexShrink: 0 }}
          onChange={(e) => set('word_timestamps', e.target.checked)}
        />
        <span>
          Word timestamps
          <div style={{ fontSize: 11, color: 'var(--text-dim)', fontWeight: 'normal', marginTop: 2 }}>
            Request token-level timestamps.
          </div>
        </span>
      </label>

      <label style={{ marginTop: 8 }}>Threads</label>
      <input
        type="number"
        min={0}
        step={1}
        value={threads}
        placeholder="0 = auto"
        onChange={(e) => setInt('threads', e.target.value)}
      />
      {hint('Inference threads. 0 = one per CPU.')}

      <label style={{ marginTop: 8 }}>Initial prompt</label>
      <input
        type="text"
        value={str('initial_prompt')}
        placeholder="Optional context / vocabulary"
        onChange={(e) => set('initial_prompt', e.target.value)}
      />
      {hint('Optional context/biasing prompt.')}

      <label style={{ marginTop: 8 }}>Output format</label>
      <select value={outputFormat} onChange={(e) => set('output_format', e.target.value)}>
        <option value="srt">SRT</option>
        <option value="vtt">VTT (WebVTT)</option>
        <option value="json">JSON</option>
        <option value="txt">TXT (plain)</option>
      </select>
      {hint('Sidecar transcript format (used when an output file is set).')}

      <FileField
        label="output_file"
        value={str('output_file')}
        mode="save"
        filter={ext}
        defaultFilename={`transcript${ext}`}
        placeholder={`/path/to/transcript${ext}`}
        onChange={(val) => set('output_file', val)}
      />
      {hint('Optional. Leave blank to emit segment events only.')}

      {Object.keys(overflow).length > 0 && (
        <>
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 10 }}>Other params</div>
          <ParamsEditor params={overflow} onChange={(next) => onChange({ ...known, ...next })} />
        </>
      )}
    </>
  );
}

/* ---------- FaceDetectParams ----------
 * Inspector body for the face_detect go_processor: frame sampling, detector
 * confidence, the embedding toggle, and an optional bundled-models override.
 * Unknown keys fall through to the generic ParamsEditor. Requires a with_onnx
 * build with bundled models. See docs/architecture/face-detection.md. */
function FaceDetectParams({
  params,
  onChange,
}: {
  params: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const set = (key: string, value: unknown) => {
    const next = { ...params };
    const blank = value === '' || value === undefined || value === null || value === false;
    if (blank) delete next[key];
    else next[key] = value;
    onChange(next);
  };
  const str = (key: string, fallback = ''): string =>
    typeof params[key] === 'string' ? (params[key] as string) : fallback;
  const num = (key: string, fallback: number): number =>
    typeof params[key] === 'number' ? (params[key] as number) : fallback;

  const every = num('every', 1);
  const conf = num('conf', 0);
  const embeddings = params['embeddings'] === true;
  const outputFormat = str('output_format', 'jsonl');
  const EXT: Record<string, string> = { jsonl: '.jsonl', csv: '.csv', timecodes: '.txt' };
  const ext = EXT[outputFormat] ?? '.jsonl';

  const KNOWN = new Set(['every', 'conf', 'embeddings', 'models_dir', 'ort_lib', 'output_file', 'output_format']);
  const known = Object.fromEntries(Object.entries(params).filter(([k]) => KNOWN.has(k)));
  const overflow = Object.fromEntries(Object.entries(params).filter(([k]) => !KNOWN.has(k)));

  const hint = (text: string) => (
    <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 8 }}>{text}</div>
  );

  const setInt = (key: string, raw: string) => {
    const v = parseInt(raw, 10);
    set(key, Number.isNaN(v) || v <= 0 ? undefined : v);
  };
  const setFloat = (key: string, raw: string) => {
    const v = parseFloat(raw);
    set(key, Number.isNaN(v) || v <= 0 ? undefined : v);
  };

  return (
    <>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 8 }}>
        <strong style={{ color: 'var(--text)' }}>Face detection</strong> — detect faces
        (YOLOv8-face), align each, and optionally embed them (SFace) for recognition/clustering.
        Video passes through unchanged; each face emits a box, 5-point landmarks, and an optional
        128-d embedding. Set an output file to write detections to a sidecar directly — no
        separate writer node needed. Requires a <code>with_onnx</code> build with bundled models.
      </div>

      <label style={{ marginTop: 8 }}>Analyse every Nth frame</label>
      <input
        type="number"
        min={1}
        step={1}
        value={every}
        placeholder="1 = every frame"
        onChange={(e) => setInt('every', e.target.value)}
      />
      {hint('Sub-sample video to trade accuracy for speed. 1 = every frame.')}

      <label style={{ marginTop: 8 }}>Confidence threshold</label>
      <input
        type="number"
        min={0}
        max={1}
        step={0.05}
        value={conf}
        placeholder="0 = default (0.5)"
        onChange={(e) => setFloat('conf', e.target.value)}
      />
      {hint('Minimum detector score, 0–1. 0 uses the package default (0.5).')}

      <label style={{ display: 'flex', alignItems: 'flex-start', gap: 6, marginTop: 8, cursor: 'pointer' }}>
        <input
          type="checkbox"
          checked={embeddings}
          style={{ marginTop: 2, flexShrink: 0 }}
          onChange={(e) => set('embeddings', e.target.checked)}
        />
        <span>
          Compute embeddings
          <div style={{ fontSize: 11, color: 'var(--text-dim)', fontWeight: 'normal', marginTop: 2 }}>
            Also run SFace to emit a 128-d recognition vector per face (slower).
          </div>
        </span>
      </label>

      <FileField
        label="Models directory"
        value={str('models_dir')}
        mode="directory"
        placeholder="overrides MEDIAMOLDER_FACE_MODELS"
        onChange={(val) => set('models_dir', val)}
      />
      {hint('Optional. Browse to the folder holding the bundled .onnx models (e.g. testdata/face_models). Overrides MEDIAMOLDER_FACE_MODELS.')}

      <FileField
        label="ONNX runtime library"
        value={str('ort_lib')}
        mode="open"
        filter="dylib,so,dll"
        placeholder="auto-discovered if installed"
        onChange={(val) => set('ort_lib', val)}
      />
      {hint('Optional. Usually auto-found after installing ONNX Runtime (brew install onnxruntime). Set only for a non-standard install.')}

      <label style={{ marginTop: 8 }}>Output format</label>
      <select value={outputFormat} onChange={(e) => set('output_format', e.target.value)}>
        <option value="jsonl">JSON Lines</option>
        <option value="csv">CSV</option>
        <option value="timecodes">Timecodes</option>
      </select>
      {hint('Sidecar format (used when an output file is set).')}

      <FileField
        label="output_file"
        value={str('output_file')}
        mode="save"
        filter={ext}
        defaultFilename={`faces${ext}`}
        placeholder={`/path/to/faces${ext}`}
        onChange={(val) => set('output_file', val)}
      />
      {hint('Optional. Write detections here directly — the graph then needs no media output.')}

      {Object.keys(overflow).length > 0 && (
        <>
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 10 }}>Other params</div>
          <ParamsEditor params={overflow} onChange={(next) => onChange({ ...known, ...next })} />
        </>
      )}
    </>
  );
}

/* ---------- SequenceEditorParams ----------
 * Inspector body for the sequence_editor go_processor: the output format is
 * edited inline; the timeline (tracks/clips) is too wide for the panel, so it
 * opens in the TimelineEditorDialog table editor. */
function SequenceEditorParams({
  params,
  inputIds = [],
  onChange,
}: {
  params: Record<string, unknown>;
  inputIds?: string[];
  onChange: (next: Record<string, unknown>) => void;
}) {
  const [editing, setEditing] = useState(false);
  const fmt = (params.format && typeof params.format === 'object'
    ? params.format
    : {}) as Record<string, unknown>;
  const tracks = Array.isArray(params.tracks)
    ? (params.tracks as Array<Record<string, unknown>>)
    : [];
  const clipCount = tracks.reduce(
    (n, t) => n + (Array.isArray(t.clips) ? (t.clips as unknown[]).length : 0),
    0,
  );

  const sampleRate = typeof fmt.sample_rate === 'number' ? fmt.sample_rate : 0;
  const audioOn = sampleRate > 0;
  const channels = typeof fmt.channels === 'number' ? fmt.channels : 2;
  const setFmt = (patch: Record<string, unknown>) =>
    onChange({ ...params, format: { ...fmt, ...patch } });
  const disableAudio = () => {
    const next = { ...fmt };
    delete next.sample_rate;
    delete next.channels;
    onChange({ ...params, format: next });
  };

  return (
    <>
      <label style={{ marginBottom: 6 }}>Output format</label>
      <ParamsEditor params={fmt} onChange={(next) => onChange({ ...params, format: next })} />

      <label style={{ marginTop: 12, marginBottom: 4 }}>Audio output</label>
      {audioOn ? (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <label style={{ fontSize: 11, color: 'var(--text-dim)' }}>
            Rate{' '}
            <input
              type="number"
              min={8000}
              step={100}
              value={sampleRate}
              onChange={(e) => setFmt({ sample_rate: Number(e.target.value) || 0 })}
              style={{ width: 84 }}
            />{' '}Hz
          </label>
          <label style={{ fontSize: 11, color: 'var(--text-dim)' }}>
            Ch{' '}
            <input
              type="number"
              min={1}
              max={8}
              step={1}
              value={channels}
              onChange={(e) => setFmt({ channels: Number(e.target.value) || 1 })}
              style={{ width: 52 }}
            />
          </label>
          <button className="mini-btn" onClick={disableAudio} title="Remove the audio output stream">Disable</button>
        </div>
      ) : (
        <button onClick={() => setFmt({ sample_rate: 48000, channels: 2 })} style={{ width: '100%' }}>
          Enable audio output (mix clips + crossfades)
        </button>
      )}

      <label style={{ marginTop: 12, marginBottom: 4 }}>Timeline</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 6 }}>
        {tracks.length} track{tracks.length === 1 ? '' : 's'} · {clipCount} clip{clipCount === 1 ? '' : 's'}
      </div>
      <button onClick={() => setEditing(true)} style={{ width: '100%' }}>Edit Timeline…</button>

      {editing && (
        <TimelineEditorDialog
          params={params}
          inputIds={inputIds}
          onCancel={() => setEditing(false)}
          onApply={(next) => { onChange(next); setEditing(false); }}
        />
      )}
    </>
  );
}

/* ---------- SceneChangeParams ----------
 * Pre-populated Inspector for all scene_change_* Go processors.
 * Renders a threshold slider with detector-appropriate range and default,
 * a min_scene_len text field, and detector-specific controls. Extra params
 * not handled here fall through to the generic ParamsEditor. */

/** Per-detector UI configuration. */
interface SceneDetectorMeta {
  label: string;
  thresholdDefault: number;
  thresholdMin: number;
  thresholdMax: number;
  thresholdStep: number;
  thresholdHint: string;
  extra?: ExtraControl[];
}

interface ExtraControl {
  key: string;
  label: string;
  type: 'select' | 'number' | 'checkbox' | 'number-list';
  options?: Array<{ value: string; label: string }>;
  min?: number;
  max?: number;
  step?: number;
  defaultValue?: unknown;
  hint?: string;
  placeholder?: string;
}

const SCENE_DETECTOR_META: Record<string, SceneDetectorMeta> = {
  scene_change: {
    label: 'Scene change (MAFD / scdet)',
    thresholdDefault: 0.1,
    thresholdMin: 0,
    thresholdMax: 1,
    thresholdStep: 0.01,
    thresholdHint: 'MAFD score threshold. Lower = more sensitive.',
  },
  scene_change_content: {
    label: 'Scene change — content (go-scene-detect)',
    thresholdDefault: 27.0,
    thresholdMin: 0,
    thresholdMax: 100,
    thresholdStep: 0.5,
    thresholdHint: 'Weighted HSV delta threshold. Default 27. Lower = more sensitive.',
    extra: [
      {
        key: 'luma_only',
        label: 'Luma only',
        type: 'checkbox',
        defaultValue: false,
        hint: 'When checked, only the luma (brightness) channel is used.',
      },
      {
        key: 'filter_mode',
        label: 'Flash filter',
        type: 'select',
        defaultValue: 'merge',
        options: [
          { value: 'merge', label: 'Merge (collapse adjacent cuts)' },
          { value: 'suppress', label: 'Suppress (drop short scenes)' },
        ],
      },
    ],
  },
  scene_change_adaptive: {
    label: 'Scene change — adaptive (go-scene-detect)',
    thresholdDefault: 3.0,
    thresholdMin: 0,
    thresholdMax: 20,
    thresholdStep: 0.1,
    thresholdHint: 'Adaptive ratio threshold. Default 3.0. Lower = more sensitive.',
    extra: [
      {
        key: 'luma_only',
        label: 'Luma only',
        type: 'checkbox',
        defaultValue: false,
        hint: 'When checked, only the luma channel is used for the content score.',
      },
      {
        key: 'min_content_val',
        label: 'Min content val',
        type: 'number',
        defaultValue: 15,
        min: 0,
        max: 100,
        step: 1,
        hint: 'Minimum raw content score required for a cut. Filters low-motion transitions.',
      },
    ],
  },
  scene_change_threshold: {
    label: 'Scene change — threshold / fade (go-scene-detect)',
    thresholdDefault: 12.0,
    thresholdMin: 0,
    thresholdMax: 255,
    thresholdStep: 1,
    thresholdHint: 'Average pixel brightness threshold. Default 12. Detects fades to black.',
    extra: [
      {
        key: 'method',
        label: 'Method',
        type: 'select',
        defaultValue: 'floor',
        options: [
          { value: 'floor', label: 'Floor — fade to black (avg < threshold)' },
          { value: 'ceiling', label: 'Ceiling — fade to white (avg > threshold)' },
        ],
      },
      {
        key: 'fade_bias',
        label: 'Fade bias',
        type: 'number',
        defaultValue: 0,
        min: -1,
        max: 1,
        step: 0.05,
        hint: 'Skews cut toward fade-in (+1) or fade-out (−1). Default 0 = midpoint.',
      },
    ],
  },
  scene_change_hash: {
    label: 'Scene change — perceptual hash (go-scene-detect)',
    thresholdDefault: 0.395,
    thresholdMin: 0,
    thresholdMax: 1,
    thresholdStep: 0.005,
    thresholdHint: 'Normalised Hamming distance threshold. Default 0.395.',
    extra: [
      {
        key: 'size',
        label: 'Hash size',
        type: 'number',
        defaultValue: 16,
        min: 4,
        max: 64,
        step: 4,
        hint: 'DCT low-frequency block edge length. Default 16.',
      },
      {
        key: 'lowpass',
        label: 'Lowpass factor',
        type: 'number',
        defaultValue: 2,
        min: 1,
        max: 8,
        step: 1,
        hint: 'Resize multiplier for low-pass smoothing. Default 2 → 32×32 input to DCT.',
      },
    ],
  },
  scene_change_histogram: {
    label: 'Scene change — histogram (go-scene-detect)',
    thresholdDefault: 0.05,
    thresholdMin: 0,
    thresholdMax: 1,
    thresholdStep: 0.005,
    thresholdHint: 'Histogram divergence threshold (1 − Pearson correlation). Default 0.05.',
    extra: [
      {
        key: 'bins',
        label: 'Bins',
        type: 'number',
        defaultValue: 256,
        min: 16,
        max: 256,
        step: 16,
        hint: 'Number of histogram bins. Default 256.',
      },
    ],
  },
  scene_change_mc: {
    label: 'Scene change — motion-compensated lookahead',
    thresholdDefault: 0.5,
    thresholdMin: 0,
    thresholdMax: 1,
    thresholdStep: 0.01,
    thresholdHint:
      'Hard-cut score-surface peak threshold [0–1]. A frame is flagged as a cut when its ' +
      'aggregated excess score exceeds this. Lower = more sensitive; raise to suppress false ' +
      'positives on fast-motion content. Default 0.50.',
    extra: [
      {
        key: 'coarse_prediction_distance',
        label: 'Coarse prediction distance(s)',
        type: 'number-list',
        defaultValue: [5],
        placeholder: 'e.g. 15, 45',
        hint:
          'Prediction distance(s) used (alongside lag 1) in the cheap coarse first pass over the ' +
          'whole video. A lag k only forms a flat-top plateau on dissolves shorter than k, so a ' +
          'single distance cannot localize both short and long blends — a multi-scale set (e.g. ' +
          '15, 45 or 30, 90) is recommended: a short distance localizes short dissolves, a long ' +
          'one localizes long dissolves. Main performance control; cost is (1 + count) lowres ME ' +
          'per frame. Comma- or space-separated. Default 5.',
      },
      {
        key: 'refined_prediction_distances',
        label: 'Refined prediction distances',
        type: 'number-list',
        defaultValue: [5, 15, 30, 45, 60, 75, 90, 105, 120],
        placeholder: 'e.g. 5 15 30 60 90',
        hint:
          'Menu of prediction distances from which the staged refinement stage picks value(s) near ' +
          'each detected dissolve’s estimated duration D, computed only inside a window (~±D) ' +
          'around the candidate. This supplies accurate long-lag plateau data exactly where needed. ' +
          'Comma- or space-separated. Default covers common dissolve lengths.',
      },
      {
        key: 'excess_threshold',
        label: 'Excess threshold',
        type: 'number',
        defaultValue: 0.15,
        min: 0,
        max: 1,
        step: 0.01,
        hint:
          'Minimum excess above the per-frame baseline that counts as transition activity [0–1]. ' +
          'Distinguishes gradual transitions from ordinary inter-frame variation. Default 0.15.',
      },
      {
        key: 'dissolve_min_len',
        label: 'Dissolve min length',
        type: 'number',
        defaultValue: 2,
        min: 1,
        max: 40,
        step: 1,
        hint:
          'Minimum measured blend duration (frames) to classify a transition as a dissolve/fade ' +
          'rather than a hard cut. Default 2.',
      },
      {
        key: 'dissolve_max_len',
        label: 'Dissolve max length',
        type: 'number',
        defaultValue: 0,
        min: 0,
        max: 240,
        step: 1,
        hint:
          'Maximum active-lag width for dissolve/fade classification. 0 = auto (L/2). Increase for ' +
          'long dissolves (e.g. 30+ frames). Default 0.',
      },
      {
        key: 'agg_window',
        label: 'Aggregation window',
        type: 'number',
        defaultValue: 5,
        min: 1,
        max: 60,
        step: 1,
        hint:
          'Frames used for score-surface aggregation. A larger window aids slow dissolves. Default 5.',
      },
      {
        key: 'prediction_failure_threshold',
        label: 'Prediction failure threshold',
        type: 'number',
        defaultValue: 0.985,
        min: 0.5,
        max: 1,
        step: 0.005,
        hint:
          'Inter/intra cost-ratio level (near 1.0) at which temporal prediction is treated as fully ' +
          'failed (the plateau saturation level for extracting saturated runs). Higher = stricter; ' +
          'lower captures more flanking ramp (noisier). Default 0.985.',
      },
      {
        key: 'fade_threshold',
        label: 'Fade-to-black threshold',
        type: 'number',
        defaultValue: 0.1,
        min: 0.01,
        max: 0.5,
        step: 0.01,
        hint:
          'Normalised luma [0–1] below which a frame is considered dark (part of a fade to/from ' +
          'black). Default 0.10.',
      },
      {
        key: 'fade_white_threshold',
        label: 'Fade-to-white threshold',
        type: 'number',
        defaultValue: 0.9,
        min: 0,
        max: 1.1,
        step: 0.01,
        hint:
          'Normalised luma [0–1] above which a frame is considered bright (part of a fade to/from ' +
          'white). Set above 1.0 to disable white-fade detection. Default 0.90.',
      },
      {
        key: 'fade_min_len',
        label: 'Fade min length',
        type: 'number',
        defaultValue: 3,
        min: 1,
        max: 60,
        step: 1,
        hint:
          'Minimum consecutive dark/bright frames required to classify a valley as a fade rather ' +
          'than a single-frame flash or noise. Default 3.',
      },
      {
        key: 'fade_max_len',
        label: 'Fade max length',
        type: 'number',
        defaultValue: 120,
        min: 5,
        max: 1800,
        step: 5,
        hint:
          'Maximum dark/bright-region length (frames) before the valley is treated as programme ' +
          'black/white rather than a fade and suppressed. Default 120.',
      },
      {
        key: 'fullres_refine',
        label: 'Full-resolution edge refine',
        type: 'checkbox',
        defaultValue: false,
        hint:
          'Measure full-resolution inter-prediction ratios around each detected dissolve and refine ' +
          'the END of short/mid blends (D≤25). Requires the source to be re-decoded (uses ' +
          'source_url). Adds CSV ratio columns. Default off.',
      },
      {
        key: 'lookahead',
        label: 'Lookahead length (legacy)',
        type: 'number',
        defaultValue: 34,
        min: 1,
        max: 80,
        step: 1,
        hint:
          'Legacy / informational. The current staged implementation drives the coarse pass from ' +
          'the coarse prediction distance(s); this value is accepted for compatibility but has ' +
          'limited effect. Default 34.',
      },
    ],
  },
};

const SCENE_CHANGE_KNOWN_KEYS = new Set([
  'output_file', 'output_format',
  'threshold', 'min_scene_len',
  'luma_only', 'filter_mode', 'kernel_size',
  'min_content_val',
  'method', 'fade_bias', 'add_final_scene',
  'size', 'lowpass',
  'bins',
  'pts_threshold',
  'lookahead', 'excess_threshold', 'dissolve_min_len', 'dissolve_max_len',
  'agg_window', 'prediction_failure_threshold',
  'fade_threshold', 'fade_min_len', 'fade_max_len', 'fade_white_threshold',
  'fullres_refine', 'cost_matrix_csv',
  'coarse_prediction_distance', 'refined_prediction_distances',
]);

function SceneChangeParams({
  processorName,
  params,
  onChange,
}: {
  processorName: string;
  params: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const meta = SCENE_DETECTOR_META[processorName];

  const get = (key: string, fallback: unknown) => {
    const v = params[key];
    return v !== undefined ? v : fallback;
  };
  const set = (key: string, value: unknown) => onChange({ ...params, [key]: value });

  const thresholdDefault = meta?.thresholdDefault ?? 0;
  const thresholdValue = Number(get('threshold', thresholdDefault));
  const minSceneLen = String(get('min_scene_len', '0.6s'));
  const outputFile = typeof params['output_file'] === 'string' ? params['output_file'] : '';
  const outputFormat = String(get('output_format', 'jsonl'));
  const costMatrixCsv = typeof params['cost_matrix_csv'] === 'string' ? params['cost_matrix_csv'] : '';

  const fileFilter = outputFormat === 'csv' ? '.csv' : outputFormat === 'timecodes' ? '.txt' : '.jsonl';
  const defaultFn  = outputFormat === 'csv' ? 'scene_changes.csv'
                   : outputFormat === 'timecodes' ? 'scene_changes.txt'
                   : 'scene_changes.jsonl';

  // Params not rendered by the structured UI fall through to the generic editor.
  const extraKeys = meta?.extra?.map((e) => e.key) ?? [];
  const structuredKeys = new Set(['threshold', 'min_scene_len', 'output_file', 'output_format', 'cost_matrix_csv', ...extraKeys]);
  const overflow = Object.fromEntries(
    Object.entries(params).filter(([k]) => !SCENE_CHANGE_KNOWN_KEYS.has(k) && !structuredKeys.has(k)),
  );

  return (
    <>
      <label>Save detections to file</label>
      <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginBottom: 4 }}>
        <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>Format</span>
        <select
          value={outputFormat}
          style={{ fontSize: 11 }}
          onChange={(e) => {
            const { output_format: _fmt, ...rest } = params as Record<string, unknown>;
            onChange(e.target.value === 'jsonl' ? rest : { ...rest, output_format: e.target.value });
          }}
        >
          <option value="jsonl">jsonl</option>
          <option value="csv">csv</option>
          <option value="timecodes">timecodes (.txt)</option>
        </select>
      </div>
      <FileField
        label="output_file"
        value={outputFile}
        mode="save"
        filter={fileFilter}
        defaultFilename={defaultFn}
        placeholder={defaultFn}
        onChange={(val) => {
          const next = { ...params };
          if (val) next['output_file'] = val; else delete next['output_file'];
          onChange(next);
        }}
      />
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 8 }}>
        Leave blank to emit cut events to the event bus only.{' '}
        <code>.jsonl</code>: one JSON record per cut;{' '}
        <code>.csv</code>: Frame Index, Timecode, PTS, Score;{' '}
        <code>.txt</code>: comma-separated cut timecodes (written at stream end).
      </div>

      {processorName === 'scene_change_mc' && (
        <>
          <label style={{ marginTop: 8 }}>Cost matrix CSV (x264 lookahead debug)</label>
          <FileField
            label="cost_matrix_csv"
            value={costMatrixCsv}
            mode="save"
            filter=".csv"
            defaultFilename="x264_costs.csv"
            placeholder="x264_costs.csv"
            onChange={(val) => {
              const next = { ...params };
              if (val) next['cost_matrix_csv'] = val; else delete next['cost_matrix_csv'];
              onChange(next);
            }}
          />
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 8 }}>
            Optional path for a CSV of the full per-frame intra vs. per-lag inter prediction costs
            (and ratios) from the motion-compensated x264 lookahead. Useful for analysing the
            detector. Leave blank to disable.
          </div>
        </>
      )}

      {meta && (
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 8 }}>
          {meta.label}
          {processorName !== 'scene_change_mc' && (
            <span style={{ marginLeft: 6 }}>
              {'— '}
              <a
                href="https://scenedetect.com"
                target="_blank"
                rel="noopener noreferrer"
                style={{ color: 'var(--text-dim)' }}
              >
                PySceneDetect
              </a>
            </span>
          )}
        </div>
      )}

      <label>Threshold</label>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <input
          type="range"
          min={meta?.thresholdMin ?? 0}
          max={meta?.thresholdMax ?? 100}
          step={meta?.thresholdStep ?? 1}
          value={thresholdValue}
          style={{ flex: 1 }}
          onChange={(e) => set('threshold', parseFloat(e.target.value))}
        />
        <input
          type="number"
          value={thresholdValue}
          min={meta?.thresholdMin ?? 0}
          max={meta?.thresholdMax ?? 100}
          step={meta?.thresholdStep ?? 1}
          style={{ width: 70 }}
          onChange={(e) => {
            const v = parseFloat(e.target.value);
            if (!isNaN(v)) set('threshold', v);
          }}
        />
      </div>
      {meta?.thresholdHint && (
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
          {meta.thresholdHint}
        </div>
      )}

      <label style={{ marginTop: 8 }}>Min scene length</label>
      <input
        type="text"
        value={minSceneLen}
        placeholder="e.g. 0.6s, 15, 00:00:00.600"
        onChange={(e) => set('min_scene_len', e.target.value)}
      />
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
        Frames (int), seconds (<code>0.6s</code>), or timecode (<code>HH:MM:SS.mmm</code>).
      </div>

      {meta?.extra?.map((ctrl) => {
        const value = get(ctrl.key, ctrl.defaultValue);
        if (ctrl.type === 'checkbox') {
          return (
            <label key={ctrl.key} style={{ display: 'flex', alignItems: 'flex-start', gap: 6, marginTop: 6, cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={Boolean(value)}
                style={{ marginTop: 2, flexShrink: 0 }}
                onChange={(e) => set(ctrl.key, e.target.checked)}
              />
              <span>
                {ctrl.label}
                {ctrl.hint && (
                  <div style={{ fontSize: 11, color: 'var(--text-dim)', fontWeight: 'normal', marginTop: 2 }}>
                    {ctrl.hint}
                  </div>
                )}
              </span>
            </label>
          );
        }
        if (ctrl.type === 'select') {
          return (
            <Fragment key={ctrl.key}>
              <label style={{ marginTop: 8 }}>{ctrl.label}</label>
              <select
                value={String(value ?? ctrl.defaultValue ?? '')}
                onChange={(e) => set(ctrl.key, e.target.value)}
              >
                {ctrl.options?.map((opt) => (
                  <option key={opt.value} value={opt.value}>{opt.label}</option>
                ))}
              </select>
              {ctrl.hint && (
                <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
                  {ctrl.hint}
                </div>
              )}
            </Fragment>
          );
        }
        if (ctrl.type === 'number') {
          return (
            <Fragment key={ctrl.key}>
              <label style={{ marginTop: 8 }}>{ctrl.label}</label>
              <input
                type="number"
                value={Number(value ?? ctrl.defaultValue ?? 0)}
                min={ctrl.min}
                max={ctrl.max}
                step={ctrl.step}
                onChange={(e) => {
                  const v = parseFloat(e.target.value);
                  if (!isNaN(v)) set(ctrl.key, v);
                }}
              />
              {ctrl.hint && (
                <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
                  {ctrl.hint}
                </div>
              )}
            </Fragment>
          );
        }
        if (ctrl.type === 'number-list') {
          // Render as comma/space separated numbers; store as number[].
          // Coerce a saved scalar (legacy single value) to a one-element list.
          const arr = Array.isArray(value)
            ? (value as number[])
            : typeof value === 'number'
              ? [value]
              : ((ctrl.defaultValue as number[]) ?? []);
          const text = arr.join(', ');
          return (
            <Fragment key={ctrl.key}>
              <label style={{ marginTop: 8 }}>{ctrl.label}</label>
              <input
                type="text"
                value={text}
                placeholder={ctrl.placeholder ?? 'e.g. 5, 15, 30, 45, 60'}
                onChange={(e) => {
                  const parts = e.target.value.split(/[,\s]+/).filter(Boolean);
                  const nums = parts.map((p) => parseFloat(p)).filter((n) => !isNaN(n) && n > 0);
                  set(ctrl.key, nums.length ? nums : undefined);
                }}
              />
              {ctrl.hint && (
                <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
                  {ctrl.hint}
                </div>
              )}
            </Fragment>
          );
        }
        return null;
      })}

      {Object.keys(overflow).length > 0 && (
        <ParamsEditor params={overflow} onChange={(next) => onChange({ ...params, ...next })} />
      )}
    </>
  );
}

/* ---------- TwelveLabsParams ----------
 * Structured Inspector form for the four `twelvelabs_*` go_processor nodes.
 * Each processor has a task-specific section above a collapsible
 * “Authentication & polling” block that mirrors the params accepted by
 * `processors/twelvelabs_common.go`. Params not handled here fall through
 * to the generic ParamsEditor at the bottom. */

interface TLField {
  key: string;
  label: string;
  type: 'text' | 'password' | 'number' | 'checkbox' | 'select' | 'string-list' | 'textarea';
  placeholder?: string;
  defaultValue?: unknown;
  options?: Array<{ value: string; label: string }>;
  min?: number;
  max?: number;
  step?: number;
  required?: boolean;
  hint?: string;
  /** When true, render a "…" button on text fields that opens a save-file browser. */
  fileSave?: boolean;
  /** Only render when this predicate against the current params is true. */
  visibleWhen?: (params: Record<string, unknown>) => boolean;
}

const TL_AUTH_FIELDS: TLField[] = [
  { key: 'api_key', label: 'API key (overrides env / config file)', type: 'password',
    placeholder: 'tlk_…',
    hint: 'Leave blank to fall back to TWELVELABS_API_KEY or ~/.config/mediamolder/twelvelabs.json.' },
  { key: 'api_key_env', label: 'API-key env var', type: 'text',
    placeholder: 'TWELVELABS_API_KEY',
    hint: 'Name of the environment variable to read when api_key is unset.' },
  { key: 'base_url', label: 'Base URL override', type: 'text',
    placeholder: 'https://api.twelvelabs.io/v1.3',
    hint: 'Leave blank to use the production endpoint.' },
  { key: 'poll_interval_s', label: 'Initial poll interval (s)', type: 'number',
    min: 0.1, step: 0.1, placeholder: '2', hint: 'Initial WaitForTask backoff. Default 2s.' },
  { key: 'poll_max_interval_s', label: 'Max poll interval (s)', type: 'number',
    min: 1, step: 1, placeholder: '30', hint: 'Backoff ceiling for long-running tasks. Default 30s.' },
  { key: 'request_timeout_s', label: 'Per-request timeout (s)', type: 'number',
    min: 0, step: 1, placeholder: '0 (unbounded)',
    hint: 'Per-HTTP-request timeout. 0 / blank = unbounded.' },
  { key: 'max_concurrent', label: 'Max concurrent uploads / calls', type: 'number',
    min: 1, step: 1, placeholder: '2', hint: 'Cap on in-flight TwelveLabs operations. Default 2.' },
  { key: 'log_file', label: 'API log file (JSONL)', type: 'text',
    placeholder: '/tmp/tl_api.jsonl',
    fileSave: true,
    hint: 'Append each API request/response as a JSON line to this file. Leave blank to disable.' },
  { key: 'log_api_calls', label: 'Log API calls to stderr', type: 'checkbox',
    defaultValue: false,
    hint: 'When checked, print each API request/response to stderr in addition to (or instead of) log_file.' },
];

interface TLProcessorSchema {
  title: string;
  description: string;
  /** Operation-specific fields; rendered top-down. */
  fields: TLField[];
  /** When true, render an index picker (with refresh) above the fields. */
  indexPicker?: boolean;
  /** Show only a subset of the shared auth/polling fields. */
  authFields?: TLField[];
}

const TL_SCHEMAS: Record<string, TLProcessorSchema> = {
  twelvelabs_indexer: {
    title: 'TwelveLabs indexer',
    description: 'Uploads each completed segment to a TwelveLabs index. Emits an "indexed" event with the resulting video_id.',
    indexPicker: true,
    fields: [
      { key: 'auto_create_index', label: 'Auto-create index on first segment', type: 'checkbox',
        defaultValue: false,
        hint: 'When checked, creates a new index with the name + models below the first time a segment arrives.' },
      { key: 'index_name', label: 'Index name (when auto-creating)', type: 'text',
        placeholder: 'my-index',
        visibleWhen: (p) => p['auto_create_index'] === true,
        hint: 'Name for the auto-created index. Required when auto-create is on.' },
      { key: 'models', label: 'Models (auto-create)', type: 'string-list',
        defaultValue: ['marengo2.7'],
        placeholder: 'marengo2.7',
        visibleWhen: (p) => p['auto_create_index'] === true,
        hint: 'Comma-separated. Common values: marengo3.0, pegasus1.5.' },
      { key: 'wait_for_ready', label: 'Wait for indexing to complete', type: 'checkbox',
        defaultValue: true,
        hint: 'When checked, block until each upload reaches the "ready" state. Required if a downstream node needs the video_id.' },
    ],
  },
  twelvelabs_analyzer: {
    title: 'TwelveLabs analyzer (Pegasus)',
    description: 'Uploads each segment to a staging index, then runs Pegasus analyze with your prompt. Emits captions / chapters as events.',
    indexPicker: true,
    fields: [
      { key: 'prompt', label: 'Prompt', type: 'textarea',
        placeholder: 'Describe what happens in this video.',
        defaultValue: 'Describe what happens in this video.',
        hint: 'Natural-language prompt sent to Pegasus for every segment.' },
      { key: 'temperature', label: 'Temperature', type: 'number',
        min: 0, max: 1, step: 0.05, placeholder: '0.2', defaultValue: 0.2,
        hint: 'Pegasus sampling temperature. 0 = deterministic, 1 = most creative.' },
      { key: 'segments', label: 'Return structured chapter markers', type: 'checkbox',
        defaultValue: false,
        hint: 'When checked, request Pegasus to emit timestamped chapter segments alongside the free-text response.' },
    ],
  },
  twelvelabs_searcher: {
    title: 'TwelveLabs searcher (Marengo)',
    description: 'Runs Marengo natural-language search on the configured index. Per segment by default, or on a fixed timer.',
    indexPicker: true,
    fields: [
      { key: 'query', label: 'Query text', type: 'text',
        placeholder: 'a person walking a dog',
        hint: 'Required unless query_media_url is set.' },
      { key: 'query_media_url', label: 'Query media URL (image / audio)', type: 'text',
        placeholder: 'https://example.com/query.jpg',
        hint: 'Alternative to query text — search by an example image or audio clip.' },
      { key: 'search_options', label: 'Modalities', type: 'string-list',
        defaultValue: ['visual', 'audio'], placeholder: 'visual, audio',
        hint: 'Subset of ["visual", "audio"]. Default both.' },
      { key: 'threshold', label: 'Score threshold', type: 'select',
        defaultValue: 'medium',
        options: [
          { value: 'low', label: 'low' },
          { value: 'medium', label: 'medium (default)' },
          { value: 'high', label: 'high' },
        ],
        hint: 'Server-side relevance filter applied by Marengo.' },
      { key: 'min_score', label: 'Min client-side score', type: 'number',
        min: 0, max: 1, step: 0.05, placeholder: '0',
        hint: 'Drop any matches with a score below this value (after the server threshold). 0 = keep all.' },
      { key: 'page_limit', label: 'Page limit', type: 'number',
        min: 0, step: 1, placeholder: '0 (server default)',
        hint: 'Max matches per page. 0 / blank = let the API decide.' },
      { key: 'interval_s', label: 'Periodic query interval (s)', type: 'number',
        min: 0, step: 1, placeholder: '0 (per-segment)',
        hint: 'When > 0, re-run the query on a timer instead of per completed segment.' },
    ],
  },
  twelvelabs_embedder: {
    title: 'TwelveLabs embedder (Marengo)',
    description: 'Generates Marengo video embeddings per segment. Inline on the event bus, or to disk.',
    fields: [
      { key: 'model', label: 'Embedding model', type: 'select',
        defaultValue: 'marengo3.0',
        options: [
          { value: 'marengo3.0', label: 'marengo3.0' },
          { value: 'marengo2.7', label: 'marengo2.7' },
        ],
        hint: 'TwelveLabs embedding model.' },
      { key: 'scopes', label: 'Scopes', type: 'string-list',
        defaultValue: ['clip'], placeholder: 'clip, video',
        hint: 'Subset of ["clip", "video"]. "clip" = one vector for the whole segment; "video" = sliding window.' },
      { key: 'window_s', label: 'Video-window length (s)', type: 'number',
        min: 0.5, step: 0.5, placeholder: '6', defaultValue: 6,
        visibleWhen: (p) => {
          const s = p['scopes'];
          return Array.isArray(s) && s.some((x) => String(x).toLowerCase() === 'video');
        },
        hint: 'time_segment_duration for the "video" scope (ignored otherwise).' },
      { key: 'out_dir', label: 'Output directory (optional)', type: 'text',
        placeholder: 'e.g. out/embeddings',
        hint: 'When set, vectors are written to "<out_dir>/<basename>.embeddings.<ext>" and stripped from the inline payload.' },
      { key: 'out_format', label: 'Output format', type: 'select',
        defaultValue: 'json',
        options: [
          { value: 'json', label: 'json (one file per segment)' },
          { value: 'jsonl', label: 'jsonl (one vector per line)' },
        ],
        visibleWhen: (p) => typeof p['out_dir'] === 'string' && (p['out_dir'] as string).length > 0,
        hint: 'File format when writing to out_dir.' },
    ],
  },
};

interface TLIndexSummary { id: string; name: string }

function TwelveLabsParams({
  processorName,
  params,
  onChange,
}: {
  processorName: string;
  params: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const schema = TL_SCHEMAS[processorName];
  const authFields = schema?.authFields ?? TL_AUTH_FIELDS;
  const knownKeys = new Set<string>([
    ...(schema?.fields.map((f) => f.key) ?? []),
    ...authFields.map((f) => f.key),
    'index_id',
  ]);
  const overflow = Object.fromEntries(Object.entries(params).filter(([k]) => !knownKeys.has(k)));

  const set = (key: string, value: unknown) => {
    const next = { ...params };
    const isBlank = value === '' || value === undefined || value === null;
    if (isBlank) delete next[key]; else next[key] = value;
    onChange(next);
  };

  if (!schema) {
    return <ParamsEditor params={params} onChange={onChange} />;
  }

  return (
    <>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 8 }}>
        <strong style={{ color: 'var(--text)' }}>{schema.title}</strong>
        <div style={{ marginTop: 2 }}>{schema.description}</div>
        <div style={{ marginTop: 4 }}>
          See{' '}
          <a href="/api/twelvelabs/ping" target="_blank" rel="noopener noreferrer"
             style={{ color: 'var(--text-dim)' }}>API ping</a>
          {' · '}
          <a href="https://docs.twelvelabs.io/v1.3/api-reference/introduction"
             target="_blank" rel="noopener noreferrer"
             style={{ color: 'var(--text-dim)' }}>API reference</a>
        </div>
      </div>

      {schema.indexPicker && (
        <TLIndexPicker
          value={typeof params['index_id'] === 'string' ? (params['index_id'] as string) : ''}
          onChange={(id) => set('index_id', id)}
        />
      )}

      {schema.fields.map((f) => (
        (f.visibleWhen?.(params) ?? true) && (
          <TLFieldRow key={f.key} field={f}
            value={params[f.key]}
            onChange={(v) => set(f.key, v)} />
        )
      ))}

      <div style={{ marginTop: 12, borderTop: '1px solid var(--border)', paddingTop: 8 }}>
        <div style={{ fontSize: 11, color: 'var(--text-dim)', textTransform: 'uppercase',
          letterSpacing: '0.4px', marginBottom: 4 }}>Authentication &amp; polling</div>
        {authFields.map((f) => (
          <TLFieldRow key={f.key} field={f}
            value={params[f.key]}
            onChange={(v) => set(f.key, v)} />
        ))}
      </div>

      {Object.keys(overflow).length > 0 && (
        <>
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 10 }}>Other params</div>
          <ParamsEditor params={overflow} onChange={(next) => onChange({ ...params, ...next })} />
        </>
      )}
    </>
  );
}

/** Text field with a "…" button that opens the FileBrowser in save mode. */
function TLFileSaveField({
  field,
  value,
  onChange,
}: {
  field: TLField;
  value: string;
  onChange: (v: unknown) => void;
}) {
  const [browserOpen, setBrowserOpen] = useState(false);
  const dir = value ? value.substring(0, Math.max(value.lastIndexOf('/'), value.lastIndexOf('\\'))) : '';
  const file = value ? value.substring(Math.max(value.lastIndexOf('/'), value.lastIndexOf('\\')) + 1) : '';
  const labelEl = (
    <label style={{ marginTop: 8 }}>
      {field.label}
      {field.required ? <span style={{ color: 'var(--danger, #c33)' }}> *</span> : null}
    </label>
  );
  return (
    <Fragment>
      {labelEl}
      <div style={{ display: 'flex', gap: 4 }}>
        <input type="text" value={value} placeholder={field.placeholder}
          style={{ flex: 1, minWidth: 0 }}
          onChange={(e) => onChange(e.target.value || undefined)} />
        <button
          type="button"
          title="Browse for save location"
          style={{ flexShrink: 0, padding: '0 8px' }}
          onClick={() => setBrowserOpen(true)}
        >…</button>
      </div>
      {field.hint && (
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
          {field.hint}
        </div>
      )}
      <FileBrowser
        open={browserOpen}
        mode="save"
        title={`Choose location for ${field.label}`}
        initialPath={dir || undefined}
        defaultFilename={file || undefined}
        onClose={() => setBrowserOpen(false)}
        onPick={(path) => { onChange(path); setBrowserOpen(false); }}
      />
    </Fragment>
  );
}

function TLFieldRow({
  field,
  value,
  onChange,
}: {
  field: TLField;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const labelEl = (
    <label style={{ marginTop: 8 }}>
      {field.label}
      {field.required ? <span style={{ color: 'var(--danger, #c33)' }}> *</span> : null}
    </label>
  );
  const hintEl = field.hint && (
    <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
      {field.hint}
    </div>
  );

  switch (field.type) {
    case 'checkbox': {
      const checked = Boolean(value ?? field.defaultValue ?? false);
      return (
        <Fragment>
          <label style={{
            display: 'flex', alignItems: 'flex-start', gap: 6, marginTop: 8, cursor: 'pointer',
            textTransform: 'none', fontSize: 13, color: 'var(--text)', letterSpacing: 'normal',
          }}>
            <input type="checkbox" checked={checked} style={{ marginTop: 2, flexShrink: 0 }}
              onChange={(e) => onChange(e.target.checked)} />
            <span>
              {field.label}
              {field.hint && (
                <div style={{ fontSize: 11, color: 'var(--text-dim)', fontWeight: 'normal', marginTop: 2 }}>
                  {field.hint}
                </div>
              )}
            </span>
          </label>
        </Fragment>
      );
    }
    case 'select': {
      const v = value ?? field.defaultValue ?? '';
      return (
        <Fragment>
          {labelEl}
          <select value={String(v)} onChange={(e) => onChange(e.target.value)}>
            {field.options?.map((opt) => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
          {hintEl}
        </Fragment>
      );
    }
    case 'number': {
      const v = value === undefined || value === null || value === '' ? '' : String(value);
      return (
        <Fragment>
          {labelEl}
          <input type="number" value={v}
            min={field.min} max={field.max} step={field.step}
            placeholder={field.placeholder}
            onChange={(e) => {
              const t = e.target.value;
              if (t === '') { onChange(undefined); return; }
              const n = parseFloat(t);
              if (!Number.isNaN(n)) onChange(n);
            }} />
          {hintEl}
        </Fragment>
      );
    }
    case 'string-list': {
      const arr = Array.isArray(value) ? (value as unknown[]).map(String)
                : Array.isArray(field.defaultValue) ? (field.defaultValue as unknown[]).map(String)
                : [];
      return (
        <Fragment>
          {labelEl}
          <input type="text" value={arr.join(', ')}
            placeholder={field.placeholder}
            onChange={(e) => {
              const parts = e.target.value.split(',').map((s) => s.trim()).filter(Boolean);
              onChange(parts.length ? parts : undefined);
            }} />
          {hintEl}
        </Fragment>
      );
    }
    case 'textarea': {
      const v = value === undefined || value === null ? '' : String(value);
      return (
        <Fragment>
          {labelEl}
          <textarea value={v} rows={3} placeholder={field.placeholder}
            style={{ width: '100%', resize: 'vertical' }}
            onChange={(e) => onChange(e.target.value || undefined)} />
          {hintEl}
        </Fragment>
      );
    }
    case 'password': {
      const v = value === undefined || value === null ? '' : String(value);
      return (
        <Fragment>
          {labelEl}
          <input type="password" value={v} placeholder={field.placeholder} autoComplete="off"
            onChange={(e) => onChange(e.target.value || undefined)} />
          {hintEl}
        </Fragment>
      );
    }
    case 'text':
    default: {
      const v = value === undefined || value === null ? '' : String(value);
      if (field.fileSave) {
        return <TLFileSaveField field={field} value={v} onChange={onChange} />;
      }
      return (
        <Fragment>
          {labelEl}
          <input type="text" value={v} placeholder={field.placeholder}
            onChange={(e) => onChange(e.target.value || undefined)} />
          {hintEl}
        </Fragment>
      );
    }
  }
}

/** Index picker that auto-fetches /api/twelvelabs/indexes when the node
 * is opened, showing a friendly-name select as the primary control.
 * Falls back to a free-text input when the API is unreachable or the
 * current ID doesn't appear in the fetched list. */
function TLIndexPicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (id: string) => void;
}) {
  const [indexes, setIndexes] = useState<TLIndexSummary[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setErr(null);
    try {
      const res = await fetch('/api/twelvelabs/indexes');
      if (!res.ok) {
        const body = await res.text();
        throw new Error(`${res.status} ${res.statusText}: ${body.slice(0, 200)}`);
      }
      const data = await res.json() as { indexes?: Array<{ _id?: string; id?: string; index_name?: string; name?: string }> };
      const list: TLIndexSummary[] = (data.indexes ?? []).map((ix) => ({
        id: String(ix._id ?? ix.id ?? ''),
        name: String(ix.index_name ?? ix.name ?? ix._id ?? ix.id ?? ''),
      })).filter((ix) => ix.id);
      setIndexes(list);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  // Auto-fetch when the node is opened in the Inspector.
  useEffect(() => { void refresh(); }, [refresh]);

  const knownIndex = indexes?.find((ix) => ix.id === value);
  const hasIndexes = indexes !== null && indexes.length > 0;

  return (
    <>
      <label style={{ marginTop: 4 }}>
        Index<span style={{ color: 'var(--danger, #c33)' }}> *</span>
      </label>
      {hasIndexes ? (
        // Primary control: friendly-name select.
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <select value={value} style={{ flex: 1 }}
            onChange={(e) => onChange(e.target.value)}>
            <option value="">— choose an index —</option>
            {indexes!.map((ix) => (
              <option key={ix.id} value={ix.id}>{ix.name}</option>
            ))}
          </select>
          <button onClick={() => void refresh()} disabled={loading} title="Refresh index list">
            {loading ? '…' : '↻'}
          </button>
        </div>
      ) : (
        // Fallback: raw text input (API unreachable, loading, or no indexes).
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <input type="text" value={value}
            placeholder={loading ? 'Loading…' : 'e.g. 68a1…'}
            style={{ flex: 1 }}
            disabled={loading}
            onChange={(e) => onChange(e.target.value)} />
          <button onClick={() => void refresh()} disabled={loading}
            title="Fetch indexes via /api/twelvelabs/indexes">
            {loading ? '…' : 'Refresh'}
          </button>
        </div>
      )}
      {/* Current value not in fetched list — show the raw ID so the user knows what's set. */}
      {hasIndexes && value && !knownIndex && (
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 4 }}>
          Current ID <code>{value}</code> not found in fetched indexes — it may belong to a different account or the list needs a refresh.
        </div>
      )}
      {indexes !== null && indexes.length === 0 && !err && (
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 4 }}>
          No indexes found — create one with <code>mediamolder twelvelabs indexes create</code>.
        </div>
      )}
      {err && (
        <div style={{ fontSize: 11, color: 'var(--danger, #c33)', marginTop: 4 }}>
          {err.includes('401') ? 'Authentication failed — set TWELVELABS_API_KEY or the api_key field below.' : err}
        </div>
      )}
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 4, marginBottom: 6 }}>
        Indexes are fetched from <code>/api/twelvelabs/indexes</code> using the same API-key precedence as the runtime processors.
      </div>
    </>
  );
}

function ParamsEditor({
  params,
  onChange,
}: {
  params: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const entries = Object.entries(params);

  const update = (i: number, key: string, value: string) => {
    const next: Record<string, unknown> = {};
    entries.forEach(([k, v], idx) => {
      if (idx === i) next[key] = value;
      else next[k] = v;
    });
    onChange(next);
  };
  const remove = (i: number) => {
    const next: Record<string, unknown> = {};
    entries.forEach(([k, v], idx) => {
      if (idx !== i) next[k] = v;
    });
    onChange(next);
  };
  const add = () => {
    onChange({ ...params, '': '' });
  };

  return (
    <>
      <label style={{ marginTop: 14 }}>Params</label>
      {entries.length === 0 && <div className="empty" style={{ marginTop: 4 }}>No params.</div>}
      {entries.map(([k, v], i) => (
        <div key={i} className="param-row">
          <input
            value={k}
            placeholder="key"
            onChange={(e) => update(i, e.target.value, String(v ?? ''))}
          />
          <input
            value={String(v ?? '')}
            placeholder="value"
            onChange={(e) => update(i, k, e.target.value)}
          />
          <button onClick={() => remove(i)} title="Remove">×</button>
        </div>
      ))}
      <button style={{ marginTop: 6 }} onClick={add}>+ add param</button>
    </>
  );
}

/* ---------- Timing / trim fields (-ss, -t, -to) ----------
 * These are the FFmpeg per-file timing flags. They live in
 * Input.Options / Output.Options as string-valued AVDictionary entries
 * (FFmpeg parses durations like `30`, `00:00:30`, `30.5`). When placed
 * before `-i` they restrict the demuxer (input form); when placed
 * before an output URL they restrict the muxer (output form). The
 * `compat/ffcli` parser routes them automatically based on position;
 * the GUI exposes them on whichever side they ended up.
 *
 * Mirroring `-t` and `-to` simultaneously is rejected by FFmpeg, so
 * the editor doesn't enforce that — it just surfaces all three. */
function TimingFields({
  kind,
  options,
  onChange,
}: {
  kind: 'input' | 'output';
  options: Record<string, unknown> | undefined;
  onChange: (next: Record<string, unknown> | undefined) => void;
}) {
  const get = (k: string): string => {
    const v = options?.[k];
    return typeof v === 'string' ? v : v == null ? '' : String(v);
  };
  const set = (k: string, v: string) => {
    const next: Record<string, unknown> = { ...(options ?? {}) };
    if (v.trim() === '') {
      delete next[k];
    } else {
      next[k] = v.trim();
    }
    onChange(Object.keys(next).length === 0 ? undefined : next);
  };
  const summary =
    kind === 'input'
      ? 'When to start or stop output from this node.'
      : 'When to start or stop accepting input to this node.';
  return (
    <>
      <label style={{ marginTop: 12 }}>Timing</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
        {summary} Seconds (<code>30</code>) or <code>HH:MM:SS[.ms]</code>.
      </div>
      <Field label="Start (-ss)" value={get('ss')} onChange={(v) => set('ss', v)} />
      <Field label="Duration (-t)" value={get('t')} onChange={(v) => set('t', v)} />
      <Field label="End (-to)" value={get('to')} onChange={(v) => set('to', v)} />
    </>
  );
}

/* ---------- Multi-output tab strip (Wave 8 #45) ----------
 * Lets the user flip between every Output node in the current graph
 * without going back to the canvas. Tabs are derived from the live
 * node list so add / delete / rename keeps them in sync. Only renders
 * when more than one output exists; a single-output graph is identical
 * to the previous single-output form. */
function OutputTabs({
  nodes,
  currentId,
  onSelectNode,
}: {
  nodes: FlowNode[];
  currentId: string;
  onSelectNode?: (id: string) => void;
}) {
  const outputs = nodes.filter((n) => n.data.ref.kind === 'output' && !n.data.implicit);
  if (outputs.length < 2 || !onSelectNode) return null;
  return (
    <div className="inspector-tabs" role="tablist" aria-label="Outputs">
      {outputs.map((o) => (
        <button
          key={o.id}
          type="button"
          role="tab"
          aria-selected={o.id === currentId}
          className={'inspector-tab' + (o.id === currentId ? ' active' : '')}
          onClick={() => onSelectNode(o.id)}
          title={o.data.sublabel || o.id}
        >
          {o.data.label || o.id}
        </button>
      ))}
    </div>
  );
}

/* ---------- BSF chain editor (Wave 8 #46) ----------
 * Sortable list with add/remove/reorder of (name, params) entries
 * for `Output.bsf_video` / `bsf_audio` / `bsf_subtitle`. Replaces the
 * single-field text input that previously forced the user to know
 * libavcodec's `f1=k=v:k=v,f2` chain syntax. The serialised string is
 * shown live as a read-only preview so power users can confirm the
 * exact spec being sent through `av_bsf_list_parse_str`. */
const BSF_PRESETS: Record<'video' | 'audio' | 'subtitle', string[]> = {
  video: [
    'h264_mp4toannexb',
    'hevc_mp4toannexb',
    'h264_metadata',
    'hevc_metadata',
    'av1_metadata',
    'h264_redundant_pps',
    'dump_extra',
    'extract_extradata',
    'filter_units',
    'noise',
    'null',
    'setts',
    'trace_headers',
  ],
  audio: [
    'aac_adtstoasc',
    'mp3decomp',
    'opus_metadata',
    'noise',
    'null',
    'setts',
  ],
  subtitle: [
    'mov2textsub',
    'text2movsub',
    'null',
  ],
};

function BSFEditor({
  label,
  kind,
  spec,
  onChange,
}: {
  label: string;
  kind: 'video' | 'audio' | 'subtitle';
  spec: string | undefined;
  onChange: (next: string | undefined) => void;
}) {
  // Parse the canonical chain spec on every render so external edits
  // (load file, undo, etc.) flow through. Local edits round-trip
  // through serializeBSFChain so the textual preview stays canonical.
  const entries: BSFEntry[] = parseBSFChain(spec ?? '');
  const presets = BSF_PRESETS[kind];

  const commit = (next: BSFEntry[]) => {
    const s = serializeBSFChain(next);
    onChange(s === '' ? undefined : s);
  };
  const update = (i: number, patch: Partial<BSFEntry>) => {
    commit(entries.map((e, j) => (j === i ? { ...e, ...patch } : e)));
  };
  const remove = (i: number) => {
    commit(entries.filter((_, j) => j !== i));
  };
  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= entries.length) return;
    const next = entries.slice();
    [next[i], next[j]] = [next[j], next[i]];
    commit(next);
  };
  const add = () => {
    commit([...entries, { name: presets[0] ?? '', params: {} }]);
  };
  const preview = serializeBSFChain(entries);

  return (
    <>
      <label style={{ marginTop: 12 }}>{label}</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 6 }}>
        Bitstream-filter chain. Syntax:{' '}
        <code>f1[=k=v[:k=v]][,f2]</code> (libavcodec
        <code> av_bsf_list_parse_str</code>).
      </div>
      {entries.length === 0 ? (
        <div className="empty" style={{ marginTop: 4 }}>
          No bitstream filters. Click <strong>+ add</strong> to build a chain.
        </div>
      ) : (
        entries.map((e, i) => (
          <div
            key={i}
            style={{
              border: '1px solid var(--border)',
              borderRadius: 4,
              padding: 6,
              marginBottom: 6,
              background: 'var(--panel-2)',
            }}
          >
            <div style={{ display: 'flex', gap: 4, alignItems: 'flex-end' }}>
              <div style={{ flex: 1 }}>
                <label style={{ marginTop: 0 }}>Filter</label>
                <input
                  list={`bsf-presets-${kind}`}
                  value={e.name}
                  onChange={(ev) => update(i, { name: ev.target.value })}
                  style={{
                    width: '100%',
                    background: 'var(--panel)',
                    color: 'var(--text)',
                    border: '1px solid var(--border)',
                    borderRadius: 4,
                    padding: '5px 7px',
                    fontSize: 12,
                  }}
                />
              </div>
              <button
                type="button"
                onClick={() => move(i, -1)}
                disabled={i === 0}
                title="Move up"
              >
                ↑
              </button>
              <button
                type="button"
                onClick={() => move(i, 1)}
                disabled={i === entries.length - 1}
                title="Move down"
              >
                ↓
              </button>
              <button
                type="button"
                className="danger"
                onClick={() => remove(i)}
                title="Remove this filter"
              >
                ×
              </button>
            </div>
            <label style={{ marginTop: 8 }}>Params</label>
            <ParamsEditor
              params={e.params}
              onChange={(p) => {
                const params: Record<string, string> = {};
                for (const [k, v] of Object.entries(p)) params[k] = String(v ?? '');
                update(i, { params });
              }}
            />
          </div>
        ))
      )}
      <datalist id={`bsf-presets-${kind}`}>
        {presets.map((p) => (
          <option key={p} value={p} />
        ))}
      </datalist>
      <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginTop: 4 }}>
        <button type="button" onClick={add} title="Add a bitstream filter">
          + add
        </button>
        {preview && (
          <code
            style={{
              flex: 1,
              fontSize: 11,
              color: 'var(--text-dim)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
            title={preview}
          >
            {preview}
          </code>
        )}
      </div>
    </>
  );
}

/* ---------- Container metadata editor (Wave 8 #47) ----------
 * Thin key/value wrapper over ParamsEditor that strips empty entries
 * on save. Used both for `Output.metadata` (per-output container
 * tags, e.g. `title`, `artist`, `comment`, `genre`, `date`,
 * `encoded_by`) and for the per-stream `metadata` field surfaced
 * via StreamSpecForm. */
function MetadataEditor({
  label,
  hint,
  metadata,
  onChange,
}: {
  label: string;
  hint?: ReactNode;
  metadata: Record<string, string> | undefined;
  onChange: (next: Record<string, string> | undefined) => void;
}) {
  const params = metadata ?? {};
  return (
    <>
      <label style={{ marginTop: 12 }}>{label}</label>
      {hint && (
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
          {hint}
        </div>
      )}
      <ParamsEditor
        params={params}
        onChange={(p) => {
          const m: Record<string, string> = {};
          for (const [k, v] of Object.entries(p)) {
            if (k.trim() === '') continue;
            m[k] = String(v ?? '');
          }
          onChange(Object.keys(m).length === 0 ? undefined : m);
        }}
      />
    </>
  );
}

/* ---------- Chapters editor (Wave 8 #47) ----------
 * Table editor for `Output.chapters` — each row is `(start, end,
 * title)` plus an expandable per-chapter metadata key/value section.
 * Backs containers that support chapters (matroska, mp4, ogg,
 * ffmetadata). FFmpeg expresses chapters as fractional seconds
 * (start/end as float64) so the form takes free-text numeric input
 * and round-trips through parseFloat — invalid input leaves the
 * existing value untouched on commit. */
function ChaptersEditor({
  chapters,
  onChange,
}: {
  chapters: Chapter[] | undefined;
  onChange: (next: Chapter[] | undefined) => void;
}) {
  const list = chapters ?? [];
  const commit = (next: Chapter[]) => onChange(next.length === 0 ? undefined : next);
  const update = (i: number, patch: Partial<Chapter>) => {
    commit(list.map((c, j) => (j === i ? { ...c, ...patch } : c)));
  };
  const remove = (i: number) => {
    commit(list.filter((_, j) => j !== i));
  };
  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= list.length) return;
    const next = list.slice();
    [next[i], next[j]] = [next[j], next[i]];
    commit(next);
  };
  const add = () => {
    const last = list[list.length - 1];
    const start = last ? last.end : 0;
    commit([...list, { start, end: start, title: '' }]);
  };

  return (
    <>
      <label style={{ marginTop: 12 }}>Chapters</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 6 }}>
        Container chapter table (matroska, mp4, ogg, ffmetadata).
        Times in seconds (e.g. <code>30</code>, <code>125.5</code>).
        Replaces any chapters mapped from inputs via <code>map_chapters</code>.
      </div>
      {list.length === 0 ? (
        <div className="empty" style={{ marginTop: 4 }}>
          No chapters. Click <strong>+ add</strong> to create one.
        </div>
      ) : (
        list.map((c, i) => (
          <ChapterRow
            key={i}
            chapter={c}
            index={i}
            isFirst={i === 0}
            isLast={i === list.length - 1}
            onChange={(patch) => update(i, patch)}
            onRemove={() => remove(i)}
            onMove={(dir) => move(i, dir)}
          />
        ))
      )}
      <button type="button" onClick={add} title="Add a chapter" style={{ marginTop: 4 }}>
        + add
      </button>
    </>
  );
}

function ChapterRow({
  chapter,
  index,
  isFirst,
  isLast,
  onChange,
  onRemove,
  onMove,
}: {
  chapter: Chapter;
  index: number;
  isFirst: boolean;
  isLast: boolean;
  onChange: (patch: Partial<Chapter>) => void;
  onRemove: () => void;
  onMove: (dir: -1 | 1) => void;
}) {
  const [showMeta, setShowMeta] = useState(false);
  const metaCount = Object.keys(chapter.metadata ?? {}).length;
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 4,
        padding: 6,
        marginBottom: 6,
        background: 'var(--panel-2)',
      }}
    >
      <div style={{ display: 'flex', gap: 4, alignItems: 'flex-end' }}>
        <div style={{ flex: '0 0 30px', color: 'var(--text-dim)', fontSize: 11, paddingBottom: 6 }}>
          #{index + 1}
        </div>
        <div style={{ flex: '0 0 90px' }}>
          <NumericField
            label="Start (s)"
            value={chapter.start}
            onChange={(v) => onChange({ start: v })}
          />
        </div>
        <div style={{ flex: '0 0 90px' }}>
          <NumericField
            label="End (s)"
            value={chapter.end}
            onChange={(v) => onChange({ end: v })}
          />
        </div>
        <div style={{ flex: 1 }}>
          <Field
            label="Title"
            value={chapter.title ?? ''}
            onChange={(v) => onChange({ title: v || undefined })}
          />
        </div>
        <button type="button" onClick={() => onMove(-1)} disabled={isFirst} title="Move up">
          ↑
        </button>
        <button type="button" onClick={() => onMove(1)} disabled={isLast} title="Move down">
          ↓
        </button>
        <button type="button" className="danger" onClick={onRemove} title="Remove chapter">
          ×
        </button>
      </div>
      <button
        type="button"
        onClick={() => setShowMeta((v) => !v)}
        style={{
          marginTop: 6,
          background: 'transparent',
          color: 'var(--text-dim)',
          border: 'none',
          padding: 0,
          fontSize: 11,
          cursor: 'pointer',
        }}
        title="Toggle per-chapter metadata"
      >
        {showMeta ? '▾' : '▸'} Metadata{metaCount > 0 ? ` (${metaCount})` : ''}
      </button>
      {showMeta && (
        <ParamsEditor
          params={chapter.metadata ?? {}}
          onChange={(p) => {
            const m: Record<string, string> = {};
            for (const [k, v] of Object.entries(p)) {
              if (k.trim() === '') continue;
              m[k] = String(v ?? '');
            }
            onChange({ metadata: Object.keys(m).length === 0 ? undefined : m });
          }}
        />
      )}
    </div>
  );
}

/* Numeric input that commits on blur and tolerates invalid input
 * (leaves the prior value in place). Used for chapter start/end. */
function NumericField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
}) {
  const [local, setLocal] = useState(String(value));
  useEffect(() => setLocal(String(value)), [value]);
  return (
    <>
      <label>{label}</label>
      <input
        type="text"
        inputMode="decimal"
        value={local}
        onChange={(e) => setLocal(e.target.value)}
        onBlur={() => {
          const n = parseFloat(local);
          if (Number.isFinite(n) && n >= 0) onChange(n);
          else setLocal(String(value));
        }}
        style={{
          width: '100%',
          background: 'var(--panel)',
          color: 'var(--text)',
          border: '1px solid var(--border)',
          borderRadius: 4,
          padding: '5px 7px',
          fontSize: 12,
        }}
      />
    </>
  );
}

/* ---------- Per-stream editor (Wave 8 #45) ----------
 * Surfaces Output.streams[]: per-stream metadata (Wave 1 #3),
 * disposition (Wave 1 #3), and per-stream encoder overrides
 * (Wave 6 #30). Renders a row of sub-tabs (one per declared stream
 * spec) plus + add / × remove buttons. The full streams[] surface
 * is data-model complete on the backend — until now there was no
 * way to author it from the GUI. */
function StreamsEditor({
  streams,
  onChange,
}: {
  streams: StreamSpec[] | undefined;
  onChange: (next: StreamSpec[] | undefined) => void;
}) {
  const list = streams ?? [];
  const [active, setActive] = useState(0);
  // Clamp active when the list shrinks.
  const idx = Math.min(active, Math.max(list.length - 1, 0));

  const update = (i: number, patch: Partial<StreamSpec>) => {
    const next = list.map((s, j) => (j === i ? { ...s, ...patch } : s));
    onChange(next.length === 0 ? undefined : next);
  };
  const add = () => {
    const next = [...list, { type: 'v' as const, index: list.length }];
    onChange(next);
    setActive(next.length - 1);
  };
  const remove = (i: number) => {
    const next = list.filter((_, j) => j !== i);
    onChange(next.length === 0 ? undefined : next);
    if (active >= next.length) setActive(Math.max(next.length - 1, 0));
  };

  return (
    <>
      <label style={{ marginTop: 14 }}>Streams</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 6 }}>
        Per-stream metadata, disposition, and encoder overrides
        (<code>-metadata:s:v:0</code>, <code>-disposition:s:a:1</code>,
        <code>-c:v:1</code>, <code>-b:v:1</code>).
      </div>
      <div className="inspector-tabs" role="tablist" aria-label="Streams">
        {list.map((s, i) => (
          <button
            key={i}
            type="button"
            role="tab"
            aria-selected={i === idx}
            className={'inspector-tab' + (i === idx ? ' active' : '')}
            onClick={() => setActive(i)}
            title={`Stream ${s.type}:${s.index}`}
          >
            {`${s.type}:${s.index}`}
          </button>
        ))}
        <button
          type="button"
          className="inspector-tab"
          onClick={add}
          title="Add stream override"
        >
          + add
        </button>
      </div>
      {list.length === 0 ? (
        <div className="empty" style={{ marginTop: 6 }}>
          No per-stream overrides. Click <strong>+ add</strong> to create one.
        </div>
      ) : (
        <StreamSpecForm
          spec={list[idx]}
          onChange={(patch) => update(idx, patch)}
          onRemove={() => remove(idx)}
        />
      )}
    </>
  );
}

function StreamSpecForm({
  spec,
  onChange,
  onRemove,
}: {
  spec: StreamSpec;
  onChange: (patch: Partial<StreamSpec>) => void;
  onRemove: () => void;
}) {
  return (
    <div style={{ marginTop: 6 }}>
      <div style={{ display: 'flex', gap: 6, alignItems: 'flex-end' }}>
        <div style={{ flex: '0 0 80px' }}>
          <label>Type</label>
          <select
            value={spec.type}
            onChange={(e) => onChange({ type: e.target.value as StreamSpec['type'] })}
            style={{
              width: '100%',
              background: 'var(--panel-2)',
              color: 'var(--text)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              padding: '5px 7px',
              fontSize: 12,
            }}
          >
            <option value="v">v (video)</option>
            <option value="a">a (audio)</option>
            <option value="s">s (subtitle)</option>
            <option value="d">d (data)</option>
          </select>
        </div>
        <div style={{ flex: '0 0 80px' }}>
          <Field
            label="Index"
            value={String(spec.index)}
            onChange={(v) => {
              const n = parseInt(v, 10);
              if (Number.isFinite(n) && n >= 0) onChange({ index: n });
            }}
          />
        </div>
        <button
          type="button"
          className="danger"
          onClick={onRemove}
          style={{ marginLeft: 'auto' }}
          title="Remove this stream override"
        >
          Remove
        </button>
      </div>
      {spec.type === 's' ? (
        <>
          <label style={{ marginTop: 6 }}>Subtitle flags</label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, marginBottom: 4 }}>
            <label style={{ display: 'flex', gap: 8, alignItems: 'center', fontWeight: 'normal', cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={hasDispFlag(spec.disposition ?? '', 'forced')}
                onChange={(e) =>
                  onChange({ disposition: toggleDispFlag(spec.disposition ?? '', 'forced', e.target.checked) || undefined })
                }
              />
              Forced — always displayed (e.g. foreign-language inserts)
            </label>
            <label style={{ display: 'flex', gap: 8, alignItems: 'center', fontWeight: 'normal', cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={hasDispFlag(spec.disposition ?? '', 'hearing_impaired')}
                onChange={(e) =>
                  onChange({ disposition: toggleDispFlag(spec.disposition ?? '', 'hearing_impaired', e.target.checked) || undefined })
                }
              />
              Hearing impaired (HI) — includes sound descriptions
            </label>
          </div>
          <Field
            label="Other disposition flags"
            value={(spec.disposition ?? '').split('+').filter((f) => f !== 'forced' && f !== 'hearing_impaired').join('+')}
            onChange={(v) => {
              const others = v.split('+').map((f) => f.trim()).filter(Boolean);
              const base = (['forced', 'hearing_impaired'] as const).filter((f) => hasDispFlag(spec.disposition ?? '', f));
              onChange({ disposition: [...base, ...others].join('+') || undefined });
            }}
          />
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
            Additional <code>+</code>-separated <code>AV_DISPOSITION_*</code> flags
            (e.g. <code>default</code>, <code>comment</code>).
          </div>
        </>
      ) : (
        <>
          <Field
            label="Disposition"
            value={spec.disposition ?? ''}
            onChange={(v) => onChange({ disposition: v || undefined })}
          />
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: -4, marginBottom: 4 }}>
            <code>+</code>-separated <code>AV_DISPOSITION_*</code> flags
            (e.g. <code>default+forced</code>, <code>hearing_impaired</code>).
          </div>
        </>
      )}
      <label style={{ marginTop: 10 }}>Metadata</label>
      <ParamsEditor
        params={spec.metadata ?? {}}
        onChange={(p) => {
          const m: Record<string, string> = {};
          for (const [k, v] of Object.entries(p)) m[k] = String(v ?? '');
          onChange({ metadata: Object.keys(m).length === 0 ? undefined : m });
        }}
      />
      <EncoderOverrideForm
        override={spec.encoder}
        onChange={(enc) => onChange({ encoder: enc })}
      />
    </div>
  );
}

function EncoderOverrideForm({
  override,
  onChange,
}: {
  override: EncoderOverride | undefined;
  onChange: (next: EncoderOverride | undefined) => void;
}) {
  const codec = override?.codec ?? '';
  const opts = override?.options ?? {};
  const set = (next: EncoderOverride) => {
    const empty = !next.codec && Object.keys(next.options ?? {}).length === 0;
    onChange(empty ? undefined : next);
  };
  return (
    <>
      <label style={{ marginTop: 10 }}>Encoder override</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
        Per-stream codec / option overrides (<code>-c:v:1 libx264</code>,
        <code>-b:v:1 5M</code>). Empty leaves output-level codec in place.
      </div>
      <Field
        label="Codec"
        value={codec}
        onChange={(v) => set({ codec: v || undefined, options: opts })}
      />
      <label>Options</label>
      <ParamsEditor
        params={opts}
        onChange={(p) => set({ codec: codec || undefined, options: p })}
      />
    </>
  );
}

/* ---------- Tiny controlled text field ---------- */
function Field({ label, value, placeholder, onChange }: { label: string; value: string; placeholder?: string; onChange: (v: string) => void }) {
  // Local state lets the user type freely; commit on blur to avoid touching
  // every parent state on every keystroke. Sync down when the prop changes
  // (e.g. user selects a different node).
  const [local, setLocal] = useState(value);
  useEffect(() => setLocal(value), [value]);
  return (
    <>
      <label>{label}</label>
      <input
        value={local}
        placeholder={placeholder}
        onChange={(e) => setLocal(e.target.value)}
        onBlur={() => {
          if (local !== value) onChange(local);
        }}
      />
    </>
  );
}

/* ---------- File-browser-aware text field ---------- */
function FileField({
  label,
  value,
  mode,
  filter,
  defaultFilename,
  placeholder,
  onChange,
  onBrowsePick,
}: {
  label: string;
  value: string;
  mode: BrowseMode;
  filter?: string;
  defaultFilename?: string;
  placeholder?: string;
  onChange: (v: string) => void;
  /** Called (in addition to onChange) only when a file is selected via the
   * file browser — never on plain text-field edits. Useful for triggering
   * side-effects (e.g. auto-probe) that should only fire on confirmed picks. */
  onBrowsePick?: (path: string) => void;
}) {
  const [local, setLocal] = useState(value);
  const [open, setOpen] = useState(false);
  useEffect(() => setLocal(value), [value]);
  const effectivePlaceholder =
    placeholder ??
    (mode === 'save' ? '/path/to/output.mp4' : mode === 'directory' ? '/path/to/folder' : '/path/to/input.mp4');
  return (
    <>
      <label>{label}</label>
      <div className="file-field">
        <input
          value={local}
          onChange={(e) => setLocal(e.target.value)}
          onBlur={() => {
            if (local !== value) onChange(local);
          }}
          placeholder={effectivePlaceholder}
        />
        <button onClick={() => setOpen(true)} title="Browse local filesystem">Browse…</button>
      </div>
      <FileBrowser
        open={open}
        mode={mode}
        filter={filter}
        defaultFilename={defaultFilename}
        initialPath={mode === 'directory' ? (value || undefined) : inferDir(value)}
        onClose={() => setOpen(false)}
        onPick={(p) => {
          setLocal(p);
          onChange(p);
          onBrowsePick?.(p);
        }}
      />
    </>
  );
}

function inferDir(p: string): string | undefined {
  if (!p) return undefined;
  const i = p.lastIndexOf('/');
  if (i <= 0) return undefined;
  return p.slice(0, i);
}
