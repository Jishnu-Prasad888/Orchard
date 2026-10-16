/**
 * Notes — long-form, per-user Markdown writing. Unlike the single caption
 * shown in the viewer (FileNote), a note can stand alone or be about a
 * file, a saved quote, or a highlight, and a file can have many notes.
 * List ⇄ detail, mirroring Collections/Tags, but the detail view is a
 * Markdown editor with live preview instead of a file grid.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useFileLabels } from '../api/fileLabels';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import {
  addNoteLink,
  addNoteTag,
  createNote,
  createTag,
  deleteNote,
  getNote,
  listNoteLinks,
  listNoteTags,
  listNotes,
  listTags,
  removeNoteLink,
  removeNoteTag,
  updateNote,
} from '../api/queries';
import type { Note, Tag } from '../api/types';
import { extractExcerpt, renderMarkdown } from '../lib/markdown';
import { ConfirmDialog } from '../components/Dialog';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import { matchesQuery, usePageQuery } from '../components/app-shell/pageSearch';
import { Icon } from '../components/ui/Icon';
import './NotesPage.css';

type SaveStatus = 'idle' | 'saving' | 'saved' | 'error';

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

/** Opens a standalone print view for a note — the lowest-risk "export to
 * PDF": the browser's own print dialog offers "Save as PDF" with no new
 * dependency and no print stylesheet needed on the main app shell. */
function printNote(title: string, body: string) {
  const win = window.open('', '_blank', 'noopener,noreferrer');
  if (!win) return;
  const heading = title || 'Untitled note';
  const escapedTitle = heading.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c]!);
  win.document.write(`<!doctype html><html><head><title>${escapedTitle}</title>
    <style>
      body { font: 16px/1.6 Georgia, 'Times New Roman', serif; max-width: 680px; margin: 48px auto; padding: 0 16px; color: #1a1a1a; }
      h1 { font-size: 1.5em; margin-bottom: 0.3em; }
    </style></head>
    <body><h1>${escapedTitle}</h1>${renderMarkdown(body).html}</body></html>`);
  win.document.close();
  win.focus();
  win.print();
}

function downloadText(fileName: string, text: string) {
  const blob = new Blob([text], { type: 'text/markdown;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName;
  a.click();
  URL.revokeObjectURL(url);
}

/** All notes currently shown (respecting the active tag/standalone filter)
 * as one Markdown document, each note under its own heading — the bulk
 * counterpart to the single-note export in NoteDetail's header. */
function notesToMarkdown(notes: Note[], titleFor: (fileId: string) => string): string {
  return notes
    .map((n) => {
      const heading = n.title || extractExcerpt(n.body, 60) || 'Untitled note';
      const about = n.file_id ? titleFor(n.file_id) : undefined;
      const lines = [`## ${heading}`, ...(about ? [`*About: ${about}*`, ''] : []), n.body];
      return lines.join('\n');
    })
    .join('\n\n---\n\n');
}

function NoteDetail({
  libraryId,
  note,
  allNotes,
  fileTitle,
  onBack,
  onSaved,
  onDeleted,
}: {
  libraryId: string;
  note: Note;
  allNotes: Note[];
  fileTitle: string | undefined;
  onBack: () => void;
  onSaved: (note: Note) => void;
  onDeleted: () => void;
}) {
  const [title, setTitle] = useState(note.title ?? '');
  const [body, setBody] = useState(note.body);
  const [status, setStatus] = useState<SaveStatus>('idle');
  const [error, setError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  const [tags, setTags] = useState<Tag[] | null>(null);
  const [libraryTags, setLibraryTags] = useState<Tag[]>([]);
  const [tagInput, setTagInput] = useState('');
  const [linked, setLinked] = useState<Note[] | null>(null);
  const [linkPick, setLinkPick] = useState('');

  const draftRef = useRef({ title: note.title ?? '', body: note.body });
  const lastSavedRef = useRef({ title: note.title ?? '', body: note.body });
  useEffect(() => {
    draftRef.current = { title, body };
  }, [title, body]);

  useEffect(() => {
    Promise.all([listNoteTags(libraryId, note.id), listTags(libraryId)])
      .then(([mine, all]) => {
        setTags(mine.tags ?? []);
        setLibraryTags(all.tags ?? []);
      })
      .catch(() => setTags([]));
    listNoteLinks(libraryId, note.id)
      .then((resp) => setLinked(resp.notes ?? []))
      .catch(() => setLinked([]));
  }, [libraryId, note.id]);

  const save = useCallback(() => {
    const draft = draftRef.current;
    if (draft.title === lastSavedRef.current.title && draft.body === lastSavedRef.current.body) return;
    setStatus('saving');
    setError(null);
    updateNote(libraryId, note.id, { title: draft.title, body: draft.body })
      .then((resp) => {
        lastSavedRef.current = { title: resp.note.title ?? '', body: resp.note.body };
        setStatus('saved');
        onSaved(resp.note);
      })
      .catch((e: unknown) => {
        setStatus('error');
        setError(e instanceof Error ? e.message : String(e));
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [libraryId, note.id]);

  useEffect(() => {
    if (title === lastSavedRef.current.title && body === lastSavedRef.current.body) return;
    const timer = setTimeout(save, 800);
    return () => clearTimeout(timer);
  }, [title, body, save]);

  const preview = useMemo(() => renderMarkdown(body).html, [body]);

  const addTag = async (raw: string) => {
    const name = raw.trim();
    if (!name) return;
    try {
      const existing = libraryTags.find((t) => t.name.toLowerCase() === name.toLowerCase());
      let tagId = existing?.id;
      if (!tagId) {
        const created = await createTag(libraryId, name);
        tagId = created.tag.id;
        setLibraryTags((prev) => [...prev, created.tag]);
      }
      await addNoteTag(libraryId, note.id, tagId);
      const mine = await listNoteTags(libraryId, note.id);
      setTags(mine.tags ?? []);
      setTagInput('');
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const removeTag = async (tag: Tag) => {
    try {
      await removeNoteTag(libraryId, note.id, tag.id);
      setTags((prev) => (prev ?? []).filter((t) => t.id !== tag.id));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const addLink = async () => {
    if (!linkPick) return;
    try {
      await addNoteLink(libraryId, note.id, linkPick);
      const resp = await listNoteLinks(libraryId, note.id);
      setLinked(resp.notes ?? []);
      setLinkPick('');
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const removeLink = async (linkedNoteId: string) => {
    try {
      await removeNoteLink(libraryId, note.id, linkedNoteId);
      setLinked((prev) => (prev ?? []).filter((n) => n.id !== linkedNoteId));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const linkCandidates = allNotes.filter(
    (n) => n.id !== note.id && !(linked ?? []).some((l) => l.id === n.id),
  );

  return (
    <section className="note-detail" data-testid="note-detail">
      <div className="note-detail-header">
        <button type="button" className="button" onClick={onBack}>
          ← Notes
        </button>
        <span className={`note-status note-status-${status}`} role="status" aria-live="polite">
          {status === 'saving' && 'Saving…'}
          {status === 'saved' && 'Saved'}
          {status === 'error' && 'Save failed'}
        </span>
        <button
          type="button"
          className="button"
          onClick={() => downloadText(`${title || 'note'}.md`, title ? `# ${title}\n\n${body}` : body)}
        >
          Export
        </button>
        <button type="button" className="button" onClick={() => printNote(title, body)}>
          Print / PDF
        </button>
        <button type="button" className="button danger-button" onClick={() => setDeleting(true)}>
          Delete note
        </button>
      </div>

      {note.file_id && (
        <p className="note-detail-source">
          About: <Link to={`/read/${libraryId}/${note.file_id}`}>{fileTitle ?? '…'}</Link>
        </p>
      )}
      {note.quote_id && !note.file_id && <p className="note-detail-source">Linked to a saved quote.</p>}
      {note.highlight_id && !note.file_id && <p className="note-detail-source">Linked to a highlight.</p>}

      <input
        className="note-title-input"
        placeholder="Untitled note"
        aria-label="Note title"
        value={title}
        onChange={(e) => setTitle(e.target.value)}
      />

      <div className="note-editor-split">
        <label className="visually-hidden" htmlFor="note-body-input">
          Note body (Markdown)
        </label>
        <textarea
          id="note-body-input"
          className="note-body-input"
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="Write in Markdown…"
        />
        <div
          className="note-body-preview"
          aria-label="Note preview"
          dangerouslySetInnerHTML={{ __html: preview }}
        />
      </div>

      <div className="note-detail-tags">
        <h3>Tags</h3>
        {tags !== null && tags.length > 0 && (
          <ul className="viewer-tag-list" data-testid="note-tag-list">
            {tags.map((t) => (
              <li key={t.id} className="tag-chip">
                {t.name}
                <button type="button" aria-label={`Remove tag ${t.name}`} onClick={() => void removeTag(t)}>
                  <Icon name="close" size={14} />
                </button>
              </li>
            ))}
          </ul>
        )}
        <form
          className="note-tag-form"
          onSubmit={(e) => {
            e.preventDefault();
            void addTag(tagInput);
          }}
        >
          <label className="visually-hidden" htmlFor="note-tag-input">
            Add a tag
          </label>
          <input
            id="note-tag-input"
            type="text"
            list="note-tag-suggestions"
            placeholder="Add tag…"
            value={tagInput}
            onChange={(e) => setTagInput(e.target.value)}
          />
          <datalist id="note-tag-suggestions">
            {libraryTags.map((t) => (
              <option key={t.id} value={t.name} />
            ))}
          </datalist>
          <button type="submit" className="button" disabled={tagInput.trim() === ''}>
            Add
          </button>
        </form>
      </div>

      <div className="note-detail-links">
        <h3>Linked notes</h3>
        {linked !== null && linked.length > 0 && (
          <ul className="note-link-list" data-testid="note-link-list">
            {linked.map((n) => (
              <li key={n.id}>
                <span>{n.title || extractExcerpt(n.body, 60) || 'Untitled note'}</span>
                <button type="button" aria-label="Unlink note" onClick={() => void removeLink(n.id)}>
                  <Icon name="close" size={14} />
                </button>
              </li>
            ))}
          </ul>
        )}
        {linkCandidates.length > 0 && (
          <div className="note-link-form">
            <select
              aria-label="Link to another note"
              value={linkPick}
              onChange={(e) => setLinkPick(e.target.value)}
            >
              <option value="">Link to a note…</option>
              {linkCandidates.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.title || extractExcerpt(n.body, 60) || 'Untitled note'}
                </option>
              ))}
            </select>
            <button type="button" className="button" disabled={!linkPick} onClick={() => void addLink()}>
              Link
            </button>
          </div>
        )}
      </div>

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}

      <ConfirmDialog
        open={deleting}
        title="Delete this note?"
        message={<p>This note will be removed permanently. Linked tags and notes are unaffected.</p>}
        destructive
        confirmLabel="Delete note"
        onCancel={() => setDeleting(false)}
        onConfirm={() => {
          deleteNote(libraryId, note.id)
            .then(() => {
              setDeleting(false);
              onDeleted();
            })
            .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
        }}
        testId="delete-note-dialog"
      />
    </section>
  );
}

export default function NotesPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const [params, setParams] = useSearchParams();
  const libraryId = gate.kind === 'ready' ? gate.libraryId : '';

  // The active note is held directly rather than looked up by id in
  // notes.data — that list can be filtered (by tag, or to standalone-only)
  // or still mid-reload, so a just-created or just-opened note might not be
  // in it yet/at all, and a lookup-based "active" would silently fail to
  // render the detail view.
  const [active, setActive] = useState<Note | null>(null);
  const [activeTagId, setActiveTagId] = useState<string | null>(null);
  const [standaloneOnly, setStandaloneOnly] = useState(false);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [openError, setOpenError] = useState<string | null>(null);
  const openID = params.get('open');

  const notes = useLibraryResource(
    useCallback(
      async (id: string) =>
        (await listNotes(id, { tagId: activeTagId ?? undefined, standalone: standaloneOnly })).notes ?? [],
      [activeTagId, standaloneOnly],
    ),
    [activeTagId, standaloneOnly],
  );
  const tags = useLibraryResource(
    useCallback(async (id: string) => (await listTags(id)).tags ?? [], []),
  );

  const fileIds = (notes.data ?? []).map((n) => n.file_id).filter((id): id is string => !!id);
  const labels = useFileLabels(libraryId, fileIds);

  // The top-bar search filters the grid in place (see pageSearch.ts).
  const pageQuery = usePageQuery();
  const shownNotes =
    notes.data?.filter((n) =>
      matchesQuery(pageQuery, n.title, n.body, n.file_id ? labels[n.file_id]?.title : undefined),
    ) ?? null;

  // A deep link from elsewhere (e.g. a content-search hit) opens a specific
  // note. If it's already in the loaded list this is derived directly
  // (no effect needed); only the fallback — fetching a note the current
  // list doesn't have, e.g. filtered out by the active tag — is async.
  const deepLinkNote = openID ? (notes.data ?? []).find((n) => n.id === openID) ?? null : null;
  const displayedNote = active ?? deepLinkNote;

  useEffect(() => {
    if (!openID || !libraryId || displayedNote) return;
    let cancelled = false;
    getNote(libraryId, openID)
      .then((resp) => {
        if (!cancelled) setActive(resp.note);
      })
      .catch((e: unknown) => {
        if (!cancelled) setOpenError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [openID, libraryId, displayedNote]);

  const clearOpenParam = useCallback(() => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete('open');
        return next;
      },
      { replace: true },
    );
  }, [setParams]);

  const newNote = () => {
    setCreating(true);
    setCreateError(null);
    createNote(libraryId, { title: '', body: '' })
      .then((resp) => {
        notes.reload();
        setActive(resp.note);
      })
      .catch((e: unknown) => setCreateError(e instanceof Error ? e.message : String(e)))
      .finally(() => setCreating(false));
  };

  const exportNotes = () => {
    const list = notes.data ?? [];
    downloadText('notes.md', notesToMarkdown(list, (id) => labels[id]?.title ?? ''));
  };

  const header = (
    <PageHeader
      title="Notes"
      subtitle="Long-form writing — about a book, a saved quote, or nothing in particular."
      controls={
        <>
          <button
            type="button"
            className="button"
            onClick={exportNotes}
            disabled={!notes.data || notes.data.length === 0}
            data-testid="export-notes-button"
          >
            Export
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={newNote}
            disabled={gate.kind !== 'ready' || creating}
            data-testid="new-note-button"
          >
            {creating ? 'Creating…' : 'New note'}
          </button>
        </>
      }
    />
  );

  if (gate.kind === 'loading') {
    return (
      <main className="page notes-page">
        {header}
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }
  if (gate.kind === 'error') {
    return (
      <main className="page notes-page">
        {header}
        <ErrorState message={gate.message} onRetry={notes.reload} />
      </main>
    );
  }
  if (gate.kind === 'empty') {
    return (
      <main className="page notes-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  const library = gate.library;

  if (displayedNote) {
    return (
      <main className="page notes-page">
        <NoteDetail
          key={displayedNote.id}
          libraryId={libraryId}
          note={displayedNote}
          allNotes={notes.data ?? []}
          fileTitle={displayedNote.file_id ? labels[displayedNote.file_id]?.title : undefined}
          onBack={() => {
            setActive(null);
            clearOpenParam();
          }}
          onSaved={(updated) => {
            setActive(updated);
            notes.reload();
          }}
          onDeleted={() => {
            setActive(null);
            clearOpenParam();
            notes.reload();
          }}
        />
      </main>
    );
  }

  return (
    <main className="page notes-page">
      {header}
      {library.status === 'offline' && <LibraryOfflineNotice library={library} />}
      {openError && (
        <p className="error-text" role="alert">
          Couldn't open that note: {openError}
        </p>
      )}
      {createError && (
        <p className="error-text" role="alert">
          {createError}
        </p>
      )}

      <div className="note-filter-bar">
        <button
          type="button"
          className={!activeTagId && !standaloneOnly ? 'chip-filter active' : 'chip-filter'}
          onClick={() => {
            setActiveTagId(null);
            setStandaloneOnly(false);
          }}
        >
          All
        </button>
        <button
          type="button"
          className={standaloneOnly ? 'chip-filter active' : 'chip-filter'}
          onClick={() => {
            setStandaloneOnly(true);
            setActiveTagId(null);
          }}
        >
          Standalone
        </button>
        {(tags.data ?? []).map((t) => (
          <button
            key={t.id}
            type="button"
            className={activeTagId === t.id ? 'chip-filter active' : 'chip-filter'}
            onClick={() => {
              setActiveTagId(t.id);
              setStandaloneOnly(false);
            }}
          >
            {t.name}
          </button>
        ))}
      </div>

      {notes.loading && <LoadingState />}
      {notes.error && <ErrorState message={notes.error} onRetry={notes.reload} />}
      {notes.data !== null && notes.data.length === 0 && (
        <EmptyState title="No notes yet" testId="notes-empty" icon="notebook">
          <p>Write about a book, a quote, or nothing in particular — notes can stand on their own.</p>
        </EmptyState>
      )}

      {shownNotes !== null && notes.data!.length > 0 && shownNotes.length === 0 && (
        <p className="muted">No notes match “{pageQuery}”.</p>
      )}

      {shownNotes !== null && shownNotes.length > 0 && (
        <ul className="note-grid" data-testid="notes-list" aria-label="Notes">
          {shownNotes.map((n) => (
            <li key={n.id}>
              <button
                type="button"
                className="note-card"
                onClick={() => setActive(n)}
                data-testid={`open-note-${n.id}`}
              >
                <span className="note-card-title">{n.title || 'Untitled note'}</span>
                <span className="note-card-excerpt">{extractExcerpt(n.body, 120) || 'Empty note.'}</span>
                <span className="note-card-meta">
                  {n.file_id && <span className="muted">{labels[n.file_id]?.title ?? '…'} · </span>}
                  <span className="muted">{formatDate(n.updated_at)}</span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
