import { useEffect, useState } from 'react';
import type { FlowEdge, FlowNode } from '../lib/jsonAdapter';
import { displayUrl, nodeDisplayLabel, nodeDisplaySublabel } from '../lib/jsonAdapter';
import { displayName, lookupFriendlyName, useNamingMode } from '../lib/friendlyNames';
import type { Input, NodeDef, Output, ProbeResponse, ProbedStream } from '../lib/jobTypes';
import { MEDIA_FILE_EXTENSIONS } from '../lib/mediaExtensions';
import { FileBrowser, type BrowseMode } from './FileBrowser';
import { EncoderForm } from './EncoderForm';
import { FilterForm } from './FilterForm';
import { describeKind } from './MMNode';

interface Props {
  node: FlowNode | null;
  /** Full node array, so the output form can resolve its upstream encoder. */
  nodes: FlowNode[];
  /** Full edge array, used to walk back from the output to the encoder. */
  edges: FlowEdge[];
  onChange: (next: FlowNode) => void;
  onDelete: (id: string) => void;
}

export function Inspector({ node, nodes, edges, onChange, onDelete }: Props) {
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

      {ref.kind === 'input' && (
        <InputForm
          def={ref.def}
          probed={node.data.probed}
          onChange={(def) => onChange(updateRef(node, { kind: 'input', def }, def.id, displayUrl(def.url)))}
          onProbed={(probed) =>
            onChange({ ...node, data: { ...node.data, probed } } as FlowNode)
          }
        />
      )}
      {ref.kind === 'output' && (
        <OutputForm
          def={ref.def}
          upstreamCodecs={resolveUpstreamCodecs(nodes, edges, node.id)}
          onChange={(def) => onChange(updateRef(node, { kind: 'output', def }, def.id, displayUrl(def.url)))}
        />
      )}
      {ref.kind === 'node' && (
        <NodeForm
          def={ref.def}
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

/* ---------- Input form ---------- */
function InputForm({
  def,
  probed,
  onChange,
  onProbed,
}: {
  def: Input;
  probed?: ProbedStream[];
  onChange: (next: Input) => void;
  onProbed: (next: ProbedStream[] | undefined) => void;
}) {
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState<string | null>(null);

  const runProbe = async () => {
    if (!def.url) {
      setProbeError('Set a URL first.');
      return;
    }
    setProbing(true);
    setProbeError(null);
    try {
      const r = await fetch('/api/probe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: def.url, options: def.options }),
      });
      if (!r.ok) {
        const body = await r.text();
        throw new Error(body || `HTTP ${r.status}`);
      }
      const resp = (await r.json()) as ProbeResponse;
      onProbed(resp.streams);
    } catch (err) {
      setProbeError((err as Error).message);
      onProbed(undefined);
    } finally {
      setProbing(false);
    }
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
      />
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
      <TimingFields
        options={def.options}
        onChange={(opts) => onChange({ ...def, options: opts })}
      />
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

  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <FileField
        label="URL"
        value={def.url}
        mode="save"
        defaultFilename="output.mp4"
        onChange={(v) => onChange({ ...def, url: v })}
      />
      <Field label="Format" value={def.format ?? ''} onChange={(v) => onChange({ ...def, format: v || undefined })} />
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
      <CodecRow
        label="Codec (subtitle)"
        upstream={upstreamCodecs.subtitle}
        explicit={def.codec_subtitle}
        onClear={() => onChange({ ...def, codec_subtitle: undefined })}
        onEdit={(v) => onChange({ ...def, codec_subtitle: v || undefined })}
      />
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
      <TagField
        label="Codec tag (subtitle)"
        value={def.codec_tag_subtitle ?? ''}
        suggestions={tagsForSubtitle(effSubtitle)}
        onChange={(v) => onChange({ ...def, codec_tag_subtitle: v || undefined })}
      />
      <TimingFields
        options={def.options}
        onChange={(opts) => onChange({ ...def, options: opts })}
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
        if (def.type === 'copy') {
          // Stream copy: muxer writes the inbound codec_id straight through.
          // Nothing to resolve - leave undefined.
          break;
        }
      }
      currentId = src.id;
    }
  }
  return result;
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
function NodeForm({ def, onChange }: { def: NodeDef; onChange: (next: NodeDef) => void }) {
  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <Field label="Type" value={def.type} onChange={(v) => onChange({ ...def, type: v })} />
      {(def.type === 'filter' || def.type === 'filter_source' || def.type === 'filter_sink') && (
        <Field
          label="Filter"
          value={def.filter ?? ''}
          onChange={(v) => onChange({ ...def, filter: v || undefined })}
        />
      )}
      {def.type === 'go_processor' && (
        <Field
          label="Processor"
          value={def.processor ?? ''}
          onChange={(v) => onChange({ ...def, processor: v || undefined })}
        />
      )}
      {def.type === 'encoder' && <EncoderForm def={def} onChange={onChange} />}
      {(def.type === 'filter' || def.type === 'filter_source' || def.type === 'filter_sink') && (
        <FilterForm def={def} onChange={onChange} />
      )}
      {def.type !== 'encoder' && def.type !== 'filter' && def.type !== 'filter_source' && def.type !== 'filter_sink' && (
        <ParamsEditor params={def.params ?? {}} onChange={(p) => onChange({ ...def, params: p })} />
      )}
    </>
  );
}

/* ---------- Params editor (key/value rows) ---------- */
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
  options,
  onChange,
}: {
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
  return (
    <>
      <label style={{ marginTop: 12 }}>Timing</label>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 4 }}>
        FFmpeg <code>-ss</code> / <code>-t</code> / <code>-to</code>. Accepts
        seconds (<code>30</code>) or <code>HH:MM:SS[.ms]</code>.
      </div>
      <Field label="Start (-ss)" value={get('ss')} onChange={(v) => set('ss', v)} />
      <Field label="Duration (-t)" value={get('t')} onChange={(v) => set('t', v)} />
      <Field label="End (-to)" value={get('to')} onChange={(v) => set('to', v)} />
    </>
  );
}

/* ---------- Tiny controlled text field ---------- */
function Field({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
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
  onChange,
}: {
  label: string;
  value: string;
  mode: BrowseMode;
  filter?: string;
  defaultFilename?: string;
  onChange: (v: string) => void;
}) {
  const [local, setLocal] = useState(value);
  const [open, setOpen] = useState(false);
  useEffect(() => setLocal(value), [value]);
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
          placeholder={mode === 'save' ? '/path/to/output.mp4' : '/path/to/input.mp4'}
        />
        <button onClick={() => setOpen(true)} title="Browse local filesystem">Browse…</button>
      </div>
      <FileBrowser
        open={open}
        mode={mode}
        filter={filter}
        defaultFilename={defaultFilename}
        initialPath={inferDir(value)}
        onClose={() => setOpen(false)}
        onPick={(p) => {
          setLocal(p);
          onChange(p);
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
