/**
 * MemoryEditor — the notebook.
 *
 * A memory is a vertical sequence of blocks on one continuous page. Between
 * any two blocks (and at the end) a quiet "+ Text  + Media" inserter appears
 * on hover or focus — always visible on touch screens. The focused block shows
 * a faint boundary, a drag handle and a ⋮ menu; unfocused blocks melt back
 * into the document, so it reads like a page rather than a stack of cards.
 *
 * Every action is reachable without right-click: the ⋮ menu, the image
 * section editor and keyboard shortcuts. Right-click (desktop) and long-press
 * (touch) open the same menus as a shortcut.
 *
 * Keyboard:
 *   ↑/↓ at the first/last line moves between blocks
 *   Alt+↑ / Alt+↓            move the focused block
 *   Enter (image section)    edit the section
 *   Delete (image section)   remove the section (undoable)
 *   Shift+F10 / Menu key     open the block menu
 *   Ctrl/Cmd+S               save now
 *   Ctrl/Cmd+Z, Shift+Z      undo/redo (typing inside a block; structure elsewhere)
 *
 * Preview hides all of this and shows the memory as an article.
 */

import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  Code2,
  Ellipsis,
  Eye,
  GripVertical,
  History,
  ImagePlus,
  Info,
  Link2,
  PenLine,
  Redo2,
  Save,
  Trash2,
  Type,
  Undo2,
} from 'lucide-react';
import {
  type DragEvent,
  Fragment,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react';

import { deleteMemory } from '../api/queries';
import { ContextMenu, type MenuEntry, type MenuPosition } from '../components/ContextMenu';
import { useLongPress } from '../components/useLongPress';
import { ConfirmDialog, PromptDialog } from '../components/Dialog';
import { RefPicker } from '../components/RefPicker';
import { ImageBlockEditor } from './ImageBlockEditor';
import { ImageBlockView } from './ImageBlockView';
import { ImageEditor } from './ImageEditor';
import { Lightbox } from './Lightbox';
import { LiveMarkdownEditor, type LiveMarkdownEditorHandle } from './LiveMarkdownEditor';
import { MediaPicker } from './MediaPicker';
import { MemoryDetails } from './MemoryDetails';
import { formatMemoryDate } from './format';
import { MemoryReader } from './MemoryReader';
import {
  type PickedMedia,
  addImages,
  blockImageCount,
  duplicateBlock,
  findImage,
  insertBlock,
  moveBlock,
  moveBlockTo,
  moveImageTo,
  newImageBlock,
  newTextBlock,
  removeBlock,
  removeImage,
  replaceImageSource,
  setMarkdown,
  updateBlock,
  updateImage,
  wordCount,
} from './model';
import type { ImageBlock, MemoryBlock, MemoryImage, MemorySettings } from './types';
import { type SaveState, useMemoryDocument } from './useMemoryDocument';
import { VersionHistory } from './VersionHistory';

type PickerTarget =
  | { kind: 'new'; index: number }
  | { kind: 'add'; blockId: string }
  | { kind: 'replace'; imageId: string };

interface MenuState {
  position: MenuPosition;
  entries: MenuEntry[];
  label: string;
}

interface Props {
  libraryId: string;
  memoryId: string;
  settings: MemorySettings;
  onDeleted: () => void;
  onBack?: () => void;
  /** Called when the title or cover changes, so the list can follow. */
  onSummaryChange?: () => void;
}

const SAVE_LABEL: Record<SaveState, string> = {
  saved: 'Saved',
  saving: 'Saving…',
  unsaved: 'Unsaved changes',
  offline: 'Offline — will retry',
  error: 'Save failed',
  conflict: 'Changed elsewhere',
};

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName);
}

function pointAt(el: HTMLElement): MenuPosition {
  const r = el.getBoundingClientRect();
  return { x: r.left, y: r.bottom + 4 };
}

export function MemoryEditor({
  libraryId,
  memoryId,
  settings,
  onDeleted,
  onBack,
  onSummaryChange,
}: Props) {
  const doc = useMemoryDocument(libraryId, memoryId, { autosave: settings.autosave });
  const { blocks, meta, setBlocks } = doc;

  const [mode, setMode] = useState<'edit' | 'preview'>(settings.default_mode);
  const [showSource, setShowSource] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [picker, setPicker] = useState<PickerTarget | null>(null);
  const [sectionEditor, setSectionEditor] = useState<{ blockId: string; imageId?: string } | null>(
    null,
  );
  const [imageEditor, setImageEditor] = useState<string | null>(null);
  const [captionFor, setCaptionFor] = useState<string | null>(null);
  const [lightbox, setLightbox] = useState<{ images: MemoryImage[]; id: string } | null>(null);
  const [details, setDetails] = useState(false);
  const [history, setHistory] = useState(false);
  const [linking, setLinking] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [toast, setToast] = useState<{ text: string; undo: boolean } | null>(null);
  const [drag, setDrag] = useState<{ id: string; over: number | null } | null>(null);
  const [announce, setAnnounce] = useState('');

  const textRefs = useRef(new Map<string, LiveMarkdownEditorHandle>());
  const blockEls = useRef(new Map<string, HTMLElement>());
  const pendingFocus = useRef<{ id: string; where: 'start' | 'end' } | null>(null);
  const lastTextId = useRef<string | null>(null);

  // Summary changes (title, cover) refresh the list.
  const summaryKey = `${meta?.title}|${meta?.cover_file_id}|${meta?.memory_date}`;
  useEffect(() => {
    if (doc.save === 'saved' && meta) onSummaryChange?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [summaryKey, doc.save === 'saved']);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 7000);
    return () => clearTimeout(t);
  }, [toast]);

  // Focus a block once it exists in the DOM.
  useEffect(() => {
    const want = pendingFocus.current;
    if (!want) return;
    const text = textRefs.current.get(want.id);
    const el = blockEls.current.get(want.id);
    if (text) text.focus(want.where);
    else if (el) el.focus();
    else return;
    pendingFocus.current = null;
  }, [blocks, mode]);

  // Ctrl/Cmd+S saves from anywhere on the page.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 's') {
        e.preventDefault();
        doc.flush();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [doc]);

  const focusBlock = useCallback((id: string, where: 'start' | 'end' = 'end') => {
    pendingFocus.current = { id, where };
    const text = textRefs.current.get(id);
    const el = blockEls.current.get(id);
    if (text) text.focus(where);
    else el?.focus();
    if (text || el) pendingFocus.current = null;
  }, []);

  /* ----------------------------- block actions ----------------------------- */

  const insertText = (index: number) => {
    const block = newTextBlock();
    setBlocks((bs) => insertBlock(bs, index, block), { undoable: true });
    pendingFocus.current = { id: block.id, where: 'start' };
  };

  const move = (id: string, delta: number) => {
    setBlocks((bs) => moveBlock(bs, id, delta), { undoable: true });
    const at = blocks.findIndex((b) => b.id === id) + delta;
    setAnnounce(
      `Moved to position ${Math.max(1, Math.min(blocks.length, at + 1))} of ${blocks.length}.`,
    );
    pendingFocus.current = { id, where: 'end' };
  };

  const duplicate = (id: string) => {
    setBlocks((bs) => duplicateBlock(bs, id), { undoable: true });
    setAnnounce('Block duplicated.');
  };

  const remove = (id: string) => {
    const at = blocks.findIndex((b) => b.id === id);
    const block = blocks[at];
    setBlocks((bs) => removeBlock(bs, id), { undoable: true });
    setToast({
      text:
        block?.type === 'image'
          ? 'Media section removed. The originals stay in your library.'
          : 'Text block removed.',
      undo: true,
    });
    const neighbour = blocks[at - 1] ?? blocks[at + 1];
    if (neighbour) pendingFocus.current = { id: neighbour.id, where: 'end' };
  };

  const onPicked = (media: PickedMedia[]) => {
    const target = picker;
    setPicker(null);
    if (!target || media.length === 0) return;
    if (target.kind === 'new') {
      const block = newImageBlock(settings.default_layout, media);
      setBlocks((bs) => insertBlock(bs, target.index, block), { undoable: true });
      pendingFocus.current = { id: block.id, where: 'end' };
    } else if (target.kind === 'add') {
      setBlocks((bs) => addImages(bs, target.blockId, media), { undoable: true });
    } else {
      setBlocks(
        (bs) => updateImage(bs, target.imageId, (img) => replaceImageSource(img, media[0]!)),
        {
          undoable: true,
        },
      );
    }
  };

  const navigate = (id: string, direction: 'up' | 'down') => {
    const at = blocks.findIndex((b) => b.id === id);
    const next = blocks[direction === 'up' ? at - 1 : at + 1];
    if (next) focusBlock(next.id, direction === 'up' ? 'end' : 'start');
  };

  /* --------------------------------- menus --------------------------------- */

  const blockMenu = (block: MemoryBlock): MenuEntry[] => {
    const at = blocks.findIndex((b) => b.id === block.id);
    const common: MenuEntry[] = [
      {
        id: 'up',
        label: 'Move up',
        hint: 'Alt+↑',
        disabled: at === 0,
        onSelect: () => move(block.id, -1),
      },
      {
        id: 'down',
        label: 'Move down',
        hint: 'Alt+↓',
        disabled: at === blocks.length - 1,
        onSelect: () => move(block.id, 1),
      },
      { id: 'duplicate', label: 'Duplicate', onSelect: () => duplicate(block.id) },
      'separator',
      { id: 'delete', label: 'Delete', danger: true, onSelect: () => remove(block.id) },
    ];
    if (block.type === 'text') return common;
    return [
      {
        id: 'edit',
        label: 'Edit media section',
        hint: 'Enter',
        onSelect: () => setSectionEditor({ blockId: block.id }),
      },
      {
        id: 'add',
        label: 'Add media',
        onSelect: () => setPicker({ kind: 'add', blockId: block.id }),
      },
      'separator',
      ...common,
    ];
  };

  const imageMenu = (image: MemoryImage): MenuEntry[] => {
    const found = findImage(blocks, image.id);
    if (!found) return [];
    const { block, index } = found;
    const editable = image.media.available && image.media.media_type !== 'video';
    return [
      {
        id: 'open',
        label: 'Open image',
        disabled: !image.media.available,
        onSelect: () => setLightbox({ images: block.images, id: image.id }),
      },
      {
        id: 'edit-image',
        label: 'Edit image',
        disabled: !editable,
        onSelect: () => setImageEditor(image.id),
      },
      {
        id: 'caption',
        label: image.caption ? 'Change caption' : 'Add caption',
        onSelect: () => setCaptionFor(image.id),
      },
      {
        id: 'replace',
        label: 'Replace media',
        onSelect: () => setPicker({ kind: 'replace', imageId: image.id }),
      },
      {
        id: 'cover',
        label: 'Use as cover',
        disabled: !editable || meta?.cover_file_id === image.file_id,
        onSelect: () => doc.setMeta({ cover_file_id: image.file_id }),
      },
      'separator',
      {
        id: 'add',
        label: 'Add media',
        onSelect: () => setPicker({ kind: 'add', blockId: block.id }),
      },
      {
        id: 'left',
        label: 'Move left',
        disabled: index === 0,
        onSelect: () =>
          setBlocks(
            (bs) =>
              updateBlock<ImageBlock>(bs, block.id, (b) => ({
                ...b,
                images: moveImageTo(b.images, image.id, index - 1),
              })),
            {
              undoable: true,
            },
          ),
      },
      {
        id: 'right',
        label: 'Move right',
        disabled: index === block.images.length - 1,
        onSelect: () =>
          setBlocks(
            (bs) =>
              updateBlock<ImageBlock>(bs, block.id, (b) => ({
                ...b,
                images: moveImageTo(b.images, image.id, index + 1),
              })),
            {
              undoable: true,
            },
          ),
      },
      {
        id: 'section',
        label: 'Edit media section',
        onSelect: () => setSectionEditor({ blockId: block.id }),
      },
      'separator',
      {
        id: 'remove',
        label: 'Remove from memory',
        danger: true,
        hint: 'Keeps the original',
        onSelect: () => {
          setBlocks((bs) => removeImage(bs, image.id), { undoable: true });
          setToast({
            text: 'Removed from this memory. The original stays in your library.',
            undo: true,
          });
        },
      },
    ];
  };

  const openImageMenu = (image: MemoryImage, e: MouseEvent<HTMLElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setMenu({
      position: { x: e.clientX, y: e.clientY },
      entries: imageMenu(image),
      label: 'Photo actions',
    });
  };

  /* -------------------------------- keyboard -------------------------------- */

  const onNotebookKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const mod = e.metaKey || e.ctrlKey;
    if (mod && !e.altKey && e.key.toLowerCase() === 'z' && !isTypingTarget(e.target)) {
      e.preventDefault();
      if (e.shiftKey) doc.redo();
      else doc.undo();
      return;
    }
    if (mod && !e.altKey && e.key.toLowerCase() === 'y' && !isTypingTarget(e.target)) {
      e.preventDefault();
      doc.redo();
      return;
    }
    if (e.altKey && !mod && (e.key === 'ArrowUp' || e.key === 'ArrowDown') && activeId) {
      e.preventDefault();
      move(activeId, e.key === 'ArrowUp' ? -1 : 1);
    }
  };

  /* --------------------------------- render --------------------------------- */

  if (doc.loading && !meta) {
    return <p className="muted editor-loading">Loading memory…</p>;
  }
  if (doc.loadError || !meta) {
    return (
      <div className="editor-error" role="alert">
        <p>{doc.loadError ?? 'This memory could not be loaded.'}</p>
        <button type="button" className="button" onClick={doc.reload}>
          Try again
        </button>
      </div>
    );
  }

  const editingBlock = sectionEditor
    ? (blocks.find((b) => b.id === sectionEditor.blockId) as ImageBlock | undefined)
    : undefined;
  const editingImage = imageEditor ? (findImage(blocks, imageEditor)?.image ?? null) : null;
  const captionImage = captionFor ? (findImage(blocks, captionFor)?.image ?? null) : null;
  const titleInvalid = meta.title.trim() === '';

  const inserter = (index: number, last: boolean) => (
    <BlockInserter
      key={`ins-${index}`}
      last={last}
      empty={blocks.length === 0}
      onText={() => insertText(index)}
      onImage={() => setPicker({ kind: 'new', index })}
    />
  );

  return (
    <div className={`memory-editor mode-${mode}`} data-testid="memory-editor">
      <div className="editor-toolbar" role="toolbar" aria-label="Memory">
        {onBack && (
          <button
            type="button"
            className="button editor-back"
            onClick={onBack}
            aria-label="Back to memories"
          >
            <ArrowLeft size={18} aria-hidden="true" />
            Memories
          </button>
        )}
        <div className="segmented mode-toggle" role="group" aria-label="Mode">
          <button
            type="button"
            className={mode === 'edit' ? 'segment active' : 'segment'}
            aria-pressed={mode === 'edit'}
            onClick={() => setMode('edit')}
          >
            <PenLine size={15} aria-hidden="true" />
            Edit
          </button>
          <button
            type="button"
            className={mode === 'preview' ? 'segment active' : 'segment'}
            aria-pressed={mode === 'preview'}
            onClick={() => setMode('preview')}
          >
            <Eye size={15} aria-hidden="true" />
            Preview
          </button>
        </div>
        {mode === 'edit' && (
          <>
            <button
              type="button"
              className={showSource ? 'button toolbar-toggle active' : 'button toolbar-toggle'}
              aria-pressed={showSource}
              title="Show Markdown syntax in every block, not only the one you are writing in"
              onClick={() => setShowSource((v) => !v)}
            >
              <Code2 size={15} aria-hidden="true" />
              Markdown
            </button>
            <button
              type="button"
              className={linking ? 'button toolbar-toggle active' : 'button toolbar-toggle'}
              aria-pressed={linking}
              onClick={() => setLinking((v) => !v)}
            >
              <Link2 size={15} aria-hidden="true" />
              Link…
            </button>
          </>
        )}
        <button type="button" className="button" onClick={() => setDetails(true)}>
          <Info size={15} aria-hidden="true" />
          Details
        </button>
        <button type="button" className="button" onClick={() => setHistory(true)}>
          <History size={15} aria-hidden="true" />
          History
        </button>
        <button
          type="button"
          className="button"
          aria-label="Undo structural change"
          title="Undo (Ctrl/Cmd+Z)"
          disabled={!doc.canUndo}
          onClick={doc.undo}
        >
          <Undo2 size={16} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="button"
          aria-label="Redo structural change"
          title="Redo (Ctrl/Cmd+Shift+Z)"
          disabled={!doc.canRedo}
          onClick={doc.redo}
        >
          <Redo2 size={16} aria-hidden="true" />
        </button>
        <span className="toolbar-spacer" />
        <span
          className={`save-status save-${doc.save}`}
          role="status"
          aria-live="polite"
          data-testid="save-status"
        >
          {SAVE_LABEL[doc.save]}
        </span>
        {(!settings.autosave || doc.save === 'error') && (
          <button
            type="button"
            className="button primary-button"
            onClick={doc.flush}
            disabled={doc.save === 'saved' || doc.save === 'saving'}
          >
            <Save size={15} aria-hidden="true" />
            {doc.save === 'error' ? 'Retry' : 'Save'}
          </button>
        )}
        <button
          type="button"
          className="button danger-button"
          onClick={() => setConfirmDelete(true)}
          data-testid="delete-memory"
        >
          <Trash2 size={15} aria-hidden="true" />
          Delete
        </button>
      </div>

      {linking && mode === 'edit' && (
        <div className="editor-linkbar">
          <RefPicker
            libraryId={libraryId}
            onInsert={(ref) => {
              const id = lastTextId.current;
              const handle = id ? textRefs.current.get(id) : undefined;
              if (handle) handle.insertText(ref);
              else {
                const block = newTextBlock(ref);
                setBlocks((bs) => [...bs, block], { undoable: true });
              }
            }}
          />
        </div>
      )}

      {doc.save === 'conflict' && (
        <div className="editor-banner warn" role="alert">
          <p>
            <strong>This memory was changed somewhere else</strong> (another tab or device) since
            you opened it. Your edits are safe on this device.
          </p>
          <div className="banner-actions">
            <button type="button" className="button" onClick={() => doc.resolveConflict('theirs')}>
              Load the other version
            </button>
            <button
              type="button"
              className="button primary-button"
              onClick={() => doc.resolveConflict('mine')}
            >
              Keep mine
            </button>
          </div>
        </div>
      )}
      {doc.recovery && (
        <div className="editor-banner" role="alert">
          <p>
            Unsaved changes from {new Date(doc.recovery.saved_at).toLocaleString()} were found on
            this device, but the memory has been saved elsewhere since.
          </p>
          <div className="banner-actions">
            <button type="button" className="button" onClick={doc.discardRecovery}>
              Discard them
            </button>
            <button type="button" className="button primary-button" onClick={doc.applyRecovery}>
              Restore my changes
            </button>
          </div>
        </div>
      )}
      {doc.restoredDraft && doc.save !== 'saved' && (
        <p className="editor-note" role="status">
          Restored changes that had not been saved yet.
        </p>
      )}
      {doc.save === 'error' && doc.saveError && (
        <p className="error-text" role="alert">
          {doc.saveError}
        </p>
      )}
      {doc.warnings.map((w) => (
        <p key={w} className="editor-note warn" role="status">
          {w}
        </p>
      ))}

      {mode === 'preview' ? (
        <MemoryReader
          meta={meta}
          blocks={blocks}
          defaultInterval={settings.slideshow_interval}
          onImageContextMenu={openImageMenu}
        />
      ) : (
        <div className="notebook" onKeyDown={onNotebookKeyDown}>
          <header className="notebook-header">
            <label className="visually-hidden" htmlFor="memory-title">
              Memory title
            </label>
            <input
              id="memory-title"
              className="memory-title"
              value={meta.title}
              placeholder="Untitled memory"
              maxLength={500}
              aria-invalid={titleInvalid}
              aria-describedby={titleInvalid ? 'memory-title-error' : undefined}
              onChange={(e) => doc.setMeta({ title: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || (e.key === 'ArrowDown' && blocks[0])) {
                  e.preventDefault();
                  if (blocks[0]) focusBlock(blocks[0].id, 'start');
                  else insertText(0);
                }
              }}
            />
            {titleInvalid && (
              <p id="memory-title-error" className="field-error">
                A memory needs a title — it is saved once you add one.
              </p>
            )}
            <button type="button" className="notebook-dateline" onClick={() => setDetails(true)}>
              {formatMemoryDate(meta.memory_date) || 'Add a date'}
              {meta.location ? ` · ${meta.location}` : ''}
            </button>
          </header>

          {inserter(0, blocks.length === 0)}
          {blocks.map((block, i) => (
            <Fragment key={block.id}>
              <BlockShell
                block={block}
                index={i}
                count={blocks.length}
                active={activeId === block.id}
                dragOver={drag?.over ?? null}
                registerEl={(el) => {
                  if (el) blockEls.current.set(block.id, el);
                  else blockEls.current.delete(block.id);
                }}
                onActivate={() => {
                  setActiveId(block.id);
                  if (block.type === 'text') lastTextId.current = block.id;
                }}
                onMenu={(position) =>
                  setMenu({
                    position,
                    entries: blockMenu(block),
                    label: block.type === 'text' ? 'Text block actions' : 'Media section actions',
                  })
                }
                onImageLongPress={(imageId, position) => {
                  const img = findImage(blocks, imageId)?.image;
                  if (img) setMenu({ position, entries: imageMenu(img), label: 'Photo actions' });
                }}
                onDragStart={() => setDrag({ id: block.id, over: null })}
                onDragOverAt={(over) => setDrag((d) => (d ? { ...d, over } : d))}
                onDrop={() => {
                  if (drag && drag.over !== null) {
                    const from = blocks.findIndex((b) => b.id === drag.id);
                    const to = drag.over > from ? drag.over - 1 : drag.over;
                    setBlocks((bs) => moveBlockTo(bs, drag.id, to), { undoable: true });
                    setAnnounce(`Moved to position ${to + 1} of ${blocks.length}.`);
                  }
                  setDrag(null);
                }}
                onDragEnd={() => setDrag(null)}
                onMove={(delta) => move(block.id, delta)}
                onKeyAction={(action) => {
                  if (action === 'edit') setSectionEditor({ blockId: block.id });
                  else if (action === 'delete') remove(block.id);
                  else if (action === 'up' || action === 'down') navigate(block.id, action);
                }}
              >
                {block.type === 'text' ? (
                  <LiveMarkdownEditor
                    ref={(h) => {
                      if (h) textRefs.current.set(block.id, h);
                      else textRefs.current.delete(block.id);
                    }}
                    value={block.markdown}
                    showSource={showSource}
                    ariaLabel={`Text block ${i + 1} of ${blocks.length}`}
                    placeholder={
                      i === 0
                        ? 'Start writing… Markdown works: # heading, **bold**, > quote, - list'
                        : 'Write…'
                    }
                    onChange={(md) => setBlocks((bs) => setMarkdown(bs, block.id, md))}
                    onNavigate={(dir) => navigate(block.id, dir)}
                    {...(blocks.length > 1 && { onBackspaceEmpty: () => remove(block.id) })}
                    testId="text-block-editor"
                  />
                ) : block.images.length === 0 ? (
                  <div className="image-block-empty">
                    <p className="muted">This section has no media yet.</p>
                    <button
                      type="button"
                      className="button"
                      onClick={() => setPicker({ kind: 'add', blockId: block.id })}
                    >
                      <ImagePlus size={15} aria-hidden="true" /> Add media
                    </button>
                  </div>
                ) : (
                  <>
                    <ImageBlockView
                      block={block}
                      defaultInterval={settings.slideshow_interval}
                      reading={false}
                      onImageContextMenu={openImageMenu}
                    />
                    <div className="image-block-tools">
                      <button
                        type="button"
                        className="button small"
                        onClick={() => setSectionEditor({ blockId: block.id })}
                      >
                        <PenLine size={14} aria-hidden="true" /> Edit section
                      </button>
                      <button
                        type="button"
                        className="button small"
                        onClick={() => setPicker({ kind: 'add', blockId: block.id })}
                      >
                        <ImagePlus size={14} aria-hidden="true" /> Add media
                      </button>
                    </div>
                  </>
                )}
              </BlockShell>
              {inserter(i + 1, i === blocks.length - 1)}
            </Fragment>
          ))}

          <footer className="notebook-footer muted">
            {wordCount(blocks)} words · {blockImageCount(blocks)} photos · {blocks.length} blocks
          </footer>
        </div>
      )}

      <p className="visually-hidden" role="status" aria-live="polite">
        {announce}
      </p>

      {toast && (
        <div className="editor-toast" role="status">
          <span>{toast.text}</span>
          {toast.undo && doc.canUndo && (
            <button
              type="button"
              className="button small"
              onClick={() => {
                doc.undo();
                setToast(null);
              }}
            >
              Undo
            </button>
          )}
        </div>
      )}

      <ContextMenu
        open={menu !== null}
        position={menu?.position ?? null}
        entries={menu?.entries ?? []}
        label={menu?.label ?? 'Actions'}
        onClose={() => setMenu(null)}
      />

      <MediaPicker
        open={picker !== null}
        libraryId={libraryId}
        mode={picker?.kind === 'replace' ? 'single' : 'multi'}
        title={
          picker?.kind === 'replace'
            ? 'Replace media'
            : picker?.kind === 'add'
              ? 'Add media to this section'
              : 'Select photos and videos'
        }
        {...(picker?.kind === 'add' && {
          alreadyIn: new Set(
            (blocks.find((b) => b.id === picker.blockId) as ImageBlock | undefined)?.images.map(
              (i) => i.file_id,
            ) ?? [],
          ),
        })}
        onCancel={() => setPicker(null)}
        onConfirm={onPicked}
      />

      <ImageBlockEditor
        open={!!editingBlock}
        libraryId={libraryId}
        block={editingBlock ?? null}
        defaultInterval={settings.slideshow_interval}
        editedCopies={settings.edited_copies}
        editImageId={sectionEditor?.imageId ?? null}
        onCancel={() => setSectionEditor(null)}
        onSave={(next) => {
          setBlocks((bs) => updateBlock<ImageBlock>(bs, next.id, () => next), { undoable: true });
          setSectionEditor(null);
        }}
      />

      <ImageEditor
        open={!!editingImage}
        image={editingImage}
        editedCopies={settings.edited_copies}
        onCancel={() => setImageEditor(null)}
        onSave={(edits) => {
          if (imageEditor) {
            setBlocks(
              (bs) => updateImage(bs, imageEditor, (img) => ({ ...img, ...edits, derived: null })),
              {
                undoable: true,
              },
            );
          }
          setImageEditor(null);
        }}
      />

      <PromptDialog
        open={!!captionImage}
        title={captionImage?.caption ? 'Change caption' : 'Add caption'}
        label="Caption"
        hint="Captions belong to this memory. The photo’s own notes in the library are not changed."
        initialValue={captionImage?.caption ?? ''}
        multiline
        confirmLabel="Save caption"
        onCancel={() => setCaptionFor(null)}
        onConfirm={(value) => {
          if (captionFor) {
            setBlocks(
              (bs) => updateImage(bs, captionFor, (img) => ({ ...img, caption: value.trim() })),
              {
                undoable: true,
              },
            );
          }
          setCaptionFor(null);
        }}
      />

      <Lightbox
        images={lightbox?.images ?? []}
        startId={lightbox?.id ?? null}
        onClose={() => setLightbox(null)}
      />

      <MemoryDetails
        open={details}
        libraryId={libraryId}
        meta={meta}
        blocks={blocks}
        onChange={doc.setMeta}
        onClose={() => setDetails(false)}
      />

      <VersionHistory
        open={history}
        libraryId={libraryId}
        memoryId={memoryId}
        onClose={() => setHistory(false)}
        onRestore={(v) => {
          setBlocks(() => v.blocks, { undoable: true });
          if (v.title.trim()) doc.setMeta({ title: v.title });
          setToast({ text: 'Version restored.', undo: true });
        }}
      />

      <ConfirmDialog
        open={confirmDelete}
        title="Delete this memory?"
        destructive
        confirmLabel="Delete"
        busy={deleting}
        error={deleteError}
        message={
          <p>
            <strong>{meta.title || 'Untitled memory'}</strong> will be moved out of your memories.
            Its history is kept, and none of its photos are touched — they stay in your library.
          </p>
        }
        onCancel={() => setConfirmDelete(false)}
        onConfirm={() => {
          setDeleting(true);
          setDeleteError(null);
          deleteMemory(libraryId, memoryId)
            .then(() => {
              setConfirmDelete(false);
              onDeleted();
            })
            .catch((e: unknown) => setDeleteError(e instanceof Error ? e.message : String(e)))
            .finally(() => setDeleting(false));
        }}
        testId="delete-memory-dialog"
      />
    </div>
  );
}

/* --------------------------------- pieces --------------------------------- */

function BlockInserter({
  last,
  empty,
  onText,
  onImage,
}: {
  last: boolean;
  empty: boolean;
  onText: () => void;
  onImage: () => void;
}) {
  return (
    <div className={`nb-inserter${last ? ' is-last' : ''}${empty ? ' is-empty' : ''}`}>
      {empty && (
        <p className="nb-start muted">Start your memory with some words, photos or videos.</p>
      )}
      <div className="nb-inserter-actions" role="group" aria-label="Insert a block here">
        <button type="button" className="nb-insert" aria-label="Add text" onClick={onText}>
          <Type size={14} aria-hidden="true" /> Text
        </button>
        <button type="button" className="nb-insert" aria-label="Add media" onClick={onImage}>
          <ImagePlus size={14} aria-hidden="true" /> Media
        </button>
      </div>
    </div>
  );
}

interface ShellProps {
  block: MemoryBlock;
  index: number;
  count: number;
  active: boolean;
  dragOver: number | null;
  children: ReactNode;
  registerEl: (el: HTMLElement | null) => void;
  onActivate: () => void;
  onMenu: (position: MenuPosition) => void;
  onImageLongPress: (imageId: string, position: MenuPosition) => void;
  onDragStart: () => void;
  onDragOverAt: (index: number) => void;
  onDrop: () => void;
  onDragEnd: () => void;
  onKeyAction: (action: 'edit' | 'delete' | 'up' | 'down') => void;
  onMove: (delta: number) => void;
}

/**
 * The editing frame around one block: focus boundary, drag handle and ⋮ menu.
 * Image sections are themselves focusable (Enter edits, Delete removes);
 * text blocks take focus in their editor.
 */
function BlockShell({
  block,
  index,
  count,
  active,
  dragOver,
  children,
  registerEl,
  onActivate,
  onMenu,
  onImageLongPress,
  onDragStart,
  onDragOverAt,
  onDrop,
  onDragEnd,
  onKeyAction,
  onMove,
}: ShellProps) {
  const longPress = useLongPress((pos, target) => {
    const imageId = target?.closest('[data-image-id]')?.getAttribute('data-image-id');
    if (imageId) onImageLongPress(imageId, pos);
    else onMenu(pos);
  });
  const isImage = block.type === 'image';

  const onDragOver = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    const r = e.currentTarget.getBoundingClientRect();
    onDragOverAt(e.clientY < r.top + r.height / 2 ? index : index + 1);
  };

  return (
    <div
      ref={isImage ? registerEl : undefined}
      className={[
        'nb-block',
        `nb-${block.type}`,
        active ? 'is-active' : '',
        dragOver === index ? 'drop-before' : '',
        dragOver === index + 1 && index === count - 1 ? 'drop-after' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      data-block-id={block.id}
      tabIndex={isImage ? 0 : undefined}
      role={isImage ? 'group' : undefined}
      aria-label={
        isImage ? `Media section ${index + 1} of ${count}, ${block.images.length} items` : undefined
      }
      onFocus={onActivate}
      onContextMenu={(e) => {
        if (e.defaultPrevented) return;
        e.preventDefault();
        onMenu({ x: e.clientX, y: e.clientY });
      }}
      onKeyDown={(e) => {
        if ((e.key === 'F10' && e.shiftKey) || e.key === 'ContextMenu') {
          e.preventDefault();
          onMenu(pointAt(e.currentTarget));
          return;
        }
        if (!isImage || e.target !== e.currentTarget) return;
        if (e.key === 'Enter') {
          e.preventDefault();
          onKeyAction('edit');
        } else if (e.key === 'Delete' || e.key === 'Backspace') {
          e.preventDefault();
          onKeyAction('delete');
        } else if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && !e.altKey) {
          e.preventDefault();
          onKeyAction(e.key === 'ArrowUp' ? 'up' : 'down');
        }
      }}
      onDragOver={onDragOver}
      onDrop={(e) => {
        e.preventDefault();
        onDrop();
      }}
      {...longPress}
    >
      <div className="nb-gutter">
        <button
          type="button"
          className="nb-tool"
          aria-label={`Move ${isImage ? 'media section' : 'text block'} up`}
          title="Move up (Alt+↑)"
          disabled={index === 0}
          onClick={() => onMove(-1)}
        >
          <ChevronUp size={16} aria-hidden="true" />
        </button>
        <span
          className="nb-handle"
          draggable
          role="button"
          tabIndex={-1}
          aria-label="Drag to reorder (or use the arrows, or Alt+↑ / Alt+↓)"
          title="Drag to reorder"
          onDragStart={(e) => {
            e.dataTransfer.effectAllowed = 'move';
            e.dataTransfer.setData('text/plain', block.id);
            onDragStart();
          }}
          onDragEnd={onDragEnd}
        >
          <GripVertical size={16} aria-hidden="true" />
        </span>
        <button
          type="button"
          className="nb-tool"
          aria-label={`Move ${isImage ? 'media section' : 'text block'} down`}
          title="Move down (Alt+↓)"
          disabled={index === count - 1}
          onClick={() => onMove(1)}
        >
          <ChevronDown size={16} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="nb-tool"
          aria-haspopup="menu"
          aria-label={isImage ? 'Media section actions' : 'Text block actions'}
          onClick={(e) => onMenu(pointAt(e.currentTarget))}
        >
          <Ellipsis size={16} aria-hidden="true" />
        </button>
      </div>
      <div className="nb-content">{children}</div>
    </div>
  );
}
