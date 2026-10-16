/**
 * The reader's side panels for what you've marked in the open book:
 * bookmarks and highlights in one (switched by tabs), quotes in the other.
 *
 * Every entry is a card that jumps to its place in the book when clicked,
 * shows where it is ("Page 12", a chapter) and when it was made, and keeps
 * its own actions (rename, copy, favorite, remove) at hand without crowding
 * the text.
 */

import { useState } from 'react';
import { Link } from 'react-router-dom';

import type { Bookmark, Highlight, Quote } from '../../api/types';
import { Icon } from '../ui/Icon';

/** "Today", "Yesterday", or a short date. */
function shortDate(iso: string, now = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(now) - day(d)) / 86_400_000);
  if (diff === 0) return 'Today';
  if (diff === 1) return 'Yesterday';
  return d.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    ...(d.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}),
  });
}

function Meta({ where, when }: { where: string | null; when: string }) {
  const date = shortDate(when);
  return (
    <span className="reader-card-meta">
      {where && <span>{where}</span>}
      {where && date && <span aria-hidden="true"> · </span>}
      {date && <time dateTime={when}>{date}</time>}
    </span>
  );
}

function PanelHeader({
  title,
  count,
  children,
}: {
  title: string;
  count: number;
  children?: React.ReactNode;
}) {
  return (
    <header className="reader-annot-header">
      <h2>
        {title}
        {count > 0 && <span className="reader-annot-count">{count}</span>}
      </h2>
      {children}
    </header>
  );
}

function Empty({
  icon,
  title,
  hint,
}: {
  icon: 'bookmark' | 'highlighter' | 'quote';
  title: string;
  hint: string;
}) {
  return (
    <div className="reader-annot-empty">
      <span className="reader-annot-empty-icon">
        <Icon name={icon} size={22} />
      </span>
      <p className="reader-annot-empty-title">{title}</p>
      <p className="muted">{hint}</p>
    </div>
  );
}

interface CommonProps {
  /** A human description of a location ("Page 12", a chapter), if any. */
  describe: (location: string) => string | null;
  onJump: (location: string) => void;
  error: string | null;
}

export function BookmarksPanel({
  bookmarks,
  highlights,
  describe,
  onJump,
  error,
  onAddBookmark,
  onRenameBookmark,
  onRemoveBookmark,
  onRemoveHighlight,
}: CommonProps & {
  bookmarks: Bookmark[];
  highlights: Highlight[];
  onAddBookmark: () => void;
  onRenameBookmark: (id: string, label: string) => void;
  onRemoveBookmark: (id: string) => void;
  onRemoveHighlight: (id: string) => void;
}) {
  const [tab, setTab] = useState<'bookmarks' | 'highlights'>('bookmarks');
  const [editing, setEditing] = useState<{ id: string; label: string } | null>(null);

  const commitRename = () => {
    if (!editing) return;
    onRenameBookmark(editing.id, editing.label.trim());
    setEditing(null);
  };

  return (
    <div className="reader-annot-panel" data-testid="reader-annotations">
      <PanelHeader title="Bookmarks & highlights" count={0} />

      <div className="reader-annot-tabs" role="tablist" aria-label="Marks">
        {(
          [
            ['bookmarks', 'Bookmarks', bookmarks.length],
            ['highlights', 'Highlights', highlights.length],
          ] as const
        ).map(([id, label, count]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            className={tab === id ? 'reader-annot-tab active' : 'reader-annot-tab'}
            onClick={() => setTab(id)}
          >
            {label}
            <span className="reader-annot-tab-count">{count}</span>
          </button>
        ))}
      </div>

      {error && <p className="error-text">{error}</p>}

      {tab === 'bookmarks' && (
        <button type="button" className="reader-annot-add" onClick={onAddBookmark}>
          <Icon name="plus" size={16} /> Bookmark this page
        </button>
      )}

      {tab === 'bookmarks' &&
        (bookmarks.length === 0 ? (
          <Empty
            icon="bookmark"
            title="No bookmarks yet"
            hint="Mark your place with “Bookmark this page” to come back to it later."
          />
        ) : (
          <ul className="reader-card-list" aria-label="Bookmarks">
            {bookmarks.map((b) => {
              const where = describe(b.location);
              const title = b.label || where || 'Bookmark';
              return (
                <li key={b.id} className="reader-card reader-bookmark-card">
                  {editing?.id === b.id ? (
                    <form
                      className="reader-card-rename"
                      onSubmit={(e) => {
                        e.preventDefault();
                        commitRename();
                      }}
                    >
                      <input
                        className="reader-selection-input"
                        aria-label="Bookmark name"
                        value={editing.label}
                        placeholder={where ?? 'Bookmark'}
                        onChange={(e) => setEditing({ id: b.id, label: e.target.value })}
                        onBlur={commitRename}
                        onKeyDown={(e) => {
                          if (e.key === 'Escape') {
                            e.stopPropagation();
                            setEditing(null);
                          }
                        }}
                        autoFocus
                      />
                    </form>
                  ) : (
                    <button
                      type="button"
                      className="reader-card-main"
                      onClick={() => onJump(b.location)}
                    >
                      <span className="reader-bookmark-icon" aria-hidden="true">
                        <Icon name="bookmark" size={16} filled />
                      </span>
                      <span className="reader-card-body">
                        <span className="reader-card-title">{title}</span>
                        <Meta where={b.label ? where : null} when={b.created_at} />
                      </span>
                    </button>
                  )}
                  {editing?.id !== b.id && (
                    <div className="reader-card-actions">
                      <button
                        type="button"
                        className="icon-button"
                        aria-label="Rename bookmark"
                        title="Rename"
                        onClick={() => setEditing({ id: b.id, label: b.label ?? '' })}
                      >
                        <Icon name="edit" size={16} />
                      </button>
                      <button
                        type="button"
                        className="icon-button"
                        aria-label="Remove bookmark"
                        title="Remove"
                        onClick={() => onRemoveBookmark(b.id)}
                      >
                        <Icon name="trash" size={16} />
                      </button>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        ))}

      {tab === 'highlights' &&
        (highlights.length === 0 ? (
          <Empty
            icon="highlighter"
            title="No highlights yet"
            hint="Select text in the book and pick a color to highlight it."
          />
        ) : (
          <ul className="reader-card-list" aria-label="Highlights">
            {highlights.map((h) => (
              <li
                key={h.id}
                className={`reader-card reader-highlight-card reader-highlight-${h.color}`}
              >
                <button
                  type="button"
                  className="reader-card-main"
                  onClick={() => onJump(h.location)}
                >
                  <span className="reader-card-body">
                    <span className="reader-card-excerpt">{h.text}</span>
                    {h.note && <span className="reader-card-note">{h.note}</span>}
                    <Meta where={describe(h.location)} when={h.created_at} />
                  </span>
                </button>
                <div className="reader-card-actions">
                  <button
                    type="button"
                    className="icon-button"
                    aria-label="Remove highlight"
                    title="Remove"
                    onClick={() => onRemoveHighlight(h.id)}
                  >
                    <Icon name="trash" size={16} />
                  </button>
                </div>
              </li>
            ))}
          </ul>
        ))}
    </div>
  );
}

export function QuotesPanel({
  quotes,
  describe,
  onJump,
  error,
  onRemove,
  onToggleFavorite,
  onCopy,
}: CommonProps & {
  quotes: Quote[];
  onRemove: (id: string) => void;
  onToggleFavorite: (quote: Quote) => void;
  onCopy: (quote: Quote) => void;
}) {
  return (
    <div className="reader-annot-panel" data-testid="reader-quotes">
      <PanelHeader title="Quotes" count={quotes.length}>
        <Link className="button ghost-button" to="/quotes">
          All quotes
        </Link>
      </PanelHeader>

      {error && <p className="error-text">{error}</p>}

      {quotes.length === 0 ? (
        <Empty
          icon="quote"
          title="No quotes yet"
          hint="Select a passage and choose Quote — or right-click it — to keep it here."
        />
      ) : (
        <ul className="reader-card-list" aria-label="Quotes">
          {quotes.map((q) => {
            const where =
              (q.location ? describe(q.location) : null) ??
              (q.chapter || (q.page ? `Page ${q.page}` : null));
            const body = (
              <span className="reader-card-body">
                <span className="reader-quote-text">{q.text}</span>
                {q.commentary && <span className="reader-card-note">{q.commentary}</span>}
                <Meta where={where} when={q.created_at} />
              </span>
            );
            return (
              <li key={q.id} className="reader-card reader-quote-card">
                {q.location ? (
                  <button
                    type="button"
                    className="reader-card-main"
                    onClick={() => onJump(q.location!)}
                  >
                    {body}
                  </button>
                ) : (
                  <div className="reader-card-main">{body}</div>
                )}
                <div className="reader-card-actions">
                  <button
                    type="button"
                    className={q.favorite ? 'icon-button active' : 'icon-button'}
                    aria-label={q.favorite ? 'Unfavorite quote' : 'Favorite quote'}
                    aria-pressed={q.favorite}
                    title={q.favorite ? 'Unfavorite' : 'Favorite'}
                    onClick={() => onToggleFavorite(q)}
                  >
                    <Icon name="star" size={16} filled={q.favorite} />
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label="Copy quote"
                    title="Copy"
                    onClick={() => onCopy(q)}
                  >
                    <Icon name="copy" size={16} />
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label="Remove quote"
                    title="Remove"
                    onClick={() => onRemove(q.id)}
                  >
                    <Icon name="trash" size={16} />
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
