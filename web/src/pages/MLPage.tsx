/**
 * Machine learning — similarity and faces, on one page.
 *
 * Both capabilities are optional and both are *local*: the server embeds the
 * provider, nothing is uploaded anywhere, and everything here is derived data
 * that a pass can rebuild. A purge is therefore offered freely, and each
 * section explains what its pass will cost before you start it.
 *
 * The ML and face routes are only registered when the server was built with the
 * corresponding dependencies, and they answer 503 when the provider is
 * disabled. A 404 or 503 therefore means "not available here", not "broken", and
 * is rendered as an explanation rather than an error.
 *
 * Layout: one master-switch card on top, then Similar images and Faces side by
 * side (stacked on narrow screens). Feedback for an action appears inside the
 * card that triggered it, and destructive actions live in their own footer.
 */

import { useEffect, useRef, useState, type ReactNode } from 'react';

import { useAuth } from '../auth/authContext';
import { waitForFacePasses } from '../api/facePasses';
import { waitForSimilarityPass } from '../api/similarityPass';
import { openFaceModelDialog, useFaceModel } from '../api/faceModel';
import { updateMLSwitch, useMLSwitch } from '../api/mlSwitch';
import { ApiError } from '../api/client';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import {
  clusterFaces,
  getFaceStatus,
  getMLSettings,
  getMLStatus,
  purgeFaces,
  purgeSimilarity,
  runFacePass,
  runSimilarityPass,
  setBatchSize,
  setFaceThreshold,
} from '../api/queries';
import type { FaceStatus, MLStatus } from '../api/types';
import { ConfirmDialog } from '../components/Dialog';
import LibraryPicker from '../components/LibraryPicker';
import { Toggle } from '../components/ui/Toggle';
import { useToast } from '../components/ui/Toast';
import {
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import './MLPage.css';
import './MLPage.layout.css';

/** Which card a status message belongs to, so feedback shows where you acted. */
type Scope = 'switch' | 'similarity' | 'faces';

/** A pass is background work: the response says it started, nothing more. */
type Job = { scope: Scope; message: string; error: string | null } | null;

/**
 * A 404 means the build has no such route; a 503 means the provider is
 * switched off. Neither is a failure to report, so it is turned into *data*
 * here rather than thrown — the resource hook only keeps the message, so a
 * check that needs the status has to happen at the fetch boundary.
 */
type Section<T> = { available: true; status: T } | { available: false };

function asSection<T>(load: (libraryId: string) => Promise<T>) {
  return async (libraryId: string): Promise<Section<T>> => {
    try {
      return { available: true, status: await load(libraryId) };
    } catch (e: unknown) {
      if (e instanceof ApiError && (e.status === 404 || e.status === 503)) {
        return { available: false };
      }
      throw e;
    }
  };
}

const fmt = (n: number | undefined) => (n ?? 0).toLocaleString();

function StatTile({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint?: string | undefined;
}) {
  return (
    <div className="ml-stat">
      <span className="ml-stat-value">{value}</span>
      <span className="ml-stat-label">{label}</span>
      {hint && <span className="ml-stat-hint">{hint}</span>}
    </div>
  );
}

function Card({
  id,
  title,
  badge,
  children,
}: {
  id: string;
  title: string;
  badge?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="ml-section ml-card" aria-labelledby={id}>
      <div className="ml-section-head">
        <h2 id={id}>{title}</h2>
        {badge}
      </div>
      {children}
    </section>
  );
}

function AvailableBadge({ testId }: { testId: string }) {
  return (
    <span className="status-badge status-badge-online" data-testid={testId}>
      Available
    </span>
  );
}

/** Shown whenever the server reports a pass running or queued, regardless of
 * whether this page is the one that started it (another tab, or new photos
 * arriving automatically, can start one too). */
function RunningBadge({ label, testId }: { label: string; testId: string }) {
  return (
    <span className="status-badge status-badge-busy" role="status" data-testid={testId}>
      {label}
    </span>
  );
}

function JobStatus({ job, scope, onDismiss }: { job: Job; scope: Scope; onDismiss: () => void }) {
  if (!job || job.scope !== scope) return null;
  return (
    <div className="ml-status">
      <p className={job.error ? 'error-text' : 'muted ml-job'} role="status">
        {job.error ?? job.message}
      </p>
      <button type="button" className="link-button" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  );
}

function DangerZone({
  text,
  label,
  onClick,
  disabled,
  title,
  testId,
}: {
  text: string;
  label: string;
  onClick: () => void;
  disabled: boolean;
  title?: string | undefined;
  testId: string;
}) {
  return (
    <div className="ml-block ml-danger">
      <p className="ml-note">{text}</p>
      <button
        type="button"
        className="button danger-button"
        onClick={onClick}
        disabled={disabled}
        title={title}
        data-testid={testId}
      >
        {label}
      </button>
    </div>
  );
}

export default function MLPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const toast = useToast();

  const [busy, setBusy] = useState<string | null>(null);
  const [job, setJob] = useState<Job>(null);
  const mlOn = useMLSwitch();
  const { status: modelStatus } = useFaceModel();
  const [switching, setSwitching] = useState(false);
  const [threshold, setThreshold] = useState<{ value: number; saved: number; def: number } | null>(
    null,
  );
  const [savingThreshold, setSavingThreshold] = useState(false);
  const [batch, setBatch] = useState<{ value: string; saved: number } | null>(null);
  const [savingBatch, setSavingBatch] = useState(false);
  const [purging, setPurging] = useState<'similarity' | 'faces' | null>(null);

  // Face-pass polling outlives the request that started it; stop it when the
  // page goes away so it does not keep hitting the API.
  const unmounted = useRef(new AbortController());
  useEffect(() => {
    const controller = new AbortController();
    unmounted.current = controller;
    return () => controller.abort();
  }, []);

  // The loaders are inline: `useLibraryResource` reads them through a ref, so a
  // fresh arrow on every render is free and this stays lint-clean.
  const status = useLibraryResource<Section<MLStatus>>((id) => asSection(getMLStatus)(id));
  // While ML is off the face routes answer 503, which the browser logs as an
  // error; there is nothing to read, so do not ask.
  const faces = useLibraryResource<Section<FaceStatus>>(
    (id) => asSection(getFaceStatus)(id),
    [mlOn],
    mlOn !== false,
  );

  useEffect(() => {
    getMLSettings().then(
      (m) => {
        setThreshold({
          value: m.face_threshold,
          saved: m.face_threshold,
          def: m.face_threshold_default,
        });
        setBatch({ value: String(m.batch_size), saved: m.batch_size });
      },
      () => setThreshold(null),
    );
  }, []);

  // `null` means "still loading", which is available enough not to flash the
  // "not available" explanation before the answer arrives.
  const similarityAvailable = status.data === null || status.data.available;
  const facesAvailable = faces.data === null || faces.data.available;
  const ml = status.data?.available ? status.data.status : null;
  const face = faces.data?.available ? faces.data.status : null;
  const simTotal = (ml?.signatured ?? 0) + (ml?.pending ?? 0);

  // A pass can start without this page's involvement — another tab, or new
  // photos arriving automatically — and it only progresses on the server, so
  // this page has to re-read the status to show it moving: the stats, the
  // running badge, and the progress bar all come from here while one runs.
  const libraryId = gate.kind === 'ready' ? gate.libraryId : null;
  useEffect(() => {
    if (!libraryId || !ml?.running) return;
    const interval = setInterval(() => status.reload(), 1000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [libraryId, ml?.running]);
  useEffect(() => {
    if (!libraryId || !face?.running) return;
    const controller = new AbortController();
    waitForFacePasses(libraryId, controller.signal).then(() => {
      if (!controller.signal.aborted) faces.reload();
    });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [libraryId, face?.running]);

  const report = (scope: Scope, message: string) => setJob({ scope, message, error: null });
  const fail = (scope: Scope, e: unknown) =>
    setJob({ scope, message: '', error: e instanceof Error ? e.message : String(e) });
  const dismiss = () => setJob(null);

  const saveBatch = (n: number) => {
    setSavingBatch(true);
    setBatchSize(n)
      .then((m) => {
        setBatch({ value: String(m.batch_size), saved: m.batch_size });
        report(
          'switch',
          `Saved. The models will run whenever ${m.batch_size === 1 ? 'a new image is' : `${m.batch_size} new images are`} waiting.`,
        );
        faces.reload();
      })
      .catch((e: unknown) => fail('switch', e))
      .finally(() => setSavingBatch(false));
  };

  const saveThreshold = (value: number) => {
    setSavingThreshold(true);
    setFaceThreshold(value)
      .then((m) => {
        setThreshold({
          value: m.face_threshold,
          saved: m.face_threshold,
          def: m.face_threshold_default,
        });
        report('faces', 'Matching threshold saved. Regrouping people…');
        if (gate.kind !== 'ready') return;
        return waitForFacePasses(gate.libraryId, unmounted.current.signal).then(() => {
          if (unmounted.current.signal.aborted) return;
          faces.reload();
          report('faces', 'Matching threshold saved and people regrouped.');
        });
      })
      .catch((e: unknown) => fail('faces', e))
      .finally(() => setSavingThreshold(false));
  };

  const toggleML = (enabled: boolean) => {
    setSwitching(true);
    report('switch', enabled ? 'Starting…' : 'Stopping…');
    updateMLSwitch(enabled)
      .then(() => {
        report(
          'switch',
          enabled
            ? 'Machine learning is on. Your library is being scanned for people now; new photos are handled as they arrive.'
            : 'Machine learning is off. Existing people and names are kept.',
        );
        status.reload();
        faces.reload();
      })
      .catch((e: unknown) => fail('switch', e))
      .finally(() => setSwitching(false));
  };

  const run = (
    scope: Scope,
    key: string,
    action: (libraryId: string) => Promise<unknown>,
    message: string,
    /** Called once the background work this started has actually finished —
     * not the purge/toggle actions that use this same scope, which have
     * nothing to wait for. Used for the "pass finished" toast. */
    onFinished?: (finalStatus: MLStatus | null) => void,
  ) => {
    if (gate.kind !== 'ready') return;
    setBusy(key);
    report(scope, 'Working…');
    action(gate.libraryId)
      .then(async () => {
        report(scope, message);
        // Passes run in the background: refresh the counters once they are
        // done rather than showing a half-finished number. Both resources
        // are reloaded regardless of scope because a similarity run and a
        // purge can each change what the other card shows next (e.g. the
        // master switch affects both).
        status.reload();
        faces.reload();
        if (scope === 'similarity') {
          const finalStatus = await waitForSimilarityPass(gate.libraryId, unmounted.current.signal);
          if (unmounted.current.signal.aborted) return;
          status.reload();
          onFinished?.(finalStatus);
        } else {
          await waitForFacePasses(gate.libraryId, unmounted.current.signal);
          if (unmounted.current.signal.aborted) return;
          faces.reload();
        }
      })
      .catch((e: unknown) => fail(scope, e))
      .finally(() => setBusy(null));
  };

  if (gate.kind === 'loading') {
    return (
      <main className="page ml-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="Machine learning"
      subtitle="Derived data for finding similar photos and the people in them. Everything is computed on this machine and can be discarded at any time."
      controls={<LibraryPicker />}
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page ml-page">
        {header}
        <ErrorState message={gate.message} onRetry={status.reload} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page ml-page">
        {header}
        <NoLibrariesState isAdmin={isAdmin} />
      </main>
    );
  }

  const offline = gate.library.status === 'offline';
  const locked = offline || busy !== null;
  const lockReason = offline ? 'This library is offline.' : undefined;

  return (
    <main className="page ml-page">
      {header}

      {offline && <LibraryOfflineNotice library={gate.library} />}

      {/* ---------------------------- master switch ------------------------- */}
      <Card
        id="ml-switch-title"
        title="Recognise people automatically"
        badge={
          mlOn !== null && (
            <span className={mlOn ? 'status-badge status-badge-online' : 'status-badge'}>
              {mlOn ? 'On' : 'Off'}
            </span>
          )
        }
      >
        <p className="muted">
          When on, Cairn works through your library straight away and handles each new photo as it
          arrives. People start as "Person 1", "Person 2" and so on — rename one and Cairn will
          recognise them by that name in future photos. When off, nothing is analysed and the People
          page is hidden; what was found is kept.
        </p>
        <Toggle
          checked={mlOn === true}
          onChange={toggleML}
          label="Find people and similar photos as images are added"
          disabled={mlOn === null || !isAdmin}
          busy={switching}
          testId="ml-switch"
        />
        {!isAdmin && <p className="ml-note">Only an administrator can change these settings.</p>}
        <JobStatus job={job} scope="switch" onDismiss={dismiss} />

        {batch && (
          <div className="ml-block ml-batch ml-field" data-testid="ml-batch">
            <label htmlFor="ml-batch-input">New photos to wait for before analysing</label>
            <div className="ml-field-row">
              <input
                id="ml-batch-input"
                type="number"
                min={1}
                max={10000}
                step={1}
                value={batch.value}
                disabled={!isAdmin || savingBatch}
                onChange={(e) => setBatch({ ...batch, value: e.target.value })}
              />
              <button
                type="button"
                className="button"
                disabled={
                  !isAdmin ||
                  savingBatch ||
                  !Number.isInteger(Number(batch.value)) ||
                  Number(batch.value) < 1 ||
                  Number(batch.value) === batch.saved
                }
                onClick={() => saveBatch(Number(batch.value))}
              >
                {savingBatch ? 'Saving…' : 'Save'}
              </button>
            </div>
            <p className="ml-note">
              With 1, every new photo is analysed as soon as Cairn notices it. With 10, Cairn waits
              until ten have piled up, then analyses them together. Photos count however they arrive
              — uploaded, or moved into the folder with a file manager, even while Cairn was
              stopped.
              {face?.pending !== undefined &&
                ` Waiting now: ${face.pending} photo${face.pending === 1 ? '' : 's'}.`}
            </p>
          </div>
        )}
      </Card>

      <div className="ml-grid">
        {/* ---------------------------- similarity --------------------------- */}
        <Card
          id="ml-similarity-title"
          title="Similar images"
          badge={
            ml?.running ? (
              <RunningBadge label="Scanning…" testId="ml-running" />
            ) : (
              ml?.enabled && <AvailableBadge testId="ml-enabled" />
            )
          }
        >
          {!similarityAvailable ? (
            <p className="muted" data-testid="ml-unavailable">
              This Cairn server was built without local image embeddings, so visual similarity is
              not available. Everything else — including duplicate detection, which compares file
              bytes — still works.
            </p>
          ) : (
            <>
              {status.loading && <LoadingState label="Reading ML status…" />}
              {status.error && <ErrorState message={status.error} onRetry={status.reload} />}
              {ml && (
                <>
                  <div className="ml-stats" data-testid="ml-stats">
                    <StatTile label="Signatures" value={fmt(ml.signatured)} />
                    <StatTile
                      label="Pending"
                      value={fmt(ml.pending)}
                      hint={ml.pending ? 'A pass will pick these up' : undefined}
                    />
                    <StatTile
                      label="Last pass"
                      value={ml.last_pass_at ? new Date(ml.last_pass_at).toLocaleString() : 'Never'}
                      hint={ml.provider ? `Provider ${ml.provider}` : undefined}
                    />
                  </div>
                  {ml.running ? (
                    <div className="ml-progress" data-testid="ml-progress">
                      <progress
                        value={ml.signatured ?? 0}
                        max={simTotal}
                        aria-label="Photos analysed"
                      />
                      <span className="ml-note" role="status">
                        Scanning — {fmt(ml.signatured)} of {fmt(simTotal)} photos analysed so far.
                      </span>
                    </div>
                  ) : ml.pending ? (
                    <p className="ml-note" data-testid="ml-waiting">
                      {fmt(ml.pending)} photo{ml.pending === 1 ? '' : 's'} waiting for a pass.
                    </p>
                  ) : null}
                  {ml.error && (
                    <p className="error-text" role="alert">
                      The last pass reported: {ml.error}
                    </p>
                  )}
                </>
              )}

              <div className="ml-actions">
                <button
                  type="button"
                  className="button primary-button"
                  onClick={() =>
                    run(
                      'similarity',
                      'similarity-run',
                      runSimilarityPass,
                      'Similarity pass started in the background.',
                      (finalStatus) =>
                        toast({
                          message: finalStatus
                            ? `Similarity pass finished — ${fmt(finalStatus.signatured)} photo${finalStatus.signatured === 1 ? '' : 's'} analysed.`
                            : 'Similarity pass finished.',
                          tone: 'success',
                        }),
                    )
                  }
                  disabled={locked || ml?.enabled === false || ml?.running}
                  title={ml?.running ? 'A pass is already running.' : lockReason}
                  data-testid="run-similarity-pass"
                >
                  {busy === 'similarity-run'
                    ? 'Starting…'
                    : ml?.running
                      ? 'Running…'
                      : 'Run similarity pass'}
                </button>
              </div>
              <JobStatus job={job} scope="similarity" onDismiss={dismiss} />
              <p className="ml-note">
                A pass reads every image and derives a small perceptual signature from it. It is CPU
                work proportional to the number of photos, and closing the server stops it safely —
                nothing is lost, only unfinished.
              </p>

              <DangerZone
                text="Signatures can always be rebuilt. Your photos are never touched."
                label={busy === 'similarity-purge' ? 'Discarding…' : 'Discard signatures'}
                onClick={() => setPurging('similarity')}
                disabled={locked || ml?.enabled === false}
                title={lockReason}
                testId="purge-similarity"
              />
            </>
          )}
        </Card>

        {/* ------------------------------ faces ------------------------------ */}
        <Card
          id="ml-faces-title"
          title="Faces and people"
          badge={
            face?.running ? (
              <RunningBadge label="Working…" testId="faces-running" />
            ) : (
              face?.enabled && <AvailableBadge testId="faces-enabled" />
            )
          }
        >
          {mlOn === false ? (
            <p className="muted" data-testid="faces-off">
              Machine learning is off. Turn it on above to detect faces and group them into people.
            </p>
          ) : !facesAvailable ? (
            <p className="muted" data-testid="faces-unavailable">
              This Cairn server was built without face recognition. People, albums, and tags all
              still work — this only affects automatic face detection and clustering.
            </p>
          ) : (
            <>
              {modelStatus && !modelStatus.installed && (
                <p className="settings-warning" role="alert" data-testid="face-model-warning">
                  The face-recognition model is not installed, so people are matched with a basic
                  method that groups poorly.{' '}
                  <button type="button" className="link-button" onClick={openFaceModelDialog}>
                    {modelStatus.state === 'downloading' ? 'Show download progress' : 'Download it'}
                  </button>
                </p>
              )}
              {faces.loading && <LoadingState label="Reading face status…" />}
              {faces.error && <ErrorState message={faces.error} onRetry={faces.reload} />}
              {face && (
                <>
                  <div className="ml-stats" data-testid="face-stats">
                    <StatTile label="Faces found" value={fmt(face.faces)} />
                    <StatTile label="People" value={fmt(face.people)} />
                    <StatTile
                      label="Unassigned"
                      value={fmt(face.unassigned)}
                      hint={
                        face.unassigned ? 'Name these on the People page' : 'Every face has a name'
                      }
                    />
                  </div>
                  {face.running ? (
                    <p className="ml-note" role="status" data-testid="faces-working">
                      Working — detecting and grouping faces now.
                      {face.pending
                        ? ` ${fmt(face.pending)} photo${face.pending === 1 ? '' : 's'} left.`
                        : ''}
                    </p>
                  ) : face.pending ? (
                    <p className="ml-note" data-testid="faces-waiting">
                      {fmt(face.pending)} photo{face.pending === 1 ? '' : 's'} waiting for
                      detection.
                    </p>
                  ) : null}
                </>
              )}

              <div className="ml-actions">
                <button
                  type="button"
                  className="button primary-button"
                  onClick={() =>
                    run('faces', 'detect', runFacePass, 'Face detection pass started.')
                  }
                  disabled={locked || face?.enabled === false || face?.running}
                  title={face?.running ? 'A pass is already running.' : lockReason}
                  data-testid="run-face-pass"
                >
                  {busy === 'detect' ? 'Starting…' : face?.running ? 'Running…' : 'Detect faces'}
                </button>
                <button
                  type="button"
                  className="button"
                  onClick={() =>
                    run('faces', 'cluster', clusterFaces, 'Face clustering pass started.')
                  }
                  disabled={locked || face?.enabled === false || !face?.faces || face?.running}
                  title={
                    face?.running
                      ? 'A pass is already running.'
                      : face?.faces
                        ? lockReason
                        : 'Detect faces first.'
                  }
                  data-testid="run-face-cluster"
                >
                  {busy === 'cluster' ? 'Starting…' : face?.running ? 'Running…' : 'Group faces'}
                </button>
              </div>
              <JobStatus job={job} scope="faces" onDismiss={dismiss} />
              <p className="ml-note">
                Detection finds the faces in each photo; grouping proposes clusters you then name on
                the People page. Names you have given are kept when either is re-run.
              </p>

              {threshold && (
                <div className="ml-block ml-threshold ml-field" data-testid="face-threshold">
                  <label htmlFor="face-threshold-input">
                    Matching strictness: <strong>{threshold.value.toFixed(2)}</strong>
                  </label>
                  <input
                    id="face-threshold-input"
                    type="range"
                    min={0.2}
                    max={0.95}
                    step={0.01}
                    value={threshold.value}
                    disabled={!isAdmin || savingThreshold}
                    onChange={(e) => setThreshold({ ...threshold, value: Number(e.target.value) })}
                  />
                  <div className="ml-threshold-scale muted">
                    <span>Looser — groups more faces together</span>
                    <span>Stricter — splits similar faces apart</span>
                  </div>
                  <p className="ml-note">
                    If different people end up in one group, make it stricter; if one person is
                    split into several, make it looser. Saving regroups the people Cairn made;
                    people you named and faces you placed by hand stay as they are.
                    {!isAdmin && ' Only an administrator can change this.'}
                  </p>
                  <div className="ml-actions">
                    <button
                      type="button"
                      className="button"
                      onClick={() => saveThreshold(threshold.value)}
                      disabled={!isAdmin || savingThreshold || threshold.value === threshold.saved}
                    >
                      {savingThreshold ? 'Saving…' : 'Save'}
                    </button>
                    <button
                      type="button"
                      className="button"
                      onClick={() => saveThreshold(0)}
                      disabled={!isAdmin || savingThreshold || threshold.saved === threshold.def}
                    >
                      Reset to {threshold.def.toFixed(2)}
                    </button>
                  </div>
                </div>
              )}

              <DangerZone
                text="Removes detected faces and their assignments. Names you have given are kept, and detection rebuilds the rest."
                label={busy === 'faces-purge' ? 'Discarding…' : 'Discard face data'}
                onClick={() => setPurging('faces')}
                disabled={locked || face?.enabled === false}
                title={lockReason}
                testId="purge-faces"
              />
            </>
          )}
        </Card>
      </div>

      <ConfirmDialog
        open={purging !== null}
        title={purging === 'faces' ? 'Discard all face data?' : 'Discard all signatures?'}
        destructive
        confirmLabel="Discard"
        busy={busy !== null}
        error={job?.error ?? null}
        message={
          <p>
            {purging === 'faces'
              ? 'Every detected face and its assignment to a person is removed. Names you have given are kept, and a new detection pass rebuilds the rest. Your photos are not touched.'
              : 'Every derived image signature is removed. Similar-image search stops working until a pass runs again. Your photos are not touched.'}
          </p>
        }
        onCancel={() => setPurging(null)}
        onConfirm={() => {
          const which = purging;
          if (!which) return;
          run(
            which,
            which === 'faces' ? 'faces-purge' : 'similarity-purge',
            which === 'faces' ? purgeFaces : purgeSimilarity,
            which === 'faces' ? 'Face data discarded.' : 'Signatures discarded.',
          );
          setPurging(null);
        }}
        testId="ml-purge-dialog"
      />
    </main>
  );
}
