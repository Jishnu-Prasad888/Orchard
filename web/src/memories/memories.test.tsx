import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { fileFixture, json, mockApi, originalFetch } from '../test/harness';
import { cssFilter, editLayers, editedAspect, isDefaultEdits, rotateBy } from './edits';
import { ImageBlockEditor } from './ImageBlockEditor';
import { LiveMarkdownEditor } from './LiveMarkdownEditor';
import { MediaPicker } from './MediaPicker';
import { MemoryReader } from './MemoryReader';
import {
  DEFAULT_EDITS,
  addImages,
  duplicateBlock,
  imageFromMedia,
  insertBlock,
  mergeServerViews,
  moveBlock,
  moveImageTo,
  newImageBlock,
  newTextBlock,
  removeImage,
} from './model';
import { Slideshow } from './Slideshow';
import type { ImageBlock, MemoryBlock, MemoryImage } from './types';

const media = (id: string) => ({
  id,
  name: `${id}.jpg`,
  media_type: 'photo',
  thumbnail_url: `/t/${id}`,
  original_url: `/o/${id}`,
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.useRealTimers();
});

describe('model', () => {
  it('inserts, moves and removes blocks without mutating', () => {
    const a = newTextBlock('a');
    const b = newTextBlock('b');
    const start: MemoryBlock[] = [a];
    const two = insertBlock(start, 1, b);
    expect(start).toHaveLength(1);
    expect(moveBlock(two, b.id, -1).map((x) => x.id)).toEqual([b.id, a.id]);
    expect(moveBlock(two, a.id, -1)).toEqual(two);
  });

  it('duplicates image blocks as references with fresh ids', () => {
    const block = newImageBlock('featured', [media('f1'), media('f2')]);
    const out = duplicateBlock([block], block.id) as ImageBlock[];
    expect(out).toHaveLength(2);
    expect(out[1]!.id).not.toBe(block.id);
    expect(out[1]!.images.map((i) => i.file_id)).toEqual(['f1', 'f2']);
    expect(out[1]!.images[0]!.id).not.toBe(block.images[0]!.id);
    expect(out[1]!.layout).toBe('featured');
  });

  it('adds photos to an existing block instead of creating one', () => {
    const block = newImageBlock('grid', [media('f1')]);
    const out = addImages([block], block.id, [media('f2'), media('f3')]);
    expect(out).toHaveLength(1);
    expect((out[0] as ImageBlock).images.map((i) => i.file_id)).toEqual(['f1', 'f2', 'f3']);
  });

  it('removes only the reference and reorders images', () => {
    const block = newImageBlock('grid', [media('f1'), media('f2'), media('f3')]);
    const [i1, , i3] = block.images;
    expect((removeImage([block], i1!.id)[0] as ImageBlock).images).toHaveLength(2);
    expect(moveImageTo(block.images, i3!.id, 0).map((i) => i.file_id)).toEqual(['f3', 'f1', 'f2']);
  });

  it('merges server media views without losing local edits made in flight', () => {
    const block = newImageBlock('grid', [media('f1')]);
    const local = [{ ...block, images: [{ ...block.images[0]!, caption: 'typed while saving' }] }];
    const server = [
      {
        ...block,
        images: [
          { ...block.images[0]!, media: { available: false, status: 'missing' }, caption: 'old' },
        ],
      },
    ];
    const merged = mergeServerViews(local, server) as ImageBlock[];
    expect(merged[0]!.images[0]!.caption).toBe('typed while saving');
    expect(merged[0]!.images[0]!.media.status).toBe('missing');
  });
});

describe('edits', () => {
  it('mirrors the server filter primitives', () => {
    expect(cssFilter('original', DEFAULT_EDITS.adjustments)).toBe('none');
    expect(cssFilter('bw', DEFAULT_EDITS.adjustments)).toBe('grayscale(1) contrast(1.12)');
    expect(cssFilter('original', { brightness: 20, contrast: -40, saturation: 50 })).toBe(
      'brightness(1.1) contrast(0.8) saturate(1.5)',
    );
  });

  it('computes rotated and cropped geometry', () => {
    expect(rotateBy(0, -90)).toBe(270);
    expect(editedAspect(400, 200, { crop: null, rotation: 90 })).toBeCloseTo(0.5);
    expect(
      editedAspect(400, 200, { crop: { x: 0, y: 0, width: 0.5, height: 1 }, rotation: 0 }),
    ).toBeCloseTo(1);
    const layers = editLayers(400, 200, {
      ...DEFAULT_EDITS,
      crop: { x: 0.5, y: 0, width: 0.5, height: 1 },
    });
    expect(layers.rot.width).toBe('200.0000%');
    expect(layers.rot.left).toBe('-100.0000%');
    expect(isDefaultEdits(DEFAULT_EDITS)).toBe(true);
  });
});

describe('Slideshow', () => {
  const images: MemoryImage[] = [
    imageFromMedia(media('a')),
    imageFromMedia(media('b')),
    imageFromMedia(media('c')),
  ];
  images[1]!.caption = 'Second';

  it('advances on the configured interval (default 15 s) and wraps', () => {
    vi.useFakeTimers();
    render(<Slideshow images={images} interval={15} label="Trip" />);
    const slide = () => screen.getByRole('group', { name: /of 3/ }).getAttribute('aria-label');
    expect(slide()).toBe('1 of 3');
    act(() => vi.advanceTimersByTime(14_000));
    expect(slide()).toBe('1 of 3');
    act(() => vi.advanceTimersByTime(1_000));
    expect(slide()).toBe('2 of 3');
    act(() => vi.advanceTimersByTime(15_000));
    expect(slide()).toBe('3 of 3');
    act(() => vi.advanceTimersByTime(15_000));
    expect(slide()).toBe('1 of 3');
  });

  it('manual navigation moves at once and restarts the countdown', () => {
    vi.useFakeTimers();
    render(<Slideshow images={images} interval={10} label="Trip" />);
    const slide = () => screen.getByRole('group', { name: /of 3/ }).getAttribute('aria-label');
    act(() => vi.advanceTimersByTime(8_000));
    fireEvent.click(screen.getByRole('button', { name: 'Previous photo' }));
    expect(slide()).toBe('3 of 3');
    act(() => vi.advanceTimersByTime(8_000));
    expect(slide()).toBe('3 of 3');
    act(() => vi.advanceTimersByTime(2_000));
    expect(slide()).toBe('1 of 3');
    fireEvent.keyDown(screen.getByTestId('slideshow'), { key: 'ArrowRight' });
    expect(slide()).toBe('2 of 3');
  });

  it('pauses on request, while hovered, and while the page is hidden', () => {
    vi.useFakeTimers();
    render(<Slideshow images={images} interval={5} label="Trip" />);
    const slide = () => screen.getByRole('group', { name: /of 3/ }).getAttribute('aria-label');
    fireEvent.click(screen.getByRole('button', { name: 'Pause slideshow' }));
    act(() => vi.advanceTimersByTime(20_000));
    expect(slide()).toBe('1 of 3');
    fireEvent.click(screen.getByRole('button', { name: 'Play slideshow' }));
    fireEvent.mouseEnter(screen.getByTestId('slideshow'));
    act(() => vi.advanceTimersByTime(20_000));
    expect(slide()).toBe('1 of 3');
    fireEvent.mouseLeave(screen.getByTestId('slideshow'));
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    act(() => vi.advanceTimersByTime(20_000));
    expect(slide()).toBe('1 of 3');
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    act(() => vi.advanceTimersByTime(5_000));
    expect(slide()).toBe('2 of 3');
  });
});

describe('LiveMarkdownEditor', () => {
  function Harness({ initial = '' }: { initial?: string }) {
    const [value, setValue] = useState(initial);
    return (
      <>
        <LiveMarkdownEditor value={value} onChange={setValue} ariaLabel="Body" testId="ed" />
        <output data-testid="value">{value}</output>
      </>
    );
  }

  it('keeps the DOM text equal to the Markdown and highlights it', () => {
    render(<Harness initial="# Kerala" />);
    const ed = screen.getByTestId('ed');
    expect(ed.textContent).toBe('# Kerala\n');
    expect(ed.querySelector('.md-h1')).not.toBeNull();
    ed.textContent = '# Kerala\n\n**Kochi**\n';
    fireEvent.input(ed);
    expect(screen.getByTestId('value').textContent).toBe('# Kerala\n\n**Kochi**');
    expect(ed.querySelector('strong')).not.toBeNull();
  });

  it('pastes plain text only and supports its own undo', () => {
    render(<Harness initial="" />);
    const ed = screen.getByTestId('ed');
    ed.focus();
    fireEvent.paste(ed, { clipboardData: { getData: () => 'pasted <b>text</b>' } });
    expect(screen.getByTestId('value').textContent).toBe('pasted <b>text</b>');
    expect(ed.querySelector('b')).toBeNull();
    fireEvent.keyDown(ed, { key: 'z', ctrlKey: true });
    expect(screen.getByTestId('value').textContent).toBe('');
    fireEvent.keyDown(ed, { key: 'z', ctrlKey: true, shiftKey: true });
    expect(screen.getByTestId('value').textContent).toBe('pasted <b>text</b>');
  });

  it('continues lists on Enter', () => {
    render(<Harness initial="- one" />);
    const ed = screen.getByTestId('ed');
    ed.focus();
    window.getSelection()!.selectAllChildren(ed);
    window.getSelection()!.collapseToEnd();
    fireEvent.keyDown(ed, { key: 'Enter' });
    expect(screen.getByTestId('value').textContent).toBe('- one\n- ');
  });
});

describe('MediaPicker', () => {
  it('multi-selects library photos and returns references in grid order', async () => {
    mockApi([
      (url) =>
        url.includes('/libraries/lib1/files?')
          ? json({
              files: [
                fileFixture({ id: 'a', name: 'a.jpg' }),
                fileFixture({ id: 'b', name: 'b.jpg', media_type: 'video' }),
                fileFixture({ id: 'c', name: 'c.jpg' }),
                fileFixture({ id: 'd', name: 'd.pdf', media_type: 'document' }),
              ],
              total: 4,
            })
          : undefined,
    ]);
    const onConfirm = vi.fn();
    render(
      <MemoryRouter>
        <MediaPicker open libraryId="lib1" onCancel={() => {}} onConfirm={onConfirm} />
      </MemoryRouter>,
    );
    const c = await screen.findByRole('button', { name: 'c.jpg' });
    expect(screen.queryByRole('button', { name: /d\.pdf/ })).toBeNull();
    fireEvent.click(c);
    fireEvent.click(screen.getByRole('button', { name: 'a.jpg' }));
    expect(screen.getByRole('status')).toHaveTextContent('2 selected');
    fireEvent.click(screen.getByRole('button', { name: 'Add media (2)' }));
    expect(onConfirm.mock.calls[0]![0].map((m: { id: string }) => m.id)).toEqual(['a', 'c']);
  });
});

describe('ImageBlockEditor', () => {
  it('reorders, captions, changes layout and enables a slideshow', () => {
    const block = newImageBlock('grid', [media('f1'), media('f2')]);
    const onSave = vi.fn();
    render(
      <MemoryRouter>
        <ImageBlockEditor
          open
          libraryId="lib1"
          block={block}
          defaultInterval={15}
          editedCopies={false}
          onCancel={() => {}}
          onSave={onSave}
        />
      </MemoryRouter>,
    );
    const dialog = screen.getByTestId('image-block-editor');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move item 2 to the beginning' }));
    fireEvent.change(within(dialog).getByLabelText('Caption for item 1'), {
      target: { value: 'Tea after rain' },
    });
    fireEvent.click(within(dialog).getByRole('radio', { name: /Masonry/ }));
    fireEvent.click(within(dialog).getByRole('switch'));
    fireEvent.change(within(dialog).getByRole('combobox'), { target: { value: '30' } });
    expect(within(dialog).getByRole('button', { name: 'Slideshow' })).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save section' }));
    const saved = onSave.mock.calls[0]![0] as ImageBlock;
    expect(saved.images.map((i) => i.file_id)).toEqual(['f2', 'f1']);
    expect(saved.images[0]!.caption).toBe('Tea after rain');
    expect(saved.layout).toBe('masonry');
    expect(saved.slideshow).toEqual({ enabled: true, interval_seconds: 30 });
  });
});

describe('large memories', () => {
  it('renders 150 text blocks and 150 images', async () => {
    const blocks: MemoryBlock[] = [];
    for (let i = 0; i < 150; i += 1) {
      blocks.push(newTextBlock(`Paragraph ${i} with **bold** words.`));
      if (i % 10 === 0) {
        blocks.push(
          newImageBlock(
            'masonry',
            Array.from({ length: 10 }, (_, j) => media(`f${i + j}`)),
          ),
        );
      }
    }
    const start = performance.now();
    render(
      <MemoryReader
        meta={{
          id: 'm',
          title: 'A long year',
          body: '',
          description: '',
          location: '',
          tags: [],
          revision: 1,
          deleted: false,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        }}
        blocks={blocks}
        defaultInterval={15}
      />,
    );
    await waitFor(() => expect(document.querySelectorAll('figure.mi')).toHaveLength(150));
    expect(document.querySelectorAll('.reader-text')).toHaveLength(150);
    // Every image is lazy: nothing beyond the first few is fetched eagerly.
    const eager = Array.from(document.querySelectorAll('img')).filter(
      (img) => img.getAttribute('loading') === 'eager',
    );
    expect(eager.length).toBeLessThanOrEqual(30);
    expect(performance.now() - start).toBeLessThan(5000);
  });
});
