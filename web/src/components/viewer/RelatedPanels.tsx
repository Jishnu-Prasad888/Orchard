/**
 * The viewer's "what is this connected to" panels: the people in a photo, the
 * memories that mention it, and visually similar files. Each loads only when
 * its tab is opened, because each is a handful of requests.
 */

import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { ApiError } from '../../api/client';
import { mlWorthAsking } from '../../api/mlSwitch';
import {
  faceImageUrl,
  getSimilarFiles,
  listMemoryRefs,
  listMemories,
  listPeople,
  searchFiles,
} from '../../api/queries';
import type { FileSummary, Memory, Person, SimilarFile } from '../../api/types';
import { thumbnailUrl } from '../media';

/**
 * "Who is in this photo?"
 *
 * The API has no per-file faces endpoint — a person is a cluster of faces
 * across the library, and the only way to ask whether a given person appears
 * in a given file is to search that person's files and look for this one. That
 * is one request per person, so it is only done when the panel is opened, and
 * the count is bounded by a single page of results per person.
 */
export function PeoplePanel({ libraryId, file }: { libraryId: string; file: FileSummary }) {
  const [result, setResult] = useState<{ people: Person[]; matches: Set<string> } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const resp = (await mlWorthAsking()) ? await listPeople(libraryId) : { people: [] };
        const all = resp.people ?? [];
        const results = await Promise.all(
          all.map((person) =>
            searchFiles(libraryId, { person: person.id, limit: 200 })
              .then((listing) => (listing.files ?? []).some((f) => f.id === file.id))
              .catch(() => false),
          ),
        );
        if (cancelled) return;
        setResult({
          people: all,
          matches: new Set(all.filter((_, i) => results[i]).map((p) => p.id)),
        });
        setError(null);
      } catch (e: unknown) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [libraryId, file.id]);

  const people = result?.people ?? null;
  const matches = result?.matches ?? new Set<string>();
  const busy = result === null && error === null;

  if (error) {
    return (
      <p className="error-text" role="alert">
        {error}
      </p>
    );
  }
  if (busy && people === null) {
    return (
      <p className="muted" role="status">
        Looking for familiar faces…
      </p>
    );
  }
  if (!people || people.length === 0) {
    return <p className="muted">No people have been identified in this library yet.</p>;
  }

  const found = people.filter((p) => matches.has(p.id));

  return (
    <div data-testid="viewer-people">
      {found.length === 0 ? (
        <p className="muted">No identified people appear in this file.</p>
      ) : (
        <ul className="viewer-person-list">
          {found.map((person) => (
            <li key={person.id} className="viewer-person">
              <span className="viewer-person-avatar" aria-hidden="true">
                {person.cover_face_id ? (
                  <img src={faceImageUrl(libraryId, person.cover_face_id)} alt="" />
                ) : person.cover_file_id ? (
                  <img src={thumbnailUrl(libraryId, { id: person.cover_file_id })} alt="" />
                ) : null}
              </span>
              <Link to={`/search?person=${person.id}`}>{person.name || 'Unnamed person'}</Link>
              <span className="muted">
                {person.face_count} {person.face_count === 1 ? 'photo' : 'photos'}
              </span>
            </li>
          ))}
        </ul>
      )}
      <p className="muted viewer-panel-footnote">
        Based on the people named in this library. Name more people on the People page to find them
        here.
      </p>
    </div>
  );
}

/**
 * Memories that mention this file.
 *
 * A memory references a file with `[[media:<file-id>]]` written in its
 * Markdown body, and the only server-side way to find those is to read each
 * memory's refs. The panel pages the memory list (bounded) and keeps the ones
 * that point here.
 */
export function MemoriesPanel({ libraryId, file }: { libraryId: string; file: FileSummary }) {
  const [linked, setLinked] = useState<Memory[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const resp = await listMemories(libraryId, { limit: 50 });
        const all = resp.memories ?? [];
        const checks = await Promise.all(
          all.map((m) =>
            listMemoryRefs(libraryId, m.id)
              .then((r) => (r.refs ?? []).some((ref) => ref.type === 'media' && ref.id === file.id))
              .catch(() => false),
          ),
        );
        if (cancelled) return;
        setLinked(all.filter((_, i) => checks[i]));
        setError(null);
      } catch (e: unknown) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [libraryId, file.id]);

  const busy = linked === null && error === null;

  if (error) {
    return (
      <p className="error-text" role="alert">
        {error}
      </p>
    );
  }
  if (busy) {
    return (
      <p className="muted" role="status">
        Checking memories…
      </p>
    );
  }
  if (!linked || linked.length === 0) {
    return (
      <p className="muted">
        No memory mentions this file yet. Add <code>[[media:{file.id}]]</code> to a memory to link
        it.
      </p>
    );
  }

  return (
    <ul className="viewer-memory-list" data-testid="viewer-memories">
      {linked.map((m) => (
        <li key={m.id}>
          <Link to={`/memories/${m.id}`}>{m.title || 'Untitled memory'}</Link>
          {m.memory_date && <span className="muted"> · {m.memory_date}</span>}
        </li>
      ))}
    </ul>
  );
}

export function SimilarPanel({
  libraryId,
  file,
  onOpen,
}: {
  libraryId: string;
  file: FileSummary;
  onOpen: (file: FileSummary) => void;
}) {
  const [similar, setSimilar] = useState<SimilarFile[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    getSimilarFiles(libraryId, file.id)
      .then((resp) => {
        if (cancelled) return;
        setSimilar(resp.similar ?? []);
        setError(null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        // ML being disabled is the common case and is not a failure to report
        // as a broken page — say so plainly instead.
        setError(
          e instanceof ApiError && e.status === 503
            ? 'off'
            : e instanceof Error
              ? e.message
              : String(e),
        );
        setSimilar([]);
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, file.id]);

  if (similar === null && error === null) {
    return (
      <p className="muted" role="status">
        Looking for similar files…
      </p>
    );
  }
  if (error === 'off') {
    return (
      <p className="muted" data-testid="viewer-similar-unavailable">
        Similarity search is turned off. An administrator can switch on machine learning in Settings
        → Machine learning.
      </p>
    );
  }
  if (error) {
    return (
      <p className="error-text" role="alert" data-testid="viewer-similar-error">
        Couldn’t look for similar files: {error}
      </p>
    );
  }
  if (!similar || similar.length === 0) {
    return <p className="muted">No visually similar files were found.</p>;
  }

  return (
    <ul className="viewer-similar-list" data-testid="viewer-similar">
      {similar.map((s) => {
        const target = s.file ?? null;
        return (
          <li key={s.file_id} className="viewer-similar-item">
            {target ? (
              <>
                <button
                  type="button"
                  className="viewer-similar-thumb"
                  onClick={() => onOpen(target)}
                  aria-label={`Open ${target.name}`}
                >
                  <img src={thumbnailUrl(libraryId, target)} alt="" loading="lazy" />
                </button>
                <span className="viewer-similar-name">{target.name}</span>
              </>
            ) : (
              <span className="viewer-similar-name" title={s.file_path}>
                {s.file_path}
              </span>
            )}
            <span className="muted">{Math.round(s.similarity * 100)}%</span>
          </li>
        );
      })}
    </ul>
  );
}
