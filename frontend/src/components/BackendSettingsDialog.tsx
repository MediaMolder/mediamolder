import { useState } from 'react';
import { type BackendSettings, saveBackendSettings } from '../lib/backendSettings';

interface Props {
  open: boolean;
  current: BackendSettings | null;
  onClose: () => void;
  onChange: (s: BackendSettings | null) => void;
}

export function BackendSettingsDialog({ open, current, onClose, onChange }: Props) {
  const [url, setUrl] = useState(current?.url ?? '');
  const [token, setToken] = useState(current?.token ?? '');

  if (!open) return null;

  const handleSave = () => {
    const trimmedUrl = url.trim().replace(/\/$/, '');
    const settings = trimmedUrl ? { url: trimmedUrl, token: token.trim() } : null;
    saveBackendSettings(settings);
    onChange(settings);
    onClose();
  };

  const handleClear = () => {
    setUrl('');
    setToken('');
    saveBackendSettings(null);
    onChange(null);
    onClose();
  };

  return (
    <div className="dialog-overlay" onClick={onClose}>
      <div className="dialog" style={{ minWidth: 420 }} onClick={(e) => e.stopPropagation()}>
        <div className="dialog-header">
          <h3>Remote Backend</h3>
          <button onClick={onClose}>×</button>
        </div>
        <div style={{ padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 12 }}>
          <p style={{ margin: 0, fontSize: 13, opacity: 0.75 }}>
            Leave URL empty to use the local server (default).
          </p>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 13 }}>
            Server URL
            <input
              type="url"
              placeholder="https://my-server.example.com:8443"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              style={{ width: '100%' }}
            />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 13 }}>
            Bearer Token
            <input
              type="password"
              placeholder="(optional)"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              style={{ width: '100%' }}
            />
          </label>
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
            <button onClick={handleClear}>Use Local</button>
            <button className="primary" onClick={handleSave}>Save</button>
          </div>
        </div>
      </div>
    </div>
  );
}
