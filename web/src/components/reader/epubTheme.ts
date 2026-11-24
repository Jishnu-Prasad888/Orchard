/**
 * The EPUB reader's light/dark/sepia themes, written into each chapter's
 * frame by the reader itself rather than through epub.js's theme registry.
 */

import type { Contents } from 'epubjs';

/** The id of the one <style> each chapter's frame gets for the theme. */
const THEME_STYLE_ID = 'orchard-reader-theme';

/**
 * The reading theme as CSS for a chapter's frame.
 *
 * Light keeps a book's own colours. Dark and sepia replace them: most books
 * colour their paragraphs black in their own CSS, which beats a colour set
 * on <body> and left dark mode as black text on a dark page. Links stay
 * distinguishable. `color-scheme` matches the reader around the frame: a
 * frame whose scheme differs from its embedder's is painted on an opaque
 * backdrop by the browser.
 */
export function themeCss(theme: string): string {
  const replace = (text: string, background: string, link: string, scheme: string) => `
    :root { color-scheme: ${scheme}; }
    html, body { background: ${background} !important; }
    body { color: ${text} !important; }
    body * {
      color: ${text} !important;
      background-color: transparent !important;
      border-color: ${text} !important;
    }
    a, a * { color: ${link} !important; }`;
  switch (theme) {
    case 'dark':
      return replace('#e4e4e4', '#1a1a1a', '#9cc0ff', 'dark');
    case 'sepia':
      return replace('#3a2e1f', '#f4ecd8', '#6b4a1f', 'light');
    default:
      return ':root { color-scheme: light; } html, body { background: #ffffff; } body { color: #1a1a1a; }';
  }
}

/**
 * Puts the theme into a chapter's frame, replacing the one before.
 *
 * epub.js's own theme registry can't switch themes: selecting one adds its
 * stylesheet and never takes the previous one out, so after dark → light
 * the dark theme's `!important` near-white text stayed on light's white
 * page, which read as a blank page. One element, rewritten, can't pile up.
 */
export function applyTheme(contents: Pick<Contents, 'document'>, theme: string) {
  const doc = contents.document;
  if (!doc?.head) return;
  let el = doc.getElementById(THEME_STYLE_ID) as HTMLStyleElement | null;
  if (!el) {
    el = doc.createElement('style');
    el.id = THEME_STYLE_ID;
  }
  el.textContent = themeCss(theme);
  // Last in <head>, so it follows the book's own stylesheets.
  doc.head.appendChild(el);
}
