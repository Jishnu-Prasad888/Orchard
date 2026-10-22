/**
 * When backups run on their own: by the clock, and when enough new data has
 * piled up since the last one.
 *
 * Both are kept on the server, and the schedule counts from the last backup
 * that actually ran — so it survives restarts, and a run owed while the
 * server was off happens once, soon after it starts again.
 */

import { useState } from 'react';

import { setBackupPolicy } from '../../api/queries';
import { formatBytes } from '../../api/types';
import type { BackupPolicy, BackupSchedule, BackupSizeTrigger } from '../../api/types';

const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const MB = 1024 * 1024;
const GB = 1024 * MB;

interface Draft {
  kind: BackupSchedule['kind'];
  hours: string;
  at: string;
  weekday: number;
  sizeOn: boolean;
  size: string;
  unit: 'MB' | 'GB';
  quiet: string;
}

function draftFrom(p: BackupPolicy): Draft {
  const bytes = p.size_trigger.bytes || 5 * GB;
  // Shown in GB when it is a whole number of tenths of one (5 GB, 2.5 GB).
  const inGB = bytes >= GB && (bytes * 10) % GB === 0;
  return {
    kind: p.schedule.kind,
    hours: String(Math.max(1, Math.round((p.schedule.every_minutes ?? 1440) / 60))),
    at: p.schedule.at ?? '03:00',
    weekday: p.schedule.weekday ?? 0,
    sizeOn: p.size_trigger.enabled,
    size: inGB ? String(bytes / GB) : String(Math.round(bytes / MB)),
    unit: inGB ? 'GB' : 'MB',
    quiet: String(p.size_trigger.quiet_minutes || 10),
  };
}

function policyFrom(d: Draft): { schedule: BackupSchedule; size_trigger: BackupSizeTrigger } {
  const schedule: BackupSchedule = { kind: d.kind };
  if (d.kind === 'interval') schedule.every_minutes = Math.round(Number(d.hours) * 60);
  if (d.kind === 'daily' || d.kind === 'weekly') schedule.at = d.at;
  if (d.kind === 'weekly') schedule.weekday = d.weekday;
  return {
    schedule,
    size_trigger: {
      enabled: d.sizeOn,
      bytes: Math.round(Number(d.size) * (d.unit === 'GB' ? GB : MB)),
      quiet_minutes: Math.round(Number(d.quiet)),
    },
  };
}

function when(iso: string): string {
  const t = new Date(iso);
  if (t.getTime() <= Date.now()) return 'now — it was missed while the server was off';
  return t.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function BackupWhen({
  policy,
  onSaved,
}: {
  /** The page remounts this (by key) when a fresh policy arrives. */
  policy: BackupPolicy;
  onSaved: (next: BackupPolicy) => void;
}) {
  const [draft, setDraft] = useState<Draft>(() => draftFrom(policy));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    setSaved(false);
    setDraft({ ...draft, [key]: value });
  };
  const dirty = JSON.stringify(policyFrom(draft)) !== JSON.stringify(policyFrom(draftFrom(policy)));

  const save = () => {
    setSaving(true);
    setError(null);
    setBackupPolicy(policyFrom(draft))
      .then((next) => {
        onSaved(next);
        setSaved(true);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  };

  return (
    <section className="backup-when" aria-labelledby="backup-when-heading">
      <h2 id="backup-when-heading">When backups run</h2>
      <form
        className="backup-when-form"
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
      >
        <fieldset className="backup-when-group">
          <legend>On a schedule</legend>
          <div className="backup-when-row">
            <select
              aria-label="How often"
              value={draft.kind}
              onChange={(e) => set('kind', e.target.value as Draft['kind'])}
              data-testid="schedule-kind"
            >
              <option value="off">Off</option>
              <option value="interval">Every few hours</option>
              <option value="daily">Every day</option>
              <option value="weekly">Every week</option>
            </select>
            {draft.kind === 'interval' && (
              <label className="backup-when-inline">
                every
                <input
                  type="number"
                  min={1}
                  max={744}
                  value={draft.hours}
                  onChange={(e) => set('hours', e.target.value)}
                  aria-label="Hours between backups"
                  data-testid="schedule-hours"
                />
                hours
              </label>
            )}
            {draft.kind === 'weekly' && (
              <label className="backup-when-inline">
                on
                <select
                  aria-label="Day of the week"
                  value={draft.weekday}
                  onChange={(e) => set('weekday', Number(e.target.value))}
                  data-testid="schedule-weekday"
                >
                  {WEEKDAYS.map((d, i) => (
                    <option key={d} value={i}>
                      {d}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {(draft.kind === 'daily' || draft.kind === 'weekly') && (
              <label className="backup-when-inline">
                at
                <input
                  type="time"
                  value={draft.at}
                  onChange={(e) => set('at', e.target.value)}
                  aria-label="Time of day"
                  data-testid="schedule-at"
                />
              </label>
            )}
          </div>
          {draft.kind !== 'off' && (
            <p className="muted backup-when-note">
              Times are the server's ({policy.timezone}). If the server is off when a backup is due,
              it runs once as soon as the server is back.
            </p>
          )}
          {policy.next_run && !dirty && (
            <p className="backup-when-next" data-testid="schedule-next">
              Next backup: {when(policy.next_run)}
            </p>
          )}
        </fieldset>

        <fieldset className="backup-when-group">
          <legend>When new files pile up</legend>
          <label className="backup-when-check">
            <input
              type="checkbox"
              checked={draft.sizeOn}
              onChange={(e) => set('sizeOn', e.target.checked)}
              data-testid="size-enabled"
            />
            <span>Back up when new or changed files add up to</span>
          </label>
          <div className="backup-when-row">
            <input
              type="number"
              min={1}
              step="any"
              value={draft.size}
              onChange={(e) => set('size', e.target.value)}
              disabled={!draft.sizeOn}
              aria-label="Size limit"
              data-testid="size-amount"
            />
            <select
              aria-label="Unit"
              value={draft.unit}
              onChange={(e) => set('unit', e.target.value as Draft['unit'])}
              disabled={!draft.sizeOn}
            >
              <option value="MB">MB</option>
              <option value="GB">GB</option>
            </select>
            <label className="backup-when-inline">
              after
              <input
                type="number"
                min={1}
                max={1440}
                value={draft.quiet}
                onChange={(e) => set('quiet', e.target.value)}
                disabled={!draft.sizeOn}
                aria-label="Quiet minutes"
                data-testid="size-quiet"
              />
              quiet minutes
            </label>
          </div>
          <p className="muted backup-when-note">
            One large file or many small ones both count. It waits until nothing new has arrived for
            the quiet period, so a batch of uploads is backed up once, whole.
          </p>
          <p className="backup-when-pending" data-testid="size-pending">
            {policy.pending.files === 0
              ? 'Everything is backed up.'
              : `Not yet backed up: ${policy.pending.files} ${
                  policy.pending.files === 1 ? 'file' : 'files'
                }, ${formatBytes(policy.pending.bytes)}.`}
          </p>
        </fieldset>

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
        <div className="backup-when-actions">
          {saved && !dirty && (
            <span className="muted" role="status">
              Saved.
            </span>
          )}
          <button
            type="submit"
            className="button primary-button"
            disabled={!dirty || saving}
            data-testid="save-backup-policy"
          >
            {saving ? 'Saving…' : 'Save schedule'}
          </button>
        </div>
      </form>
    </section>
  );
}
