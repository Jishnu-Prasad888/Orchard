/**
 * Items that change places glide there instead of jumping (the FLIP
 * technique): after each re-order, every item is drawn back where it was and
 * eased into its new place. `key` is the order — the effect runs only when
 * it changes — and `ref(id)` goes on each item.
 */

import { useCallback, useLayoutEffect, useRef } from 'react';

const DURATION_MS = 260;
const EASE = 'cubic-bezier(0.2, 0.8, 0.2, 1)';

/** Writes inline styles (kept out of React, as the animation is). */
function paint(el: HTMLElement, styles: Partial<Record<'transform' | 'transition', string>>) {
  Object.assign(el.style, styles);
}

export function useFlip(key: string) {
  const els = useRef(new Map<string, HTMLElement>());
  /** Where each item was at the last layout, in page coordinates. */
  const last = useRef(new Map<string, { x: number; y: number }>());

  const ref = useCallback(
    (id: string) => (el: HTMLElement | null) => {
      if (el) els.current.set(id, el);
      else els.current.delete(id);
    },
    [],
  );

  useLayoutEffect(() => {
    const now = new Map<string, { x: number; y: number }>();
    for (const [id, el] of els.current) {
      const r = el.getBoundingClientRect();
      now.set(id, { x: r.left + window.scrollX, y: r.top + window.scrollY });
    }
    const before = last.current;
    last.current = now;
    const still =
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    if (still || before.size === 0) return;

    const moved: HTMLElement[] = [];
    for (const [id, el] of els.current) {
      const was = before.get(id);
      const is = now.get(id);
      if (!was || !is) continue;
      const dx = was.x - is.x;
      const dy = was.y - is.y;
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) continue;
      paint(el, { transition: 'none', transform: `translate(${dx}px, ${dy}px)` });
      moved.push(el);
    }
    if (moved.length === 0) return;
    void moved[0]!.offsetHeight;
    for (const el of moved) {
      paint(el, { transition: `transform ${DURATION_MS}ms ${EASE}`, transform: '' });
    }
  }, [key]);

  return ref;
}
