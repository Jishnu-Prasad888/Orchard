/** The top of a file's right-click menu: which file the actions are for. */

import { formatBytes, type FileSummary } from '../../api/types';
import { mediaLabel } from '../media';

export function FileMenuHeader({ file }: { file: FileSummary }) {
  return (
    <div className="menu-file-header">
      <span className="menu-file-name" title={file.name}>
        {file.name}
      </span>
      <span className="menu-file-meta">
        {mediaLabel(file.media_type)} · {formatBytes(file.size_bytes)}
      </span>
    </div>
  );
}
