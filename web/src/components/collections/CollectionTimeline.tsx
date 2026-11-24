/**
 * A collection's reading timeline: its books in the order the user means to
 * read them, drawn as a line of numbered stops from the first book to the
 * last. Finished books show a tick, the first unfinished one is "Up next",
 * and a book being read shows how far along it is.
 *
 * Books are put in order by dragging them by the handle on each card (the
 * card lifts, the others slide out of its way — see useSortableList), or
 * with the up/down buttons for a keyboard. Books of the collection not yet
 * on the timeline wait in a tray below, to be dragged into place or added
 * at the end. Every change is saved at once;
 * one that can't be saved is put back.
 */

import { ArrowDown, ArrowUp, BookOpen, Check, GripVertical, ListPlus, Plus, X } from 'lucide-react';
import { useCallback, useMemo, useRef, useState } from 'react';

import type { FileSummary } from '../../api/types';
import { hasThumbnail, thumbnailUrl } from '../media';
import './CollectionTimeline.css';
import { useSortableList } from './useSortableList';

const DRAG_TYPE = 'application/orchard-timeline';

type SaveState = 'idle' | 'saving' | 'saved' | 'error';

export function CollectionTimeline({
  libraryId,
  files,
  timeline,
  completed,
  onSave,
  onOpen,
}: {
  libraryId: string;
  /** Every book in the collection. */
  files: FileSummary[];
  /** The saved reading order (file ids). */
  timeline: string[];
  /** Whether a book is finished, with marks made since the listing loaded. */
  completed: (file: FileSummary) => boolean;
  /** Saves a new order; resolves to the order the server kept. */
  onSave: (ids: string[]) => Promise<string[]>;
  onOpen: (file: FileSummary) => void;
}) {
  const byId = useMemo(() => new Map(files.map((f) => [f.id, f])), [files]);
  // The order on screen. It starts from the saved one and from then on is
  // the source of truth, so a reload behind it never undoes a drag.
  const [order, setOrder] = useState<string[]>(() => timeline.filter((id) => byId.has(id)));
  const [saveState, setSaveState] = useState<SaveState>('idle');
  const [dragId, setDragId] = useState<string | null>(null);
  /** Where a dragged book would land: an index into the timeline. */
  const [dropAt, setDropAt] = useState<number | null>(null);
  const saveSeq = useRef(0);

  const onTimeline = order.filter((id) => byId.has(id)).map((id) => byId.get(id)!);
  const placed = new Set(order);
  const waiting = files.filter((f) => !placed.has(f.id));
  const finished = onTimeline.filter(completed).length;
  const upNext = onTimeline.find((f) => !completed(f))?.id;

  const commit = useCallback(
    (next: string[]) => {
      const before = order;
      setOrder(next);
      setSaveState('saving');
      const seq = ++saveSeq.current;
      onSave(next)
        .then(() => {
          if (seq === saveSeq.current) setSaveState('saved');
        })
        .catch(() => {
          // Only the latest change is put back; an older failure is moot.
          if (seq !== saveSeq.current) return;
          setOrder(before);
          setSaveState('error');
        });
    },
    [order, onSave],
  );

  // What the list shows, in order: the saved order without books that
  // have since left the collection.
  const shownIds = onTimeline.map((f) => f.id);
  const sortable = useSortableList({
    ids: shownIds,
    onReorder: (from, to) => {
      const next = shownIds.slice();
      const [id] = next.splice(from, 1);
      next.splice(to, 0, id!);
      commit(next);
    },
  });

  /** A change not made by dragging a card: the cards glide to their places. */
  const change = (next: string[]) => {
    sortable.capture();
    commit(next);
  };

  const move = (index: number, by: -1 | 1) => {
    const target = index + by;
    if (target < 0 || target >= shownIds.length) return;
    const next = shownIds.slice();
    [next[index], next[target]] = [next[target]!, next[index]!];
    change(next);
  };

  /** A book from the tray, dropped into the line at `at`. */
  const insertAt = (id: string, at: number) => {
    const next = shownIds.filter((x) => x !== id);
    next.splice(Math.max(0, Math.min(at, next.length)), 0, id);
    if (next.join() !== shownIds.join()) change(next);
  };

  const remove = (id: string) => change(shownIds.filter((x) => x !== id));
  const append = (id: string) => change([...shownIds, id]);
  const appendAll = () => change([...shownIds, ...waiting.map((f) => f.id)]);

  const endDrag = () => {
    setDragId(null);
    setDropAt(null);
  };

  const startDrag = (event: React.DragEvent, id: string) => {
    event.dataTransfer.setData(DRAG_TYPE, id);
    event.dataTransfer.effectAllowed = 'move';
    setDragId(id);
  };

  const isOurDrag = (event: React.DragEvent) => event.dataTransfer.types.includes(DRAG_TYPE);

  /** Over a card: the drop goes above it or below it, by which half. */
  const overCard = (event: React.DragEvent, index: number) => {
    if (!isOurDrag(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    const box = event.currentTarget.getBoundingClientRect();
    setDropAt(event.clientY < box.top + box.height / 2 ? index : index + 1);
  };

  const drop = (event: React.DragEvent) => {
    if (!isOurDrag(event)) return;
    event.preventDefault();
    const id = event.dataTransfer.getData(DRAG_TYPE);
    if (id && byId.has(id)) insertAt(id, dropAt ?? order.length);
    endDrag();
  };

  const percentDone = onTimeline.length ? Math.round((finished / onTimeline.length) * 100) : 0;

  return (
    <section
      className="timeline"
      aria-labelledby="timeline-title"
      data-testid="collection-timeline"
    >
      <header className="timeline-head">
        <div>
          <h3 id="timeline-title" className="timeline-title">
            Reading timeline
          </h3>
          <p className="timeline-sub">
            {onTimeline.length === 0
              ? 'Line up the books in the order you want to read them.'
              : `${finished} of ${onTimeline.length} read`}
          </p>
        </div>
        <span className={`timeline-save is-${saveState}`} role="status" aria-live="polite">
          {saveState === 'saving' && 'Saving…'}
          {saveState === 'saved' && 'Saved'}
          {saveState === 'error' && "Couldn't save — put back"}
        </span>
      </header>

      {onTimeline.length > 0 && (
        <div
          className="timeline-meter"
          role="progressbar"
          aria-label="Read so far"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percentDone}
        >
          <span style={{ width: `${percentDone}%` }} />
        </div>
      )}

      <ol
        className={sortable.lifted ? 'timeline-list is-sorting' : 'timeline-list'}
        aria-label="Reading order"
        onDragOver={(event) => {
          if (!isOurDrag(event)) return;
          event.preventDefault();
          // Over the list's own padding, not a card: the end of the line.
          if (event.target === event.currentTarget) setDropAt(order.length);
        }}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropAt(null);
        }}
        onDrop={drop}
      >
        {onTimeline.length === 0 && (
          <li className={dropAt !== null ? 'timeline-empty is-target' : 'timeline-empty'}>
            <ListPlus aria-hidden="true" />
            <span>
              {files.length === 0
                ? 'Add books to this collection first, then put them in order here.'
                : 'Drag books here from below, or press + to add one to the end.'}
            </span>
          </li>
        )}
        {onTimeline.map((file, index) => {
          const done = completed(file);
          const current = file.id === upNext;
          const percent = file.progress_percent ?? 0;
          const name = file.title || file.name.replace(/\.[^.]+$/, '');
          const classes = ['timeline-item'];
          if (done) classes.push('is-done');
          if (current) classes.push('is-current');
          if (file.id === sortable.lifted) classes.push('is-lifted');
          if (dropAt === index) classes.push('drop-before');
          if (dropAt === index + 1 && index === onTimeline.length - 1) classes.push('drop-after');
          return (
            <li
              key={file.id}
              ref={sortable.itemRef(file.id)}
              className={classes.join(' ')}
              // Books dragged in from the tray below land above or below this one.
              onDragOver={(event) => overCard(event, index)}
              data-testid="timeline-item"
            >
              <span className="timeline-node" aria-hidden="true">
                {done ? <Check size={14} strokeWidth={3} /> : index + 1}
              </span>
              <div className="timeline-card">
                <span
                  className="timeline-grip"
                  aria-hidden="true"
                  title="Drag to reorder"
                  onPointerDown={(event) => sortable.startDrag(event, file.id)}
                >
                  <GripVertical size={16} />
                </span>
                <button
                  type="button"
                  className="timeline-open"
                  onClick={() => onOpen(file)}
                  aria-label={`${index + 1}. ${name}${done ? ', read' : current ? ', up next' : ''}. Open`}
                >
                  <Cover libraryId={libraryId} file={file} />
                  <span className="timeline-text">
                    <span className="timeline-name">{name}</span>
                    <span className="timeline-state">
                      {done ? (
                        'Read'
                      ) : current ? (
                        <span className="timeline-badge">
                          {percent > 0 ? `Reading · ${Math.round(percent)}%` : 'Up next'}
                        </span>
                      ) : percent > 0 ? (
                        `${Math.round(percent)}% read`
                      ) : (
                        'Not started'
                      )}
                    </span>
                    {!done && percent > 0 && (
                      <span className="timeline-progress" aria-hidden="true">
                        <span style={{ width: `${Math.min(100, percent)}%` }} />
                      </span>
                    )}
                  </span>
                </button>
                <span className="timeline-actions">
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`Move ${name} earlier`}
                    disabled={index === 0}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowUp size={16} />
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`Move ${name} later`}
                    disabled={index === onTimeline.length - 1}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowDown size={16} />
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`Take ${name} off the timeline`}
                    onClick={() => remove(file.id)}
                  >
                    <X size={16} />
                  </button>
                </span>
              </div>
            </li>
          );
        })}
      </ol>

      {waiting.length > 0 && (
        <div className="timeline-pool-wrap">
          <div className="timeline-pool-head">
            <h4 className="timeline-pool-title">Not on the timeline · {waiting.length}</h4>
            <button type="button" className="button ghost-button" onClick={appendAll}>
              <Plus aria-hidden="true" />
              Add all
            </button>
          </div>
          <ul className="timeline-pool" aria-label="Books not on the timeline">
            {waiting.map((file) => {
              const name = file.title || file.name.replace(/\.[^.]+$/, '');
              return (
                <li
                  key={file.id}
                  className={file.id === dragId ? 'timeline-chip is-dragged' : 'timeline-chip'}
                  draggable
                  onDragStart={(event) => startDrag(event, file.id)}
                  onDragEnd={endDrag}
                  data-testid="timeline-waiting"
                >
                  <Cover libraryId={libraryId} file={file} small />
                  <span className="timeline-chip-name" title={name}>
                    {name}
                  </span>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`Add ${name} to the end of the timeline`}
                    onClick={() => append(file.id)}
                  >
                    <Plus size={16} />
                  </button>
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </section>
  );
}

function Cover({
  libraryId,
  file,
  small = false,
}: {
  libraryId: string;
  file: FileSummary;
  small?: boolean;
}) {
  const [failed, setFailed] = useState(false);
  const className = small ? 'timeline-cover is-small' : 'timeline-cover';
  if (!hasThumbnail(file) || failed) {
    return (
      <span className={`${className} is-blank`} aria-hidden="true">
        <BookOpen size={small ? 14 : 18} />
      </span>
    );
  }
  return (
    <img
      className={className}
      src={thumbnailUrl(libraryId, file)}
      alt=""
      loading="lazy"
      draggable={false}
      onError={() => setFailed(true)}
    />
  );
}
