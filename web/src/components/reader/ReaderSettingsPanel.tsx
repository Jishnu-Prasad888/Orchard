/**
 * Reading preferences: reading direction, pages per view, a
 * light/dark/sepia theme, and font size, line spacing, margin and reading
 * width. User-level only for now — no per-book
 * override — so this panel edits the same preferences no matter which
 * book is open.
 */

import type {
  PagesPerView,
  ReaderPreferences,
  ReaderTheme,
  ReadingDirection,
} from '../../api/types';

const THEMES: Array<{ value: ReaderTheme; label: string }> = [
  { value: 'auto', label: 'Match app' },
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
  { value: 'sepia', label: 'Sepia' },
];

const DIRECTIONS: Array<{ value: ReadingDirection; label: string; hint: string }> = [
  { value: 'horizontal', label: 'Horizontal', hint: 'Turn pages side to side' },
  { value: 'vertical', label: 'Vertical', hint: 'Scroll pages top to bottom' },
];

const PAGES_PER_VIEW: Array<{ value: PagesPerView; label: string }> = [
  { value: 1, label: 'One page' },
  { value: 2, label: 'Two pages' },
];

export function ReaderSettingsPanel({
  preferences,
  onChange,
  typography = true,
  layout = true,
}: {
  preferences: ReaderPreferences;
  onChange: (next: ReaderPreferences) => void;
  /** Whether the open book's text can be restyled. A PDF's pages are fixed
   * pictures, so font and spacing controls would do nothing there. */
  typography?: boolean;
  /** Whether the open book has pages to lay out. Markdown and plain text
   * are one scrolling column, so direction and pages per view do not apply. */
  layout?: boolean;
}) {
  const set = <K extends keyof ReaderPreferences>(key: K, value: ReaderPreferences[K]) =>
    onChange({ ...preferences, [key]: value });

  return (
    <div className="reader-settings" data-testid="reader-settings">
      {layout && (
        <>
          <fieldset className="reader-settings-group">
            <legend>Reading direction</legend>
            <div className="segmented">
              {DIRECTIONS.map((d) => (
                <label
                  key={d.value}
                  title={d.hint}
                  className={
                    preferences.reading_direction === d.value
                      ? 'segmented-item active'
                      : 'segmented-item'
                  }
                >
                  <input
                    type="radio"
                    name="reader-direction"
                    className="visually-hidden"
                    checked={preferences.reading_direction === d.value}
                    onChange={() => set('reading_direction', d.value)}
                  />
                  <span>{d.label}</span>
                </label>
              ))}
            </div>
          </fieldset>

          <fieldset className="reader-settings-group">
            <legend>Pages per view</legend>
            <div className="segmented">
              {PAGES_PER_VIEW.map((p) => (
                <label
                  key={p.value}
                  className={
                    preferences.pages_per_view === p.value
                      ? 'segmented-item active'
                      : 'segmented-item'
                  }
                >
                  <input
                    type="radio"
                    name="reader-pages-per-view"
                    className="visually-hidden"
                    checked={preferences.pages_per_view === p.value}
                    onChange={() => set('pages_per_view', p.value)}
                  />
                  <span>{p.label}</span>
                </label>
              ))}
            </div>
          </fieldset>
        </>
      )}

      <fieldset className="reader-settings-group">
        <legend>Theme</legend>
        <div className="segmented">
          {THEMES.map((t) => (
            <label
              key={t.value}
              className={preferences.theme === t.value ? 'segmented-item active' : 'segmented-item'}
            >
              <input
                type="radio"
                name="reader-theme"
                className="visually-hidden"
                checked={preferences.theme === t.value}
                onChange={() => set('theme', t.value)}
              />
              <span>{t.label}</span>
            </label>
          ))}
        </div>
      </fieldset>

      {typography && (
        <>
          <label className="field reader-settings-field">
            <span className="field-label">Font size: {preferences.font_size}px</span>
            <input
              type="range"
              min={12}
              max={32}
              step={1}
              value={preferences.font_size}
              onChange={(e) => set('font_size', Number(e.target.value))}
            />
          </label>

          <label className="field reader-settings-field">
            <span className="field-label">Line spacing: {preferences.line_spacing.toFixed(1)}</span>
            <input
              type="range"
              min={1}
              max={2.2}
              step={0.1}
              value={preferences.line_spacing}
              onChange={(e) => set('line_spacing', Number(e.target.value))}
            />
          </label>

          <label className="field reader-settings-field">
            <span className="field-label">Margin: {preferences.margin}px</span>
            <input
              type="range"
              min={0}
              max={96}
              step={4}
              value={preferences.margin}
              onChange={(e) => set('margin', Number(e.target.value))}
            />
          </label>

          <label className="field reader-settings-field">
            <span className="field-label">Reading width: {preferences.reading_width}px</span>
            <input
              type="range"
              min={360}
              max={1200}
              step={20}
              value={preferences.reading_width}
              onChange={(e) => set('reading_width', Number(e.target.value))}
            />
          </label>
        </>
      )}
    </div>
  );
}
