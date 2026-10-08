/**
 * The contextual bar that replaces the top bar while media is selected.
 *
 * It says how many items are selected, offers the actions that make sense for
 * them on this page, and gets out of the way the moment the selection is
 * cleared. Pages pass their own actions — Trash offers Restore, an album
 * offers Remove from album — so the bar never shows something that cannot be
 * done here.
 */

import { useEffect } from 'react';

import { Icon, type IconName } from '../ui/Icon';
import './SelectionToolbar.css';

export interface SelectionAction {
  id: string;
  label: string;
  icon: IconName;
  onClick?: () => void;
  /** A link action (download a single file). */
  href?: string;
  danger?: boolean;
  disabled?: boolean;
  testId?: string;
}

interface SelectionToolbarProps {
  count: number;
  total?: number | undefined;
  actions: SelectionAction[];
  onClear: () => void;
  onSelectAll?: (() => void) | undefined;
  /** The Delete key runs this, when nothing else has focus of a text field. */
  onDeleteKey?: (() => void) | undefined;
}

export function SelectionToolbar({
  count,
  total,
  actions,
  onClear,
  onSelectAll,
  onDeleteKey,
}: SelectionToolbarProps) {
  useEffect(() => {
    if (!onDeleteKey) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Delete' || event.defaultPrevented) return;
      const target = event.target as HTMLElement | null;
      if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;
      event.preventDefault();
      onDeleteKey();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onDeleteKey]);

  const allSelected = total !== undefined && count >= total;

  return (
    <div
      className="selection-bar"
      role="toolbar"
      aria-label="Actions for selected media"
      data-testid="selection-bar"
    >
      <button type="button" className="icon-button" onClick={onClear} aria-label="Clear selection">
        <Icon name="close" />
      </button>
      <span className="selection-count" data-testid="selection-count" aria-live="polite">
        {count} selected
      </span>
      {onSelectAll && !allSelected && (
        <button
          type="button"
          className="icon-button"
          onClick={onSelectAll}
          aria-label="Select all"
          title="Select all (Ctrl+A)"
        >
          <Icon name="select-all" />
        </button>
      )}
      <div className="selection-actions">
        {actions.map((action) =>
          action.href && !action.disabled ? (
            <a
              key={action.id}
              className="selection-action"
              href={action.href}
              target="_blank"
              rel="noreferrer"
              title={action.label}
              data-testid={action.testId}
            >
              <Icon name={action.icon} />
              <span className="selection-action-label">{action.label}</span>
            </a>
          ) : (
            <button
              key={action.id}
              type="button"
              className={action.danger ? 'selection-action is-danger' : 'selection-action'}
              onClick={action.onClick}
              disabled={action.disabled}
              title={action.label}
              data-testid={action.testId}
            >
              <Icon name={action.icon} />
              <span className="selection-action-label">{action.label}</span>
            </button>
          ),
        )}
      </div>
    </div>
  );
}
