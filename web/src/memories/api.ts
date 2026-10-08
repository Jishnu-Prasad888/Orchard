/**
 * Endpoint wrappers for notebook memories and memory settings. Each function
 * maps to one documented route in docs/openapi.yaml.
 */

import { apiGet, apiPatch, apiPost, apiPut } from '../api/client';
import type {
  ImageBlock,
  MemoryBlock,
  MemoryMetaPatch,
  MemoryResponse,
  MemorySettings,
  MemoryVersionDetail,
} from './types';

const base = (libraryId: string, memoryId: string) =>
  `/libraries/${libraryId}/memories/${memoryId}`;

export const getMemoryDocument = (libraryId: string, memoryId: string) =>
  apiGet<MemoryResponse>(base(libraryId, memoryId));

export const createMemoryDocument = (
  libraryId: string,
  input: { title: string; blocks: unknown[]; memory_date?: string },
) => apiPost<MemoryResponse>(`/libraries/${libraryId}/memories`, input);

/** The block payload the server accepts: references and edits, no media views. */
export function toPayloadBlocks(blocks: MemoryBlock[]): unknown[] {
  return blocks.map((block) =>
    block.type === 'text'
      ? { id: block.id, type: 'text', markdown: block.markdown }
      : {
          id: block.id,
          type: 'image',
          layout: block.layout,
          slideshow: block.slideshow,
          images: (block as ImageBlock).images.map((img) => ({
            id: img.id,
            file_id: img.file_id,
            caption: img.caption,
            crop: img.crop,
            rotation: img.rotation,
            filter: img.filter,
            adjustments: img.adjustments,
          })),
        },
  );
}

export const saveMemoryDocument = (
  libraryId: string,
  memoryId: string,
  baseRevision: number | null,
  blocks: MemoryBlock[],
) =>
  apiPut<MemoryResponse>(`${base(libraryId, memoryId)}/document`, {
    base_revision: baseRevision ?? undefined,
    blocks: toPayloadBlocks(blocks),
  });

export const patchMemoryMeta = (
  libraryId: string,
  memoryId: string,
  baseRevision: number | null,
  patch: MemoryMetaPatch,
) =>
  apiPatch<MemoryResponse>(base(libraryId, memoryId), {
    ...patch,
    base_revision: baseRevision ?? undefined,
  });

export const getMemoryVersionDetail = (libraryId: string, memoryId: string, version: number) =>
  apiGet<{ version: MemoryVersionDetail }>(`${base(libraryId, memoryId)}/versions/${version}`);

export const getMemorySettings = () => apiGet<{ settings: MemorySettings }>('/settings/memories');

export const updateMemorySettings = (patch: Partial<MemorySettings>) =>
  apiPatch<{ settings: MemorySettings }>('/settings/memories', patch);
