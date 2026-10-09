import type { ReactNode } from 'react';

/**
 * Orchard wordmark. Uses the `--font-brand` token (brand header, empty
 * states, welcome screens) — never used for dense UI.
 */
export default function Brand({ children }: { children: ReactNode }) {
  return <span className="brand">{children}</span>;
}
