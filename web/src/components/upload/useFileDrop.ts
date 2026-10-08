/**
 * Dropping files from the desktop onto a page to upload them.
 *
 * Only real files count — dragging a tile inside Cairn (to move it into a
 * folder) is a different gesture and must not light up the upload target.
 * Enter/leave events fire for every child crossed, so a depth counter decides
 * when the drag has really left.
 */

import { useCallback, useRef, useState } from 'react';

const hasFiles = (event: React.DragEvent) => event.dataTransfer.types.includes('Files');

export function useFileDrop(onFiles: (files: File[]) => void, enabled = true) {
  const [dragging, setDragging] = useState(false);
  const depth = useRef(0);

  const onDragEnter = useCallback(
    (event: React.DragEvent) => {
      if (!enabled || !hasFiles(event)) return;
      event.preventDefault();
      depth.current += 1;
      setDragging(true);
    },
    [enabled],
  );

  const onDragOver = useCallback(
    (event: React.DragEvent) => {
      if (!enabled || !hasFiles(event)) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'copy';
    },
    [enabled],
  );

  const onDragLeave = useCallback(
    (event: React.DragEvent) => {
      if (!enabled || !hasFiles(event)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setDragging(false);
    },
    [enabled],
  );

  const onDrop = useCallback(
    (event: React.DragEvent) => {
      if (!enabled || !hasFiles(event)) return;
      event.preventDefault();
      depth.current = 0;
      setDragging(false);
      const files = Array.from(event.dataTransfer.files ?? []);
      if (files.length) onFiles(files);
    },
    [enabled, onFiles],
  );

  return { dragging, bind: { onDragEnter, onDragOver, onDragLeave, onDrop } };
}
