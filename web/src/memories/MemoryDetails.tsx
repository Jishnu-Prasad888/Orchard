/**
 * MemoryDetails — the memory's metadata, kept out of the writing surface:
 * description, date, location, tags and cover image. Changes save like
 * everything else (debounced, revision-checked).
 *
 * The cover is a reference — chosen from the memory's own photos or from the
 * library — never a copy.
 */

import { useState } from 'react';

import { Dialog } from '../components/Dialog';
import { MediaPicker } from './MediaPicker';
import { MemoryImageView } from './MemoryImageView';
import type { MemoryMeta } from './useMemoryDocument';
import type { MemoryBlock, MemoryMetaPatch } from './types';

interface Props {
  open: boolean;
  libraryId: string;
  meta: MemoryMeta;
  blocks: MemoryBlock[];
  onChange: (patch: MemoryMetaPatch) => void;
  onClose: () => void;
}

function dateInputValue(iso?: string): string {
  if (!iso) return '';
  return iso.slice(0, 10);
}

export function MemoryDetails({ open, libraryId, meta, blocks, onChange, onClose }: Props) {
  const [tagDraft, setTagDraft] = useState('');
  const [picking, setPicking] = useState(false);
  const memoryImages = blocks.flatMap((b) =>
    b.type === 'image'
      ? b.images.filter((i) => i.media.available && i.media.media_type !== 'video')
      : [],
  );

  const addTag = () => {
    const names = tagDraft
      .split(',')
      .map((t) => t.trim().replace(/^#/, ''))
      .filter(Boolean);
    if (names.length === 0) return;
    const next = [...meta.tags];
    for (const n of names) if (!next.some((t) => t.toLowerCase() === n.toLowerCase())) next.push(n);
    onChange({ tags: next });
    setTagDraft('');
  };

  return (
    <>
      <Dialog
        open={open && !picking}
        title="Memory details"
        onClose={onClose}
        size="medium"
        testId="memory-details"
      >
        <div className="details">
          <label className="details-field">
            <span>Description</span>
            <textarea
              rows={3}
              value={meta.description}
              placeholder="A sentence about what this memory holds…"
              onChange={(e) => onChange({ description: e.target.value })}
            />
          </label>
          <div className="details-row">
            <label className="details-field">
              <span>Date</span>
              <input
                type="date"
                value={dateInputValue(meta.memory_date)}
                onChange={(e) =>
                  onChange({ memory_date: e.target.value ? `${e.target.value}T00:00:00Z` : null })
                }
              />
            </label>
            <label className="details-field">
              <span>Location</span>
              <input
                type="text"
                value={meta.location}
                placeholder="Where was this?"
                onChange={(e) => onChange({ location: e.target.value })}
              />
            </label>
          </div>

          <div className="details-field">
            <span id="details-tags">Tags</span>
            <ul className="details-tags" aria-labelledby="details-tags">
              {meta.tags.map((t) => (
                <li key={t} className="chip">
                  #{t}
                  <button
                    type="button"
                    className="chip-remove"
                    aria-label={`Remove tag ${t}`}
                    onClick={() => onChange({ tags: meta.tags.filter((x) => x !== t) })}
                  >
                    ×
                  </button>
                </li>
              ))}
            </ul>
            <div className="details-tag-add">
              <input
                type="text"
                value={tagDraft}
                aria-label="Add tags, separated by commas"
                placeholder="Add a tag…"
                onChange={(e) => setTagDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ',') {
                    e.preventDefault();
                    addTag();
                  }
                }}
              />
              <button type="button" className="button" onClick={addTag} disabled={!tagDraft.trim()}>
                Add
              </button>
            </div>
          </div>

          <div className="details-field">
            <span id="details-cover">Cover image</span>
            <div className="details-cover" aria-labelledby="details-cover">
              {meta.cover?.available && meta.cover.thumbnail_url ? (
                <img
                  src={meta.cover.thumbnail_url}
                  alt="Current cover"
                  className="details-cover-img"
                />
              ) : meta.cover_file_id ? (
                <span className="muted">Cover image unavailable</span>
              ) : (
                <span className="muted">No cover yet</span>
              )}
              <div className="details-cover-actions">
                <button type="button" className="button" onClick={() => setPicking(true)}>
                  Choose from library…
                </button>
                {meta.cover_file_id && (
                  <button
                    type="button"
                    className="button"
                    onClick={() => onChange({ cover_file_id: null })}
                  >
                    Remove cover
                  </button>
                )}
              </div>
            </div>
            {memoryImages.length > 0 && (
              <>
                <p className="muted details-hint">Or pick one of this memory’s photos:</p>
                <div
                  className="details-cover-grid"
                  role="radiogroup"
                  aria-label="Cover from this memory"
                >
                  {memoryImages.slice(0, 24).map((img) => (
                    <button
                      key={img.id}
                      type="button"
                      role="radio"
                      aria-checked={meta.cover_file_id === img.file_id}
                      aria-label={img.caption || img.media.name || 'Photo'}
                      className={
                        meta.cover_file_id === img.file_id
                          ? 'details-cover-option active'
                          : 'details-cover-option'
                      }
                      onClick={() => onChange({ cover_file_id: img.file_id })}
                    >
                      <MemoryImageView image={img} fit="cover" quality="thumb" caption="none" />
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>

          <dl className="details-times">
            <div>
              <dt>Created</dt>
              <dd>{new Date(meta.created_at).toLocaleString()}</dd>
            </div>
            <div>
              <dt>Last changed</dt>
              <dd>{new Date(meta.updated_at).toLocaleString()}</dd>
            </div>
          </dl>
        </div>
      </Dialog>
      <MediaPicker
        open={picking}
        libraryId={libraryId}
        mode="single"
        photosOnly
        title="Choose a cover image"
        onCancel={() => setPicking(false)}
        onConfirm={(media) => {
          setPicking(false);
          if (media[0]) onChange({ cover_file_id: media[0].id });
        }}
      />
    </>
  );
}
