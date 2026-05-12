import type { HWAccelProbe } from '../lib/jobTypes';
import { lookupFriendlyName, friendlyFilterName } from '../lib/friendlyNames';

interface Props {
  open: boolean;
  probes: HWAccelProbe[] | null;
  onClose: () => void;
}

// Back-end type string → human-readable backend name
const BACKEND_LABEL: Record<string, string> = {
  cuda:         'NVIDIA CUDA',
  vaapi:        'VAAPI (Linux / Mesa)',
  qsv:          'Intel Quick Sync Video',
  videotoolbox: 'Apple VideoToolbox',
  dxva2:        'DirectX Video Acceleration 2',
  d3d11va:      'Direct3D 11 Video Acceleration',
  d3d12va:      'Direct3D 12 Video Acceleration',
  opencl:       'OpenCL',
  vulkan:       'Vulkan',
  vdpau:        'VDPAU (NVIDIA / Nouveau)',
  drm:          'DRM / V4L2',
};

function backendLabel(type: string): string {
  return BACKEND_LABEL[type.toLowerCase()] ?? type.toUpperCase();
}

// Best human-readable name for a codec: palette friendly-name first, then the
// raw FFmpeg name with spaces instead of underscores.
function codecLabel(name: string): string {
  return lookupFriendlyName(name) ?? name;
}

type Codec = NonNullable<HWAccelProbe['codecs']>[number];

interface CodecGroups {
  videoEncode: Codec[];
  videoDecode: Codec[];
  audioEncode: Codec[];
  audioDecode: Codec[];
}

// Group codecs by (media_type, role). Codecs without media_type default to
// video (all CUDA/VAAPI/QSV codecs are video; MediaType was added in a later
// probe version).
function groupCodecs(codecs: HWAccelProbe['codecs']): CodecGroups {
  const all = codecs ?? [];
  const isAudio = (c: Codec) => c.media_type === 'audio';
  return {
    videoEncode: all.filter((c) => !isAudio(c) && c.role === 'encode'),
    videoDecode: all.filter((c) => !isAudio(c) && c.role === 'decode'),
    audioEncode: all.filter((c) => isAudio(c) && c.role === 'encode'),
    audioDecode: all.filter((c) => isAudio(c) && c.role === 'decode'),
  };
}

interface ChipProps { label: string; note?: string }
function Chip({ label, note }: ChipProps) {
  return (
    <span
      className={`hw-chip${note ? ' hw-chip--warn' : ''}`}
      title={note || undefined}
    >
      {label}{note && <span className="hw-chip-warn-icon" aria-label="limitation">⚠</span>}
    </span>
  );
}

interface CardProps { probe: HWAccelProbe }
function DeviceCard({ probe }: CardProps) {
  const { videoEncode, videoDecode, audioEncode, audioDecode } = groupCodecs(probe.codecs);
  const filters = probe.filters ?? [];
  const hasAudio = audioEncode.length > 0 || audioDecode.length > 0;

  const deviceName = probe.display_name || backendLabel(probe.type);
  const backendName = probe.display_name ? backendLabel(probe.type) : null;

  const maxRes = (probe.max_width && probe.max_height)
    ? `${probe.max_width} × ${probe.max_height}`
    : null;

  // When the backend has audio codecs too, prefix labels with "V" / "A".
  const venc = hasAudio ? 'V Encode' : 'Encode';
  const vdec = hasAudio ? 'V Decode' : 'Decode';

  return (
    <section className="hw-card">
      <div className="hw-card-header">
        <div className="hw-card-identity">
          <span className="hw-card-name">{deviceName}</span>
          {backendName && <span className="hw-card-backend">{backendName}</span>}
          {probe.cuda_arch && (
            <span className="hw-card-arch">
              {probe.cuda_arch}
              {probe.cuda_sm && <span className="hw-card-sm"> · SM {probe.cuda_sm}</span>}
            </span>
          )}
        </div>
        <span className="hw-card-ok">✓ Available</span>
      </div>

      {(videoEncode.length > 0 || videoDecode.length > 0 || audioEncode.length > 0 || audioDecode.length > 0 || filters.length > 0) && (
        <div className="hw-card-body">
          {videoEncode.length > 0 && (
            <div className="hw-row">
              <span className="hw-row-label">{venc}</span>
              <div className="hw-row-chips">
                {videoEncode.map((c) => (
                  <Chip key={c.name} label={codecLabel(c.name)} note={c.note} />
                ))}
              </div>
            </div>
          )}
          {videoDecode.length > 0 && (
            <div className="hw-row">
              <span className="hw-row-label">{vdec}</span>
              <div className="hw-row-chips">
                {videoDecode.map((c) => (
                  <Chip key={c.name} label={codecLabel(c.name)} note={c.note} />
                ))}
              </div>
            </div>
          )}
          {audioEncode.length > 0 && (
            <div className="hw-row">
              <span className="hw-row-label">A Encode</span>
              <div className="hw-row-chips">
                {audioEncode.map((c) => (
                  <Chip key={c.name} label={codecLabel(c.name)} note={c.note} />
                ))}
              </div>
            </div>
          )}
          {audioDecode.length > 0 && (
            <div className="hw-row">
              <span className="hw-row-label">A Decode</span>
              <div className="hw-row-chips">
                {audioDecode.map((c) => (
                  <Chip key={c.name} label={codecLabel(c.name)} note={c.note} />
                ))}
              </div>
            </div>
          )}
          {filters.length > 0 && (
            <div className="hw-row">
              <span className="hw-row-label">Filters</span>
              <div className="hw-row-chips">
                {filters.map((f) => (
                  <Chip key={f} label={friendlyFilterName(f)} />
                ))}
              </div>
            </div>
          )}

          {(maxRes || (probe.sw_formats && probe.sw_formats.length > 0) ||
            probe.nvenc_caps || probe.nvdec_caps || probe.amf_caps || probe.static_caps) && (
            <details className="hw-advanced">
              <summary>Advanced</summary>
              <div className="hw-advanced-body">
                {maxRes && (
                  <div className="hw-adv-row">
                    <span className="hw-adv-key">Max resolution</span>
                    <span className="hw-adv-val">{maxRes}</span>
                  </div>
                )}
                {probe.sw_formats && probe.sw_formats.length > 0 && (
                  <div className="hw-adv-row">
                    <span className="hw-adv-key">SW pixel formats</span>
                    <span className="hw-adv-val">{probe.sw_formats.join(', ')}</span>
                  </div>
                )}
                {probe.static_caps?.nvdec_max_sessions != null && (
                  <div className="hw-adv-row">
                    <span className="hw-adv-key">Max NVDEC sessions</span>
                    <span className="hw-adv-val">{probe.static_caps.nvdec_max_sessions}</span>
                  </div>
                )}
                {probe.static_caps?.vt_max_width != null && probe.static_caps.vt_max_height != null && (
                  <div className="hw-adv-row">
                    <span className="hw-adv-key">VT max encode res</span>
                    <span className="hw-adv-val">{probe.static_caps.vt_max_width} × {probe.static_caps.vt_max_height}</span>
                  </div>
                )}
                {probe.nvenc_caps && probe.nvenc_caps.length > 0 && (
                  <div className="hw-caps-section">
                    <div className="hw-caps-title">NVENC encoder limits</div>
                    <table className="hw-caps-table">
                      <thead>
                        <tr>
                          <th>Codec</th>
                          <th>Max res</th>
                          <th title="Theoretical macroblock throughput ceiling">MB/s</th>
                          <th title="Hardware encoder engines">Engines</th>
                          <th>Level</th>
                          <th title="Maximum consecutive B-frames">B-frames</th>
                          <th title="Comma-separated feature flags: 10-bit, 4:4:4, lossless, lookahead, temporal AQ, weighted prediction, B-frame reference">Features</th>
                          {probe.static_caps?.nvenc_max_bitrate_kbps && <th title="Vendor-published maximum bitrate">Max Mbps</th>}
                        </tr>
                      </thead>
                      <tbody>
                        {probe.nvenc_caps.map((c) => {
                          const flags: string[] = [];
                          if (c.support_10bit) flags.push('10-bit');
                          if (c.support_yuv444) flags.push('4:4:4');
                          if (c.support_lossless) flags.push('lossless');
                          if (c.support_lookahead) flags.push('lookahead');
                          if (c.support_temporal_aq) flags.push('temporal AQ');
                          if (c.support_weighted_pred) flags.push('weighted pred');
                          if (c.support_bframe_ref) flags.push('B-ref');
                          const maxKbps = probe.static_caps?.nvenc_max_bitrate_kbps?.[c.codec_name];
                          return (
                            <tr key={c.codec_name}>
                              <td>{codecLabel(c.codec_name)}</td>
                              <td>{c.max_width}×{c.max_height}</td>
                              <td>{c.mb_per_sec_max > 0 ? (c.mb_per_sec_max / 1000).toFixed(0) + 'k' : '—'}</td>
                              <td>{c.num_encoder_engines > 0 ? c.num_encoder_engines : '—'}</td>
                              <td>{c.level_min}–{c.level_max}</td>
                              <td>{c.max_bframes}</td>
                              <td>{flags.join(', ') || '—'}</td>
                              {probe.static_caps?.nvenc_max_bitrate_kbps && (
                                <td>{maxKbps ? (maxKbps / 1000).toFixed(0) + ' Mbps' : '—'}</td>
                              )}
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                {probe.nvdec_caps && probe.nvdec_caps.length > 0 && (
                  <div className="hw-caps-section">
                    <div className="hw-caps-title">NVDEC decoder limits</div>
                    <table className="hw-caps-table">
                      <thead>
                        <tr>
                          <th>Codec</th>
                          <th>Max res</th>
                          <th>Chroma</th>
                          <th>Bit depth</th>
                          <th title="Pixel format output bitmask">Formats</th>
                        </tr>
                      </thead>
                      <tbody>
                        {probe.nvdec_caps.map((c) => (
                          <tr key={`${c.codec_name}-${c.chroma_fmt}-${c.bit_depth}`}>
                            <td>{codecLabel(c.codec_name)}</td>
                            <td>{c.max_width}×{c.max_height}</td>
                            <td>{c.chroma_fmt}</td>
                            <td>{c.bit_depth}-bit</td>
                            <td>0x{c.output_format_mask.toString(16).padStart(2, '0')}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                {probe.amf_caps && probe.amf_caps.length > 0 && (
                  <div className="hw-caps-section">
                    <div className="hw-caps-title">AMF encoder limits</div>
                    <table className="hw-caps-table">
                      <thead>
                        <tr>
                          <th>Codec</th>
                          <th>Max res</th>
                          <th title="Maximum concurrent encode streams">Streams</th>
                        </tr>
                      </thead>
                      <tbody>
                        {probe.amf_caps.map((c) => (
                          <tr key={c.codec_name}>
                            <td>{codecLabel(c.codec_name)}</td>
                            <td>{c.max_width}×{c.max_height}</td>
                            <td>{c.max_num_of_streams}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </details>
          )}
        </div>
      )}
    </section>
  );
}

interface UnavailableCardProps { probe: HWAccelProbe }
function UnavailableCard({ probe }: UnavailableCardProps) {
  return (
    <section className="hw-card hw-card--unavailable">
      <div className="hw-card-header">
        <span className="hw-card-name">{backendLabel(probe.type)}</span>
        <span className="hw-card-unavail">✗ Not available</span>
      </div>
      {probe.error && (
        <div className="hw-card-error">{probe.error}</div>
      )}
    </section>
  );
}

export function HardwareDialog({ open, probes, onClose }: Props) {
  if (!open) return null;

  const available = (probes ?? []).filter((p) => p.available);
  const unavailable = (probes ?? []).filter((p) => !p.available);

  return (
    <div className="dialog-overlay" onClick={onClose}>
      <div className="dialog dialog-hw" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-header">
          <h3>Hardware acceleration</h3>
          <button onClick={onClose}>×</button>
        </div>

        <div className="hw-dialog-body">
          {probes === null && (
            <p className="hw-loading">Probing hardware…</p>
          )}
          {probes !== null && available.length === 0 && (
            <p className="hw-none">
              No hardware acceleration backends are available on this machine.
              MediaMolder will use software processing for all nodes.
            </p>
          )}
          {available.map((p) => (
            <DeviceCard key={p.type} probe={p} />
          ))}
          {unavailable.length > 0 && (
            <details className="hw-unavail-section">
              <summary>Unavailable backends ({unavailable.length})</summary>
              <div className="hw-unavail-list">
                {unavailable.map((p) => (
                  <UnavailableCard key={p.type} probe={p} />
                ))}
              </div>
            </details>
          )}
        </div>

        <div className="dialog-footer">
          <span className="hint">
            Hardware backends are probed once at startup.
            {available.length > 0
              ? ` ${available.length} backend${available.length !== 1 ? 's' : ''} available.`
              : ''}
          </span>
          <div className="spacer" />
          <button onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}
