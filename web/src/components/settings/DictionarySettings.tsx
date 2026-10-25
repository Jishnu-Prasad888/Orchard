/**
 * Settings → Dictionary: which offline dictionaries the reader's "Search in
 * dictionary" uses.
 *
 * Administrators download WordNet (English definitions) or a FreeDict
 * language pair (translations) onto the server, switch each on or off, and
 * remove them. A download runs on the server, so it carries on if this page
 * is closed: the progress dialog only watches it (polling the job), can be
 * hidden at any time, and the page picks a running download back up when
 * it is opened again. Members see what is installed and whether it is on.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import {
  cancelDictionaryInstall,
  getDictionaryCatalog,
  installDictionary,
  listDictionaries,
  removeDictionary,
  setDictionaryEnabled,
} from '../../api/queries';
import { useResource } from '../../api/resources';
import type {
  Dictionary,
  DictionaryCatalogResponse,
  DictionaryJob,
  DictionarySource,
} from '../../api/types';
import { formatBytes } from '../../api/types';
import { formatDate } from '../../lib/dates';
import { ConfirmDialog, Dialog } from '../Dialog';
import { Toggle } from '../ui/Toggle';
import { useToast } from '../ui/Toast';

/** How often a running download is checked on. */
const POLL_MS = 500;

const PHASE_LABELS: Record<DictionaryJob['phase'], string> = {
  downloading: 'Downloading…',
  verifying: 'Checking the download…',
  extracting: 'Unpacking…',
  indexing: 'Building the word index…',
  done: 'Ready',
  failed: 'The download failed',
  cancelled: 'Cancelled',
};

function isRunning(job: DictionaryJob | null | undefined): job is DictionaryJob {
  return !!job && !job.finished_at;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** "12.3 MB of 16.4 MB" for a download; a plain percentage otherwise. */
function progressText(job: DictionaryJob): string | null {
  if (job.total <= 0) return null;
  const percent = Math.min(100, Math.round((job.done / job.total) * 100));
  if (job.phase === 'downloading') {
    return `${formatBytes(job.done)} of ${formatBytes(job.total)} · ${percent}%`;
  }
  return `${percent}%`;
}

function ProgressBar({ job }: { job: DictionaryJob }) {
  const determinate = job.total > 0;
  const percent = determinate ? Math.min(100, (job.done / job.total) * 100) : 0;
  return (
    <div
      className={
        determinate
          ? 'progress dictionary-progress'
          : 'progress progress-indeterminate dictionary-progress'
      }
      role="progressbar"
      aria-label={PHASE_LABELS[job.phase]}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={determinate ? Math.round(percent) : undefined}
      data-testid="dictionary-progress"
    >
      <div className="progress-fill" style={determinate ? { width: `${percent}%` } : undefined} />
    </div>
  );
}

/** FreeDict pairs grouped the way people look for them: from English, into
 * English, then everything else. */
function groupPairs(sources: DictionarySource[]) {
  const fromEnglish: DictionarySource[] = [];
  const intoEnglish: DictionarySource[] = [];
  const other: DictionarySource[] = [];
  for (const s of sources) {
    if (s.kind !== 'freedict') continue;
    if (s.id.startsWith('freedict-eng-')) fromEnglish.push(s);
    else if (s.id.endsWith('-eng')) intoEnglish.push(s);
    else other.push(s);
  }
  return [
    { label: 'From English', sources: fromEnglish },
    { label: 'Into English', sources: intoEnglish },
    { label: 'Other languages', sources: other },
  ].filter((g) => g.sources.length > 0);
}

/** The settings rows' shared shape (kept local: SettingsPage's own `Row`
 * isn't exported, and this section has a few rows of its own). */
function Row({
  title,
  description,
  children,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <div className="settings-row">
      <div className="settings-row-text">
        <span className="settings-row-title">{title}</span>
        {description && <span className="settings-row-description">{description}</span>}
      </div>
      {children && <div className="settings-row-control dictionary-controls">{children}</div>}
    </div>
  );
}

export function DictionarySettings({ isAdmin }: { isAdmin: boolean }) {
  const toast = useToast();
  const list = useResource(useCallback(() => listDictionaries(), []));
  const catalog = useResource<DictionaryCatalogResponse>(
    useCallback(() => getDictionaryCatalog(), []),
    [],
    isAdmin,
  );

  // The job being watched. Seeded from the list (a download started before
  // this page was opened), then kept fresh by polling while it runs.
  const [job, setJob] = useState<DictionaryJob | null>(null);
  const [dictionaries, setDictionaries] = useState<Dictionary[] | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const [pair, setPair] = useState('');
  const [busyId, setBusyId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<string | null>(null);
  const [removing, setRemoving] = useState<Dictionary | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);

  // Jobs seen running on this page: only those announce how they ended
  // (opening the page shouldn't toast about yesterday's download).
  const watched = useRef(new Set<string>());

  const dialogOpenRef = useRef(dialogOpen);
  useEffect(() => {
    dialogOpenRef.current = dialogOpen;
  }, [dialogOpen]);

  const shownJob = job ?? list.data?.job ?? null;
  const installed = useMemo(
    () => dictionaries ?? list.data?.dictionaries ?? [],
    [dictionaries, list.data],
  );
  const runningId = isRunning(shownJob) ? shownJob.id : null;
  useEffect(() => {
    if (runningId) watched.current.add(runningId);
  }, [runningId]);

  const finish = useCallback(
    (ended: DictionaryJob) => {
      if (!watched.current.has(ended.id)) return;
      watched.current.delete(ended.id);
      if (ended.phase === 'done') {
        setDialogOpen(false);
        toast({
          tone: 'success',
          message: `${ended.name} is ready. Select a word while reading to look it up.`,
          duration: 6000,
        });
      } else if (ended.phase === 'cancelled') {
        setDialogOpen(false);
        toast(`Stopped downloading ${ended.name}.`);
      } else if (ended.phase === 'failed' && !dialogOpenRef.current) {
        // An open dialog shows the reason itself.
        toast({ tone: 'error', message: `Couldn't install ${ended.name}: ${ended.error ?? ''}` });
      }
    },
    [toast],
  );

  // Poll while a job runs. Each tick refreshes the installed list too, so
  // the new dictionary appears the moment it's ready.
  useEffect(() => {
    if (!runningId) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {
      listDictionaries()
        .then((resp) => {
          if (cancelled) return;
          setDictionaries(resp.dictionaries);
          setJob(resp.job);
          if (resp.job && !isRunning(resp.job)) finish(resp.job);
          else timer = setTimeout(tick, POLL_MS);
        })
        .catch(() => {
          // A dropped request on a flaky link: try again shortly.
          if (!cancelled) timer = setTimeout(tick, POLL_MS * 4);
        });
    };
    timer = setTimeout(tick, POLL_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [runningId, finish]);

  const start = (source: DictionarySource) => {
    setStartError(null);
    installDictionary(source.id)
      .then(({ job: started }) => {
        watched.current.add(started.id);
        setJob(started);
        setDialogOpen(true);
      })
      .catch((e: unknown) => setStartError(message(e)));
  };

  const cancel = () => {
    cancelDictionaryInstall().catch((e: unknown) => setStartError(message(e)));
  };

  const toggle = (d: Dictionary, enabled: boolean) => {
    setBusyId(d.id);
    setRowError(null);
    setDictionaryEnabled(d.id, enabled)
      .then(({ dictionary }) =>
        setDictionaries(installed.map((x) => (x.id === dictionary.id ? dictionary : x))),
      )
      .catch((e: unknown) => setRowError(message(e)))
      .finally(() => setBusyId(null));
  };

  const confirmRemove = () => {
    if (!removing) return;
    setRemoveBusy(true);
    setRemoveError(null);
    removeDictionary(removing.id)
      .then(() => {
        setDictionaries(installed.filter((x) => x.id !== removing.id));
        toast(`Removed ${removing.name}.`);
        setRemoving(null);
      })
      .catch((e: unknown) => setRemoveError(message(e)))
      .finally(() => setRemoveBusy(false));
  };

  const installedIds = useMemo(() => new Set(installed.map((d) => d.id)), [installed]);
  const wordNet = catalog.data?.sources.find((s) => s.kind === 'wordnet');
  const pairGroups = useMemo(
    () => groupPairs((catalog.data?.sources ?? []).filter((s) => !installedIds.has(s.id))),
    [catalog.data, installedIds],
  );
  const chosenPair = catalog.data?.sources.find((s) => s.id === pair && !installedIds.has(s.id));
  const busy = isRunning(shownJob);

  return (
    <>
      {list.error && (
        <p className="error-text" role="alert">
          {list.error}
        </p>
      )}
      {list.loading && <p className="muted">Loading dictionaries…</p>}

      {list.data && installed.length === 0 && (
        <Row
          title="No dictionary installed"
          description={
            isAdmin
              ? 'Download one below to look words up while you read. Everything stays on this server.'
              : 'Ask an administrator to add one, so you can look words up while you read.'
          }
        />
      )}

      {installed.map((d) => (
        <Row
          key={d.id}
          title={
            <>
              {d.name} <span className="dictionary-version">{d.version}</span>
            </>
          }
          description={`${d.kind === 'wordnet' ? 'Definitions' : 'Translations'} · ${formatBytes(d.size_bytes)} · Added ${formatDate(d.installed_at)}`}
        >
          <Toggle
            label={`Use ${d.name}`}
            hideLabel
            checked={d.enabled}
            disabled={!isAdmin}
            busy={busyId === d.id}
            onChange={(next) => toggle(d, next)}
            testId={`dictionary-toggle-${d.id}`}
          />
          {isAdmin && (
            <button
              type="button"
              className="button ghost-button"
              onClick={() => {
                setRemoveError(null);
                setRemoving(d);
              }}
            >
              Remove
            </button>
          )}
        </Row>
      ))}
      {rowError && (
        <p className="error-text" role="alert">
          {rowError}
        </p>
      )}

      {isAdmin && busy && !dialogOpen && (
        <div className="settings-row dictionary-running" data-testid="dictionary-running">
          <div className="settings-row-text">
            <span className="settings-row-title">
              {shownJob.name}: {PHASE_LABELS[shownJob.phase]}
            </span>
            <ProgressBar job={shownJob} />
          </div>
          <div className="settings-row-control">
            <button
              type="button"
              className="button ghost-button"
              onClick={() => setDialogOpen(true)}
            >
              Show
            </button>
          </div>
        </div>
      )}

      {isAdmin && (
        <>
          {catalog.loading && <p className="muted">Checking what can be downloaded…</p>}
          {wordNet && !installedIds.has(wordNet.id) && (
            <Row
              title="WordNet"
              description={`${wordNet.description} ${formatBytes(wordNet.download_bytes)} download.`}
            >
              <button
                type="button"
                className="button primary-button"
                disabled={busy}
                onClick={() => start(wordNet)}
                data-testid="dictionary-download-wordnet"
              >
                Download
              </button>
            </Row>
          )}
          {catalog.data && (
            <Row
              title="FreeDict"
              description={
                catalog.data.freedict_error ? (
                  <span className="error-text">
                    Couldn't load FreeDict's dictionaries: {catalog.data.freedict_error}
                  </span>
                ) : chosenPair ? (
                  `${chosenPair.description}${chosenPair.headwords ? ` ${chosenPair.headwords.toLocaleString()} words.` : ''} ${formatBytes(chosenPair.download_bytes)} download.`
                ) : (
                  'Translation dictionaries between two languages. Pick a language pair.'
                )
              }
            >
              {catalog.data.freedict_error ? (
                <button type="button" className="button" onClick={catalog.reload}>
                  Retry
                </button>
              ) : (
                <>
                  <select
                    aria-label="FreeDict language pair"
                    value={chosenPair ? pair : ''}
                    onChange={(e) => setPair(e.target.value)}
                    disabled={busy}
                    className="dictionary-pair-select"
                  >
                    <option value="">Choose languages…</option>
                    {pairGroups.map((g) => (
                      <optgroup key={g.label} label={g.label}>
                        {g.sources.map((s) => (
                          <option key={s.id} value={s.id}>
                            {s.name.replace(/^FreeDict /, '')}
                          </option>
                        ))}
                      </optgroup>
                    ))}
                  </select>
                  <button
                    type="button"
                    className="button primary-button"
                    disabled={busy || !chosenPair}
                    onClick={() => chosenPair && start(chosenPair)}
                    data-testid="dictionary-download-freedict"
                  >
                    Download
                  </button>
                </>
              )}
            </Row>
          )}
          {catalog.error && (
            <p className="error-text" role="alert">
              {catalog.error}
            </p>
          )}
          {startError && (
            <p className="error-text" role="alert">
              {startError}
            </p>
          )}
        </>
      )}

      <Dialog
        open={dialogOpen && shownJob !== null}
        title={shownJob ? `Installing ${shownJob.name}` : 'Installing'}
        onClose={() => setDialogOpen(false)}
        testId="dictionary-install-dialog"
        footer={
          shownJob?.phase === 'failed' ? (
            <>
              <button type="button" className="button" onClick={() => setDialogOpen(false)}>
                Close
              </button>
              <button
                type="button"
                className="button primary-button"
                onClick={() => {
                  const source = catalog.data?.sources.find((s) => s.id === shownJob.dictionary_id);
                  if (source) start(source);
                }}
              >
                Try again
              </button>
            </>
          ) : (
            <>
              <button type="button" className="button" onClick={cancel} disabled={!busy}>
                Cancel download
              </button>
              <button
                type="button"
                className="button primary-button"
                onClick={() => setDialogOpen(false)}
              >
                Hide
              </button>
            </>
          )
        }
      >
        {shownJob && (
          <div className="dictionary-install" aria-live="polite">
            <p className="dictionary-install-phase">{PHASE_LABELS[shownJob.phase]}</p>
            {shownJob.phase === 'failed' ? (
              <p className="error-text" role="alert">
                {shownJob.error}
              </p>
            ) : (
              <>
                <ProgressBar job={shownJob} />
                <p className="muted dictionary-install-detail">{progressText(shownJob) ?? ' '}</p>
                <p className="dialog-hint">
                  It's saved on the server. You can hide this — the download carries on in the
                  background.
                </p>
              </>
            )}
          </div>
        )}
      </Dialog>

      <ConfirmDialog
        open={removing !== null}
        title={`Remove ${removing?.name ?? 'dictionary'}?`}
        destructive
        confirmLabel="Remove"
        busy={removeBusy}
        error={removeError}
        message={
          <p>
            Its files are deleted from the server. Words you've already saved keep their meanings.
          </p>
        }
        onCancel={() => setRemoving(null)}
        onConfirm={confirmRemove}
        testId="remove-dictionary-dialog"
      />
    </>
  );
}
