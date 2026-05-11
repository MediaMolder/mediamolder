import type { HWAccelProbe } from '../lib/jobTypes';
import { lookupFriendlyName } from '../lib/friendlyNames';

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

// Capitalise "encode" / "decode" for display
function roleLabel(role: string): string {
  return role === 'encode' ? 'Encode' : role === 'decode' ? 'Decode' : role;
}

// Best human-readable name for a codec: palette friendly-name first, then the
// raw FFmpeg name with spaces instead of underscores.
function codecLabel(name: string): string {
  return lookupFriendlyName(name) ?? name;
}

type Codec = NonNullable<HWAccelProbe['codecs']>[number];

// Group codecs by role
function groupCodecs(codecs: HWAccelProbe['codecs']): { encode: Codec[]; decode: Codec[] } {
  const encode = (codecs ?? []).filter((c) => c.role === 'encode');
  const decode = (codecs ?? []).filter((c) => c.role === 'decode');
  return { encode, decode };
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
  const { encode, decode } = groupCodecs(probe.codecs);
  const filters = probe.filters ?? [];

  const deviceName = probe.display_name || backendLabel(probe.type);
  const backendName = probe.display_name ? backendLabel(probe.type) : null;

  const maxRes = (probe.max_width && probe.max_height)
    ? `${probe.max_width} × ${probe.max_height}`
    : null;

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

      {(encode.length > 0 || decode.length > 0 || filters.length > 0) && (
        <div className="hw-card-body">
          {encode.length > 0 && (
            <div className="hw-row">
              <span className="hw-row-label">{roleLabel('encode')}</span>
              <div className="hw-row-chips">
                {encode.map((c) => (
                  <Chip key={c.name} label={codecLabel(c.name)} note={c.note} />
                ))}
              </div>
            </div>
          )}
          {decode.length > 0 && (
            <div className="hw-row">
              <span className="hw-row-label">{roleLabel('decode')}</span>
              <div className="hw-row-chips">
                {decode.map((c) => (
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
                  <Chip key={f} label={f} />
                ))}
              </div>
            </div>
          )}

          {(maxRes || (probe.sw_formats && probe.sw_formats.length > 0)) && (
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
