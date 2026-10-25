/**
 * The dictionary card shown over the book for "Search in dictionary".
 *
 * It floats above the page (absolutely positioned inside the reader
 * surface) rather than opening the side panel, so looking a word up never
 * reflows the book or moves the reading position. Beside the point that
 * was right-clicked when there is one; otherwise — and always on a phone —
 * as a sheet along the bottom edge.
 *
 * Lookups are cached for the session, so a word looked up twice (or a
 * synonym followed and then gone back from) answers instantly.
 */

import { useEffect, useLayoutEffect, useRef, useState } from 'react';

import { lookupWord } from '../../api/queries';
import type { DictionaryLookupResponse, DictionaryResult } from '../../api/types';
import { Icon } from '../ui/Icon';
import { meaningText } from './dictionaryText';
import type { ReaderPoint } from './types';

/** Most recent lookups, by lowercased word; oldest dropped first. */
const cache = new Map<string, DictionaryLookupResponse>();
const CACHE_SIZE = 200;

function remember(key: string, value: DictionaryLookupResponse) {
  cache.delete(key);
  cache.set(key, value);
  if (cache.size > CACHE_SIZE) cache.delete(cache.keys().next().value!);
}

/** Viewport margin kept around the card, in px. */
const EDGE = 8;
/** Below this width the card is a bottom sheet. */
const SHEET_BELOW = 600;

interface Props {
  /** What to look up, as selected. */
  word: string;
  /** Where the lookup was asked for (viewport coordinates), if anywhere. */
  point: ReaderPoint | null;
  /** Whether this word is already among the book's saved words. */
  saved: boolean;
  busy: boolean;
  onSave: (word: string, meaning: string, source: string) => void;
  onUnsave: () => void;
  onClose: () => void;
}

export function DictionaryPopover({ word, point, saved, busy, onSave, onUnsave, onClose }: Props) {
  // A followed synonym replaces the word shown; `history` goes back.
  const [history, setHistory] = useState<string[]>([word]);
  const current = history[history.length - 1]!;
  const key = current.trim().toLowerCase();
  const [fetched, setFetched] = useState<{
    key: string;
    data?: DictionaryLookupResponse;
    error?: string;
  } | null>(null);
  const [tab, setTab] = useState(0);
  const cardRef = useRef<HTMLDivElement | null>(null);

  const cached = cache.get(key);
  const data = cached ?? (fetched?.key === key ? fetched.data : undefined);
  const error = fetched?.key === key ? fetched.error : undefined;

  useEffect(() => {
    if (cache.has(key)) return;
    const controller = new AbortController();
    lookupWord(current, controller.signal)
      .then((resp) => {
        remember(key, resp);
        setFetched({ key, data: resp });
      })
      .catch((e: unknown) => {
        if (controller.signal.aborted) return;
        setFetched({ key, error: e instanceof Error ? e.message : String(e) });
      });
    return () => controller.abort();
  }, [key, current]);

  // Escape closes; so does a press anywhere outside the card. (A press in
  // an EPUB chapter lands in its frame and isn't seen here; choosing
  // another word replaces the card instead.)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    const onPointer = (e: PointerEvent) => {
      if (cardRef.current && !cardRef.current.contains(e.target as Node)) onClose();
    };
    window.addEventListener('keydown', onKey, true);
    document.addEventListener('pointerdown', onPointer, true);
    return () => {
      window.removeEventListener('keydown', onKey, true);
      document.removeEventListener('pointerdown', onPointer, true);
    };
  }, [onClose]);

  // Keyboard users land in the card; the book's selection is already
  // snapshotted, so moving focus loses nothing.
  useEffect(() => {
    cardRef.current?.focus({ preventScroll: true });
  }, []);

  // Place the card beside the point, inside the reader surface: below it
  // when there's room, otherwise above, never past an edge. Done on the
  // element itself before paint, so it never visibly jumps and placing it
  // costs no extra render.
  useLayoutEffect(() => {
    const card = cardRef.current;
    const surface = card?.offsetParent as HTMLElement | null;
    if (!card || !surface) return;
    const bounds = surface.getBoundingClientRect();
    const sheet = !point || bounds.width < SHEET_BELOW;
    card.classList.toggle('dict-popover-sheet', sheet);
    if (sheet || !point) {
      card.style.left = '';
      card.style.top = '';
      return;
    }
    const w = card.offsetWidth;
    const h = card.offsetHeight;
    const x = point.x - bounds.left;
    const y = point.y - bounds.top;
    const below = y + 16;
    const above = y - h - 16;
    card.style.left = `${Math.max(EDGE, Math.min(x - w / 2, bounds.width - w - EDGE))}px`;
    card.style.top = `${
      below + h <= bounds.height - EDGE
        ? below
        : Math.max(EDGE, Math.min(above, bounds.height - h - EDGE))
    }px`;
    // Re-placed when the content (and so the height) changes.
  }, [point, data, error, tab, current]);

  const results = data?.results ?? [];
  const result: DictionaryResult | undefined = results[Math.min(tab, results.length - 1)];
  const first = result?.entries[0];
  const pronunciation = result?.entries.find((e) => e.pronunciation)?.pronunciation;

  const follow = (next: string) => {
    setTab(0);
    setHistory((h) => [...h, next]);
  };

  return (
    <div
      ref={cardRef}
      className="dict-popover"
      role="dialog"
      aria-label={`Dictionary: ${current}`}
      tabIndex={-1}
      data-testid="dictionary-popover"
      // Pressing inside mustn't clear the book's selection.
      onMouseDown={(e) => {
        if (!(e.target as HTMLElement).closest('button, a')) e.preventDefault();
      }}
    >
      <header className="dict-head">
        {history.length > 1 && (
          <button
            type="button"
            className="icon-button"
            aria-label="Back"
            onClick={() => {
              setTab(0);
              setHistory((h) => h.slice(0, -1));
            }}
          >
            <Icon name="chevron-left" size={18} />
          </button>
        )}
        <div className="dict-title">
          <h2>{first?.headword ?? data?.word ?? current}</h2>
          {pronunciation && <span className="dict-pron">{pronunciation}</span>}
        </div>
        <button
          type="button"
          className="icon-button"
          aria-label="Close dictionary"
          onClick={onClose}
        >
          <Icon name="close" size={18} />
        </button>
      </header>

      {results.length > 1 && (
        <div className="dict-tabs" role="tablist" aria-label="Dictionaries">
          {results.map((r, i) => (
            <button
              key={r.dictionary_id}
              type="button"
              role="tab"
              aria-selected={i === tab}
              className={i === tab ? 'dict-tab active' : 'dict-tab'}
              onClick={() => setTab(i)}
            >
              {r.dictionary_name.replace(/^FreeDict /, '')}
            </button>
          ))}
        </div>
      )}

      <div className="dict-body">
        {!data && !error && (
          <div className="dict-loading" aria-label="Looking up…">
            <span className="dict-skeleton" />
            <span className="dict-skeleton dict-skeleton-short" />
          </div>
        )}
        {error && <p className="error-text">{error}</p>}
        {data && results.length === 0 && (
          <p className="muted">No entry for “{data.word}” in your dictionaries.</p>
        )}
        {result?.entries.map((entry, i) => (
          <section
            key={`${entry.headword}-${entry.part_of_speech ?? ''}-${i}`}
            className="dict-entry"
          >
            {(entry.part_of_speech || entry.headword !== first?.headword) && (
              <h3 className="dict-pos">
                {entry.headword !== first?.headword && <span>{entry.headword} </span>}
                {entry.part_of_speech}
              </h3>
            )}
            <ol className="dict-senses">
              {entry.senses.map((sense, j) => (
                <li key={j}>
                  <span className="dict-def">{sense.definition}</span>
                  {sense.notes?.map((n, k) => (
                    <span key={k} className="dict-note">
                      {n}
                    </span>
                  ))}
                  {sense.examples?.slice(0, 2).map((ex, k) => (
                    <q key={k} className="dict-example">
                      {ex}
                    </q>
                  ))}
                  {sense.synonyms && sense.synonyms.length > 0 && (
                    <span className="dict-synonyms">
                      {sense.synonyms.map((syn) => (
                        <button
                          key={syn}
                          type="button"
                          className="dict-synonym"
                          onClick={() => follow(syn)}
                        >
                          {syn}
                        </button>
                      ))}
                    </span>
                  )}
                </li>
              ))}
            </ol>
          </section>
        ))}
      </div>

      {result && (
        <footer className="dict-foot">
          <span className="dict-source">{result.dictionary_name}</span>
          {history.length === 1 &&
            (saved ? (
              <button
                type="button"
                className="button ghost-button dict-save is-saved"
                onClick={onUnsave}
                disabled={busy}
                aria-pressed="true"
              >
                <Icon name="bookmark" size={16} filled /> Saved
              </button>
            ) : (
              <button
                type="button"
                className="button primary-button dict-save"
                onClick={() =>
                  onSave(data?.word ?? current, meaningText(result), result.dictionary_name)
                }
                disabled={busy}
                data-testid="dictionary-save"
              >
                <Icon name="bookmark" size={16} /> Save word
              </button>
            ))}
        </footer>
      )}
    </div>
  );
}
