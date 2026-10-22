import type { FileSummary } from '../../api/types';
import { ShareForm } from '../ShareDialog';

/**
 * The viewer's Share tab: a link to exactly this file, made with the same
 * form as everywhere else (see ShareDialog).
 */
export function SharePanel({ libraryId, file }: { libraryId: string; file: FileSummary }) {
  return (
    <div data-testid="viewer-share">
      <p className="muted">
        Anyone with the link (and the password, if you set one) can open this file without an
        Orchard account. Manage links from the Sharing page.
      </p>
      <ShareForm
        key={file.id}
        libraryId={libraryId}
        fixed={{ target: { kind: 'file', id: file.id }, name: file.name }}
      />
    </div>
  );
}
