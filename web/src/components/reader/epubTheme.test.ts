import { describe, expect, it } from 'vitest';

import { applyTheme, themeCss } from './epubTheme';

function frame() {
  const doc = document.implementation.createHTMLDocument('chapter');
  const bookCss = doc.createElement('style');
  bookCss.textContent = 'p { color: black; }';
  doc.head.appendChild(bookCss);
  return { document: doc } as unknown as Parameters<typeof applyTheme>[0];
}

describe('applyTheme', () => {
  it('swaps one theme for the next instead of piling them up', () => {
    const contents = frame();
    applyTheme(contents, 'dark');
    applyTheme(contents, 'sepia');
    applyTheme(contents, 'light');

    const styles = contents.document.querySelectorAll('#orchard-reader-theme');
    expect(styles).toHaveLength(1);
    const css = styles[0]!.textContent ?? '';
    // Nothing of dark's near-white text is left behind on light's white page.
    expect(css).not.toContain('#e4e4e4');
    expect(css).not.toContain('!important');
    expect(css).toBe(themeCss('light'));
  });

  it('comes after the book’s own stylesheets', () => {
    const contents = frame();
    applyTheme(contents, 'dark');
    const late = contents.document.createElement('style');
    contents.document.head.appendChild(late);
    applyTheme(contents, 'dark');
    expect(contents.document.head.lastElementChild?.id).toBe('orchard-reader-theme');
  });

  it('gives dark its own colour scheme and readable text over the book’s', () => {
    const css = themeCss('dark');
    expect(css).toContain('color-scheme: dark');
    expect(css).toMatch(/body \* \{[^}]*color: #e4e4e4 !important/);
  });
});
