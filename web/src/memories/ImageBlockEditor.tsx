/**
 * ImageBlockEditor — configure a whole image section.
 *
 * Which photos and videos it holds, their order (drag and drop, or explicit
 * move buttons), each photo's memory-specific caption, the layout, and the
 * slideshow. The preview at the top re-renders as you change anything, so the
 * section looks here exactly as it will in the memory. Nothing is saved until
 * "Save"; "Remove from memory" only drops the reference — originals are never
 * deleted.
 *
 * Per-image visual edits (crop, rotate, filters) open the separate
 * ImageEditor; this dialog is about the section, not pixels.
 */

import {
  ArrowLeftToLine,
  ArrowRightToLine,
  ChevronLeft,
  ChevronRight,
  GripVertical,
  ImagePlus,
  Pencil,
  Play,
  Replace,
  Trash2,
} from 'lucide-react';
import { type DragEvent, useState } from 'react';

import { Dialog } from '../components/Dialog';
import { VideoThumb } from '../components/media/VideoThumb';
import { ImageEditor } from './ImageEditor';
import { LAYOUTS, LayoutRenderer } from './layouts';
import { MediaPicker } from './MediaPicker';
import { MemoryImageView } from './MemoryImageView';
import { isDefaultEdits } from './edits';
import { INTERVAL_CHOICES } from './format';
import { type PickedMedia, imageFromMedia, moveImageTo, replaceImageSource } from './model';
import { Slideshow } from './Slideshow';
import type { ImageBlock, ImageEdits, MemoryImage } from './types';

interface Props {
  open: boolean;
  libraryId: string;
  block: ImageBlock | null;
  defaultInterval: number;
  editedCopies: boolean;
  /** Open with this image's editor already showing. */
  editImageId?: string | null;
  onCancel: () => void;
  onSave: (block: ImageBlock) => void;
}

export function ImageBlockEditor(props: Props) {
  if (!props.open || !props.block) return null;
  return <SectionEditor key={props.block.id} {...props} block={props.block} />;
}

function SectionEditor({
  open,
  libraryId,
  block,
  defaultInterval,
  editedCopies,
  editImageId,
  onCancel,
  onSave,
}: Props & { block: ImageBlock }) {
  const [draft, setDraft] = useState<ImageBlock>(block);
  const [picker, setPicker] = useState<null | { replace?: string }>(null);
  const [editing, setEditing] = useState<string | null>(editImageId ?? null);
  const [dragging, setDragging] = useState<number | null>(null);
  // The slot (0..n) a dropped card would land in, drawn as an insertion bar.
  const [slot, setSlot] = useState<number | null>(null);
  const [previewMode, setPreviewMode] = useState<'layout' | 'slideshow'>('layout');
  const [status, setStatus] = useState('');

  const images = draft.images;
  const setImages = (next: MemoryImage[]) => setDraft({ ...draft, images: next });
  const move = (id: string, to: number, label: string) => {
    setImages(moveImageTo(images, id, to));
    setStatus(label);
  };
  const updateImage = (id: string, change: (img: MemoryImage) => MemoryImage) =>
    setImages(images.map((i) => (i.id === id ? change(i) : i)));

  const onPicked = (media: PickedMedia[]) => {
    const replace = picker?.replace;
    setPicker(null);
    if (replace && media[0]) {
      updateImage(replace, (img) => replaceImageSource(img, media[0]!));
      setStatus('Photo replaced.');
      return;
    }
    setImages([...images, ...media.map(imageFromMedia)]);
    setStatus(`${media.length} photo${media.length === 1 ? '' : 's'} added.`);
  };

  const endDrag = () => {
    setDragging(null);
    setSlot(null);
  };

  /** Dragging over the left half of a card targets the slot before it. */
  const onCardDragOver = (i: number) => (e: DragEvent) => {
    if (dragging === null) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const r = e.currentTarget.getBoundingClientRect();
    const next = e.clientX < r.left + r.width / 2 ? i : i + 1;
    setSlot((cur) => (cur === next ? cur : next));
  };

  const commitDrop = (e: DragEvent) => {
    e.preventDefault();
    const from = dragging;
    const target = slot;
    endDrag();
    if (from === null || target === null) return;
    // Removing the card first shifts every later slot down by one.
    const to = target > from ? target - 1 : target;
    const img = images[from];
    if (img && to !== from) move(img.id, to, `Moved to position ${to + 1} of ${images.length}.`);
  };

  const editingImage = images.find((i) => i.id === editing) ?? null;
  const interval = draft.slideshow.interval_seconds;

  return (
    <>
      <Dialog
        open={open && !picker && !editingImage}
        title="Edit media section"
        onClose={onCancel}
        size="large"
        testId="image-block-editor"
        footer={
          <div className="ibe-footer">
            <button type="button" className="button" onClick={onCancel}>
              Cancel
            </button>
            <button type="button" className="button primary-button" onClick={() => onSave(draft)}>
              Save section
            </button>
          </div>
        }
      >
        <div className="ibe">
          <section className="ibe-preview" aria-label="Preview">
            {draft.slideshow.enabled && (
              <div className="segmented ibe-preview-toggle" role="group" aria-label="Preview as">
                <button
                  type="button"
                  className={previewMode === 'layout' ? 'segment active' : 'segment'}
                  aria-pressed={previewMode === 'layout'}
                  onClick={() => setPreviewMode('layout')}
                >
                  Layout view
                </button>
                <button
                  type="button"
                  className={previewMode === 'slideshow' ? 'segment active' : 'segment'}
                  aria-pressed={previewMode === 'slideshow'}
                  onClick={() => setPreviewMode('slideshow')}
                >
                  Slideshow
                </button>
              </div>
            )}
            {images.length === 0 ? (
              <p className="muted ibe-empty">This section has no media yet. Add some below.</p>
            ) : previewMode === 'slideshow' && draft.slideshow.enabled ? (
              <Slideshow
                images={images}
                interval={interval ?? defaultInterval}
                label="Slideshow preview"
              />
            ) : (
              <LayoutRenderer layout={draft.layout} images={images} reading={false} />
            )}
          </section>

          <section className="ibe-section" aria-labelledby="ibe-photos">
            <div className="ibe-section-head">
              <h3 id="ibe-photos">Media</h3>
              <span className="muted">{images.length}</span>
              <button type="button" className="button" onClick={() => setPicker({})}>
                <ImagePlus size={15} aria-hidden="true" /> Add media
              </button>
            </div>
            <p className="visually-hidden" role="status" aria-live="polite">
              {status}
            </p>
            <p className="ibe-hint muted">
              Drag the cards to reorder them, or use the arrows. The order is also the slideshow
              order.
            </p>
            <ol
              className={`ibe-photos${dragging !== null ? ' is-reordering' : ''}`}
              onDragOver={(e) => dragging !== null && e.preventDefault()}
              onDrop={commitDrop}
            >
              {images.map((img, i) => {
                const isVideo = img.media.media_type === 'video';
                const before =
                  slot === i && dragging !== null && slot !== dragging && slot !== dragging + 1;
                const after =
                  slot === i + 1 &&
                  i === images.length - 1 &&
                  dragging !== null &&
                  slot !== dragging &&
                  slot !== dragging + 1;
                return (
                  <li
                    key={img.id}
                    className={[
                      'ibe-photo',
                      dragging === i ? 'is-dragging' : '',
                      before ? 'drop-before' : '',
                      after ? 'drop-after' : '',
                    ]
                      .filter(Boolean)
                      .join(' ')}
                    onDragOver={onCardDragOver(i)}
                  >
                    <div
                      className="ibe-thumb"
                      draggable
                      onDragStart={(e) => {
                        setDragging(i);
                        e.dataTransfer.effectAllowed = 'move';
                        e.dataTransfer.setData('text/plain', img.id);
                      }}
                      onDragEnd={endDrag}
                    >
                      {isVideo && img.media.available ? (
                        <VideoThumb
                          libraryId={libraryId}
                          fileId={img.file_id}
                          className="ibe-thumb-media"
                        />
                      ) : (
                        <MemoryImageView image={img} fit="cover" quality="thumb" caption="none" />
                      )}
                      <span className="ibe-index">{i + 1}</span>
                      <span className="ibe-grip" title="Drag to reorder" aria-hidden="true">
                        <GripVertical size={16} />
                      </span>
                      {isVideo && (
                        <span className="ibe-kind" aria-label="Video">
                          <Play size={12} fill="currentColor" aria-hidden="true" />
                        </span>
                      )}
                      {!isDefaultEdits(img) && (
                        <span className="ibe-badge" title="Edited in this memory">
                          Edited
                        </span>
                      )}
                      <div
                        className="ibe-move"
                        role="group"
                        aria-label={`Position of item ${i + 1}`}
                      >
                        <button
                          type="button"
                          className="ibe-move-btn"
                          aria-label={`Move item ${i + 1} to the beginning`}
                          title="Move to the beginning"
                          disabled={i === 0}
                          onClick={() => move(img.id, 0, 'Moved to the beginning.')}
                        >
                          <ArrowLeftToLine size={16} aria-hidden="true" />
                        </button>
                        <button
                          type="button"
                          className="ibe-move-btn"
                          aria-label={`Move item ${i + 1} left`}
                          title="Move left"
                          disabled={i === 0}
                          onClick={() => move(img.id, i - 1, `Moved to position ${i}.`)}
                        >
                          <ChevronLeft size={18} aria-hidden="true" />
                        </button>
                        <button
                          type="button"
                          className="ibe-move-btn"
                          aria-label={`Move item ${i + 1} right`}
                          title="Move right"
                          disabled={i === images.length - 1}
                          onClick={() => move(img.id, i + 1, `Moved to position ${i + 2}.`)}
                        >
                          <ChevronRight size={18} aria-hidden="true" />
                        </button>
                        <button
                          type="button"
                          className="ibe-move-btn"
                          aria-label={`Move item ${i + 1} to the end`}
                          title="Move to the end"
                          disabled={i === images.length - 1}
                          onClick={() => move(img.id, images.length - 1, 'Moved to the end.')}
                        >
                          <ArrowRightToLine size={16} aria-hidden="true" />
                        </button>
                      </div>
                    </div>
                    <div className="ibe-photo-body">
                      <label className="ibe-caption">
                        <span className="visually-hidden">Caption for item {i + 1}</span>
                        <textarea
                          rows={2}
                          placeholder="Add a caption…"
                          value={img.caption}
                          onChange={(e) =>
                            updateImage(img.id, (x) => ({ ...x, caption: e.target.value }))
                          }
                        />
                      </label>
                      <div className="ibe-photo-actions">
                        <button
                          type="button"
                          className="button small"
                          disabled={!img.media.available || isVideo}
                          title={isVideo ? 'Videos cannot be edited' : 'Crop, rotate, filters'}
                          onClick={() => setEditing(img.id)}
                        >
                          <Pencil size={14} aria-hidden="true" /> Edit
                        </button>
                        <button
                          type="button"
                          className="button small"
                          onClick={() => setPicker({ replace: img.id })}
                        >
                          <Replace size={14} aria-hidden="true" /> Replace
                        </button>
                        <button
                          type="button"
                          className="button small danger-button"
                          onClick={() => {
                            setImages(images.filter((x) => x.id !== img.id));
                            setStatus(
                              'Removed from this memory. The original stays in your library.',
                            );
                          }}
                          aria-label={`Remove item ${i + 1} from memory`}
                          title="Removes it from this memory only. The original stays in your library."
                        >
                          <Trash2 size={14} aria-hidden="true" /> Remove
                        </button>
                      </div>
                    </div>
                  </li>
                );
              })}
            </ol>
            <p className="ibe-hint muted">
              “Remove” only takes the item out of this memory. The original file stays in your
              library.
            </p>
          </section>

          <section className="ibe-section" aria-labelledby="ibe-layout">
            <h3 id="ibe-layout">Layout</h3>
            <div className="layout-picker" role="radiogroup" aria-labelledby="ibe-layout">
              {LAYOUTS.map((l) => (
                <button
                  key={l.id}
                  type="button"
                  role="radio"
                  aria-checked={draft.layout === l.id}
                  className={`layout-option${draft.layout === l.id ? ' active' : ''}`}
                  onClick={() => setDraft({ ...draft, layout: l.id })}
                >
                  {l.icon}
                  <span className="layout-option-label">{l.label}</span>
                  <span className="layout-option-desc">{l.description}</span>
                </button>
              ))}
            </div>
          </section>

          <section className="ibe-section" aria-labelledby="ibe-slideshow">
            <h3 id="ibe-slideshow">Slideshow</h3>
            <label className="switch-row">
              <input
                type="checkbox"
                role="switch"
                checked={draft.slideshow.enabled}
                onChange={(e) => {
                  const enabled = e.target.checked;
                  setDraft({ ...draft, slideshow: { ...draft.slideshow, enabled } });
                  if (!enabled) setPreviewMode('layout');
                }}
              />
              <span>Show this section as a slideshow</span>
            </label>
            {draft.slideshow.enabled && (
              <label className="ibe-interval">
                <span>Advance every</span>
                <select
                  value={interval === null ? 'inherit' : String(interval)}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      slideshow: {
                        ...draft.slideshow,
                        interval_seconds:
                          e.target.value === 'inherit' ? null : Number(e.target.value),
                      },
                    })
                  }
                >
                  <option value="inherit">My default ({defaultInterval} seconds)</option>
                  {INTERVAL_CHOICES.map((s) => (
                    <option key={s} value={s}>
                      {s} seconds (this section only)
                    </option>
                  ))}
                </select>
              </label>
            )}
            <p className="muted ibe-hint">
              Readers can switch between the slideshow and the layout. The order above is the
              slideshow order.
            </p>
          </section>
        </div>
      </Dialog>

      <MediaPicker
        open={picker !== null}
        libraryId={libraryId}
        mode={picker?.replace ? 'single' : 'multi'}
        title={picker?.replace ? 'Replace media' : 'Add media to this section'}
        confirmLabel="Add to section"
        alreadyIn={new Set(images.map((i) => i.file_id))}
        onCancel={() => setPicker(null)}
        onConfirm={onPicked}
      />

      <ImageEditor
        open={editingImage !== null}
        image={editingImage}
        editedCopies={editedCopies}
        onCancel={() => setEditing(null)}
        onSave={(edits: ImageEdits) => {
          if (editingImage)
            updateImage(editingImage.id, (img) => ({ ...img, ...edits, derived: null }));
          setEditing(null);
        }}
      />
    </>
  );
}
