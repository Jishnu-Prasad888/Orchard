/**
 * Keeping Tab inside a modal dialog.
 *
 * A dialog that is `aria-modal="true"` promises the rest of the page is not
 * reachable, and that is a promise assistive technology takes seriously: a
 * screen reader in focus mode will happily walk past the end of the dialog and
 * start announcing the page behind it. Trapping Tab is what makes the promise
 * true, and it is behaviour rather than markup, which is why it is a hook and
 * not a `<div>`.
 *
 * Shared by `components/Dialog.tsx` (which listens on the panel) and
 * `components/ViewerModal.tsx` (which listens on `window`, because its arrow
 * keys and Escape are global).
 */

import { useEffect } from 'react';
import type { RefObject } from 'react';

/** Selector for everything that can hold focus inside a dialog. */
export const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** The part of a keyboard event the trap needs, from either DOM or React. */
export interface TabKeyEvent {
  key: string;
  shiftKey: boolean;
  preventDefault(): void;
}

/**
 * Whether an element can be reached right now.
 *
 * Deliberately not `offsetParent !== null`: that is the usual trick, but jsdom
 * never lays anything out, so it reports every element as hidden and the trap
 * degenerates to "only the element that already has focus". Hidden-ness that
 * actually matters here — a collapsed panel, an `aria-hidden` subtree — is
 * expressed in the attributes this checks.
 */
function isReachable(el: HTMLElement): boolean {
  if (el.hidden) return false;
  return !el.closest('[hidden], [aria-hidden="true"]');
}

/**
 * Wrap focus from the last focusable element back to the first, and the first
 * back to the last. Returns true when it handled the key.
 */
export function trapTab(panel: HTMLElement | null, event: TabKeyEvent): boolean {
  if (event.key !== 'Tab' || !panel) return false;
  const focusable = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(isReachable);
  if (focusable.length === 0) {
    // Nothing to move to, so the panel itself keeps focus rather than letting
    // it escape to the page behind.
    event.preventDefault();
    panel.focus();
    return true;
  }
  const first = focusable[0]!;
  const last = focusable[focusable.length - 1]!;
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
    return true;
  }
  if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
    return true;
  }
  return false;
}

/** The same trap for a dialog that listens on `window` rather than on itself. */
export function useFocusTrap(ref: RefObject<HTMLElement | null>, active = true): void {
  useEffect(() => {
    if (!active) return;
    const onKey = (event: KeyboardEvent) => {
      trapTab(ref.current, event);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [ref, active]);
}
