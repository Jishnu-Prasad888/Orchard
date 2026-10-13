import { describe, expect, it } from 'vitest';

import { fileFixture } from '../test/harness';
import { hasThumbnail, thumbnailUrl } from './media';

describe('hasThumbnail', () => {
  it('is true for a photo', () => {
    expect(hasThumbnail(fileFixture({ media_type: 'photo' }))).toBe(true);
  });

  it('is true for a document with a cover, false without one', () => {
    expect(
      hasThumbnail(fileFixture({ media_type: 'document', cover_url: '/cover.jpg' })),
    ).toBe(true);
    expect(hasThumbnail(fileFixture({ media_type: 'document' }))).toBe(false);
  });

  it('is false for video, audio, and other', () => {
    expect(hasThumbnail(fileFixture({ media_type: 'video' }))).toBe(false);
    expect(hasThumbnail(fileFixture({ media_type: 'audio' }))).toBe(false);
    expect(hasThumbnail(fileFixture({ media_type: 'other' }))).toBe(false);
  });
});

describe('thumbnailUrl', () => {
  it("uses a document's cover_url when it has one", () => {
    const file = fileFixture({
      id: 'f1',
      media_type: 'document',
      cover_url: '/api/v1/libraries/lib1/files/f1/cover',
    });
    expect(thumbnailUrl('lib1', file)).toBe('/api/v1/libraries/lib1/files/f1/cover');
  });

  it('falls back to the download URL for a photo, or a cover-less document', () => {
    const photo = fileFixture({ id: 'f2', media_type: 'photo' });
    expect(thumbnailUrl('lib1', photo)).toContain('/files/f2/download');

    const uncovered = fileFixture({ id: 'f3', media_type: 'document' });
    expect(thumbnailUrl('lib1', uncovered)).toContain('/files/f3/download');
  });
});
