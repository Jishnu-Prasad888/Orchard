/**
 * Smooth drag-to-reorder for a vertical list, driven by pointer events
 * rather than the browser's drag and drop (whose ghost image jumps, shows
 * nothing moving until the drop, and doesn't work with a finger).
 *
 * Pressing an item's handle lifts the item; it follows the pointer while the
 * items it passes slide out of its way, and on release it glides into the
 * gap before the new order is committed. Everything during the drag is done
 * with transforms written straight to the elements — layout never changes
 * until the drop, and React renders only when the drag starts and ends.
 * Near the top or bottom of the window the page scrolls along.
 *
 * Changes made any other way (the up/down buttons, adding, removing) animate
 * too: call `capture()` just before committing them, and items glide from
 * where they were to where they now are.
 */

import { useCallback, useLayoutEffect, useRef, useState } from 'react';

const SHIFT_MS = 200;
const DROP_MS = 180;
const EASE = 'cubic-bezier(0.2, 0.8, 0.2, 1)';
/** How close to the window's edge the page starts scrolling, and how fast. */
const EDGE_PX = 72;
const MAX_SCROLL_PX = 18;

interface Drag {
  from: number;
  over: number;
  startY: number;
  startScroll: number;
  lastY: number;
  /** Each item's box in page coordinates, as it was when the drag began. */
  rects: { top: number; height: number }[];
  els: HTMLElement[];
  gap: number;
  scrollFrame: number;
  stop: () => void;
}

/** Writes inline styles. Everything moving during a drag goes through here:
 * transforms written to the elements, never through React state. */
function paint(
  el: HTMLElement,
  styles: Partial<Record<'transform' | 'transition' | 'zIndex', string>>,
) {
  Object.assign(el.style, styles);
}

const reducedMotion = () =>
  typeof window.matchMedia === 'function' &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

export function useSortableList({
  ids,
  onReorder,
}: {
  ids: string[];
  /** Move the item at `from` to `to`; called once, on the drop. */
  onReorder: (from: number, to: number) => void;
}) {
  const els = useRef(new Map<string, HTMLElement>());
  const drag = useRef<Drag | null>(null);
  const [lifted, setLifted] = useState<string | null>(null);
  /** Elements whose drag transforms to drop once the new order is drawn. */
  const settle = useRef<HTMLElement[] | null>(null);
  /** Where each item was before a non-drag change, for it to glide from. */
  const flip = useRef<Map<string, number> | null>(null);
  const idsRef = useRef(ids);
  const onReorderRef = useRef(onReorder);
  useLayoutEffect(() => {
    idsRef.current = ids;
    onReorderRef.current = onReorder;
  });

  const itemRef = useCallback(
    (id: string) => (el: HTMLElement | null) => {
      if (el) els.current.set(id, el);
      else els.current.delete(id);
    },
    [],
  );

  const capture = useCallback(() => {
    if (reducedMotion()) return;
    const tops = new Map<string, number>();
    for (const [id, el] of els.current) tops.set(id, el.getBoundingClientRect().top);
    flip.current = tops;
  }, []);

  const key = ids.join('|');
  useLayoutEffect(() => {
    // After a drop: the items now sit where their transforms were showing
    // them, so the transforms go without anything visibly moving.
    if (settle.current) {
      const list = settle.current;
      settle.current = null;
      for (const el of list) {
        paint(el, { transition: 'none' });
        paint(el, { transform: '' });
        paint(el, { zIndex: '' });
      }
      void list[0]?.offsetHeight;
      for (const el of list) paint(el, { transition: '' });
      return;
    }
    // After any other change: glide from the old place to the new one.
    const before = flip.current;
    flip.current = null;
    if (!before) return;
    const moved: HTMLElement[] = [];
    for (const [id, el] of els.current) {
      const was = before.get(id);
      if (was === undefined) continue;
      const delta = was - el.getBoundingClientRect().top;
      if (Math.abs(delta) < 1) continue;
      paint(el, { transition: 'none' });
      paint(el, { transform: `translateY(${delta}px)` });
      moved.push(el);
    }
    if (moved.length === 0) return;
    void moved[0]!.offsetHeight;
    for (const el of moved) {
      paint(el, { transition: `transform ${SHIFT_MS}ms ${EASE}` });
      paint(el, { transform: '' });
    }
  }, [key]);

  /** Lays the drag out for the pointer's latest position. */
  const update = useCallback(() => {
    const d = drag.current;
    if (!d) return;
    const dy = d.lastY - d.startY + (window.scrollY - d.startScroll);
    paint(d.els[d.from]!, { transform: `translateY(${dy}px) scale(1.02)` });

    const own = d.rects[d.from]!;
    const centre = own.top + own.height / 2 + dy;
    let over = d.from;
    for (let i = d.from + 1; i < d.rects.length; i++) {
      if (centre > d.rects[i]!.top + d.rects[i]!.height / 2) over = i;
    }
    for (let i = d.from - 1; i >= 0; i--) {
      if (centre < d.rects[i]!.top + d.rects[i]!.height / 2) over = i;
    }
    if (over === d.over) return;
    d.over = over;
    const slot = own.height + d.gap;
    d.els.forEach((el, i) => {
      if (i === d.from) return;
      let shift = 0;
      if (d.from < over && i > d.from && i <= over) shift = -slot;
      else if (d.from > over && i >= over && i < d.from) shift = slot;
      paint(el, { transform: shift ? `translateY(${shift}px)` : '' });
    });
  }, []);

  const finish = useCallback((cancelled: boolean) => {
    const d = drag.current;
    if (!d) return;
    drag.current = null;
    d.stop();
    const { from, rects, els: list } = d;
    const to = cancelled ? from : d.over;
    const moving = list[from]!;
    // Glide the lifted item into its gap (or back home), then commit.
    let offset = 0;
    if (to > from) {
      offset = rects[to]!.top + rects[to]!.height - (rects[from]!.top + rects[from]!.height);
    } else if (to < from) {
      offset = rects[to]!.top - rects[from]!.top;
    }
    const ms = reducedMotion() ? 0 : DROP_MS;
    paint(moving, { transition: `transform ${ms}ms ${EASE}` });
    paint(moving, { transform: `translateY(${offset}px)` });
    if (to === from) {
      for (const el of list) if (el !== moving) paint(el, { transform: '' });
    }
    window.setTimeout(() => {
      setLifted(null);
      if (to === from) {
        paint(moving, { transform: '' });
        paint(moving, { zIndex: '' });
        return;
      }
      settle.current = list;
      onReorderRef.current(from, to);
    }, ms);
  }, []);

  /** Pointer-down on an item's handle. */
  const startDrag = useCallback(
    (event: React.PointerEvent, id: string) => {
      if (event.button !== 0 || drag.current) return;
      const order = idsRef.current;
      const from = order.indexOf(id);
      const list = order.map((x) => els.current.get(x));
      if (from === -1 || list.some((el) => !el)) return;
      event.preventDefault();

      const scroll = window.scrollY;
      const rects = (list as HTMLElement[]).map((el) => {
        const r = el.getBoundingClientRect();
        return { top: r.top + scroll, height: r.height };
      });
      const gap = rects.length > 1 ? rects[1]!.top - (rects[0]!.top + rects[0]!.height) : 0;
      const shiftMs = reducedMotion() ? 0 : SHIFT_MS;
      (list as HTMLElement[]).forEach((el, i) => {
        paint(el, { transition: i === from ? 'none' : `transform ${shiftMs}ms ${EASE}` });
      });
      paint(list[from]!, { zIndex: '2' });

      const autoScroll = () => {
        const d = drag.current;
        if (!d) return;
        const y = d.lastY;
        let speed = 0;
        if (y < EDGE_PX) speed = -MAX_SCROLL_PX * (1 - y / EDGE_PX);
        else if (y > window.innerHeight - EDGE_PX)
          speed = MAX_SCROLL_PX * (1 - (window.innerHeight - y) / EDGE_PX);
        if (speed) {
          window.scrollBy(0, speed);
          update();
        }
        d.scrollFrame = window.requestAnimationFrame(autoScroll);
      };

      const onMove = (e: PointerEvent) => {
        const d = drag.current;
        if (!d) return;
        d.lastY = e.clientY;
        update();
      };
      const onUp = () => finish(false);
      const onCancel = () => finish(true);
      const onKey = (e: KeyboardEvent) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          finish(true);
        }
      };
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
      window.addEventListener('pointercancel', onCancel);
      window.addEventListener('keydown', onKey);

      const state: Drag = {
        from,
        over: from,
        startY: event.clientY,
        startScroll: scroll,
        lastY: event.clientY,
        rects,
        els: list as HTMLElement[],
        gap,
        scrollFrame: 0,
        stop: () => {
          window.removeEventListener('pointermove', onMove);
          window.removeEventListener('pointerup', onUp);
          window.removeEventListener('pointercancel', onCancel);
          window.removeEventListener('keydown', onKey);
          window.cancelAnimationFrame(state.scrollFrame);
        },
      };
      drag.current = state;
      state.scrollFrame = window.requestAnimationFrame(autoScroll);
      setLifted(id);
    },
    [finish, update],
  );

  return { itemRef, startDrag, lifted, capture };
}
