/* eslint-disable react-refresh/only-export-components --
 * The menu, the hook that opens it from a button, and the helper that orders
 * its items are one unit; splitting them would hide the coupling.
 */
/**
 * Menus: the dropdown behind a "More" button, and the context menu on a
 * right-click or long press.
 *
 * One implementation, because the behaviour is the hard part: focus moves into
 * the menu when it opens and back to the trigger when it closes, arrow keys,
 * Home/End, and type-ahead move between items, Escape and an outside click
 * close it, and the panel is positioned so it never spills out of the
 * viewport. Destructive items are separated from the rest and drawn in the
 * danger color.
 */

import {
  type ReactNode,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';

import { Icon, type IconName } from './Icon';
import './Menu.css';

export interface MenuItem {
  id: string;
  label: string;
  icon?: IconName;
  /** Drawn in the danger color and grouped at the end after a separator. */
  danger?: boolean;
  disabled?: boolean;
  /** A link item (Download, Open in new tab) instead of an action. */
  href?: string;
  /** For a link item: open in a new tab. */
  external?: boolean;
  onSelect?: () => void;
  testId?: string;
}

export type MenuEntry = MenuItem | 'separator';

/** Where the menu opens: under an element, or at a pointer position. */
export type MenuAnchor = { element: HTMLElement } | { x: number; y: number };

interface MenuProps {
  anchor: MenuAnchor;
  items: MenuEntry[];
  onClose: () => void;
  label: string;
  /** Align the panel's right edge with the anchor element (toolbar menus). */
  align?: 'start' | 'end';
  /** Render inside the viewer, on its dark surface. */
  tone?: 'default' | 'dark';
  /** Non-interactive content above the items (who is signed in, say). */
  header?: ReactNode;
  /**
   * Where the panel is mounted. Defaults to `document.body`; a menu opened
   * from inside a modal (the viewer) mounts inside it, so it stays within the
   * modal's accessibility tree and focus trap.
   */
  container?: HTMLElement | null;
}

const MARGIN = 8;

function isItem(entry: MenuEntry): entry is MenuItem {
  return entry !== 'separator';
}

export function Menu({
  anchor,
  items,
  onClose,
  label,
  align = 'start',
  tone = 'default',
  header,
  container,
}: MenuProps) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const [active, setActive] = useState(0);
  const typeahead = useRef({ text: '', at: 0 });
  const menuId = useId();

  const actionable = items.filter(isItem);
  const enabled = actionable.map((item, i) => ({ item, i })).filter(({ item }) => !item.disabled);

  // Place the panel, then clamp it into the viewport once its size is known.
  useLayoutEffect(() => {
    const panel = panelRef.current;
    if (!panel) return;
    const { width, height } = panel.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    let top: number;
    let left: number;
    if ('element' in anchor) {
      const rect = anchor.element.getBoundingClientRect();
      top = rect.bottom + 4;
      left = align === 'end' ? rect.right - width : rect.left;
      if (top + height > vh - MARGIN) top = Math.max(MARGIN, rect.top - height - 4);
    } else {
      top = anchor.y;
      left = anchor.x;
      if (top + height > vh - MARGIN) top = Math.max(MARGIN, anchor.y - height);
      if (left + width > vw - MARGIN) left = Math.max(MARGIN, anchor.x - width);
    }
    left = Math.min(Math.max(MARGIN, left), Math.max(MARGIN, vw - width - MARGIN));
    top = Math.min(Math.max(MARGIN, top), Math.max(MARGIN, vh - height - MARGIN));
    setPosition({ top, left });
  }, [anchor, align]);

  // Focus follows the active item.
  useEffect(() => {
    const buttons = panelRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]');
    buttons?.[active]?.focus();
  }, [active, position]);

  // Focus returns to whatever opened the menu.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    return () => {
      if (opener && document.contains(opener)) opener.focus();
    };
  }, []);

  // Outside press, scroll, or resize closes the menu.
  useEffect(() => {
    const onDown = (event: PointerEvent) => {
      if (panelRef.current?.contains(event.target as Node)) return;
      if ('element' in anchor && anchor.element.contains(event.target as Node)) return;
      onClose();
    };
    const onResize = () => onClose();
    document.addEventListener('pointerdown', onDown, true);
    window.addEventListener('resize', onResize);
    return () => {
      document.removeEventListener('pointerdown', onDown, true);
      window.removeEventListener('resize', onResize);
    };
  }, [anchor, onClose]);

  const move = useCallback(
    (direction: 1 | -1) => {
      if (enabled.length === 0) return;
      const pos = enabled.findIndex(({ i }) => i === active);
      const next = enabled[(pos + direction + enabled.length) % enabled.length]!;
      setActive(next.i);
    },
    [active, enabled],
  );

  const onKeyDown = (event: React.KeyboardEvent) => {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        move(1);
        return;
      case 'ArrowUp':
        event.preventDefault();
        move(-1);
        return;
      case 'Home':
        event.preventDefault();
        if (enabled[0]) setActive(enabled[0].i);
        return;
      case 'End':
        event.preventDefault();
        if (enabled.length) setActive(enabled[enabled.length - 1]!.i);
        return;
      case 'Escape':
        // Handled here so the viewer or a selection behind the menu does not
        // also react to the same key.
        event.preventDefault();
        event.stopPropagation();
        event.nativeEvent.stopImmediatePropagation();
        onClose();
        return;
      case 'Tab':
        event.preventDefault();
        onClose();
        return;
      case 'ArrowLeft':
      case 'ArrowRight':
        // Swallowed so the page behind (the viewer's previous/next) does not
        // react while the menu has focus.
        event.preventDefault();
        return;
      default:
        break;
    }
    if (event.key.length === 1 && /\S/.test(event.key)) {
      const now = Date.now();
      const state = typeahead.current;
      state.text =
        now - state.at > 600 ? event.key.toLowerCase() : state.text + event.key.toLowerCase();
      state.at = now;
      const match = enabled.find(({ item }) => item.label.toLowerCase().startsWith(state.text));
      if (match) setActive(match.i);
    }
  };

  // Each actionable item's position among the actionable items, which is
  // what roving focus counts (separators are skipped).
  const actionIndexes = items.map((entry, i) =>
    entry === 'separator' ? -1 : items.slice(0, i).filter(isItem).length,
  );
  const rendered: ReactNode[] = items.map((entry, i) => {
    if (entry === 'separator') {
      return <div key={`sep-${i}`} role="separator" className="menu-separator" />;
    }
    const index = actionIndexes[i]!;
    const className = ['menu-item', entry.danger ? 'menu-item-danger' : '']
      .filter(Boolean)
      .join(' ');
    const content = (
      <>
        {entry.icon ? <Icon name={entry.icon} size={18} /> : <span className="menu-item-spacer" />}
        <span>{entry.label}</span>
      </>
    );
    if (entry.href && !entry.disabled) {
      return (
        <a
          key={entry.id}
          role="menuitem"
          tabIndex={index === active ? 0 : -1}
          className={className}
          href={entry.href}
          target={entry.external ? '_blank' : undefined}
          rel={entry.external ? 'noreferrer' : undefined}
          onClick={() => onClose()}
          onMouseEnter={() => setActive(index)}
          data-testid={entry.testId}
        >
          {content}
        </a>
      );
    }
    return (
      <button
        key={entry.id}
        type="button"
        role="menuitem"
        tabIndex={index === active ? 0 : -1}
        className={className}
        disabled={entry.disabled}
        onClick={() => {
          onClose();
          entry.onSelect?.();
        }}
        onMouseEnter={() => setActive(index)}
        data-testid={entry.testId}
      >
        {content}
      </button>
    );
  });

  return createPortal(
    <div
      ref={panelRef}
      id={menuId}
      role="menu"
      aria-label={label}
      className={tone === 'dark' ? 'menu menu-dark' : 'menu'}
      style={
        position
          ? { top: position.top, left: position.left }
          : { top: 0, left: 0, visibility: 'hidden' }
      }
      onKeyDown={onKeyDown}
      onContextMenu={(event) => event.preventDefault()}
    >
      {header && (
        <>
          <div className="menu-header">{header}</div>
          <div role="separator" className="menu-separator" />
        </>
      )}
      {rendered}
    </div>,
    container ?? document.body,
  );
}

/**
 * Put destructive items last, after a separator, so "Delete" is never one slip
 * away from "Rename".
 */
export function withDangerLast(items: MenuItem[]): MenuEntry[] {
  const safe = items.filter((item) => !item.danger);
  const danger = items.filter((item) => item.danger);
  return danger.length && safe.length ? [...safe, 'separator', ...danger] : [...safe, ...danger];
}

/** State for a menu toggled by a button: the anchor element, or null. */
export function useMenuButton() {
  const [anchor, setAnchor] = useState<MenuAnchor | null>(null);
  const toggle = useCallback((event: React.MouseEvent<HTMLElement>) => {
    const element = event.currentTarget;
    setAnchor((current) => (current ? null : { element }));
  }, []);
  const close = useCallback(() => setAnchor(null), []);
  return { anchor, open: anchor !== null, toggle, close, setAnchor };
}
