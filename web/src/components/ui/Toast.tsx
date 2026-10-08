/* eslint-disable react-refresh/only-export-components --
 * The provider and the hook that reads it are one unit.
 */
/**
 * Toasts: short confirmations that an action happened ("Added to album"),
 * and failures that do not deserve a dialog ("Couldn't move 3 files").
 *
 * Restrained on purpose — one line, an optional action, gone after a few
 * seconds, never stacked more than three deep, and announced politely to
 * screen readers. Toasts sit above the bottom navigation and never cover the
 * viewer's controls.
 */

import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { Icon } from './Icon';
import './Toast.css';

export interface ToastOptions {
  message: string;
  tone?: 'neutral' | 'success' | 'error';
  action?: { label: string; onClick: () => void };
  /** Milliseconds before it dismisses itself. Errors stay a little longer. */
  duration?: number;
}

interface ToastEntry extends ToastOptions {
  id: number;
}

type ToastFn = (options: ToastOptions | string) => void;

const ToastContext = createContext<ToastFn | null>(null);

const MAX_VISIBLE = 3;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastEntry[]>([]);
  const nextId = useRef(1);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());

  const dismiss = useCallback((id: number) => {
    setToasts((list) => list.filter((t) => t.id !== id));
    const timer = timers.current.get(id);
    if (timer) clearTimeout(timer);
    timers.current.delete(id);
  }, []);

  const show = useCallback<ToastFn>(
    (input) => {
      const options = typeof input === 'string' ? { message: input } : input;
      const id = nextId.current++;
      setToasts((list) => [...list, { ...options, id }].slice(-MAX_VISIBLE));
      const duration = options.duration ?? (options.tone === 'error' ? 6000 : 4000);
      timers.current.set(
        id,
        setTimeout(() => dismiss(id), duration),
      );
    },
    [dismiss],
  );

  const value = useMemo(() => show, [show]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="toast-region" role="status" aria-live="polite" aria-atomic="false">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={`toast toast-${toast.tone ?? 'neutral'}`}
            data-testid="toast"
          >
            {toast.tone === 'success' && <Icon name="check" size={18} />}
            {toast.tone === 'error' && <Icon name="alert" size={18} />}
            <span className="toast-message">{toast.message}</span>
            {toast.action && (
              <button
                type="button"
                className="toast-action"
                onClick={() => {
                  toast.action?.onClick();
                  dismiss(toast.id);
                }}
              >
                {toast.action.label}
              </button>
            )}
            <button
              type="button"
              className="toast-dismiss"
              aria-label="Dismiss notification"
              onClick={() => dismiss(toast.id)}
            >
              <Icon name="close" size={16} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

/**
 * Show a toast. Outside a provider (an isolated component test, the public
 * share page) it is a no-op rather than an error, so shared components can
 * call it unconditionally.
 */
export function useToast(): ToastFn {
  return useContext(ToastContext) ?? noop;
}

function noop() {}
