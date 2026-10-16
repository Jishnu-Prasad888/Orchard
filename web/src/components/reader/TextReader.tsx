/**
 * Markdown and plain-text reader. Markdown renders through the app's
 * shared Markdown renderer; anything else (plain .txt, or any
 * document type without a dedicated reader) shows as preformatted text.
 *
 * Navigation is scroll-based: the table of contents is built from the
 * rendered headings, search matches are paragraph-level (a search result
 * scrolls to and briefly highlights the containing block, not an exact
 * character range — reflowing HTML makes an exact-range highlight brittle
 * for little benefit here), and reading position is the scroll fraction of
 * the content container.
 */

import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import type { CSSProperties } from 'react';

import { renderMarkdown } from '../../lib/markdown';
import { clearHighlights, findTextRange, paintHighlights } from './highlightPaint';
import type { ReaderHandle, ReaderProps, SearchMatch, TocEntry } from './types';

const SCROLL_SAVE_DEBOUNCE_MS = 1500;
const HIGHLIGHT_FLASH_MS = 1600;

function isMarkdown(fileName: string): boolean {
  return /\.(md|markdown)$/i.test(fileName);
}

/** Assigns a stable id to every heading/paragraph-like block so the TOC and
 * search results have something to scroll to. */
function indexBlocks(root: HTMLElement): { toc: TocEntry[]; blocks: Map<string, HTMLElement> } {
  const toc: TocEntry[] = [];
  const blocks = new Map<string, HTMLElement>();
  const selector = 'h1, h2, h3, h4, h5, h6, p, li, blockquote, pre';
  const elements = root.querySelectorAll<HTMLElement>(selector);
  elements.forEach((el, i) => {
    const id = `block-${i}`;
    el.id = id;
    blocks.set(id, el);
    const level = /^H([1-6])$/.exec(el.tagName)?.[1];
    if (level) {
      const text = el.textContent?.trim();
      if (text) toc.push({ label: text, location: id, depth: Number(level) - 1 });
    }
  });
  return { toc, blocks };
}

function flashHighlight(el: HTMLElement) {
  el.classList.add('reader-text-highlight');
  setTimeout(() => el.classList.remove('reader-text-highlight'), HIGHLIGHT_FLASH_MS);
}

export const TextReader = forwardRef<ReaderHandle, ReaderProps>(function TextReader(
  {
    fileName,
    contentUrl,
    initialLocation,
    preferences,
    onProgress,
    onToc,
    onError,
    onSelectionChange,
    highlights,
  },
  ref,
) {
  const [html, setHtml] = useState<string | null>(null);
  const [plainText, setPlainText] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const blocksRef = useRef<Map<string, HTMLElement>>(new Map());
  const appliedInitialLocation = useRef(false);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const markdown = isMarkdown(fileName);

  useEffect(() => {
    let cancelled = false;
    fetch(contentUrl, { credentials: 'same-origin' })
      .then((resp) => {
        if (!resp.ok) throw new Error(`Could not load the file (status ${resp.status}).`);
        return resp.text();
      })
      .then((text) => {
        if (cancelled) return;
        if (markdown) {
          setHtml(renderMarkdown(text).html);
        } else {
          setPlainText(text);
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) onError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
    // contentUrl is keyed to the file; markdown/onError are stable for its lifetime.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contentUrl]);

  // Index blocks and restore position once the content is in the DOM.
  useEffect(() => {
    if (html === null && plainText === null) return;
    const root = containerRef.current;
    if (!root) return;
    const { toc, blocks } = indexBlocks(root);
    blocksRef.current = blocks;
    onToc(toc);

    if (!appliedInitialLocation.current) {
      appliedInitialLocation.current = true;
      if (initialLocation.startsWith('block-')) {
        blocks.get(initialLocation)?.scrollIntoView?.({ block: 'start' });
        onProgress(initialLocation, 0);
      } else {
        const fraction = Number(initialLocation);
        if (Number.isFinite(fraction) && fraction > 0) {
          root.scrollTop = fraction * (root.scrollHeight - root.clientHeight);
        }
        // Report a position immediately rather than waiting for the first
        // scroll, so "add bookmark here" has a location to use right away
        // (bookmarks, unlike resume progress, can't be stored as "").
        onProgress(
          Number.isFinite(fraction) ? fraction.toFixed(4) : '0.0000',
          Math.max(0, fraction) * 100,
        );
      }
    }
    // Re-runs only when the rendered content itself changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [html, plainText]);

  // Paint saved highlights once the content is in the DOM, and again
  // whenever they change. Each is searched for from its own block onward.
  useEffect(() => {
    const root = containerRef.current;
    if (!root || (html === null && plainText === null)) return;
    const entries = [];
    for (const h of highlights) {
      const range = findTextRange(root, h.text, blocksRef.current.get(h.location));
      if (range) entries.push({ range, color: h.color });
    }
    paintHighlights('text', entries);
  }, [highlights, html, plainText]);
  useEffect(() => () => clearHighlights('text'), []);

  // Plain DOM, so native selection works; report it whenever it changes so
  // ReaderPage can offer "highlight"/"save as quote". The location anchor
  // is the id of the nearest indexed block (same ids goTo/TOC/search use).
  useEffect(() => {
    const onSelectionEvent = () => {
      const root = containerRef.current;
      const sel = window.getSelection();
      const text = sel?.toString().trim();
      if (!root || !text || !sel?.anchorNode || !root.contains(sel.anchorNode)) {
        onSelectionChange(null);
        return;
      }
      let el: HTMLElement | null =
        sel.anchorNode instanceof HTMLElement ? sel.anchorNode : sel.anchorNode.parentElement;
      while (el && !el.id.startsWith('block-')) el = el.parentElement;
      onSelectionChange({ text, location: el?.id ?? '' });
    };
    document.addEventListener('selectionchange', onSelectionEvent);
    return () => document.removeEventListener('selectionchange', onSelectionEvent);
  }, [onSelectionChange]);

  const onScroll = () => {
    const root = containerRef.current;
    if (!root) return;
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      const scrollable = root.scrollHeight - root.clientHeight;
      const fraction = scrollable > 0 ? root.scrollTop / scrollable : 0;
      onProgress(fraction.toFixed(4), Math.min(100, Math.max(0, fraction * 100)));
    }, SCROLL_SAVE_DEBOUNCE_MS);
  };

  useImperativeHandle(ref, () => ({
    goTo: (location: string) => {
      const el = blocksRef.current.get(location);
      if (el) {
        el.scrollIntoView?.({ block: 'center' });
        flashHighlight(el);
      }
    },
    search: async (query: string): Promise<SearchMatch[]> => {
      const needle = query.trim().toLowerCase();
      if (!needle) return [];
      const matches: SearchMatch[] = [];
      for (const [id, el] of blocksRef.current) {
        const text = el.textContent ?? '';
        const at = text.toLowerCase().indexOf(needle);
        if (at === -1) continue;
        const start = Math.max(0, at - 30);
        const excerpt = (start > 0 ? '…' : '') + text.slice(start, at + needle.length + 30) + '…';
        matches.push({ location: id, excerpt });
        if (matches.length >= 50) break;
      }
      return matches;
    },
  }));

  return (
    <div
      ref={containerRef}
      className="reader-text-scroll"
      onScroll={onScroll}
      style={
        {
          '--reader-font-size': `${preferences.font_size}px`,
          '--reader-line-spacing': preferences.line_spacing,
          '--reader-margin': `${preferences.margin}px`,
          '--reader-width': `${preferences.reading_width}px`,
        } as CSSProperties
      }
    >
      <div className="reader-text-page">
        {html !== null && (
          <article className="reader-text-article" dangerouslySetInnerHTML={{ __html: html }} />
        )}
        {plainText !== null && <pre className="reader-text-plain">{plainText}</pre>}
      </div>
    </div>
  );
});
