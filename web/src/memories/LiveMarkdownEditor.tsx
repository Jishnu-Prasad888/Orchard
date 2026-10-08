/**
 * LiveMarkdownEditor — Obsidian-style live Markdown editing for one text block.
 *
 * The editable element's text is always exactly the Markdown source. After
 * every input the source is re-highlighted in place ({@link highlightMarkdown})
 * and the caret restored, so headings grow, bold turns bold and quotes indent
 * while you type, without a separate preview pane. Markdown markers (`##`,
 * `**`, `[](url)`) live in `.md-mark` spans that the stylesheet hides while
 * the block is not focused — an unfocused block reads as formatted text, and
 * clicking into it places the caret natively, exactly where you clicked.
 *
 * Because the DOM is rewritten on input, the browser's own undo stack is
 * useless; the editor keeps its own (Ctrl/Cmd+Z, Ctrl/Cmd+Shift+Z or Ctrl+Y).
 * Enter is handled here so lists, tasks and quotes continue, and paste always
 * inserts plain text, so no foreign HTML ever enters the document.
 */

import {
  type ClipboardEvent,
  type FormEvent,
  type KeyboardEvent,
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';

import { highlightMarkdown } from '../lib/markdown';

export interface LiveMarkdownEditorHandle {
  focus: (where?: 'start' | 'end') => void;
  /** Inserts text at the caret (or the end when the editor has no caret). */
  insertText: (text: string) => void;
}

interface Props {
  value: string;
  onChange: (value: string) => void;
  /** Show Markdown markers even when the block is not focused. */
  showSource?: boolean;
  placeholder?: string;
  ariaLabel: string;
  onFocus?: () => void;
  onBlur?: () => void;
  /** Arrow up on the first line / down on the last line. */
  onNavigate?: (direction: 'up' | 'down') => void;
  /** Backspace in an empty block. */
  onBackspaceEmpty?: () => void;
  /** Keys the editor does not handle itself, for block-level shortcuts. */
  onKeyDownExtra?: (event: KeyboardEvent<HTMLDivElement>) => void;
  testId?: string;
}

interface Snapshot {
  text: string;
  start: number;
  end: number;
}

const HISTORY_LIMIT = 300;
const COALESCE_MS = 900;

const plaintextOnly = (() => {
  if (typeof document === 'undefined') return false;
  try {
    const probe = document.createElement('div');
    probe.contentEditable = 'plaintext-only';
    return probe.contentEditable === 'plaintext-only';
  } catch {
    return false;
  }
})();

/** The source held in the element: its text minus the trailing sentinel. */
function readSource(el: HTMLElement): string {
  const text = el.textContent ?? '';
  return text.endsWith('\n') ? text.slice(0, -1) : text;
}

function render(el: HTMLElement, source: string) {
  // The trailing newline gives the caret a line to sit on after a final "\n".
  el.innerHTML = `${highlightMarkdown(source)}\n`;
}

function getOffsets(root: HTMLElement): { start: number; end: number } | null {
  const sel = typeof window !== 'undefined' ? window.getSelection() : null;
  if (!sel || sel.rangeCount === 0) return null;
  const range = sel.getRangeAt(0);
  if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) return null;
  const pre = document.createRange();
  pre.selectNodeContents(root);
  pre.setEnd(range.startContainer, range.startOffset);
  const start = pre.toString().length;
  pre.setEnd(range.endContainer, range.endOffset);
  const end = pre.toString().length;
  return { start, end };
}

function locate(root: HTMLElement, offset: number): { node: Node; offset: number } {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let remaining = offset;
  let node = walker.nextNode();
  let lastNode: Node | null = null;
  while (node) {
    const len = node.textContent?.length ?? 0;
    if (remaining <= len) return { node, offset: remaining };
    remaining -= len;
    lastNode = node;
    node = walker.nextNode();
  }
  if (lastNode) return { node: lastNode, offset: lastNode.textContent?.length ?? 0 };
  return { node: root, offset: 0 };
}

function setOffsets(root: HTMLElement, start: number, end = start) {
  const sel = window.getSelection();
  if (!sel) return;
  try {
    const a = locate(root, start);
    const b = locate(root, end);
    sel.setBaseAndExtent(a.node, a.offset, b.node, b.offset);
  } catch {
    // Selection APIs can be partial in test DOMs; the text is still right.
  }
}

const LIST_LINE = /^(\s*)([-+*]|(\d+)([.)]))(\s+)(\[[ xX]\]\s+)?(.*)$/;
const QUOTE_LINE = /^(\s*>\s?)(.*)$/;

export const LiveMarkdownEditor = forwardRef<LiveMarkdownEditorHandle, Props>(
  function LiveMarkdownEditor(
    {
      value,
      onChange,
      showSource = false,
      placeholder,
      ariaLabel,
      onFocus,
      onBlur,
      onNavigate,
      onBackspaceEmpty,
      onKeyDownExtra,
      testId,
    },
    ref,
  ) {
    const elRef = useRef<HTMLDivElement | null>(null);
    const rendered = useRef<string | null>(null);
    const history = useRef<{ stack: Snapshot[]; index: number; lastPush: number }>({
      stack: [{ text: value, start: value.length, end: value.length }],
      index: 0,
      lastPush: 0,
    });
    const composing = useRef(false);
    const [focused, setFocused] = useState(false);
    const [empty, setEmpty] = useState(value === '');

    // Keep the DOM in sync with external changes (load, undo, restore).
    useLayoutEffect(() => {
      const el = elRef.current;
      if (!el || rendered.current === value) return;
      const hadFocus = document.activeElement === el;
      const caret = hadFocus ? getOffsets(el) : null;
      render(el, value);
      rendered.current = value;
      setEmpty(value === '');
      if (hadFocus && caret) {
        setOffsets(el, Math.min(caret.start, value.length), Math.min(caret.end, value.length));
      }
    }, [value]);

    const commit = useCallback(
      (next: string, start: number, end: number, kind: 'type' | 'break' | 'replace') => {
        const el = elRef.current;
        if (!el) return;
        render(el, next);
        rendered.current = next;
        setEmpty(next === '');
        setOffsets(el, start, end);

        const h = history.current;
        const now = Date.now();
        const top = h.stack[h.index];
        if (kind === 'type' && top && now - h.lastPush < COALESCE_MS && h.index > 0) {
          h.stack[h.index] = { text: next, start, end };
        } else {
          h.stack = h.stack.slice(0, h.index + 1);
          h.stack.push({ text: next, start, end });
          if (h.stack.length > HISTORY_LIMIT) h.stack.shift();
          h.index = h.stack.length - 1;
        }
        h.lastPush = kind === 'type' ? now : 0;
        onChange(next);
      },
      [onChange],
    );

    /** Replaces [start, end) of the source with `text` and places the caret. */
    const replaceRange = useCallback(
      (
        start: number,
        end: number,
        text: string,
        caret?: number,
        kind: 'type' | 'break' | 'replace' = 'replace',
      ) => {
        const el = elRef.current;
        if (!el) return;
        const source = readSource(el);
        const next = source.slice(0, start) + text + source.slice(end);
        const at = caret ?? start + text.length;
        commit(next, at, at, kind);
      },
      [commit],
    );

    const restore = useCallback(
      (snap: Snapshot) => {
        const el = elRef.current;
        if (!el) return;
        render(el, snap.text);
        rendered.current = snap.text;
        setEmpty(snap.text === '');
        setOffsets(el, snap.start, snap.end);
        onChange(snap.text);
      },
      [onChange],
    );

    const undo = useCallback(() => {
      const h = history.current;
      if (h.index === 0) return;
      h.index -= 1;
      h.lastPush = 0;
      restore(h.stack[h.index]!);
    }, [restore]);

    const redo = useCallback(() => {
      const h = history.current;
      if (h.index >= h.stack.length - 1) return;
      h.index += 1;
      restore(h.stack[h.index]!);
    }, [restore]);

    const selection = useCallback((): { start: number; end: number; source: string } => {
      const el = elRef.current!;
      const source = readSource(el);
      const off = getOffsets(el) ?? { start: source.length, end: source.length };
      // A caret after the sentinel newline belongs at the end of the source.
      return {
        source,
        start: Math.min(off.start, source.length),
        end: Math.min(off.end, source.length),
      };
    }, []);

    useImperativeHandle(
      ref,
      () => ({
        focus: (where = 'end') => {
          const el = elRef.current;
          if (!el) return;
          el.focus();
          const at = where === 'start' ? 0 : readSource(el).length;
          setOffsets(el, at);
        },
        insertText: (text: string) => {
          const el = elRef.current;
          if (!el) return;
          const { start, end } = selection();
          el.focus();
          replaceRange(start, end, text);
        },
      }),
      [replaceRange, selection],
    );

    const onInput = (event: FormEvent<HTMLDivElement>) => {
      if (composing.current) return;
      const el = event.currentTarget;
      const source = readSource(el);
      if (source === rendered.current) return;
      const off = getOffsets(el);
      const at = off ? Math.min(off.start, source.length) : source.length;
      const end = off ? Math.min(off.end, source.length) : source.length;
      commit(source, at, end, 'type');
    };

    const wrap = (before: string, after = before) => {
      const { source, start, end } = selection();
      const inner = source.slice(start, end);
      if (
        inner.length > 0 &&
        source.slice(start - before.length, start) === before &&
        source.slice(end, end + after.length) === after
      ) {
        // Toggle off.
        const next =
          source.slice(0, start - before.length) + inner + source.slice(end + after.length);
        commit(next, start - before.length, end - before.length, 'replace');
        return;
      }
      const next = source.slice(0, start) + before + inner + after + source.slice(end);
      commit(next, start + before.length, end + before.length, 'replace');
    };

    const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
      if (event.nativeEvent.isComposing || composing.current) return;
      const mod = event.metaKey || event.ctrlKey;
      const key = event.key.toLowerCase();

      if (mod && key === 'z' && !event.altKey) {
        event.preventDefault();
        if (event.shiftKey) redo();
        else undo();
        return;
      }
      if (mod && key === 'y' && !event.shiftKey && !event.altKey) {
        event.preventDefault();
        redo();
        return;
      }
      if (mod && !event.altKey && !event.shiftKey && (key === 'b' || key === 'i')) {
        event.preventDefault();
        wrap(key === 'b' ? '**' : '*');
        return;
      }
      if (mod && !event.altKey && !event.shiftKey && key === 'k') {
        event.preventDefault();
        const { source, start, end } = selection();
        const label = source.slice(start, end) || 'link';
        const text = `[${label}](https://)`;
        const next = source.slice(0, start) + text + source.slice(end);
        // Select the URL so it can be typed over.
        const urlStart = start + label.length + 3;
        commit(next, urlStart, urlStart + 'https://'.length, 'replace');
        return;
      }

      if (event.key === 'Enter' && !mod && !event.altKey) {
        event.preventDefault();
        const { source, start, end } = selection();
        const lineStart = source.lastIndexOf('\n', start - 1) + 1;
        const line = source.slice(lineStart, start);
        if (!event.shiftKey) {
          const list = LIST_LINE.exec(line);
          if (list) {
            const [, indent, , num, delim, gap, task, rest] = list;
            if ((rest ?? '').trim() === '' && end === start) {
              // Enter on an empty item ends the list.
              replaceRange(lineStart, start, '', lineStart, 'break');
              return;
            }
            const marker = num ? `${Number(num) + 1}${delim}` : list[2];
            const nextTask = task ? '[ ] ' : '';
            replaceRange(start, end, `\n${indent}${marker}${gap}${nextTask}`, undefined, 'break');
            return;
          }
          const quote = QUOTE_LINE.exec(line);
          if (quote && line.trim() !== '>') {
            replaceRange(start, end, `\n${quote[1]}`, undefined, 'break');
            return;
          }
        }
        replaceRange(start, end, '\n', undefined, 'break');
        return;
      }

      if (event.key === 'Tab' && !mod && !event.altKey) {
        const { source, start } = selection();
        const lineStart = source.lastIndexOf('\n', start - 1) + 1;
        const lineEnd = source.indexOf('\n', start);
        const line = source.slice(lineStart, lineEnd < 0 ? undefined : lineEnd);
        // Tab indents list items only; elsewhere it keeps moving focus.
        if (LIST_LINE.test(line)) {
          event.preventDefault();
          if (event.shiftKey) {
            const remove = line.startsWith('  ') ? 2 : line.startsWith('\t') ? 1 : 0;
            if (remove) {
              const next =
                source.slice(0, lineStart) +
                line.slice(remove) +
                source.slice(lineStart + line.length);
              commit(
                next,
                Math.max(lineStart, start - remove),
                Math.max(lineStart, start - remove),
                'replace',
              );
            }
          } else {
            const next = `${source.slice(0, lineStart)}  ${source.slice(lineStart)}`;
            commit(next, start + 2, start + 2, 'replace');
          }
          return;
        }
      }

      if (
        event.key === 'Backspace' &&
        !mod &&
        onBackspaceEmpty &&
        readSource(event.currentTarget) === ''
      ) {
        event.preventDefault();
        onBackspaceEmpty();
        return;
      }

      if (
        (event.key === 'ArrowUp' || event.key === 'ArrowDown') &&
        onNavigate &&
        !event.shiftKey &&
        !mod &&
        !event.altKey
      ) {
        const { source, start, end } = selection();
        if (start === end) {
          const onFirst = !source.slice(0, start).includes('\n');
          const onLast = !source.slice(start).includes('\n');
          if (event.key === 'ArrowUp' && onFirst) {
            event.preventDefault();
            onNavigate('up');
            return;
          }
          if (event.key === 'ArrowDown' && onLast) {
            event.preventDefault();
            onNavigate('down');
            return;
          }
        }
      }

      onKeyDownExtra?.(event);
    };

    const onPaste = (event: ClipboardEvent<HTMLDivElement>) => {
      event.preventDefault();
      const text = event.clipboardData.getData('text/plain').replace(/\r\n?/g, '\n');
      if (!text) return;
      const { start, end } = selection();
      replaceRange(start, end, text);
    };

    // History events from the browser's Edit menu or a touch keyboard.
    useEffect(() => {
      const el = elRef.current;
      if (!el) return;
      const onBeforeInput = (e: InputEvent) => {
        if (e.inputType === 'historyUndo') {
          e.preventDefault();
          undo();
        } else if (e.inputType === 'historyRedo') {
          e.preventDefault();
          redo();
        }
      };
      el.addEventListener('beforeinput', onBeforeInput);
      return () => el.removeEventListener('beforeinput', onBeforeInput);
    }, [undo, redo]);

    return (
      <div
        ref={elRef}
        className={`md-live${showSource || focused ? ' md-live-source' : ''}${empty ? ' md-live-empty' : ''}`}
        contentEditable={plaintextOnly ? 'plaintext-only' : true}
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        aria-label={ariaLabel}
        data-placeholder={placeholder}
        spellCheck
        tabIndex={0}
        onInput={onInput}
        onKeyDown={onKeyDown}
        onPaste={onPaste}
        onDrop={(event) => {
          // Only plain text may be dropped in; never foreign markup.
          event.preventDefault();
          const text = event.dataTransfer.getData('text/plain');
          if (text) {
            const { start, end } = selection();
            replaceRange(start, end, text);
          }
        }}
        onCompositionStart={() => {
          composing.current = true;
        }}
        onCompositionEnd={(event) => {
          composing.current = false;
          onInput(event as unknown as FormEvent<HTMLDivElement>);
        }}
        onFocus={() => {
          setFocused(true);
          onFocus?.();
        }}
        onBlur={() => {
          setFocused(false);
          onBlur?.();
        }}
        data-testid={testId}
      />
    );
  },
);
