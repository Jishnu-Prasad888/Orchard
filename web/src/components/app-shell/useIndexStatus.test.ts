import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { IndexStatus } from '../../api/types';

const statuses: IndexStatus[] = [];
vi.mock('../../api/queries', () => ({
  getIndexStatus: vi.fn(() => Promise.resolve({ status: statuses.shift() ?? {} })),
}));

const { useIndexStatus } = await import('./useIndexStatus');

const running = { ID: 'j', Kind: 'scan', Status: 'running' };

describe('useIndexStatus', () => {
  afterEach(() => {
    vi.useRealTimers();
    window.removeEventListener('cairn:library-changed', listener);
  });

  const seen: string[] = [];
  const listener = (e: Event) =>
    seen.push((e as CustomEvent<{ libraryId: string }>).detail.libraryId);

  it('announces changes while a new library is scanned and when the scan ends', async () => {
    vi.useFakeTimers();
    seen.length = 0;
    window.addEventListener('cairn:library-changed', listener);
    // Counts climb during the scan, then the last poll finds them unchanged
    // but the scan finished: that last step used to be swallowed.
    statuses.push(
      { present: 0, active_job: running },
      { present: 12, active_job: running },
      { present: 20, active_job: running },
      { present: 20 },
    );
    renderHook(() => useIndexStatus('lib1'));
    for (let i = 0; i < 4; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4000);
      });
    }
    // 0→12, 12→20, and running→finished.
    expect(seen).toEqual(['lib1', 'lib1', 'lib1']);
  });

  it('stays quiet when asked not to announce', async () => {
    vi.useFakeTimers();
    seen.length = 0;
    window.addEventListener('cairn:library-changed', listener);
    statuses.push({ present: 0, active_job: running }, { present: 5 });
    renderHook(() => useIndexStatus('lib1', true, false));
    for (let i = 0; i < 2; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4000);
      });
    }
    expect(seen).toEqual([]);
  });
});
