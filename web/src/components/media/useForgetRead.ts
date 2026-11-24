/**
 * "Forget read" from a file's menu: as if the book had never been opened —
 * it leaves Recently opened and Continue reading, its progress goes back to
 * 0, and a Completed mark is cleared. Bookmarks, highlights and quotes stay.
 */

import { useCallback } from 'react';

import { forgetFileReading } from '../../api/queries';
import type { FileSummary } from '../../api/types';
import { useToast } from '../ui/Toast';
import { useHideCompleted } from './useHideCompleted';

export function useForgetRead(libraryId: string, onChanged: () => void) {
  const toast = useToast();
  const { markCompleted } = useHideCompleted();
  return useCallback(
    async (file: FileSummary) => {
      try {
        await forgetFileReading(libraryId, file.id);
        // The server cleared Completed already; this updates the tiles on
        // screen, which lay unsaved marks over their listing.
        if (file.completed) await markCompleted(libraryId, [file], false);
        toast({ message: 'Forgotten — it reads from the start next time', tone: 'success' });
      } catch {
        toast({ message: "Couldn't forget reading this file", tone: 'error' });
      } finally {
        onChanged();
      }
    },
    [libraryId, markCompleted, onChanged, toast],
  );
}
