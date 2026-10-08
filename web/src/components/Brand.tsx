import type { ReactNode } from 'react';

/**
 * Cairn wordmark. Uses the Handlee branding font. Handlee is reserved for
 * branding contexts only (brand header, empty states, welcome screens) and is
 * never used for dense UI.
 */
export default function Brand({ children }: { children: ReactNode }) {
  return <span className="brand">{children}</span>;
}
