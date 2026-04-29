import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

interface Props {
  visible: boolean;
  children: ReactNode;
}

const MIN_HEIGHT = 80;
const MAX_FRACTION = 0.7;

/**
 * RunDock — bottom panel host that mirrors the VS Code terminal: a
 * resizable container that occupies the dedicated `dock` grid area
 * once a run is in flight. Renders nothing when hidden so the canvas
 * reclaims the full viewport.
 *
 * Height is persisted to localStorage so the user's preferred panel
 * size survives reloads. Drag the top edge to resize.
 */
export function RunDock({ visible, children }: Props) {
  const [height, setHeight] = useState<number>(() => {
    const saved = Number(localStorage.getItem('mm.runDock.height'));
    return Number.isFinite(saved) && saved >= MIN_HEIGHT ? saved : 240;
  });
  const [dragging, setDragging] = useState(false);
  const startY = useRef(0);
  const startH = useRef(0);

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setDragging(true);
      startY.current = e.clientY;
      startH.current = height;
    },
    [height],
  );

  useEffect(() => {
    if (!dragging) return;
    const onMove = (e: MouseEvent) => {
      const dy = startY.current - e.clientY; // dragging up grows the panel
      const max = Math.max(MIN_HEIGHT, window.innerHeight * MAX_FRACTION);
      const next = Math.min(max, Math.max(MIN_HEIGHT, startH.current + dy));
      setHeight(next);
    };
    const onUp = () => {
      setDragging(false);
      localStorage.setItem('mm.runDock.height', String(height));
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, [dragging, height]);

  if (!visible) return null;
  return (
    <div className="run-dock" style={{ height }}>
      <div
        className={`run-dock-resize${dragging ? ' dragging' : ''}`}
        onMouseDown={onMouseDown}
      />
      {children}
    </div>
  );
}
