import { lazy } from 'react';
import type { ComponentType } from 'react';

const RELOAD_KEY = 'cairn.chunkReload';

/**
 * `lazy` for route pages that survives a new build. A tab opened before a
 * rebuild still points at page files that no longer exist, so the import
 * fails ("Failed to fetch dynamically imported module"); reloading fetches the
 * new index and fixes it. Reloads at most once per 10 seconds, so a genuinely
 * broken deploy shows its error instead of looping.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- any page component
export function lazyPage<T extends ComponentType<any>>(load: () => Promise<{ default: T }>) {
  return lazy(() =>
    load().catch((error: unknown) => {
      try {
        const last = Number(sessionStorage.getItem(RELOAD_KEY) ?? 0);
        if (Date.now() - last > 10_000) {
          sessionStorage.setItem(RELOAD_KEY, String(Date.now()));
          window.location.reload();
          return new Promise<never>(() => undefined); // the page is going away
        }
      } catch {
        // Storage unavailable: fall through and report the failure.
      }
      throw error;
    }),
  );
}
