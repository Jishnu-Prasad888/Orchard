import { useSyncExternalStore } from 'react';

import { resolvedAppTheme, subscribeAppTheme } from './theme';

/** The app's light/dark palette as it is on screen, kept current. */
export function useAppTheme(): 'light' | 'dark' {
  return useSyncExternalStore(subscribeAppTheme, resolvedAppTheme, () => 'light');
}
