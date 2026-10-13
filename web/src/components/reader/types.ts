/**
 * The contract every reader (PDF, EPUB, Markdown/text) implements, so
 * ReaderPage's toolbar, TOC, search, and preferences panel work the same
 * way regardless of format.
 *
 * `location` is opaque outside the reader that produced it (a page number
 * for PDF, a CFI for EPUB, a scroll fraction for Markdown/text) — nothing
 * outside this module is meant to parse it, only round-trip it through
 * `goTo` and the reading-progress API.
 */

import type { ReaderPreferences } from '../../api/types';

export interface TocEntry {
  label: string;
  location: string;
  /** 0 for a top-level entry; nesting depth otherwise. */
  depth: number;
}

export interface SearchMatch {
  location: string;
  /** Plain-text context around the match, for the results list. */
  excerpt: string;
}

/** Imperative operations ReaderPage drives: TOC/search clicks and future
 * deep-linking (bookmarks, search results) all go through `goTo`. */
export interface ReaderHandle {
  goTo: (location: string) => void;
  search: (query: string) => Promise<SearchMatch[]>;
}

/** The text a reader has selected, plus an opaque location anchor for it
 * (same convention as everywhere else: a page number, a CFI, a block id).
 * Powers "add highlight" / "save as quote" from a selection. */
export interface ReaderSelection {
  text: string;
  location: string;
}

export interface ReaderProps {
  libraryId: string;
  fileId: string;
  fileName: string;
  /** Absolute URL to the file's bytes (same-origin, cookie-authenticated). */
  contentUrl: string;
  /** Resume point from the last session, or "" to start at the beginning. */
  initialLocation: string;
  preferences: ReaderPreferences;
  /** Called whenever the reading position settles (debounced upstream). */
  onProgress: (location: string, percent: number) => void;
  /** Called once the table of contents is known (empty if the format has none). */
  onToc: (toc: TocEntry[]) => void;
  /** Called if the document fails to load. */
  onError: (message: string) => void;
  /** Called whenever the user's text selection inside the reader changes;
   * null when the selection is cleared. */
  onSelectionChange: (selection: ReaderSelection | null) => void;
}
