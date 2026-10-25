/**
 * Plain-text helpers for the reader's dictionary: which selections are
 * worth looking up, and how a meaning is written down when a word is saved.
 */

import type { DictionaryResult } from '../../api/types';

/** Senses kept per entry when a meaning is saved, and lines overall. */
const SAVED_SENSES_PER_ENTRY = 3;
const SAVED_LINES = 6;

/** The meaning as saved with a word: one line per sense, led by its part
 * of speech — readable in the panel without the dictionary installed. */
export function meaningText(result: DictionaryResult): string {
  const lines: string[] = [];
  for (const entry of result.entries) {
    for (const sense of entry.senses.slice(0, SAVED_SENSES_PER_ENTRY)) {
      const lead = [entry.headword, entry.part_of_speech].filter(Boolean).join(', ');
      lines.push(lead ? `${lead} · ${sense.definition}` : sense.definition);
      if (lines.length === SAVED_LINES) return lines.join('\n');
    }
  }
  return lines.join('\n');
}

/** Whether a selection is worth offering to look up: a word or a short
 * phrase with at least one letter, not a sentence. */
export function isLookupable(text: string): boolean {
  const t = text.trim();
  return t.length > 0 && t.length <= 64 && t.split(/\s+/).length <= 5 && /\p{L}/u.test(t);
}
