/**
 * The building blocks every page shares: its header, and the states a page
 * can be in besides "showing content" — loading, empty, failed, offline, and
 * not allowed.
 *
 * They are written for people, not for operators: what happened, what it
 * means for them, and what to do next, in a sentence or two. Technical detail
 * (paths, codes) stays out of the way.
 */

import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useLibraries } from '../api/libraries';
import type { Library, LibraryStatus } from '../api/types';
import { Icon, type IconName } from './ui/Icon';
import './States.css';

/** The title row every page starts with. */
export function PageHeader({
  title,
  subtitle,
  controls,
}: {
  title: string;
  subtitle?: ReactNode;
  controls?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div className="page-header-titles">
        <h1>{title}</h1>
        {subtitle && <p className="page-header-subtitle">{subtitle}</p>}
      </div>
      {controls && <div className="header-controls">{controls}</div>}
    </header>
  );
}

/**
 * A library dropdown, for pages (permissions, sharing) that act on a library
 * other than the one being browsed.
 */
export function LibrarySelect({
  label = 'Library',
  value,
  onChange,
  includeOffline = true,
}: {
  label?: string;
  value: string | null;
  onChange: (id: string) => void;
  includeOffline?: boolean;
}) {
  const { libraries } = useLibraries();
  const visible = includeOffline ? libraries : libraries.filter((lib) => lib.status !== 'offline');

  if (visible.length === 0) return null;

  return (
    <select
      aria-label={label}
      value={value ?? ''}
      onChange={(event) => onChange(event.target.value)}
    >
      {visible.map((lib) => (
        <option key={lib.id} value={lib.id}>
          {lib.name}
          {lib.status === 'offline' ? ' (offline)' : ''}
        </option>
      ))}
    </select>
  );
}

/** Connected or disconnected, as a small dot-and-word badge. */
export function LibraryStatusBadge({ status }: { status: LibraryStatus }) {
  const offline = status === 'offline';
  return (
    <span
      className={offline ? 'status-badge status-badge-offline' : 'status-badge status-badge-online'}
      data-testid="library-status"
      data-status={status}
    >
      {offline ? 'Offline' : 'Online'}
    </span>
  );
}

/** A small labelled pill for a media type. */
export function TypeBadge({ children }: { children: ReactNode }) {
  return <span className="type-badge">{children}</span>;
}

/**
 * Inline "this is loading" for lists and panels. Media grids use their own
 * skeleton instead, which holds the shape of what is coming.
 */
export function LoadingState({ label = 'Loading…' }: { label?: string }) {
  return (
    <p className="loading-state" role="status">
      <span className="loading-dots" aria-hidden="true">
        <span />
        <span />
        <span />
      </span>
      {label}
    </p>
  );
}

/** Rows of placeholder text in the shape of a list. */
export function ListSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="list-skeleton" aria-hidden="true">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="list-skeleton-row">
          <span className="skeleton list-skeleton-thumb" />
          <span
            className="skeleton list-skeleton-line"
            style={{ width: `${40 + ((i * 17) % 40)}%` }}
          />
        </div>
      ))}
    </div>
  );
}

/**
 * Something failed. Calm, specific, and with a way forward: the server's
 * message (already written for people), and Try again when it makes sense.
 */
export function ErrorState({
  message,
  onRetry,
  title = "Couldn't load this",
}: {
  message: string;
  onRetry?: () => void;
  title?: string;
}) {
  return (
    <div className="error-state" role="alert">
      <span className="state-icon state-icon-error" aria-hidden="true">
        <Icon name="alert" />
      </span>
      <div className="error-state-text">
        <p className="error-state-title">{title}</p>
        <p className="error-text">{message}</p>
      </div>
      {onRetry && (
        <button type="button" className="button" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

/**
 * Nothing here (yet). The heading is set in the brand hand, because an empty
 * library is a beginning rather than an error; the body says what will fill it.
 */
export function EmptyState({
  title,
  children,
  action,
  testId,
  icon = 'photo',
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  testId?: string;
  icon?: IconName;
}) {
  return (
    <div className="empty-state" data-testid={testId}>
      <span className="empty-state-pebble" aria-hidden="true">
        <Icon name={icon} size={28} />
      </span>
      <h2 className="empty-state-title">{title}</h2>
      {children && <div className="empty-state-body">{children}</div>}
      {action && <div className="empty-state-action">{action}</div>}
    </div>
  );
}

/**
 * The "no libraries at all" state. Administrators get a route to add one;
 * members are told to ask, because adding a library is admin-only.
 */
export function NoLibrariesState({ isAdmin }: { isAdmin: boolean }) {
  return (
    <EmptyState
      title="Let's find your photos"
      testId="no-libraries"
      icon="drive"
      action={
        isAdmin ? (
          <Link className="button primary-button" to="/libraries">
            Add a library
          </Link>
        ) : undefined
      }
    >
      <p>
        Cairn organizes media that already lives on your disk. Point it at a folder and it will
        index it in place — nothing is copied or changed.
      </p>
      {!isAdmin && (
        <p>You don't have access to a library yet. Ask an administrator to share one.</p>
      )}
    </EmptyState>
  );
}

/**
 * The library's drive is not connected. Not an error and not empty: the
 * details Cairn keeps (albums, tags, memories) are still here, the files are
 * not. Administrators also see where the drive was last mounted.
 */
export function LibraryOfflineNotice({ library }: { library: Library }) {
  const { user } = useAuth();
  return (
    <div className="notice notice-warning" role="status" data-testid="library-offline">
      <span className="state-icon state-icon-warning" aria-hidden="true">
        <Icon name="drive-off" />
      </span>
      <div className="notice-text">
        <p className="notice-title">{library.name} is unavailable</p>
        <p>
          The drive holding this library is disconnected. Your albums, tags, and memories are safe —
          reconnect it and Cairn will pick up where it left off.
        </p>
        {user?.role === 'admin' && (
          <p className="notice-detail">
            Last seen at <code>{library.root}</code> · <Link to="/libraries">Reconnect</Link>
          </p>
        )}
      </div>
    </div>
  );
}

/** Inline "you can't see this", for a section of a page rather than a whole route. */
export function PermissionDeniedState({ what = 'this' }: { what?: string }) {
  return (
    <EmptyState title="Not shared with you" icon="lock" testId="permission-denied">
      <p>You don't have access to {what}. Ask the library's owner to share it with you.</p>
    </EmptyState>
  );
}
