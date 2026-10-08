/**
 * Toggle — an on/off switch that reads as a control, not a form field.
 *
 * A real <button role="switch">: Space and Enter flip it, a screen reader
 * hears "on"/"off", and it is disabled while a change is being saved. The
 * visible "On"/"Off" word keeps the state clear without relying on colour.
 */

import './Toggle.css';

interface Props {
  checked: boolean;
  onChange: (next: boolean) => void;
  /** The accessible name; also shown beside the switch unless `hideLabel`. */
  label: string;
  hideLabel?: boolean;
  disabled?: boolean;
  /** Shows a quiet "Saving…" instead of On/Off while a change is in flight. */
  busy?: boolean;
  testId?: string;
}

export function Toggle({ checked, onChange, label, hideLabel, disabled, busy, testId }: Props) {
  return (
    <span className="toggle">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={hideLabel ? label : undefined}
        className={checked ? 'toggle-button is-on' : 'toggle-button'}
        disabled={disabled || busy}
        onClick={() => onChange(!checked)}
        data-testid={testId}
      >
        <span className="toggle-track" aria-hidden="true">
          <span className="toggle-thumb" />
        </span>
        <span className="toggle-state">{busy ? 'Saving…' : checked ? 'On' : 'Off'}</span>
        {!hideLabel && <span className="toggle-label">{label}</span>}
      </button>
    </span>
  );
}
