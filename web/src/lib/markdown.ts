/**
 * A small, safe Markdown renderer and live highlighter for Cairn memories.
 *
 * Markdown is the canonical content of a memory's text blocks. This module
 * renders it two ways:
 *
 *   - {@link renderMarkdown} — reading HTML for previews and the article
 *     view;
 *   - {@link highlightMarkdown} — the live editor's in-place styling, which
 *     keeps every source character (markers are wrapped, never removed) so the
 *     editable element's text is always exactly the Markdown.
 *
 * Safety: user text is HTML-escaped before any markup is added, raw HTML is
 * never passed through, and link targets are restricted to http(s), mailto,
 * fragments and relative paths. Markdown images are rendered as links rather
 * than fetched, so a memory can never make the browser load an arbitrary
 * remote URL. Supported syntax:
 *
 *   - ATX headings (# to ######), paragraphs (single newlines become <br>)
 *   - **bold**, __bold__, *italic*, _italic_, ~~strikethrough~~, `code`
 *   - links [text](url), autolinks <https://…>, internal [[type:id|label]]
 *   - unordered/ordered lists (nested by indentation) and - [ ] task lists
 *   - blockquotes, fenced code blocks, tables, thematic breaks (---)
 *
 * It is intentionally not a full CommonMark implementation; anything
 * unsupported is shown as escaped text rather than dropped.
 */

export type RefType = 'media' | 'memory' | 'album' | 'person' | 'tag';

/** A `[[type:id]]` reference found in a Markdown body. */
interface RefLink {
  type: RefType;
  id: string;
  label: string;
}

const REF_TYPES: readonly string[] = ['media', 'memory', 'album', 'person', 'tag'];

const ESCAPE: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

export function escapeHtml(input: string): string {
  return input.replace(/[&<>"']/g, (ch) => ESCAPE[ch] ?? ch);
}

/**
 * Extracts the internal references from a memory body. Used by the picker and
 * to render [[type:id]] affordances in the preview.
 */
export function extractRefs(body: string, limit = 200): RefLink[] {
  const refs: RefLink[] = [];
  const seen = new Set<string>();
  const source = body.replace(/```[\s\S]*?```/g, '');
  for (const match of source.matchAll(/\[\[([a-z]+):([^|\]\n]+)(?:\|([^\]\n]*))?\]\]/g)) {
    const type = match[1] as RefType;
    const id = match[2];
    const label = match[3] ?? '';
    if (!REF_TYPES.includes(type) || !id) {
      continue;
    }
    const key = `${type}:${id}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    refs.push({ type, id, label });
    if (refs.length >= limit) {
      break;
    }
  }
  return refs;
}

const SAFE_URL_RE = /^(?:https?:\/\/|mailto:|#|\/(?!\/)|\.\/|\.\.\/)[^\s]*$/i;

export function safeUrl(url: string): string {
  return SAFE_URL_RE.test(url) ? url : '#';
}

/* ------------------------------ inline ------------------------------ */

const PLACEHOLDER = '\u0000';

/**
 * Inline Markdown → HTML. Code spans, references and links are lifted out
 * into placeholders first so emphasis never reaches inside them; everything
 * else is escaped before emphasis markup is applied.
 */
function renderInline(raw: string, refs: RefLink[]): string {
  const saved: string[] = [];
  const keep = (html: string) => `${PLACEHOLDER}${saved.push(html) - 1}${PLACEHOLDER}`;
  let text = raw.split(PLACEHOLDER).join('');

  text = text.replace(/`([^`\n]+)`/g, (_m, code: string) =>
    keep(`<code>${escapeHtml(code)}</code>`),
  );

  text = text.replace(
    /\[\[([a-z]+):([^|\]\n]+)(?:\|([^\]\n]*))?\]\]/g,
    (full, type: string, id: string, label: string | undefined) => {
      if (!REF_TYPES.includes(type) || !id) return full;
      refs.push({ type: type as RefType, id, label: label ?? '' });
      const shown = escapeHtml(label || `${type} ${id}`);
      return keep(
        `<a class="md-ref md-ref-${type}" data-ref-type="${type}" data-ref-id="${escapeHtml(id)}">${shown}</a>`,
      );
    },
  );

  text = text.replace(
    /(!?)\[([^\]\n]*)\]\(([^)\s]+)\)/g,
    (_m, bang: string, label: string, url: string) => {
      const href = escapeHtml(safeUrl(url));
      const inner = emphasis(escapeHtml(label || url));
      const cls = bang ? ' class="md-image-link"' : '';
      return keep(`<a href="${href}"${cls} rel="noopener noreferrer">${inner}</a>`);
    },
  );

  text = text.replace(/<((?:https?:\/\/|mailto:)[^>\s]+)>/g, (_m, url: string) =>
    keep(`<a href="${escapeHtml(safeUrl(url))}" rel="noopener noreferrer">${escapeHtml(url)}</a>`),
  );

  let out = emphasis(escapeHtml(text));
  out = out.replace(
    new RegExp(`${PLACEHOLDER}(\\d+)${PLACEHOLDER}`, 'g'),
    (_m, i: string) => saved[Number(i)] ?? '',
  );
  return out;
}

/** Bold, italic and strikethrough over already-escaped text. */
function emphasis(escaped: string): string {
  return escaped
    .replace(/\*\*(?=\S)([^*]+?)\*\*/g, '<strong>$1</strong>')
    .replace(/__(?=\S)([^_]+?)__/g, '<strong>$1</strong>')
    .replace(/~~(?=\S)([^~]+?)~~/g, '<del>$1</del>')
    .replace(/(?<![\w*])\*(?=\S)([^*\n]+?)\*(?![\w*])/g, '<em>$1</em>')
    .replace(/(?<![\w_])_(?=\S)([^_\n]+?)_(?![\w_])/g, '<em>$1</em>');
}

/* ------------------------------ blocks ------------------------------ */

const LIST_RE = /^(\s*)([-+*]|\d+[.)])\s+(.*)$/;
const TASK_RE = /^\[([ xX])\]\s+(.*)$/;
const TABLE_SEP_RE = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/;
const HR_RE = /^\s*([-*_])(\s*\1){2,}\s*$/;
const HEADING_RE = /^(#{1,6})\s+(.+?)\s*#*\s*$/;
const FENCE_RE = /^\s*(```|~~~)\s*([\w+-]*)/;

function isBlockStart(line: string, next: string | undefined): boolean {
  return (
    HEADING_RE.test(line) ||
    FENCE_RE.test(line) ||
    HR_RE.test(line) ||
    LIST_RE.test(line) ||
    /^\s*>/.test(line) ||
    (line.includes('|') && next !== undefined && TABLE_SEP_RE.test(next))
  );
}

function splitRow(line: string): string[] {
  let row = line.trim();
  if (row.startsWith('|')) row = row.slice(1);
  if (row.endsWith('|') && !row.endsWith('\\|')) row = row.slice(0, -1);
  return row.split(/(?<!\\)\|/).map((c) => c.trim().replace(/\\\|/g, '|'));
}

function renderTable(lines: string[], refs: RefLink[]): string {
  const header = splitRow(lines[0]!);
  const aligns = splitRow(lines[1]!).map((c) =>
    c.startsWith(':') && c.endsWith(':')
      ? 'center'
      : c.endsWith(':')
        ? 'right'
        : c.startsWith(':')
          ? 'left'
          : '',
  );
  const cell = (tag: string, text: string, i: number) => {
    const align = aligns[i] ? ` style="text-align:${aligns[i]}"` : '';
    return `<${tag}${align}>${renderInline(text, refs)}</${tag}>`;
  };
  const head = `<thead><tr>${header.map((h, i) => cell('th', h, i)).join('')}</tr></thead>`;
  const body = lines
    .slice(2)
    .map((l) => {
      const cells = splitRow(l);
      return `<tr>${header.map((_h, i) => cell('td', cells[i] ?? '', i)).join('')}</tr>`;
    })
    .join('');
  return `<div class="md-table-wrap"><table>${head}${body ? `<tbody>${body}</tbody>` : ''}</table></div>`;
}

interface ListItem {
  indent: number;
  ordered: boolean;
  text: string;
}

function renderList(items: ListItem[], refs: RefLink[]): string {
  let html = '';
  let i = 0;
  const render = (indent: number): string => {
    const first = items[i]!;
    const tag = first.ordered ? 'ol' : 'ul';
    let out = '';
    while (i < items.length && items[i]!.indent >= indent) {
      const item = items[i]!;
      if (item.indent > indent) {
        out = out.replace(/<\/li>$/, '') + render(item.indent) + '</li>';
        continue;
      }
      i += 1;
      const task = TASK_RE.exec(item.text);
      if (task) {
        const done = task[1] !== ' ';
        out += `<li class="md-task${done ? ' md-task-done' : ''}"><input type="checkbox" disabled${done ? ' checked' : ''}> ${renderInline(task[2]!, refs)}</li>`;
      } else {
        out += `<li>${renderInline(item.text, refs)}</li>`;
      }
    }
    return `<${tag}>${out}</${tag}>`;
  };
  while (i < items.length) html += render(items[i]!.indent);
  return html;
}

/**
 * Renders Markdown body text to an HTML string that is safe to inject with
 * dangerouslySetInnerHTML. Also returns the references encountered.
 */
export function renderMarkdown(body: string): { html: string; refs: RefLink[] } {
  const refs: RefLink[] = [];
  const out: string[] = [];
  const lines = body.split(/\r?\n/);

  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;

    if (line.trim() === '') {
      i += 1;
      continue;
    }

    const fence = FENCE_RE.exec(line);
    if (fence) {
      const marker = fence[1]!;
      const lang = fence[2] ? ` class="language-${escapeHtml(fence[2])}"` : '';
      const buffer: string[] = [];
      i += 1;
      while (i < lines.length && !lines[i]!.trim().startsWith(marker)) {
        buffer.push(escapeHtml(lines[i]!));
        i += 1;
      }
      if (i < lines.length) i += 1; // closing fence
      out.push(`<pre><code${lang}>${buffer.join('\n')}</code></pre>`);
      continue;
    }

    if (HR_RE.test(line)) {
      out.push('<hr>');
      i += 1;
      continue;
    }

    const heading = HEADING_RE.exec(line);
    if (heading) {
      const level = heading[1]!.length;
      out.push(`<h${level}>${renderInline(heading[2]!, refs)}</h${level}>`);
      i += 1;
      continue;
    }

    if (line.includes('|') && i + 1 < lines.length && TABLE_SEP_RE.test(lines[i + 1]!)) {
      const rows = [line, lines[i + 1]!];
      i += 2;
      while (i < lines.length && lines[i]!.includes('|') && lines[i]!.trim() !== '') {
        rows.push(lines[i]!);
        i += 1;
      }
      out.push(renderTable(rows, refs));
      continue;
    }

    if (LIST_RE.test(line)) {
      const items: ListItem[] = [];
      while (i < lines.length) {
        const m = LIST_RE.exec(lines[i]!);
        if (!m) {
          // A lazy continuation line belongs to the previous item.
          const cont = lines[i]!;
          if (cont.trim() !== '' && /^\s+\S/.test(cont) && items.length > 0) {
            items[items.length - 1]!.text += ` ${cont.trim()}`;
            i += 1;
            continue;
          }
          break;
        }
        items.push({
          indent: m[1]!.replace(/\t/g, '    ').length,
          ordered: /\d/.test(m[2]!),
          text: m[3]!,
        });
        i += 1;
      }
      out.push(renderList(items, refs));
      continue;
    }

    if (/^\s*>/.test(line)) {
      const quoted: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i]!)) {
        quoted.push(lines[i]!.replace(/^\s*>\s?/, ''));
        i += 1;
      }
      const paragraphs = quoted
        .join('\n')
        .split(/\n\s*\n/)
        .map((p) =>
          p
            .split('\n')
            .map((l) => renderInline(l, refs))
            .join('<br>'),
        );
      out.push(
        `<blockquote>${paragraphs.length === 1 ? paragraphs[0] : paragraphs.map((p) => `<p>${p}</p>`).join('')}</blockquote>`,
      );
      continue;
    }

    const buffer: string[] = [line];
    i += 1;
    while (i < lines.length && lines[i]!.trim() !== '' && !isBlockStart(lines[i]!, lines[i + 1])) {
      buffer.push(lines[i]!);
      i += 1;
    }
    out.push(`<p>${buffer.map((l) => renderInline(l, refs)).join('<br>')}</p>`);
  }

  return { html: out.join('\n'), refs };
}

/* ---------------------------- live highlight ---------------------------- */

const mark = (s: string, cls = 'md-mark') => `<span class="${cls}">${escapeHtml(s)}</span>`;

const INLINE_TOKEN_RE =
  /(`[^`\n]+`)|(\[\[[a-z]+:[^|\]\n]+(?:\|[^\]\n]*)?\]\])|(!?\[[^\]\n]*\]\([^)\s]*\))|(\*\*(?=\S)[^*\n]+?\*\*|__(?=\S)[^_\n]+?__)|(~~(?=\S)[^~\n]+?~~)|((?<![\w*])\*(?=\S)[^*\n]+?\*(?![\w*])|(?<![\w_])_(?=\S)[^_\n]+?_(?![\w_]))/g;

/** Highlights one line's inline syntax, keeping every character. */
function highlightInline(line: string): string {
  let out = '';
  let last = 0;
  for (const m of line.matchAll(INLINE_TOKEN_RE)) {
    const at = m.index;
    out += escapeHtml(line.slice(last, at));
    const tok = m[0];
    if (m[1]) {
      out += `<span class="md-code">${mark('`')}${escapeHtml(tok.slice(1, -1))}${mark('`')}</span>`;
    } else if (m[2]) {
      const inner = tok.slice(2, -2);
      const pipe = inner.indexOf('|');
      if (pipe >= 0) {
        out += `<span class="md-ref">${mark(`[[${inner.slice(0, pipe + 1)}`)}${escapeHtml(inner.slice(pipe + 1))}${mark(']]')}</span>`;
      } else {
        const colon = inner.indexOf(':');
        out += `<span class="md-ref">${mark(`[[${inner.slice(0, colon + 1)}`)}${escapeHtml(inner.slice(colon + 1))}${mark(']]')}</span>`;
      }
    } else if (m[3]) {
      const close = tok.indexOf('](');
      const open = tok.startsWith('!') ? 2 : 1;
      out += `<span class="md-link">${mark(tok.slice(0, open))}${highlightInline(tok.slice(open, close))}${mark(tok.slice(close), 'md-mark md-url')}</span>`;
    } else if (m[4]) {
      out += `<strong>${mark(tok.slice(0, 2))}${highlightInline(tok.slice(2, -2))}${mark(tok.slice(-2))}</strong>`;
    } else if (m[5]) {
      out += `<del>${mark('~~')}${highlightInline(tok.slice(2, -2))}${mark('~~')}</del>`;
    } else {
      out += `<em>${mark(tok[0]!)}${highlightInline(tok.slice(1, -1))}${mark(tok.slice(-1))}</em>`;
    }
    last = at + tok.length;
  }
  return out + escapeHtml(line.slice(last));
}

/**
 * Live-editor styling. Returns HTML whose text content is exactly `source`:
 * one `.md-line` span per line (each holding its own trailing newline, so a
 * hidden line collapses completely) with Markdown markers wrapped in
 * `.md-mark` spans that CSS shows only while the block is focused.
 */
export function highlightMarkdown(source: string): string {
  const lines = source.split('\n');
  let fence: string | null = null;
  return lines
    .map((line, idx) => {
      const nl = idx < lines.length - 1 ? '\n' : '';
      const span = (cls: string, html: string) =>
        `<span class="md-line ${cls}">${html}${nl}</span>`;

      const fenceMatch = FENCE_RE.exec(line);
      if (fence !== null) {
        if (line.trim().startsWith(fence)) {
          fence = null;
          return span('md-fence', mark(line));
        }
        return span('md-codeline', escapeHtml(line));
      }
      if (fenceMatch) {
        fence = fenceMatch[1]!;
        return span('md-fence', mark(line));
      }
      if (line.trim() === '') return span('md-blank', '');
      if (HR_RE.test(line)) return span('md-hr', mark(line));

      const heading = /^(#{1,6})(\s+)(.*)$/.exec(line);
      if (heading) {
        return span(
          `md-h${heading[1]!.length}`,
          mark(heading[1]! + heading[2]!) + highlightInline(heading[3]!),
        );
      }
      const quote = /^(\s*>\s?)(.*)$/.exec(line);
      if (quote) return span('md-quote', mark(quote[1]!) + highlightInline(quote[2]!));

      const list = /^(\s*)([-+*]|\d+[.)])(\s+)(.*)$/.exec(line);
      if (list) {
        const [, indent, bullet, gap, rest] = list as unknown as [
          string,
          string,
          string,
          string,
          string,
        ];
        const depth = Math.min(4, Math.floor(indent.replace(/\t/g, '    ').length / 2));
        const task = /^(\[[ xX]\])(\s+)(.*)$/.exec(rest);
        const ordered = /\d/.test(bullet);
        if (task && !ordered) {
          const done = task[1] !== '[ ]';
          return span(
            `md-li md-task${done ? ' md-task-done' : ''} md-depth-${depth}`,
            mark(indent + bullet + gap + task[1]! + task[2]!, 'md-mark md-task-mark') +
              highlightInline(task[3]!),
          );
        }
        return span(
          `md-li ${ordered ? 'md-ol' : 'md-ul'} md-depth-${depth}`,
          (ordered
            ? mark(indent) + `<span class="md-num">${escapeHtml(bullet + gap)}</span>`
            : mark(indent + bullet + gap, 'md-mark md-bullet')) + highlightInline(rest),
        );
      }
      if (/^\s*\|/.test(line)) {
        const html = line
          .split('|')
          .map((cell) => highlightInline(cell))
          .join('<span class="md-pipe">|</span>');
        return span(TABLE_SEP_RE.test(line) ? 'md-table md-table-sep' : 'md-table', html);
      }
      return span('md-p', highlightInline(line));
    })
    .join('');
}

/**
 * A plain-text excerpt of a Markdown body for cards and lists: formatting,
 * links, and `[[ref:id]]` markers are stripped, whitespace is collapsed, and
 * the result is cut at a word boundary.
 */
export function extractExcerpt(body: string, maxLength = 160): string {
  const text = body
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/\[\[[a-z]+:[^\]]*\]\]/gi, ' ')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}(#{1,6}|>|[-*+]|\d+\.)\s+/gm, '')
    .replace(/[*_`~]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
  if (text.length <= maxLength) return text;
  const cut = text.slice(0, maxLength);
  const space = cut.lastIndexOf(' ');
  return `${(space > maxLength * 0.6 ? cut.slice(0, space) : cut).trimEnd()}…`;
}
