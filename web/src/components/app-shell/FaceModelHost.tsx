/**
 * Offers to download the face-recognition model when it is missing.
 *
 * On sign-in an administrator who has no model sees a dialog explaining what
 * will be downloaded; "Download" starts it on the server, and the dialog turns
 * into a progress bar. The dialog can be closed at any time — the download is
 * server-side and goes on — and a toast announces the result wherever the
 * person happens to be.
 */

import { useEffect, useRef } from 'react';

import {
  beginFaceModelDownload,
  closeFaceModelDialog,
  openFaceModelDialog,
  refreshFaceModel,
  useFaceModel,
} from '../../api/faceModel';
import { useMLSwitch } from '../../api/mlSwitch';
import { useAuth } from '../../auth/authContext';
import { Dialog } from '../Dialog';
import { useToast } from '../ui/Toast';

function mb(bytes: number): string {
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function FaceModelHost() {
  const { user } = useAuth();
  const toast = useToast();
  const { status, dialogOpen } = useFaceModel();
  const isAdmin = user?.role === 'admin';
  // The model is only needed (and only offered) while machine learning is on.
  const mlOn = useMLSwitch();
  const previous = useRef<string | null>(null);

  // Once ML is on: find out whether the model is there, and ask
  // if not. Turning ML on later asks then.
  useEffect(() => {
    if (!isAdmin || mlOn !== true) return;
    void refreshFaceModel().then((s) => {
      if (!s || s.installed || s.state === 'downloading') return;
      openFaceModelDialog();
    });
  }, [isAdmin, mlOn]);

  // The toast fires on the transition, not on the dialog: it must appear even
  // if the person closed the dialog and moved on.
  useEffect(() => {
    const now = status?.state ?? null;
    const was = previous.current;
    previous.current = now;
    if (was !== 'downloading') return;
    if (now === 'done') {
      toast({
        message:
          'Face-recognition models downloaded. People will now be matched with them — Cairn is re-scanning your photos.',
        tone: 'success',
        duration: 8000,
      });
    } else if (now === 'error') {
      toast({
        message: `Face-recognition model download failed${status?.error ? `: ${status.error}` : '.'}`,
        tone: 'error',
        duration: 8000,
      });
    }
  }, [status, toast]);

  if (!isAdmin || !status) return null;

  const downloading = status.state === 'downloading';
  const pct = status.total > 0 ? Math.min(100, (status.downloaded / status.total) * 100) : null;

  let body;
  let footer;
  if (downloading) {
    body = (
      <>
        <p>Downloading the face-recognition model…</p>
        <div
          className="model-progress"
          role="progressbar"
          aria-label="Model download progress"
          aria-valuemin={0}
          aria-valuemax={100}
          {...(pct !== null ? { 'aria-valuenow': Math.round(pct) } : {})}
        >
          <div
            className={pct === null ? 'model-progress-bar indeterminate' : 'model-progress-bar'}
            style={pct === null ? undefined : { width: `${pct}%` }}
          />
        </div>
        <p className="muted">
          {mb(status.downloaded)}
          {status.total > 0 ? ` of ${mb(status.total)} (${Math.round(pct ?? 0)}%)` : ''}
        </p>
        <p className="muted">
          You can close this: the download carries on in the background and you will get a message
          when it finishes.
        </p>
      </>
    );
    footer = (
      <button type="button" className="button" onClick={closeFaceModelDialog}>
        Close
      </button>
    );
  } else if (status.state === 'done' || status.installed) {
    body = <p>The face-recognition model is installed.</p>;
    footer = (
      <button type="button" className="button primary-button" onClick={closeFaceModelDialog}>
        Done
      </button>
    );
  } else {
    body = (
      <>
        {status.state === 'error' && (
          <p className="error-text" role="alert">
            The last attempt failed{status.error ? `: ${status.error}` : '.'}
          </p>
        )}
        <p>
          To match the same person across your photos, Cairn needs two face-recognition models
          (SCRFD detector + ArcFace R50, about 180&nbsp;MB total). They will be downloaded once from{' '}
          <code>{status.url ? new URL(status.url).host : 'huggingface.co'}</code> and kept on this
          server; your photos are never uploaded.
        </p>
        <p className="muted">The models’ licence allows non-commercial research use only.</p>
      </>
    );
    footer = (
      <>
        <button type="button" className="button" onClick={closeFaceModelDialog}>
          Not now
        </button>
        <button
          type="button"
          className="button primary-button"
          onClick={() => void beginFaceModelDownload()}
          data-testid="face-model-download"
        >
          {status.state === 'error' ? 'Try again' : 'OK, download'}
        </button>
      </>
    );
  }

  return (
    <Dialog
      open={dialogOpen}
      title="Download face-recognition model"
      onClose={closeFaceModelDialog}
      footer={footer}
      testId="face-model-dialog"
    >
      {body}
    </Dialog>
  );
}
