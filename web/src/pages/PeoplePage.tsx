/**
 * People — the face grouping for a library.
 *
 * Face recognition is optional on the server, so this page has to be honest
 * about three very different states: faces turned off (the `/ml/faces` route
 * 503s), faces unavailable in this build (the route does not exist and 404s),
 * and faces working but empty. All three used to render as the same red
 * sentence with an HTTP status in it.
 *
 * Every destructive action is a dialog rather than `window.confirm`, and the
 * merge target is chosen from a list instead of being pasted as a raw id.
 */

import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { ApiError } from '../api/client';
import { waitForFacePasses } from '../api/facePasses';
import { useLibraryGate } from '../api/libraries';
import { mlWorthAsking } from '../api/mlSwitch';
import { useLibraryResource } from '../api/resources';
import {
  assignFace,
  clusterFaces,
  createPerson,
  deleteFace,
  deletePerson,
  faceImageUrl,
  getFaceStatus,
  getPerson,
  listPeople,
  listUnassignedFaces,
  mergePeople,
  purgeFaces,
  renamePerson,
  runFacePass,
  setPersonCover,
  unassignFace,
} from '../api/queries';
import type { FaceStatus, FaceSummary, Person } from '../api/types';
import { ConfirmDialog, Dialog, PromptDialog } from '../components/Dialog';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import { Icon } from '../components/ui/Icon';
import { Menu, useMenuButton } from '../components/ui/Menu';
import { useToast } from '../components/ui/Toast';
import './PeoplePage.css';

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Everything the page renders, fetched together so the counts agree. */
interface PeopleData {
  status: FaceStatus;
  people: Person[];
  unassigned: FaceSummary[];
  /** Set when the server build has no face routes at all. */
  unsupported: boolean;
}

async function loadPeople(libraryId: string): Promise<PeopleData> {
  let status: FaceStatus;
  // ML off: the routes would answer 503 (logged by the browser as an error),
  // and the answer is already known.
  if (!(await mlWorthAsking())) {
    return {
      status: {
        enabled: false,
        provider: '',
        provider_version: 0,
        faces: 0,
        people: 0,
        unassigned: 0,
      },
      people: [],
      unassigned: [],
      unsupported: false,
    };
  }
  try {
    status = await getFaceStatus(libraryId);
  } catch (e: unknown) {
    // 503 = ML compiled in but disabled; 404 = this build has no face
    // routes. Both mean "there is nothing to show", not "something broke".
    if (e instanceof ApiError && (e.status === 404 || e.status === 503)) {
      return {
        status: {
          enabled: false,
          provider: '',
          provider_version: 0,
          faces: 0,
          people: 0,
          unassigned: 0,
        },
        people: [],
        unassigned: [],
        unsupported: e.status === 404,
      };
    }
    throw e;
  }

  if (!status.enabled) {
    return { status, people: [], unassigned: [], unsupported: false };
  }

  const [peopleResp, facesResp] = await Promise.all([
    listPeople(libraryId),
    // The unassigned pool is optional: an older build may expose people but
    // not the standalone faces listing.
    listUnassignedFaces(libraryId).catch(() => ({ faces: [] as FaceSummary[] })),
  ]);

  return {
    status,
    people: peopleResp.people ?? [],
    unassigned: facesResp.faces ?? [],
    unsupported: false,
  };
}

/** Drag payload type: one person's card, dropped on another to merge them. */
const PERSON_DRAG = 'application/x-cairn-person';
/** Which person is being dragged (dataTransfer contents are unreadable mid-drag). */
const dragging: { current: string | null } = { current: null };

interface PersonCardProps {
  libraryId: string;
  person: Person;
  expanded: boolean;
  onToggle: () => void;
  onRename: () => void;
  onMerge: () => void;
  /** Another person's card was dropped on this one. */
  onDropPerson: (sourceId: string) => void;
  onDelete: () => void;
  onUnassign: (faceId: string) => void;
  onSetCover: (faceId: string) => void;
  /** Everyone, for the "Move to…" choice on each face. */
  people: Person[];
  onMove: (faceId: string, personId: string) => void;
  onMoveToNew: (faceId: string) => void;
  onDeleteFace: (faceId: string) => void;
}

/** Option value of the picker entry that starts a new person. */
const NEW_PERSON = '__new__';

interface FacePersonPickerProps {
  people: Person[];
  /** The person the face belongs to now, left out of the choices. */
  currentId?: string;
  placeholder: string;
  onPick: (personId: string) => void;
  onNewPerson: () => void;
}

/** Choose who a face is: someone already known, or a new person. */
function FacePersonPicker({
  people,
  currentId,
  placeholder,
  onPick,
  onNewPerson,
}: FacePersonPickerProps) {
  const choices = people.filter((p) => p.id !== currentId);
  return (
    <select
      aria-label={placeholder}
      value=""
      onChange={(event) => {
        const value = event.target.value;
        if (value === NEW_PERSON) onNewPerson();
        else if (value) onPick(value);
      }}
    >
      <option value="">{placeholder}</option>
      {choices.map((person) => (
        <option key={person.id} value={person.id}>
          {person.name || 'Unnamed person'}
        </option>
      ))}
      <option value={NEW_PERSON}>New person…</option>
    </select>
  );
}

function PersonCard({
  libraryId,
  person,
  expanded,
  onToggle,
  onRename,
  onMerge,
  onDropPerson,
  onDelete,
  onUnassign,
  onSetCover,
  people,
  onMove,
  onMoveToNew,
  onDeleteFace,
}: PersonCardProps) {
  const [dropActive, setDropActive] = useState(false);
  // A person's faces are only fetched when their card is open, so a library with
  // thousands of people does not fetch thousands of image lists. The result is
  // tagged with the person it belongs to, which keeps a re-opened card from
  // re-fetching and lets the render derive loading/error from a comparison
  // instead of a flag flipped around the request.
  const [request, setRequest] = useState<
    { key: string; faces: FaceSummary[] } | { key: string; error: string } | null
  >(null);

  const personId = person.id;
  // Keyed on the face count too, so moving a face in or out re-reads the list.
  const requestKey = `${personId}:${person.face_count}`;
  const settled = request?.key === requestKey ? request : null;
  const needsFetch = expanded && settled === null;

  useEffect(() => {
    if (!needsFetch) return;
    let cancelled = false;
    getPerson(libraryId, personId)
      .then((resp) => {
        if (!cancelled) setRequest({ key: requestKey, faces: resp.faces ?? [] });
      })
      .catch((e: unknown) => {
        if (!cancelled) setRequest({ key: requestKey, error: message(e) });
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, needsFetch, personId, requestKey]);

  const menu = useMenuButton();
  const faces = settled !== null && 'faces' in settled ? settled.faces : null;
  const facesError = settled !== null && 'error' in settled ? settled.error : null;

  const label = person.name || 'Unnamed person';

  return (
    <article
      className={dropActive ? 'person-card is-drop-target' : 'person-card'}
      data-testid={`person-${person.id}`}
      draggable
      onDragStart={(e) => {
        dragging.current = person.id;
        e.dataTransfer.setData(PERSON_DRAG, person.id);
        e.dataTransfer.effectAllowed = 'move';
      }}
      onDragOver={(e) => {
        // Only another person's card can be dropped here, never onto itself.
        if (!e.dataTransfer.types.includes(PERSON_DRAG) || dragging.current === person.id) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        setDropActive(true);
      }}
      onDragEnd={() => {
        dragging.current = null;
      }}
      onDragLeave={() => setDropActive(false)}
      onDrop={(e) => {
        setDropActive(false);
        const source = e.dataTransfer.getData(PERSON_DRAG);
        if (!source || source === person.id) return;
        e.preventDefault();
        onDropPerson(source);
      }}
    >
      <Link
        to={`/search?person=${person.id}`}
        draggable={false}
        className="person-face"
        aria-label={`${label}, ${person.face_count} ${person.face_count === 1 ? 'photo' : 'photos'}`}
      >
        {person.cover_face_id ? (
          <img
            src={faceImageUrl(libraryId, person.cover_face_id)}
            alt=""
            loading="lazy"
            draggable={false}
          />
        ) : (
          <span className="person-face-fallback" aria-hidden="true">
            {person.name ? person.name.slice(0, 1).toUpperCase() : <Icon name="person" size={32} />}
          </span>
        )}
      </Link>
      <div className="person-meta">
        {person.name ? (
          <span className="person-name">{person.name}</span>
        ) : (
          <button type="button" className="link-button person-name" onClick={onRename}>
            Add a name
          </button>
        )}
        <span className="person-count">
          {person.face_count} {person.face_count === 1 ? 'photo' : 'photos'}
        </span>
      </div>
      <button
        type="button"
        className="icon-button person-options"
        aria-label={`Options for ${label}`}
        aria-haspopup="menu"
        aria-expanded={menu.open}
        onClick={menu.toggle}
      >
        <Icon name="more" />
      </button>
      {menu.anchor && (
        <Menu
          anchor={menu.anchor}
          align="end"
          label={`Options for ${label}`}
          onClose={menu.close}
          items={[
            { id: 'faces', label: 'Manage faces', icon: 'person', onSelect: onToggle },
            { id: 'rename', label: 'Rename', icon: 'edit', onSelect: onRename },
            { id: 'merge', label: 'Merge into…', icon: 'people', onSelect: onMerge },
            'separator',
            { id: 'delete', label: 'Delete', icon: 'trash', danger: true, onSelect: onDelete },
          ]}
        />
      )}

      <Dialog
        open={expanded}
        title={`Faces of ${label}`}
        onClose={onToggle}
        size="medium"
        testId={`person-faces-${person.id}`}
      >
        {settled === null && <LoadingState label="Loading faces…" />}
        {facesError && (
          <p className="error-text" role="alert">
            {facesError}
          </p>
        )}
        {faces !== null && faces.length === 0 && <p className="muted">No faces yet.</p>}
        {faces !== null && faces.length > 0 && (
          <div className="face-grid">
            {faces.map((face) => (
              <figure className="face-tile" key={face.id}>
                <img src={faceImageUrl(libraryId, face.id)} alt="" loading="lazy" />
                <figcaption>
                  <button
                    type="button"
                    className="button button-sm"
                    onClick={() => onSetCover(face.id)}
                    disabled={person.cover_face_id === face.id}
                  >
                    {person.cover_face_id === face.id ? 'Cover' : 'Set cover'}
                  </button>
                  <FacePersonPicker
                    people={people}
                    currentId={person.id}
                    placeholder="Move to…"
                    onPick={(to) => onMove(face.id, to)}
                    onNewPerson={() => onMoveToNew(face.id)}
                  />
                  <button
                    type="button"
                    className="button ghost-button button-sm"
                    onClick={() => onUnassign(face.id)}
                  >
                    Not {label}
                  </button>
                  <button
                    type="button"
                    className="button ghost-button button-sm"
                    onClick={() => onDeleteFace(face.id)}
                    title="Delete this face. Cairn will not suggest it again; the photo is kept."
                  >
                    Delete face
                  </button>
                </figcaption>
              </figure>
            ))}
          </div>
        )}
      </Dialog>
    </article>
  );
}

export default function PeoplePage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const [searchParams, setSearchParams] = useSearchParams();

  const selected = searchParams.get('person');

  const data = useLibraryResource<PeopleData>(loadPeople);

  const [expanded, setExpanded] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const toast = useToast();

  const [renaming, setRenaming] = useState<Person | null>(null);
  const [renamingBusy, setRenamingBusy] = useState(false);
  const [renamingError, setRenamingError] = useState<string | null>(null);

  const [merging, setMerging] = useState<Person | null>(null);
  const [mergeTarget, setMergeTarget] = useState('');
  const [mergingBusy, setMergingBusy] = useState(false);
  const [mergingError, setMergingError] = useState<string | null>(null);

  // A card dropped on another: ask once, then merge into the one it landed on.
  const [dropMerge, setDropMerge] = useState<{ source: Person; target: Person } | null>(null);

  const [deleting, setDeleting] = useState<Person | null>(null);
  const [deletingBusy, setDeletingBusy] = useState(false);
  const [deletingError, setDeletingError] = useState<string | null>(null);
  // Deleting a group usually means "forget these faces"; without them gone,
  // the next regroup would form the same group again.
  const [deleteFaces, setDeleteFaces] = useState(true);

  const [purging, setPurging] = useState(false);
  const [purgingBusy, setPurgingBusy] = useState(false);
  const [purgingError, setPurgingError] = useState<string | null>(null);
  const tools = useMenuButton();

  const people = useMemo(() => data.data?.people ?? [], [data.data]);
  const libraryId = data.libraryId;

  // `?person=<id>` deep-links from the viewer's people panel and from search.
  // Following it is a state adjustment during render rather than an effect, so
  // the card is already expanded on the first paint.
  const deepLinked = selected !== null && people.some((p) => p.id === selected);
  const [prevDeepLink, setPrevDeepLink] = useState<string | null>(null);
  if (deepLinked && prevDeepLink !== selected) {
    setPrevDeepLink(selected);
    setExpanded(selected);
  } else if (!deepLinked && prevDeepLink !== null) {
    setPrevDeepLink(null);
  }

  const mergeOptions = useMemo(
    () => (merging ? people.filter((p) => p.id !== merging.id) : []),
    [merging, people],
  );

  // A pass may already be running (after a scan, an upload, or a threshold
  // change): show it, and refresh once it is done.
  const runningOnLoad = data.data?.status.running === true;
  const working = busy ?? (runningOnLoad ? 'working' : null);
  useEffect(() => {
    if (!runningOnLoad || !libraryId) return;
    let live = true;
    void waitForFacePasses(libraryId).then(() => {
      if (live) data.reload();
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runningOnLoad, libraryId]);

  const runPass = (kind: 'detect' | 'cluster') => {
    if (!libraryId) return;
    setBusy(kind);
    setActionError(null);
    const call = kind === 'detect' ? runFacePass : clusterFaces;
    call(libraryId)
      // The pass runs on the server; wait for it rather than guessing.
      .then(() => waitForFacePasses(libraryId))
      .then((status) => {
        data.reload();
        if (!status) return;
        toast({
          message: `${status.people} ${status.people === 1 ? 'person' : 'people'} found; ${status.unassigned} ${status.unassigned === 1 ? 'face' : 'faces'} to review.`,
          tone: 'success',
        });
      })
      .catch((e: unknown) => setActionError(message(e)))
      .finally(() => setBusy(null));
  };

  const submitRename = (name: string) => {
    if (!renaming || !libraryId) return;
    setRenamingBusy(true);
    setRenamingError(null);
    renamePerson(libraryId, renaming.id, name)
      .then(() => {
        setRenaming(null);
        data.reload();
      })
      .catch((e: unknown) => setRenamingError(message(e)))
      .finally(() => setRenamingBusy(false));
  };

  const submitMerge = () => {
    if (!merging || !mergeTarget || !libraryId) return;
    setMergingBusy(true);
    setMergingError(null);
    // The URL is the survivor; the source is the person being absorbed.
    mergePeople(libraryId, mergeTarget, merging.id)
      .then(() => {
        toast({ message: `Merged ${merging.name} into the person you chose.`, tone: 'success' });
        setMerging(null);
        setMergeTarget('');
        data.reload();
      })
      .catch((e: unknown) => setMergingError(message(e)))
      .finally(() => setMergingBusy(false));
  };

  const confirmDropMerge = () => {
    if (!dropMerge || !libraryId) return;
    const { source, target } = dropMerge;
    setMergingBusy(true);
    setMergingError(null);
    mergePeople(libraryId, target.id, source.id)
      .then((r) => {
        const joined = r.consolidated ?? 0;
        toast({
          message:
            `Merged ${source.name || 'the group'} into ${target.name || 'the group'}.` +
            (joined > 0
              ? ` ${joined} similar ${joined === 1 ? 'group was' : 'groups were'} joined too.`
              : '') +
            ' Cairn will use both when recognising them.',
          tone: 'success',
        });
        setDropMerge(null);
        data.reload();
      })
      .catch((e: unknown) => setMergingError(message(e)))
      .finally(() => setMergingBusy(false));
  };

  const confirmDelete = () => {
    if (!deleting || !libraryId) return;
    setDeletingBusy(true);
    setDeletingError(null);
    deletePerson(libraryId, deleting.id, deleteFaces)
      .then(() => {
        setDeleting(null);
        refreshAfterRegroup();
      })
      .catch((e: unknown) => setDeletingError(message(e)))
      .finally(() => setDeletingBusy(false));
  };

  const confirmPurge = () => {
    if (!libraryId) return;
    setPurgingBusy(true);
    setPurgingError(null);
    purgeFaces(libraryId)
      .then((resp) => {
        toast({ message: `Removed ${resp.faces_removed} faces.`, tone: 'success' });
        setPurging(false);
        data.reload();
      })
      .catch((e: unknown) => setPurgingError(message(e)))
      .finally(() => setPurgingBusy(false));
  };

  // Hand edits make the server regroup in the background (so a new person is
  // used for matching at once): show the edit now, and the regroup when done.
  const refreshAfterRegroup = () => {
    data.reload();
    if (libraryId) void waitForFacePasses(libraryId).then(() => data.reload());
  };

  const removeFace = (faceId: string) => {
    if (!libraryId) return;
    setActionError(null);
    deleteFace(libraryId, faceId)
      .then(() => {
        toast({ message: 'Face deleted. The photo itself is kept.', tone: 'success' });
        data.reload();
      })
      .catch((e: unknown) => setActionError(message(e)));
  };

  const unassign = (personId: string, faceId: string) => {
    if (!libraryId) return;
    setActionError(null);
    unassignFace(libraryId, personId, faceId)
      .then(data.reload)
      .catch((e: unknown) => setActionError(message(e)));
  };

  const setCover = (personId: string, faceId: string) => {
    if (!libraryId) return;
    setActionError(null);
    setPersonCover(libraryId, personId, faceId)
      .then(data.reload)
      .catch((e: unknown) => setActionError(message(e)));
  };

  const assign = (faceId: string, personId: string) => {
    if (!libraryId) return;
    setActionError(null);
    assignFace(libraryId, personId, faceId)
      .then(refreshAfterRegroup)
      .catch((e: unknown) => setActionError(message(e)));
  };

  // "New person…" on a face: ask for a name, create them, and place the face.
  const [naming, setNaming] = useState<string | null>(null);
  const [namingBusy, setNamingBusy] = useState(false);
  const [namingError, setNamingError] = useState<string | null>(null);

  const startNewPerson = (faceId: string) => {
    setNamingError(null);
    setNaming(faceId);
  };

  const submitNewPerson = (name: string) => {
    const faceId = naming;
    const trimmed = name.trim();
    if (!faceId || !libraryId) return;
    if (!trimmed) {
      setNamingError('Enter a name.');
      return;
    }
    setNamingBusy(true);
    setNamingError(null);
    createPerson(libraryId, trimmed)
      .then((resp) => assignFace(libraryId, resp.person.id, faceId))
      .then(() => {
        setNaming(null);
        toast({
          message: `Added ${trimmed}. Cairn is looking for them in your other photos.`,
          tone: 'success',
        });
        refreshAfterRegroup();
      })
      .catch((e: unknown) => setNamingError(message(e)))
      .finally(() => setNamingBusy(false));
  };

  if (gate.kind === 'loading') {
    return (
      <main className="page people-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="People"
      subtitle={
        data.data?.status?.enabled ? (
          <span data-testid="people-stats">
            {data.data.status.people} {data.data.status.people === 1 ? 'person' : 'people'}
            {data.data.status.unassigned > 0 && ` · ${data.data.status.unassigned} faces to review`}
          </span>
        ) : undefined
      }
      controls={
        data.data?.status?.enabled && (
          <>
            <button
              type="button"
              className="button"
              aria-haspopup="menu"
              aria-expanded={tools.open}
              onClick={tools.toggle}
              disabled={working !== null}
            >
              <Icon name="spark" />
              {working === 'detect'
                ? 'Looking for faces…'
                : working === 'cluster'
                  ? 'Grouping…'
                  : working === 'working'
                    ? 'Finding people…'
                    : 'Find people'}
            </button>
            {tools.anchor && (
              <Menu
                anchor={tools.anchor}
                align="end"
                label="Face tools"
                onClose={tools.close}
                items={[
                  {
                    id: 'detect',
                    label: 'Look for new faces',
                    icon: 'search',
                    onSelect: () => runPass('detect'),
                    testId: 'detect-faces',
                  },
                  {
                    id: 'cluster',
                    label: 'Group similar faces',
                    icon: 'people',
                    onSelect: () => runPass('cluster'),
                    testId: 'cluster-faces',
                  },
                  'separator',
                  {
                    id: 'purge',
                    label: 'Remove all face data…',
                    icon: 'trash',
                    danger: true,
                    onSelect: () => setPurging(true),
                    testId: 'purge-faces',
                  },
                ]}
              />
            )}
          </>
        )
      }
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page people-page">
        {header}
        <ErrorState message={gate.message} onRetry={data.reload} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page people-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  const payload = data.data;
  const status = payload?.status ?? null;
  const enabled = Boolean(status?.enabled);
  const unassigned = payload?.unassigned ?? [];

  return (
    <main className="page people-page">
      {header}
      {gate.library.status === 'offline' && <LibraryOfflineNotice library={gate.library} />}

      {data.error && <ErrorState message={data.error} onRetry={data.reload} />}
      {actionError && (
        <p className="error-text" role="alert">
          {actionError}
        </p>
      )}
      {data.loading && (
        <div className="people-grid" aria-hidden="true">
          {Array.from({ length: 6 }, (_, i) => (
            <span key={i} className="skeleton person-skeleton" />
          ))}
        </div>
      )}

      {!data.loading && payload?.unsupported && (
        <EmptyState title="People isn't available here" testId="faces-unsupported" icon="people">
          <p>
            This Cairn server can't recognize faces, so it can't group photos by person. Everything
            else in your library works as usual.
          </p>
        </EmptyState>
      )}

      {!data.loading && payload && !enabled && !payload.unsupported && (
        <EmptyState title="People is turned off" testId="faces-disabled" icon="people">
          <p>
            Cairn can group your photos by the people in them, but it's switched off on this server.
          </p>
          {user?.role === 'admin' && (
            <p>
              To turn it on, set <code>CAIRN_ML_ENABLED</code> and <code>CAIRN_ML_FACES</code> and
              restart Cairn.
            </p>
          )}
        </EmptyState>
      )}

      {enabled && status && (
        <>
          {people.length === 0 ? (
            <EmptyState title="No people yet" testId="people-empty" icon="people">
              <p>
                Use “Find people” to look for faces in your photos. Name the groups that appear, and
                Cairn will keep recognizing them.
              </p>
            </EmptyState>
          ) : (
            <section aria-label="People in your library">
              <div className="people-grid" data-testid="people-grid">
                {people.map((person) => (
                  <PersonCard
                    key={person.id}
                    libraryId={gate.libraryId}
                    person={person}
                    expanded={expanded === person.id}
                    onToggle={() =>
                      setExpanded((current) => (current === person.id ? null : person.id))
                    }
                    onRename={() => {
                      setRenamingError(null);
                      setRenaming(person);
                    }}
                    onMerge={() => {
                      setMergingError(null);
                      setMergeTarget('');
                      setMerging(person);
                    }}
                    onDropPerson={(sourceId) => {
                      const source = people.find((p) => p.id === sourceId);
                      if (!source || source.id === person.id) return;
                      setMergingError(null);
                      setDropMerge({ source, target: person });
                    }}
                    onDelete={() => {
                      setDeletingError(null);
                      setDeleteFaces(true);
                      setDeleting(person);
                    }}
                    onUnassign={(faceId) => unassign(person.id, faceId)}
                    onSetCover={(faceId) => setCover(person.id, faceId)}
                    people={people}
                    onMove={assign}
                    onMoveToNew={startNewPerson}
                    onDeleteFace={removeFace}
                  />
                ))}
              </div>
            </section>
          )}

          {unassigned.length > 0 && (
            <section className="section" aria-labelledby="unassigned-title">
              <div className="section-head">
                <h2 id="unassigned-title" className="section-title">
                  Who is this?
                </h2>
              </div>
              <p className="secondary-text">
                Faces Cairn found but could not place. Choose who each one is, or add them as a new
                person.
              </p>
              <div className="face-grid" data-testid="unassigned-faces">
                {unassigned.map((face) => (
                  <figure className="face-tile" key={face.id}>
                    <img src={faceImageUrl(gate.libraryId, face.id)} alt="" loading="lazy" />
                    <figcaption>
                      <FacePersonPicker
                        people={people}
                        placeholder="Choose a person…"
                        onPick={(personId) => assign(face.id, personId)}
                        onNewPerson={() => startNewPerson(face.id)}
                      />
                      <button
                        type="button"
                        className="button ghost-button button-sm"
                        onClick={() => removeFace(face.id)}
                        title="Delete this face. Cairn will not suggest it again; the photo is kept."
                      >
                        Delete
                      </button>
                    </figcaption>
                  </figure>
                ))}
              </div>
            </section>
          )}
        </>
      )}

      <PromptDialog
        open={naming !== null}
        title="New person"
        label="Name"
        initialValue=""
        placeholder="Who is this?"
        hint="Cairn will use this face to recognise them in your other photos."
        confirmLabel="Add"
        busy={namingBusy}
        error={namingError}
        onCancel={() => setNaming(null)}
        onConfirm={submitNewPerson}
        testId="new-person-dialog"
      />

      <PromptDialog
        open={renaming !== null}
        title="Rename person"
        label="Name"
        initialValue={renaming?.name ?? ''}
        confirmLabel="Rename"
        busy={renamingBusy}
        error={renamingError}
        onCancel={() => setRenaming(null)}
        onConfirm={submitRename}
        testId="rename-person-dialog"
      />

      <ConfirmDialog
        open={dropMerge !== null}
        title="Are these the same person?"
        confirmLabel="Yes, merge"
        busy={mergingBusy}
        error={mergingError}
        message={
          <p>
            Merge <strong>{dropMerge?.source.name || 'this group'}</strong> into{' '}
            <strong>{dropMerge?.target.name || 'this group'}</strong>? Cairn will treat them as one
            person from now on, use both sets of photos to recognise them, and join any other
            unnamed group that looks like them. This cannot be undone.
          </p>
        }
        onCancel={() => setDropMerge(null)}
        onConfirm={confirmDropMerge}
        testId="drop-merge-dialog"
      />

      <Dialog
        open={merging !== null}
        title={`Merge ${merging?.name ?? ''} into another person`}
        onClose={() => setMerging(null)}
        testId="merge-person-dialog"
        footer={
          <>
            <button
              type="button"
              className="button"
              onClick={() => setMerging(null)}
              disabled={mergingBusy}
            >
              Cancel
            </button>
            <button
              type="button"
              className="button primary-button"
              onClick={submitMerge}
              disabled={mergingBusy || mergeTarget === ''}
            >
              {mergingBusy ? 'Merging…' : 'Merge'}
            </button>
          </>
        }
      >
        <p>
          Every face of <strong>{merging?.name}</strong> moves to the person you choose, and{' '}
          <strong>{merging?.name}</strong> is removed. This cannot be undone.
        </p>
        <label className="dialog-label" htmlFor="merge-target">
          Keep
        </label>
        <select
          id="merge-target"
          className="dialog-input"
          value={mergeTarget}
          onChange={(event) => setMergeTarget(event.target.value)}
          disabled={mergingBusy}
        >
          <option value="">Choose a person…</option>
          {mergeOptions.map((person) => (
            <option key={person.id} value={person.id}>
              {person.name}
            </option>
          ))}
        </select>
        {mergeOptions.length === 0 && (
          <p className="muted">You need at least two people to merge.</p>
        )}
        {mergingError && (
          <p className="error-text" role="alert">
            {mergingError}
          </p>
        )}
      </Dialog>

      <ConfirmDialog
        open={deleting !== null}
        title="Delete this person?"
        destructive
        confirmLabel="Delete"
        busy={deletingBusy}
        error={deletingError}
        message={
          <>
            <p>
              <strong>{deleting?.name}</strong> will no longer be a person in this library. Your
              photos are not touched.
            </p>
            <label className="checkbox-row">
              <input
                type="checkbox"
                checked={deleteFaces}
                onChange={(e) => setDeleteFaces(e.target.checked)}
                data-testid="delete-person-faces"
              />
              <span>
                Also delete their {deleting?.face_count ?? 0}{' '}
                {deleting?.face_count === 1 ? 'face' : 'faces'}
              </span>
            </label>
            <p className="muted">
              {deleteFaces
                ? 'Cairn forgets these faces and will not group them again.'
                : 'The faces go back to the pool and may be grouped again.'}
            </p>
          </>
        }
        onCancel={() => setDeleting(null)}
        onConfirm={confirmDelete}
        testId="delete-person-dialog"
      />

      <ConfirmDialog
        open={purging}
        title="Remove every detected face?"
        destructive
        confirmLabel="Purge"
        busy={purgingBusy}
        error={purgingError}
        message={
          <p>
            Cairn will delete all detected faces and every grouping they produced. The photos
            themselves are not touched, and the people you named become unnamed again. This cannot
            be undone.
          </p>
        }
        onCancel={() => setPurging(false)}
        onConfirm={confirmPurge}
        testId="purge-faces-dialog"
      />

      {selected && (
        <button type="button" className="visually-hidden" onClick={() => setSearchParams({})}>
          Clear person filter
        </button>
      )}
    </main>
  );
}
