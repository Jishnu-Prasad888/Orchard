/**
 * The frame for a page that needs a library: while the library list loads,
 * fails, or is empty, it shows the matching state under the page's title;
 * once a library is chosen it renders the page for it.
 *
 * Every library page used to repeat these three branches with small
 * differences in wording and layout. Now the wording is written once.
 */

import type { ReactNode } from 'react';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import type { Library } from '../api/types';
import { MediaGridSkeleton } from './media/MediaGrid';
import { ErrorState, NoLibrariesState, PageHeader } from './States';

export function LibraryGatePage({
  title,
  header,
  className = '',
  children,
}: {
  title: string;
  /** Replaces the standard page header, for pages with their own (Home). */
  header?: ReactNode;
  className?: string;
  children: (library: Library) => ReactNode;
}) {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const frame = (content: ReactNode) => (
    <main className={`page ${className}`.trim()}>
      {header ?? <PageHeader title={title} />}
      {content}
    </main>
  );

  if (gate.kind === 'loading') return frame(<MediaGridSkeleton />);
  if (gate.kind === 'error') {
    return frame(<ErrorState message={gate.message} title="Couldn't load your libraries" />);
  }
  if (gate.kind === 'empty') return frame(<NoLibrariesState isAdmin={user?.role === 'admin'} />);
  return <>{children(gate.library)}</>;
}
