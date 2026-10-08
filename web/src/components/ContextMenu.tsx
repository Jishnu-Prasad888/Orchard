/**
 * ContextMenu — an accessible action menu for right-click, long-press and
 * overflow (⋮) buttons.
 *
 * It follows the WAI-ARIA menu pattern: focus moves into the menu, arrow
 * keys, Home and End move between items, Enter or Space activates, Escape or
 * Tab closes and focus returns to whatever opened it. On narrow or touch-first
 * screens the same menu opens as a bottom sheet with large targets.
 *
 * Menus never replace visible controls — every action they offer is also
 * reachable from a button — they are a shortcut for people who expect them.
 */

import { type KeyboardEvent, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

import './ContextMenu.css';

export type MenuEntry =
  | {
      id: string;
      label: string;
      onSelect: () => void;
      danger?: boolean;
      disabled?: boolean;
      hint?: string;
    }
  | 'separator';

export interface MenuPosition {
  x: number;
  y: number;
}

interface Props {
  open: boolean;
  position: MenuPosition | null;
  entries: MenuEntry[];
  label: string;
  onClose: () => void;
}

function useSheet(): boolean {
  const query = '(max-width: 600px), (hover: none) and (pointer: coarse)';
  const [sheet, setSheet] = useState(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia(query).matches
      : false,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const mql = window.matchMedia(query);
    const on = () => setSheet(mql.matches);
    mql.addEventListener?.('change', on);
    return () => mql.removeEventListener?.('change', on);
  }, []);
  return sheet;
}

export function ContextMenu({ open, position, entries, label, onClose }: Props) {
  const menuRef = useRef<HTMLDivElement | null>(null);
  const restoreRef = useRef<HTMLElement | null>(null);
  const [place, setPlace] = useState<{ left: number; top: number } | null>(null);
  const sheet = useSheet();

  useEffect(() => {
    if (!open) return;
    restoreRef.current = document.activeElement as HTMLElement | null;
    const first = menuRef.current?.querySelector<HTMLElement>(
      '[role="menuitem"]:not([aria-disabled="true"])',
    );
    first?.focus();
    return () => {
      restoreRef.current?.focus?.();
    };
  }, [open]);

  // Keep the menu inside the viewport.
  useLayoutEffect(() => {
    if (!open || !position || sheet) return;
    const el = menuRef.current;
    const w = el?.offsetWidth ?? 220;
    const h = el?.offsetHeight ?? 240;
    const left = Math.max(8, Math.min(position.x, window.innerWidth - w - 8));
    const top = Math.max(8, Math.min(position.y, window.innerHeight - h - 8));
    setPlace({ left, top });
  }, [open, position, sheet, entries.length]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!menuRef.current?.contains(e.target as Node)) onClose();
    };
    const onScroll = () => {
      if (!sheet) onClose();
    };
    document.addEventListener('pointerdown', onDown, true);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', onClose);
    return () => {
      document.removeEventListener('pointerdown', onDown, true);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', onClose);
    };
  }, [open, onClose, sheet]);

  if (!open) return null;

  const items = () =>
    Array.from(
      menuRef.current?.querySelectorAll<HTMLElement>(
        '[role="menuitem"]:not([aria-disabled="true"])',
      ) ?? [],
    );

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const list = items();
    const at = list.indexOf(document.activeElement as HTMLElement);
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        list[(at + 1) % list.length]?.focus();
        break;
      case 'ArrowUp':
        e.preventDefault();
        list[(at - 1 + list.length) % list.length]?.focus();
        break;
      case 'Home':
        e.preventDefault();
        list[0]?.focus();
        break;
      case 'End':
        e.preventDefault();
        list[list.length - 1]?.focus();
        break;
      case 'Escape':
        e.preventDefault();
        e.stopPropagation();
        onClose();
        break;
      case 'Tab':
        e.preventDefault();
        onClose();
        break;
    }
  };

  const menu = (
    <div
      ref={menuRef}
      className={sheet ? 'ctx-menu ctx-sheet' : 'ctx-menu'}
      role="menu"
      aria-label={label}
      style={
        sheet
          ? undefined
          : { left: place?.left ?? position?.x ?? 0, top: place?.top ?? position?.y ?? 0 }
      }
      onKeyDown={onKeyDown}
      onContextMenu={(e) => e.preventDefault()}
      data-testid="context-menu"
    >
      {sheet && <div className="ctx-sheet-title">{label}</div>}
      {entries.map((entry, i) =>
        entry === 'separator' ? (
          <div key={`sep-${i}`} className="ctx-separator" role="separator" />
        ) : (
          <div
            key={entry.id}
            role="menuitem"
            tabIndex={-1}
            aria-disabled={entry.disabled || undefined}
            className={`ctx-item${entry.danger ? ' ctx-danger' : ''}`}
            onClick={() => {
              if (entry.disabled) return;
              onClose();
              entry.onSelect();
            }}
            onKeyDown={(e) => {
              if ((e.key === 'Enter' || e.key === ' ') && !entry.disabled) {
                e.preventDefault();
                onClose();
                entry.onSelect();
              }
            }}
          >
            <span>{entry.label}</span>
            {entry.hint && <span className="ctx-hint">{entry.hint}</span>}
          </div>
        ),
      )}
      {sheet && (
        <button type="button" className="ctx-sheet-cancel" onClick={onClose}>
          Cancel
        </button>
      )}
    </div>
  );

  return createPortal(
    sheet ? (
      <div className="ctx-scrim" onClick={onClose}>
        <div onClick={(e) => e.stopPropagation()}>{menu}</div>
      </div>
    ) : (
      menu
    ),
    document.body,
  );
}
