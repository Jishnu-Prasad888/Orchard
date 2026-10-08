import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { called, json, mockApi, originalFetch, renderPage } from '../test/harness';
import PeoplePage from './PeoplePage';

const status = {
  enabled: true,
  provider: 'pigo_appearance',
  provider_version: 1,
  faces: 4,
  people: 2,
  unassigned: 2,
};

const people = {
  people: [
    { id: 'p1', name: 'Mom', cover_face_id: 'f1', cover_file_id: 'file1', face_count: 2 },
    { id: 'p2', name: 'Person 2', cover_face_id: '', cover_file_id: '', face_count: 0 },
  ],
};

const unassigned = {
  faces: [{ id: 'u1', file_id: 'file9', x: 0, y: 0, width: 24, height: 24, confidence: 0.9 }],
};

const personDetail = {
  person: people.people[0],
  faces: [
    { id: 'f1', file_id: 'file1', x: 0, y: 0, width: 24, height: 24, confidence: 0.9 },
    { id: 'f2', file_id: 'file2', x: 0, y: 0, width: 24, height: 24, confidence: 0.8 },
  ],
};

interface Options {
  /** The `/ml/faces` status endpoint's response. */
  faceStatus?: unknown;
  faceStatusCode?: number;
  peopleList?: unknown;
  unassignedFaces?: unknown;
  versions?: unknown;
}

function setup(options: Options = {}) {
  const fn = mockApi([
    (url) =>
      /\/libraries\/lib1\/ml\/faces$/.test(url)
        ? json(options.faceStatus ?? status, options.faceStatusCode ?? 200)
        : undefined,
    (url) =>
      /\/libraries\/lib1\/ml\/faces\/pass$/.test(url)
        ? json({ status: 'started' }, 202)
        : undefined,
    (url) =>
      /\/libraries\/lib1\/ml\/faces\/cluster$/.test(url)
        ? json({ status: 'started' }, 202)
        : undefined,
    (url, init) =>
      /\/libraries\/lib1\/ml\/faces\/purge$/.test(url) && init?.method === 'POST'
        ? json({ library_id: 'lib1', faces_removed: 4 })
        : undefined,
    (url, init) =>
      /\/libraries\/lib1\/people$/.test(url) && init?.method === 'POST'
        ? json(
            {
              person: {
                id: 'p9',
                name: 'Asha',
                created_at: '2026-01-01T00:00:00Z',
                updated_at: '2026-01-01T00:00:00Z',
                face_count: 0,
              },
            },
            201,
          )
        : undefined,
    (url) =>
      /\/libraries\/lib1\/people$/.test(url) ? json(options.peopleList ?? people) : undefined,
    (url) =>
      /\/libraries\/lib1\/faces$/.test(url)
        ? json(options.unassignedFaces ?? unassigned)
        : undefined,
    (url) => (/\/libraries\/lib1\/people\/p1$/.test(url) ? json(personDetail) : undefined),
    (url) => (/\/people\/p1\/rename$/.test(url) ? json({ renamed: true }) : undefined),
    (url, init) =>
      /\/people\/p1\/cover$/.test(url) && init?.method === 'POST'
        ? json({ cover_set: true })
        : undefined,
    (url, init) =>
      /\/people\/p2\/merge$/.test(url) && init?.method === 'POST'
        ? json({ merged: true })
        : undefined,
    (url) => (/\/people\/p1\/faces\/f\d$/.test(url) ? json({ assigned: true }) : undefined),
    (url) => (/\/people\/p[29]\/faces\/[fu]\d$/.test(url) ? json({ assigned: true }) : undefined),
    (url, init) =>
      /\/people\/p1(\?faces=delete)?$/.test(url) && init?.method === 'DELETE'
        ? json({ deleted: true })
        : undefined,
    (url, init) =>
      /\/libraries\/lib1\/faces\/[fu]\d$/.test(url) && init?.method === 'DELETE'
        ? json({ deleted: true })
        : undefined,
  ]);
  renderPage(<PeoplePage />);
  return fn;
}

/** Open a person's options menu and choose an item from it. */
async function chooseFromPerson(personId: string, name: string, item: string) {
  fireEvent.click(
    within(screen.getByTestId(`person-${personId}`)).getByRole('button', {
      name: `Options for ${name}`,
    }),
  );
  fireEvent.click(await screen.findByRole('menuitem', { name: item }));
}

/** Open the "Find people" tools menu and choose a tool by its test id. */
async function chooseTool(testId: string) {
  fireEvent.click(screen.getByRole('button', { name: /Find people/ }));
  fireEvent.click(await screen.findByTestId(testId));
}

describe('PeoplePage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('renders the people grid and the server stats', async () => {
    setup();

    expect(await screen.findByTestId('people-grid')).toBeInTheDocument();
    expect(screen.getByTestId('people-stats')).toHaveTextContent('2 people · 2 faces to review');
    expect(within(screen.getByTestId('person-p1')).getByText('Mom')).toBeInTheDocument();
  });

  it('lists the unassigned pool and assigns a face from it', async () => {
    const fetchMock = setup();

    const pool = await screen.findByTestId('unassigned-faces');
    expect(pool).toBeInTheDocument();

    const select = within(pool).getAllByLabelText('Choose a person…')[0] as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'p1' } });

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p1/faces/u1')).toBe(true);
    });
  });

  it('adds an unplaced face as a new person', async () => {
    const fetchMock = setup();
    const pool = await screen.findByTestId('unassigned-faces');

    const select = within(pool).getAllByLabelText('Choose a person…')[0] as HTMLSelectElement;
    fireEvent.change(select, { target: { value: '__new__' } });

    const dialog = await screen.findByTestId('new-person-dialog');
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'Asha' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people')).toBe(true);
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p9/faces/u1')).toBe(true);
    });
    expect(await screen.findByText(/^Added Asha\./)).toBeInTheDocument();
  });

  it("moves one of a person's faces to someone else or to a new person", async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');
    await chooseFromPerson('p1', 'Mom', 'Manage faces');
    const faces = await screen.findByTestId('person-faces-p1');

    const first = (await within(faces).findAllByLabelText('Move to…'))[0] as HTMLSelectElement;
    // The person the face already belongs to is not offered.
    expect(within(first).queryByRole('option', { name: 'Mom' })).toBeNull();
    fireEvent.change(first, { target: { value: 'p2' } });
    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p2/faces/f1')).toBe(true);
    });

    // The page re-reads after the move, so look the pickers up again.
    const second = (
      await within(await screen.findByTestId('person-faces-p1')).findAllByLabelText('Move to…')
    )[1] as HTMLSelectElement;
    fireEvent.change(second, { target: { value: '__new__' } });
    const dialog = await screen.findByTestId('new-person-dialog');
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'Asha' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }));
    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p9/faces/f2')).toBe(true);
    });
  });

  it('expands a person to show their faces, fetched on demand', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    // The face list is a sibling of `person` in the detail response, so it is a
    // separate request that must not happen until the card is opened.
    expect(called(fetchMock, 'GET', '/api/v1/libraries/lib1/people/p1')).toBe(false);

    await chooseFromPerson('p1', 'Mom', 'Manage faces');

    const faces = await screen.findByTestId('person-faces-p1');
    expect(within(faces).getAllByRole('figure')).toHaveLength(2);
    expect(within(faces).getByRole('button', { name: 'Cover' })).toBeDisabled();
    expect(within(faces).getByRole('button', { name: 'Set cover' })).toBeEnabled();
  });

  it('closes the faces dialog again', async () => {
    setup();
    await screen.findByTestId('people-grid');

    await chooseFromPerson('p1', 'Mom', 'Manage faces');
    await screen.findByTestId('person-faces-p1');

    fireEvent.click(screen.getByRole('button', { name: /Close Faces of Mom/ }));
    expect(screen.queryByTestId('person-faces-p1')).not.toBeInTheDocument();
  });

  it('links a person to their photos', async () => {
    setup();
    await screen.findByTestId('people-grid');

    expect(
      within(screen.getByTestId('person-p1')).getByRole('link', { name: /Mom/ }),
    ).toHaveAttribute('href', '/search?person=p1');
  });

  it('renames a person through the dialog', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    await chooseFromPerson('p1', 'Mom', 'Rename');
    const dialog = await screen.findByTestId('rename-person-dialog');
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'Mum' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Rename' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p1/rename')).toBe(true);
    });
  });

  it('merges into a person chosen from a list, never a typed id', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    await chooseFromPerson('p1', 'Mom', 'Merge into…');
    const dialog = await screen.findByTestId('merge-person-dialog');

    const select = within(dialog).getByLabelText('Keep') as HTMLSelectElement;
    // The person being merged away is not offered as their own destination.
    expect(
      within(select)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Choose a person…', 'Person 2']);

    const confirm = within(dialog).getByRole('button', { name: 'Merge' });
    expect(confirm).toBeDisabled();
    fireEvent.change(select, { target: { value: 'p2' } });
    fireEvent.click(confirm);

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p2/merge')).toBe(true);
    });
  });

  it('merges when one person card is dropped on another, after asking', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    const store: Record<string, string> = {};
    const dataTransfer = {
      types: ['application/x-cairn-person'],
      setData: (k: string, v: string) => {
        store[k] = v;
      },
      getData: (k: string) => store[k] ?? '',
      effectAllowed: '',
      dropEffect: '',
    };
    fireEvent.dragStart(screen.getByTestId('person-p1'), { dataTransfer });
    fireEvent.dragOver(screen.getByTestId('person-p2'), { dataTransfer });
    fireEvent.drop(screen.getByTestId('person-p2'), { dataTransfer });

    // Nothing is merged until the person confirms.
    const dialog = await screen.findByTestId('drop-merge-dialog');
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p2/merge')).toBe(false);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Yes, merge' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/people/p2/merge')).toBe(true);
    });
  });

  it('says so when there is nobody to merge into', async () => {
    mockApi([
      (url) => (/\/libraries\/lib1\/ml\/faces$/.test(url) ? json(status) : undefined),
      (url) =>
        /\/libraries\/lib1\/people$/.test(url) ? json({ people: [people.people[0]] }) : undefined,
      (url) => (/\/libraries\/lib1\/faces$/.test(url) ? json(unassigned) : undefined),
    ]);
    renderPage(<PeoplePage />);
    await screen.findByTestId('people-grid');

    await chooseFromPerson('p1', 'Mom', 'Merge into…');
    const dialog = await screen.findByTestId('merge-person-dialog');
    expect(within(dialog).getByText('You need at least two people to merge.')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Merge' })).toBeDisabled();
  });

  it('deletes a person after confirming the dialog', async () => {
    const fetchMock = setup();
    const confirmSpy = vi.spyOn(window, 'confirm');
    await screen.findByTestId('people-grid');

    await chooseFromPerson('p1', 'Mom', 'Delete');
    const dialog = await screen.findByTestId('delete-person-dialog');
    expect(within(dialog).getByText('Mom')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    // Their faces go too by default, so the group is not rebuilt.
    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/people/p1?faces=delete')).toBe(
        true,
      );
    });
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it('can delete a person but keep their faces', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    await chooseFromPerson('p1', 'Mom', 'Delete');
    const dialog = await screen.findByTestId('delete-person-dialog');
    fireEvent.click(within(dialog).getByTestId('delete-person-faces'));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/people/p1')).toBe(true);
    });
    expect(called(fetchMock, 'DELETE', 'faces=delete')).toBe(false);
  });

  it('deletes a face from "Who is this?" and from a person', async () => {
    const fetchMock = setup();
    const pool = await screen.findByTestId('unassigned-faces');
    fireEvent.click(within(pool).getAllByRole('button', { name: 'Delete' })[0] as HTMLElement);
    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/faces/u1')).toBe(true);
    });
    // The page re-reads after the delete.
    await screen.findByTestId('person-p1');

    await chooseFromPerson('p1', 'Mom', 'Manage faces');
    const faces = await screen.findByTestId('person-faces-p1');
    fireEvent.click(
      (await within(faces).findAllByRole('button', { name: 'Delete face' }))[0] as HTMLElement,
    );
    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/faces/f1')).toBe(true);
    });
  });

  it('purges every detected face after confirming, and reports how many', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    await chooseTool('purge-faces');
    const dialog = await screen.findByTestId('purge-faces-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Purge' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/faces/purge')).toBe(true);
    });
    await waitFor(() => {
      expect(screen.getByText('Removed 4 faces.')).toBeInTheDocument();
    });
  });

  it('starts a detection pass and reports the result once it has finished', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    await chooseTool('detect-faces');

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/faces/pass')).toBe(true);
    });
    // The fixture status reports no pass running, so the summary follows.
    expect(await screen.findByText(/people found; .* to review\./)).toBeInTheDocument();
  });

  it('starts a clustering pass', async () => {
    const fetchMock = setup();
    await screen.findByTestId('people-grid');

    await chooseTool('cluster-faces');

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/faces/cluster')).toBe(true);
    });
  });

  it('distinguishes "faces are off" from "this build has no face support"', async () => {
    mockApi([
      (url) =>
        /\/libraries\/lib1\/ml\/faces$/.test(url)
          ? json(
              {
                error: { code: 'SERVICE_UNAVAILABLE', message: 'ML is disabled', request_id: '1' },
              },
              503,
            )
          : undefined,
    ]);
    renderPage(<PeoplePage />);

    expect(await screen.findByTestId('faces-disabled')).toBeInTheDocument();
    expect(screen.queryByTestId('faces-unsupported')).not.toBeInTheDocument();
    expect(screen.queryByTestId('detect-faces')).not.toBeInTheDocument();
  });

  it('explains an unsupported build without pretending faces are merely empty', async () => {
    mockApi([
      (url) =>
        /\/libraries\/lib1\/ml\/faces$/.test(url)
          ? json({ error: { code: 'NOT_FOUND', message: 'no such route', request_id: '1' } }, 404)
          : undefined,
    ]);
    renderPage(<PeoplePage />);

    expect(await screen.findByTestId('faces-unsupported')).toBeInTheDocument();
    expect(screen.queryByTestId('faces-disabled')).not.toBeInTheDocument();
  });

  it('follows a ?person= deep link straight to that card', async () => {
    mockApi([
      (url) => (/\/libraries\/lib1\/ml\/faces$/.test(url) ? json(status) : undefined),
      (url) => (/\/libraries\/lib1\/people$/.test(url) ? json(people) : undefined),
      (url) => (/\/libraries\/lib1\/faces$/.test(url) ? json(unassigned) : undefined),
      (url) => (/\/libraries\/lib1\/people\/p1$/.test(url) ? json(personDetail) : undefined),
    ]);
    renderPage(<PeoplePage />, { route: '/people?person=p1' });

    // The card is already open on the first paint, so its faces load too.
    expect(await screen.findByTestId('person-faces-p1')).toBeInTheDocument();
  });

  it('explains a failed face listing with a retry', async () => {
    mockApi([
      (url) =>
        /\/libraries\/lib1\/ml\/faces$/.test(url)
          ? json(
              { error: { code: 'INTERNAL', message: 'Face index is corrupt.', request_id: '1' } },
              500,
            )
          : undefined,
    ]);
    renderPage(<PeoplePage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Face index is corrupt.');
    expect(within(alert).getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });

  it('offers no route out when the library list is empty', async () => {
    mockApi([], { libraries: [] });
    renderPage(<PeoplePage />, { libraries: [] });

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
    expect(screen.queryByTestId('detect-faces')).not.toBeInTheDocument();
  });

  it('does not fetch faces until a card is opened', async () => {
    const fetchMock = mockApi([
      (url) => (/\/libraries\/lib1\/ml\/faces$/.test(url) ? json(status) : undefined),
      (url) => (/\/libraries\/lib1\/people$/.test(url) ? json(people) : undefined),
      (url) => (/\/libraries\/lib1\/faces$/.test(url) ? json(unassigned) : undefined),
      (url) => (/\/libraries\/lib1\/people\/p1$/.test(url) ? json(personDetail) : undefined),
    ]);
    renderPage(<PeoplePage />);
    await screen.findByTestId('people-grid');

    expect(called(fetchMock, 'GET', '/api/v1/libraries/lib1/people/p1')).toBe(false);
  });
});
