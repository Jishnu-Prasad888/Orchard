/**
 * ImageEditor — quick, non-destructive edits to one memory image.
 *
 * Crop (free or a preset ratio, with handles, drag and arrow keys), rotate in
 * quarter turns, three adjustment sliders and a handful of filters. It edits
 * presentation data only: the result belongs to this memory's reference, and
 * the original file is never touched. Undo/redo cover every step; Reset
 * returns to the original.
 *
 * This is distinct from editing the image *section* (which photos, their
 * order, captions, layout, slideshow) — see ImageBlockEditor.
 */

import { type KeyboardEvent, type PointerEvent, useRef, useState } from 'react';

import { Dialog } from '../components/Dialog';
import {
  CROP_PRESETS,
  FILTERS,
  centeredCrop,
  clampCrop,
  cssFilter,
  editLayers,
  isDefaultEdits,
  normalizeCrop,
  rotateBy,
  rotatedSize,
} from './edits';
import { DEFAULT_EDITS } from './model';
import type { Crop, ImageEdits, MemoryImage } from './types';

type Tab = 'crop' | 'adjust' | 'filters';
type Handle = 'move' | 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw';

interface Props {
  open: boolean;
  image: MemoryImage | null;
  editedCopies: boolean;
  onCancel: () => void;
  onSave: (edits: ImageEdits) => void;
}

const FULL: Crop = { x: 0, y: 0, width: 1, height: 1 };

function pickEdits(img: MemoryImage): ImageEdits {
  return {
    crop: img.crop ? { ...img.crop } : null,
    rotation: img.rotation,
    filter: img.filter,
    adjustments: { ...img.adjustments },
  };
}

/** Rotating the image turns the crop rectangle with it. */
function rotateCrop(c: Crop | null, delta: 90 | -90): Crop | null {
  if (!c) return null;
  return delta === 90
    ? { x: 1 - c.y - c.height, y: c.x, width: c.height, height: c.width }
    : { x: c.y, y: 1 - c.x - c.width, width: c.height, height: c.width };
}

export function ImageEditor(props: Props) {
  if (!props.open || !props.image) return null;
  return <ImageEditorPanel key={props.image.id} {...props} image={props.image} />;
}

function ImageEditorPanel({
  open,
  image,
  editedCopies,
  onCancel,
  onSave,
}: Props & { image: MemoryImage }) {
  const [edits, setEdits] = useState<ImageEdits>(() => pickEdits(image));
  const [past, setPast] = useState<ImageEdits[]>([]);
  const [future, setFuture] = useState<ImageEdits[]>([]);
  const [tab, setTab] = useState<Tab>('crop');
  const [preset, setPreset] = useState('free');
  const [dims, setDims] = useState<[number, number]>([
    image.media.width || 4,
    image.media.height || 3,
  ]);
  const canvas = useRef<HTMLDivElement | null>(null);
  const drag = useRef<{
    handle: Handle;
    start: Crop;
    x: number;
    y: number;
    w: number;
    h: number;
  } | null>(null);
  // While a slider or crop drag is in progress, its steps are one undo step.
  const gesture = useRef(false);

  const [rw, rh] = rotatedSize(dims[0], dims[1], edits.rotation);
  const crop = edits.crop ?? FULL;
  const presetRatio = (() => {
    const p = CROP_PRESETS.find((c) => c.id === preset)?.ratio ?? null;
    return p === -1 ? dims[0] / dims[1] : p;
  })();

  /** Applies a change; `coalesce` merges the steps of one gesture into one undo step. */
  const change = (next: ImageEdits, coalesce = false) => {
    if (!coalesce || !gesture.current) {
      setPast((p) => [...p, edits].slice(-100));
      setFuture([]);
    }
    gesture.current = coalesce;
    setEdits(next);
  };
  const endGesture = () => {
    gesture.current = false;
    drag.current = null;
  };

  const undo = () => {
    const prev = past[past.length - 1];
    if (!prev) return;
    setPast(past.slice(0, -1));
    setFuture([edits, ...future]);
    setEdits(prev);
  };
  const redo = () => {
    const next = future[0];
    if (!next) return;
    setFuture(future.slice(1));
    setPast([...past, edits]);
    setEdits(next);
  };

  const rotate = (delta: 90 | -90) =>
    change({
      ...edits,
      rotation: rotateBy(edits.rotation, delta),
      crop: rotateCrop(edits.crop, delta),
    });

  const choosePreset = (id: string) => {
    setPreset(id);
    const p = CROP_PRESETS.find((c) => c.id === id);
    if (!p || p.ratio === null) return;
    const ratio = p.ratio === -1 ? dims[0] / dims[1] : p.ratio;
    change({ ...edits, crop: normalizeCrop(centeredCrop(ratio, rw, rh)) });
  };

  const setCrop = (c: Crop, coalesce = true) =>
    change({ ...edits, crop: normalizeCrop(clampCrop(c)) }, coalesce);

  const onHandleDown = (e: PointerEvent<HTMLElement>) => {
    e.preventDefault();
    e.stopPropagation();
    const handle = (e.currentTarget.dataset.handle ?? 'move') as Handle;
    gesture.current = false;
    const box = canvas.current?.getBoundingClientRect();
    if (!box) return;
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    drag.current = {
      handle,
      start: { ...crop },
      x: e.clientX,
      y: e.clientY,
      w: box.width,
      h: box.height,
    };
  };

  const onPointerMove = (e: PointerEvent<HTMLElement>) => {
    const d = drag.current;
    if (!d) return;
    const dx = (e.clientX - d.x) / d.w;
    const dy = (e.clientY - d.y) / d.h;
    let { x, y, width, height } = d.start;
    if (d.handle === 'move') {
      x += dx;
      y += dy;
    } else {
      if (d.handle.includes('w')) {
        x += dx;
        width -= dx;
      }
      if (d.handle.includes('e')) width += dx;
      if (d.handle.includes('n')) {
        y += dy;
        height -= dy;
      }
      if (d.handle.includes('s')) height += dy;
      if (presetRatio) {
        // Keep the preset's pixel ratio: derive height from width.
        const h = (width * rw) / presetRatio / rh;
        if (d.handle.includes('n')) y += height - h;
        height = h;
      }
    }
    if (width < 0.02 || height < 0.02) return;
    if (d.handle === 'move') {
      x = Math.max(0, Math.min(1 - width, x));
      y = Math.max(0, Math.min(1 - height, y));
    }
    if (x < -1e-6 || y < -1e-6 || x + width > 1 + 1e-6 || y + height > 1 + 1e-6) {
      if (d.handle !== 'move') return;
    }
    setCrop({ x, y, width, height });
  };

  const onCropKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.altKey ? 0.002 : 0.01;
    const map: Record<string, [number, number]> = {
      ArrowLeft: [-step, 0],
      ArrowRight: [step, 0],
      ArrowUp: [0, -step],
      ArrowDown: [0, step],
    };
    const v = map[e.key];
    if (!v) return;
    e.preventDefault();
    const c = { ...crop };
    if (e.shiftKey) {
      c.width += v[0];
      c.height += v[1];
    } else {
      c.x += v[0];
      c.y += v[1];
    }
    setCrop(c);
  };

  const layers = editLayers(dims[0], dims[1], { ...edits, crop: null });
  const src = image.media.original_url ?? image.media.thumbnail_url;
  const result = { ...edits, crop: normalizeCrop(edits.crop) };

  return (
    <Dialog
      open={open}
      title="Edit image"
      onClose={onCancel}
      size="large"
      testId="image-editor"
      footer={
        <div className="ie-footer">
          <div className="ie-history">
            <button
              type="button"
              className="button"
              onClick={undo}
              disabled={past.length === 0}
              aria-label="Undo edit"
            >
              ↶ Undo
            </button>
            <button
              type="button"
              className="button"
              onClick={redo}
              disabled={future.length === 0}
              aria-label="Redo edit"
            >
              ↷ Redo
            </button>
            <button
              type="button"
              className="button"
              onClick={() =>
                change({ ...DEFAULT_EDITS, adjustments: { ...DEFAULT_EDITS.adjustments } })
              }
              disabled={isDefaultEdits(edits)}
            >
              Reset to original
            </button>
          </div>
          <div className="ie-actions">
            <button type="button" className="button" onClick={onCancel}>
              Cancel
            </button>
            <button type="button" className="button primary-button" onClick={() => onSave(result)}>
              Save edits
            </button>
          </div>
        </div>
      }
    >
      <div className="ie">
        <div className="ie-stage" onPointerMove={onPointerMove} onPointerUp={endGesture}>
          <div
            ref={canvas}
            className="ie-canvas"
            style={{ aspectRatio: `${rw} / ${rh}`, ['--ratio' as string]: rw / rh }}
          >
            <div className="mi-rot" style={{ ...layers.rot, inset: 0 }}>
              <img
                className="mi-img"
                src={src}
                alt={image.caption || image.media.name || 'Photo being edited'}
                draggable={false}
                style={layers.img}
                onLoad={(e) => {
                  const el = e.currentTarget;
                  // Only the proportions matter here, so a thumbnail will do.
                  if (el.naturalWidth && el.naturalHeight) {
                    setDims([el.naturalWidth, el.naturalHeight]);
                  }
                }}
              />
            </div>
            {tab === 'crop' ? (
              <div
                className="ie-crop"
                style={{
                  left: `${crop.x * 100}%`,
                  top: `${crop.y * 100}%`,
                  width: `${crop.width * 100}%`,
                  height: `${crop.height * 100}%`,
                }}
                role="slider"
                tabIndex={0}
                aria-label="Crop area. Arrow keys move it, Shift+arrows resize it."
                aria-valuetext={`${Math.round(crop.width * rw)} by ${Math.round(crop.height * rh)} pixels`}
                onKeyDown={onCropKey}
                onKeyUp={endGesture}
                data-handle="move"
                onPointerDown={onHandleDown}
              >
                {(['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'] as Handle[]).map((h) => (
                  <span
                    key={h}
                    data-handle={h}
                    className={`ie-handle ie-handle-${h}`}
                    onPointerDown={onHandleDown}
                  />
                ))}
              </div>
            ) : (
              edits.crop && (
                <div
                  className="ie-crop ie-crop-static"
                  style={{
                    left: `${crop.x * 100}%`,
                    top: `${crop.y * 100}%`,
                    width: `${crop.width * 100}%`,
                    height: `${crop.height * 100}%`,
                  }}
                  aria-hidden="true"
                />
              )
            )}
          </div>
        </div>

        <div className="ie-panel">
          <div className="segmented ie-tabs" role="tablist" aria-label="Edit tools">
            {(
              [
                ['crop', 'Crop & rotate'],
                ['adjust', 'Adjust'],
                ['filters', 'Filters'],
              ] as const
            ).map(([id, label]) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={tab === id}
                className={tab === id ? 'segment active' : 'segment'}
                onClick={() => setTab(id)}
              >
                {label}
              </button>
            ))}
          </div>

          {tab === 'crop' && (
            <div className="ie-tool" role="tabpanel" aria-label="Crop and rotate">
              <div className="ie-row" role="group" aria-label="Aspect ratio">
                {CROP_PRESETS.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    className={preset === p.id ? 'chip active' : 'chip'}
                    aria-pressed={preset === p.id}
                    onClick={() => choosePreset(p.id)}
                  >
                    {p.label}
                  </button>
                ))}
              </div>
              <div className="ie-row">
                <button
                  type="button"
                  className="button"
                  onClick={() => rotate(-90)}
                  aria-label="Rotate left"
                >
                  ↶ Rotate left
                </button>
                <button
                  type="button"
                  className="button"
                  onClick={() => rotate(90)}
                  aria-label="Rotate right"
                >
                  ↷ Rotate right
                </button>
                <button
                  type="button"
                  className="button"
                  disabled={!edits.crop && edits.rotation === 0}
                  onClick={() => change({ ...edits, crop: null, rotation: 0 })}
                >
                  Reset crop & rotation
                </button>
              </div>
            </div>
          )}

          {tab === 'adjust' && (
            <div className="ie-tool" role="tabpanel" aria-label="Adjust">
              {(['brightness', 'contrast', 'saturation'] as const).map((k) => (
                <label key={k} className="ie-slider">
                  <span className="ie-slider-label">
                    {k[0]!.toUpperCase() + k.slice(1)}
                    <output>
                      {edits.adjustments[k] > 0 ? `+${edits.adjustments[k]}` : edits.adjustments[k]}
                    </output>
                  </span>
                  <input
                    type="range"
                    min={-100}
                    max={100}
                    step={1}
                    value={edits.adjustments[k]}
                    onChange={(e) =>
                      change(
                        {
                          ...edits,
                          adjustments: { ...edits.adjustments, [k]: Number(e.target.value) },
                        },
                        true,
                      )
                    }
                    onPointerUp={endGesture}
                    onKeyUp={endGesture}
                    onBlur={endGesture}
                    onDoubleClick={() =>
                      change({ ...edits, adjustments: { ...edits.adjustments, [k]: 0 } })
                    }
                  />
                </label>
              ))}
            </div>
          )}

          {tab === 'filters' && (
            <div className="ie-tool ie-filters" role="tabpanel" aria-label="Filters">
              {FILTERS.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  className={`ie-filter${edits.filter === f.id ? ' active' : ''}`}
                  aria-pressed={edits.filter === f.id}
                  onClick={() => change({ ...edits, filter: f.id })}
                >
                  <span className="ie-filter-thumb">
                    {image.media.thumbnail_url && (
                      <img
                        src={image.media.thumbnail_url}
                        alt=""
                        loading="lazy"
                        style={{ filter: cssFilter(f.id, edits.adjustments) }}
                      />
                    )}
                  </span>
                  <span>{f.label}</span>
                </button>
              ))}
            </div>
          )}

          <p className="ie-note">
            Edits belong to this memory only — the original photo is never changed.
            {editedCopies
              ? ' Because “Create edited copies” is on, Cairn also saves an edited copy inside the library’s .cairn/memory-media folder.'
              : ''}
          </p>
        </div>
      </div>
    </Dialog>
  );
}
