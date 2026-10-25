import { describe, expect, it } from 'vitest';

import { WORD_MARK, findTextRange, highlightName } from './highlightPaint';

function dom(html: string): HTMLElement {
  const root = document.createElement('div');
  root.innerHTML = html;
  document.body.appendChild(root);
  return root;
}

describe('findTextRange', () => {
  it('finds text split across elements, whatever the whitespace', () => {
    // Like a PDF text layer: one span per run, line breaks between them.
    const root = dom('<span>The quick</span><br><span>brown fox</span><span> jumps</span>');
    const range = findTextRange(root, 'quick\nbrown   fox');
    expect(range?.toString()).toBe('quickbrown fox');
  });

  it('searches from the anchor first, falling back to the whole text', () => {
    const root = dom('<p id="a">echo</p><p id="b">echo again</p>');
    const fromB = findTextRange(root, 'echo', root.querySelector('#b'));
    expect(fromB?.startContainer.parentElement?.id).toBe('b');

    const fallback = findTextRange(root, 'echo', root.querySelector('#b')?.nextSibling ?? null);
    expect(fallback).not.toBeNull();
  });

  it('returns null for text that is not there', () => {
    expect(findTextRange(dom('<p>hello</p>'), 'goodbye')).toBeNull();
    expect(findTextRange(dom('<p>hello</p>'), '   ')).toBeNull();
  });
});

describe('highlightName', () => {
  it('names known colors and falls back for unknown ones', () => {
    expect(highlightName('green')).toBe('orchard-hl-green');
    expect(highlightName('#123456')).toBe('orchard-hl-yellow');
  });

  it('gives saved dictionary words their own gray mark', () => {
    expect(highlightName(WORD_MARK)).toBe('orchard-hl-word');
  });
});
