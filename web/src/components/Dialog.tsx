/**
 * Accessible dialog primitives.
 *
 * The pages previously used `window.prompt` and `window.confirm` for every
 * destructive or naming action. Those are unusable with a screen reader, they
 * cannot be styled, they block the main thread, and they give no focus
 * management. These components replace them with real `role="dialog"` markup,
 * a focus trap, focus restore, Escape to dismiss, and a backdrop that only
 * closes on an intentional click.
 */

import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { FOCUSABLE, trapTab } from '../lib/focusTrap';
import { Icon } from './ui/Icon';
import './Dialog.css';

export interface DialogProps {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  /** Footer buttons; the dialog handles the layout and default focus. */
  footer?: ReactNode;
  /** Width hint for the panel. */
  size?: 'small' | 'medium' | 'large';
  /** Whether Escape and a backdrop click dismiss the dialog. */
  dismissible?: boolean | undefined;
  testId?: string | undefined;
}

/**
 * A modal dialog. Traps Tab within itself, moves focus in on open, restores it
 * to the invoking element on close, and dismisses on Escape.
 */
export function Dialog({
  open,
  title,
  onClose,
  children,
  footer,
  size = 'small',
  dismissible = true,
  testId,
}: DialogProps) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const restoreRef = useRef<HTMLElement | null>(null);
  const titleId = useId();

  // Remember the invoker so focus returns there on close, then move focus in.
  useEffect(() => {
    if (!open) return;
    restoreRef.current = document.activeElement as HTMLElement | null;
    const panel = panelRef.current;
    if (!panel) return;
    const first = panel.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? panel).focus();
    return () => {
      restoreRef.current?.focus?.();
    };
  }, [open]);

  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'Escape' && dismissible) {
        event.stopPropagation();
        onClose();
        return;
      }
      trapTab(panelRef.current, event);
    },
    [dismissible, onClose],
  );

  if (!open) return null;

  return (
    <div
      className="dialog-backdrop"
      onMouseDown={(event) => {
        // Only a click that both starts and ends on the backdrop dismisses, so
        // a text selection dragged out of the panel does not close it.
        if (dismissible && event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={panelRef}
        className={`dialog-panel dialog-${size}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        data-testid={testId}
      >
        <header className="dialog-header">
          <h2 id={titleId}>{title}</h2>
          {dismissible && (
            <button
              type="button"
              className="dialog-close"
              onClick={onClose}
              aria-label={`Close ${title}`}
            >
              <Icon name="close" />
            </button>
          )}
        </header>
        <div className="dialog-body">{children}</div>
        {footer && <div className="dialog-footer">{footer}</div>}
      </div>
    </div>
  );
}

export interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** Styles the confirm button as destructive and focuses it first. */
  destructive?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  busy?: boolean | undefined;
  error?: string | null | undefined;
  testId?: string | undefined;
}

/** A yes/no dialog replacing `window.confirm`. */
export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  destructive,
  onConfirm,
  onCancel,
  busy,
  error,
  testId,
}: ConfirmDialogProps) {
  return (
    <Dialog
      open={open}
      title={title}
      onClose={onCancel}
      dismissible={!busy}
      testId={testId}
      footer={
        <>
          <button type="button" className="button" onClick={onCancel} disabled={busy}>
            {cancelLabel}
          </button>
          <button
            type="button"
            className={destructive ? 'button danger-button' : 'button primary-button'}
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? 'Working…' : confirmLabel}
          </button>
        </>
      }
    >
      <div className="dialog-message">{message}</div>
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </Dialog>
  );
}

export interface PromptDialogProps {
  open: boolean;
  title: string;
  label: string;
  /** Initial value for the field. */
  initialValue?: string;
  placeholder?: string;
  hint?: string;
  confirmLabel?: string | undefined;
  /** Renders a textarea instead of a single-line input. */
  multiline?: boolean | undefined;
  /**
   * Replaces the plain field with richer controls that edit the same value —
   * the folder dropdown in the move and copy dialogs, which offers the
   * library's real folders and writes back through `setValue`. Without this
   * the dialog would show a menu and a text box that disagree, because only
   * one of them would know the value.
   */
  control?: ((state: PromptControl) => ReactNode) | undefined;
  onConfirm: (value: string) => void;
  onCancel: () => void;
  busy?: boolean | undefined;
  error?: string | null | undefined;
  testId?: string | undefined;
}

/** The live value of a {@link PromptDialog}'s field, for `control`. */
export interface PromptControl {
  value: string;
  setValue: (next: string) => void;
}

/**
 * A single-field dialog replacing `window.prompt`.
 *
 * The stateful form lives in a child component so that it is unmounted along
 * with the dialog: callers pass `initialValue` derived from whatever they are
 * acting on, and reopening for a different file has to reset the field instead
 * of showing whatever was last typed.
 */
export function PromptDialog({ open, initialValue = '', ...rest }: PromptDialogProps) {
  if (!open) return null;
  return <PromptForm {...rest} initialValue={initialValue} />;
}

/** {@link PromptDialogProps} without the `open` flag the wrapper consumes. */
type PromptFormProps = Omit<PromptDialogProps, 'open'>;

function PromptForm({
  title,
  label,
  initialValue = '',
  placeholder,
  hint,
  confirmLabel = 'Save',
  multiline,
  control,
  onConfirm,
  onCancel,
  busy,
  error,
  testId,
}: PromptFormProps) {
  const [value, setValue] = useState(initialValue);

  return (
    <Dialog
      open
      title={title}
      onClose={onCancel}
      dismissible={!busy}
      testId={testId}
      footer={
        <>
          <button type="button" className="button" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button
            type="submit"
            form={promptFormId}
            className="button primary-button"
            disabled={busy}
          >
            {busy ? 'Saving…' : confirmLabel}
          </button>
        </>
      }
    >
      <form
        id={promptFormId}
        className="dialog-form"
        onSubmit={(event) => {
          event.preventDefault();
          const next = value.trim();
          if (next) onConfirm(next);
        }}
      >
        {control ? (
          control({ value, setValue })
        ) : (
          <>
            <label className="dialog-label" htmlFor={`${promptFormId}-field`}>
              {label}
            </label>
            {multiline ? (
              <textarea
                id={`${promptFormId}-field`}
                name="value"
                className="dialog-input"
                rows={4}
                value={value}
                placeholder={placeholder}
                disabled={busy}
                onChange={(e) => setValue(e.target.value)}
              />
            ) : (
              <input
                id={`${promptFormId}-field`}
                name="value"
                type="text"
                className="dialog-input"
                value={value}
                placeholder={placeholder}
                disabled={busy}
                onChange={(e) => setValue(e.target.value)}
              />
            )}
          </>
        )}
        {hint && <p className="dialog-hint">{hint}</p>}
        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </form>
    </Dialog>
  );
}

const promptFormId = 'cairn-prompt-form';
