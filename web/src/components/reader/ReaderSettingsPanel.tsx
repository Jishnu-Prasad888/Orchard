/**
 * Reading preferences: font size, line spacing, margin, reading width, and
 * a light/dark/sepia theme. User-level only for now — no per-book
 * override — so this panel edits the same preferences no matter which
 * book is open.
 */

import type { ReaderPreferences, ReaderTheme } from '../../api/types';

const THEMES: Array<{ value: ReaderTheme; label: string }> = [
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
  { value: 'sepia', label: 'Sepia' },
];

export function ReaderSettingsPanel({
  preferences,
  onChange,
}: {
  preferences: ReaderPreferences;
  onChange: (next: ReaderPreferences) => void;
}) {
  const set = <K extends keyof ReaderPreferences>(key: K, value: ReaderPreferences[K]) =>
    onChange({ ...preferences, [key]: value });

  return (
    <div className="reader-settings" data-testid="reader-settings">
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
    </div>
  );
}
