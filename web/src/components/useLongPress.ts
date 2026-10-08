import { useRef } from 'react';

import type { MenuPosition } from './ContextMenu';

/**
 * Long-press support for touch: fires after `ms` without movement and
 * suppresses the click that follows. Spread the returned handlers on the
 * element that should respond.
 */
export function useLongPress(
  onLongPress: (pos: MenuPosition, target: Element | null) => void,
  ms = 500,
) {
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const start = useRef<MenuPosition | null>(null);
  const fired = useRef(false);

  const cancel = () => {
    clearTimeout(timer.current);
    start.current = null;
  };

  return {
    onPointerDown: (e: React.PointerEvent) => {
      if (e.pointerType === 'mouse') return;
      fired.current = false;
      start.current = { x: e.clientX, y: e.clientY };
      const pos = { x: e.clientX, y: e.clientY };
      const target = e.target instanceof Element ? e.target : null;
      clearTimeout(timer.current);
      timer.current = setTimeout(() => {
        fired.current = true;
        onLongPress(pos, target);
      }, ms);
    },
    onPointerMove: (e: React.PointerEvent) => {
      const s = start.current;
      if (s && Math.hypot(e.clientX - s.x, e.clientY - s.y) > 10) cancel();
    },
    onPointerUp: cancel,
    onPointerCancel: cancel,
    onClickCapture: (e: React.MouseEvent) => {
      if (fired.current) {
        fired.current = false;
        e.preventDefault();
        e.stopPropagation();
      }
    },
  };
}
