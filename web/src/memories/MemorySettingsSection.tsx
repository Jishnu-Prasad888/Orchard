/**
 * The Memories section of Settings. Every option here is stored per account
 * on the server (GET/PATCH /api/v1/settings/memories) and takes effect the
 * next time a memory is opened.
 */

import { useEffect, useState } from 'react';

import { getMemorySettings, updateMemorySettings } from './api';
import { INTERVAL_CHOICES } from './format';
import { LAYOUTS } from './layouts';
import { DEFAULT_MEMORY_SETTINGS, type MemorySettings } from './types';

export function MemorySettingsSection() {
  const [settings, setSettings] = useState<MemorySettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getMemorySettings()
      .then((r) => !cancelled && setSettings({ ...DEFAULT_MEMORY_SETTINGS, ...r.settings }))
      .catch((e: unknown) => {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : String(e));
        setSettings(DEFAULT_MEMORY_SETTINGS);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const update = (patch: Partial<MemorySettings>) => {
    if (!settings) return;
    const previous = settings;
    setSettings({ ...settings, ...patch });
    setSaved(false);
    setError(null);
    updateMemorySettings(patch)
      .then((r) => {
        setSettings({ ...DEFAULT_MEMORY_SETTINGS, ...r.settings });
        setSaved(true);
      })
      .catch((e: unknown) => {
        setSettings(previous);
        setError(e instanceof Error ? e.message : String(e));
      });
  };

  const intervals =
    settings && !INTERVAL_CHOICES.includes(settings.slideshow_interval)
      ? [...INTERVAL_CHOICES, settings.slideshow_interval].sort((a, b) => a - b)
      : INTERVAL_CHOICES;

  return (
    <section
      className="settings-section"
      id="memories"
      aria-labelledby="settings-memories"
      data-testid="memory-settings"
    >
      <div className="settings-section-head">
        <h2 id="settings-memories">Memories</h2>
        <p className="settings-section-description">
          Saved to your account; applies the next time a memory is opened.
        </p>
      </div>
      {!settings ? (
        <p className="muted">Loading…</p>
      ) : (
        <div className="memory-settings">
          <label className="memory-setting">
            <span className="memory-setting-label">Slideshow interval</span>
            <span className="muted settings-hint">
              How long each photo shows before a slideshow moves on.
            </span>
            <select
              value={settings.slideshow_interval}
              onChange={(e) => update({ slideshow_interval: Number(e.target.value) })}
            >
              {intervals.map((s) => (
                <option key={s} value={s}>
                  {s} seconds
                </option>
              ))}
            </select>
          </label>

          <fieldset className="memory-setting">
            <legend className="memory-setting-label">
              When editing library photos inside memories
            </legend>
            <label className="memory-radio">
              <input
                type="radio"
                name="edited-copies"
                checked={!settings.edited_copies}
                onChange={() => update({ edited_copies: false })}
              />
              <span>
                Keep edits as memory-only presentation
                <span className="muted settings-hint"> — nothing new is written to disk.</span>
              </span>
            </label>
            <label className="memory-radio">
              <input
                type="radio"
                name="edited-copies"
                checked={settings.edited_copies}
                onChange={() => update({ edited_copies: true })}
              />
              <span>
                Also save edited copies in the library
                <span className="muted settings-hint">
                  {' '}
                  — stored in the library’s reserved <code>.cairn/memory-media</code> folder.
                  Originals are never changed.
                </span>
              </span>
            </label>
          </fieldset>

          <label className="memory-setting">
            <span className="memory-setting-label">Default image layout</span>
            <select
              value={settings.default_layout}
              onChange={(e) =>
                update({ default_layout: e.target.value as MemorySettings['default_layout'] })
              }
            >
              {LAYOUTS.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.label}
                </option>
              ))}
            </select>
          </label>

          <label className="memory-setting">
            <span className="memory-setting-label">Open memories in</span>
            <select
              value={settings.default_mode}
              onChange={(e) =>
                update({ default_mode: e.target.value as MemorySettings['default_mode'] })
              }
            >
              <option value="edit">Edit mode</option>
              <option value="preview">Preview mode</option>
            </select>
          </label>

          <label className="memory-radio">
            <input
              type="checkbox"
              checked={settings.autosave}
              onChange={(e) => update({ autosave: e.target.checked })}
            />
            <span>
              Autosave
              <span className="muted settings-hint">
                {' '}
                — when off, changes are kept on this device until you press Save or Ctrl/Cmd+S.
              </span>
            </span>
          </label>
        </div>
      )}
      {error && (
        <p className="settings-hint error-text" role="alert">
          Memory settings could not be {settings === DEFAULT_MEMORY_SETTINGS ? 'loaded' : 'saved'}:{' '}
          {error}
        </p>
      )}
      {saved && !error && <p className="settings-hint">Saved.</p>}
    </section>
  );
}
