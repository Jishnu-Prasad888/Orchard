import { describe, expect, it } from 'vitest';

import { extractExcerpt, extractRefs, highlightMarkdown, renderMarkdown } from './markdown';

const REF_TYPES = ['media', 'memory', 'album', 'person', 'tag'] as const;

describe('renderMarkdown', () => {
  it('renders paragraphs and headings', () => {
    const out = renderMarkdown('# Hello\n\nWorld body.');
    expect(out.html).toContain('<h1>Hello</h1>');
    expect(out.html).toContain('<p>World body.</p>');
  });

  it('renders bold, italic, and inline code', () => {
    const out = renderMarkdown('**bold** and *italic* and `code`.');
    expect(out.html).toContain('<strong>bold</strong>');
    expect(out.html).toContain('<em>italic</em>');
    expect(out.html).toContain('<code>code</code>');
  });

  it('renders lists and blockquotes', () => {
    const out = renderMarkdown('- one\n- two\n\n> a quote');
    expect(out.html).toContain('<ul><li>one</li><li>two</li></ul>');
    expect(out.html).toContain('<blockquote>a quote</blockquote>');
  });

  it('escapes raw HTML (XSS safety)', () => {
    const out = renderMarkdown('<script>alert(1)</script>');
    expect(out.html).not.toContain('<script>');
    expect(out.html).toContain('&lt;script&gt;');
  });

  it('renders safe links and blocks javascript: URLs', () => {
    const out = renderMarkdown('[ok](https://example.com) [bad](javascript:alert(1))');
    expect(out.html).toContain('href="https://example.com"');
    expect(out.html).not.toContain('javascript:');
  });

  it('turns internal references into ref anchors', () => {
    const out = renderMarkdown('[[media:abc123|The cove]]');
    expect(out.html).toContain('class="md-ref md-ref-media"');
    expect(out.html).toContain('data-ref-type="media"');
    expect(out.html).toContain('The cove');
  });

  it('exposes parsed internal refs from the preview', () => {
    const out = renderMarkdown('See [[album:a1]] and [[person:p2|Maya]].');
    expect(out.refs).toEqual([
      { type: 'album', id: 'a1', label: '' },
      { type: 'person', id: 'p2', label: 'Maya' },
    ]);
  });

  it('ignores unknown or empty references', () => {
    const out = renderMarkdown('[[wat:ignored]] and [[media:|]] and plain [[bracket]] text.');
    expect(out.refs).toHaveLength(0);
  });

  it('renders a horizontal rule and code fence', () => {
    const out = renderMarkdown('---\n\n```go\nfmt.Println("hi")\n```');
    expect(out.html).toContain('<hr>');
    expect(out.html).toContain('<pre><code class="language-go">');
    expect(out.html).toContain('fmt.Println');
  });

  it('refuses markdown links with unbalanced or unsafe targets', () => {
    const out = renderMarkdown('[x](https://ok.example) [y](data:text/html,boom)');
    expect(out.html).toContain('href="https://ok.example"');
    expect(out.html).not.toContain('data:text/html');
  });
});

describe('extractRefs', () => {
  it('finds distinct references with optional labels', () => {
    const refs = extractRefs('A [[media:m1|One]] plus [[album:a2]] plus [[media:m1|One again]].');
    expect(refs).toEqual([
      { type: 'media', id: 'm1', label: 'One' },
      { type: 'album', id: 'a2', label: '' },
    ]);
  });

  it('ignores references inside fenced code blocks', () => {
    const refs = extractRefs('```\n[[media:fake]]\n```');
    expect(refs).toHaveLength(0);
  });

  it('supports every documented reference type', () => {
    const body = REF_TYPES.map((t) => `[[${t}:id]]`).join(' ');
    const refs = extractRefs(body);
    expect(refs.map((r) => r.type)).toEqual(REF_TYPES);
  });
});

describe('renderMarkdown (extended syntax)', () => {
  it('renders strikethrough, task lists and nested lists', () => {
    const out = renderMarkdown('~~gone~~\n\n- [ ] pack\n- [x] book\n  - train\n- done');
    expect(out.html).toContain('<del>gone</del>');
    expect(out.html).toContain('<input type="checkbox" disabled> pack');
    expect(out.html).toContain('<input type="checkbox" disabled checked> book');
    expect(out.html).toMatch(/book<ul><li>train<\/li><\/ul><\/li>/);
  });

  it('renders tables with alignment', () => {
    const out = renderMarkdown('| Day | Place |\n|:--|--:|\n| 1 | **Kochi** |');
    expect(out.html).toContain('<table>');
    expect(out.html).toContain('<th style="text-align:left">Day</th>');
    expect(out.html).toContain('<td style="text-align:right"><strong>Kochi</strong></td>');
  });

  it('keeps emphasis out of code spans and escapes their content', () => {
    const out = renderMarkdown('`**not bold** <b>`');
    expect(out.html).toContain('<code>**not bold** &lt;b&gt;</code>');
    expect(out.html).not.toContain('<strong>');
  });

  it('escapes a fenced code info string instead of injecting it', () => {
    const out = renderMarkdown('```<img src=x onerror=alert(1)>\ncode\n```');
    expect(out.html).not.toContain('<img');
    expect(out.html).toContain('code');
  });

  it('renders markdown images as safe links, never as remote <img>', () => {
    const out = renderMarkdown(
      '![tracker](https://evil.example/pixel.gif) ![x](javascript:alert(1))',
    );
    expect(out.html).not.toContain('<img');
    expect(out.html).toContain('class="md-image-link"');
    expect(out.html).not.toContain('javascript:');
  });

  it('escapes reference labels', () => {
    const out = renderMarkdown('[[media:abc|<b>hi</b>]]');
    expect(out.html).not.toContain('<b>');
  });

  it('keeps single newlines as line breaks inside a paragraph', () => {
    expect(renderMarkdown('one\ntwo').html).toBe('<p>one<br>two</p>');
  });

  it('does not treat snake_case as italic', () => {
    expect(renderMarkdown('file_name_here').html).not.toContain('<em>');
  });
});

describe('highlightMarkdown', () => {
  const textOf = (html: string) => {
    const el = document.createElement('div');
    el.innerHTML = html;
    return el.textContent;
  };

  const samples = [
    '',
    '# My Trip to Kerala\n\nWe arrived at **Kochi** early.\n\n> The weather was perfect.',
    '- [ ] pack\n- [x] book\n  1. nested\n\n---\n\n```js\nconst a = `x` < 3 && "q";\n```\n',
    '| a | b |\n|---|---|\n| [link](https://x.y) | [[media:abc|cove]] ~~s~~ *i* _j_ `c` |',
    '<script>alert(1)</script> & "quotes" \'single\'',
    'trailing newline\n',
  ];

  it.each(samples)('preserves the exact source text: %j', (src) => {
    expect(textOf(highlightMarkdown(src))).toBe(src);
  });

  it('wraps markers so CSS can hide them', () => {
    const html = highlightMarkdown('## Title with **bold**');
    expect(html).toContain('md-h2');
    expect(html).toContain('<span class="md-mark">## </span>');
    expect(html).toContain(
      '<strong><span class="md-mark">**</span>bold<span class="md-mark">**</span></strong>',
    );
  });

  it('never emits raw markup from the source', () => {
    const el = document.createElement('div');
    el.innerHTML = highlightMarkdown('<img src=x onerror=alert(1)>');
    expect(el.querySelector('img')).toBeNull();
  });

  it('marks code fence content as code, not inline syntax', () => {
    const html = highlightMarkdown('```\n**x**\n```');
    expect(html).toContain('md-codeline');
    expect(html).not.toContain('<strong>');
  });
});

describe('extractExcerpt', () => {
  it('strips formatting, links, and references', () => {
    expect(
      extractExcerpt('# Title\n\nWe saw **the pier** and [[album:a1|Rye]] [a link](http://x).'),
    ).toBe('Title We saw the pier and a link.');
  });

  it('cuts long text at a word boundary with an ellipsis', () => {
    const out = extractExcerpt('word '.repeat(100), 30);
    expect(out.endsWith('…')).toBe(true);
    expect(out.length).toBeLessThanOrEqual(31);
  });

  it('leaves short text alone', () => {
    expect(extractExcerpt('short')).toBe('short');
  });
});
