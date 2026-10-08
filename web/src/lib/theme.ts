/**
 * Appearance preference for the UI theme.
 *
 * The design tokens in `styles/tokens.css` already define a light palette
 * (`:root`) and a dark palette (`:root[data-theme='dark']`). This module only
 * decides *which* of those two existing palettes is active and remembers the
 * choice — it never introduces colors of its own.
 */

export type ThemePreference = 'system' | 'light' | 'dark';

export const THEME_STORAGE_KEY = 'cairn.theme';

/** Read the stored preference, defaulting to following the OS setting. */
export function readStoredTheme(): ThemePreference {
  if (typeof localStorage === 'undefined') return 'system';
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored;
  } catch {
    // Storage can be unavailable (private mode, disabled cookies) — fall back.
  }
  return 'system';
}

/**
 * Apply a preference to the document. `system` removes the attribute so the
 * `prefers-color-scheme` media query in the token sheet takes over.
 */
export function applyTheme(theme: ThemePreference): void {
  if (typeof document === 'undefined') return;
  if (theme === 'system') {
    delete document.documentElement.dataset.theme;
  } else {
    document.documentElement.dataset.theme = theme;
  }
}

/** Persist and immediately apply a preference. */
export function storeTheme(theme: ThemePreference): void {
  applyTheme(theme);
  if (typeof localStorage === 'undefined') return;
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // A failed write only means the choice is not remembered next visit.
  }
}
