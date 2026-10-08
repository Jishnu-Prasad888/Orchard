/**
 * Shared scaffolding for page tests.
 *
 * Almost every page is a `RequireAuth` route that also depends on the shared
 * library list, so each test used to re-declare the same three providers and
 * then drift: some wrapped in `AuthProvider`, some did not, and the library
 * fixture was written against the old `{ id, name, path }` shape. `renderPage`
 * is the one place that gets it right.
 */

import { render } from '@testing-library/react';
import type { RenderResult } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { vi } from 'vitest';
import type { ReactElement } from 'react';

import type { FileSummary, Library, User } from '../api/types';
import Providers from './Providers';

export const admin: User = {
  id: 'u1',
  username: 'jishnu',
  role: 'admin',
  created_at: '2026-01-01T00:00:00Z',
};

export const member: User = {
  id: 'u2',
  username: 'alice',
  role: 'user',
  created_at: '2026-02-02T00:00:00Z',
};

/** A library fixture matching what `GET /libraries` actually returns. */
export function libraryFixture(overrides: Partial<Library> = {}): Library {
  return {
    id: 'lib1',
    name: 'Photos',
    root: '/srv/photos',
    status: 'online',
    schema_version: 1,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    ...overrides,
  };
}

/** A file fixture matching what `GET /search` and the album/trash lists return. */
export function fileFixture(overrides: Partial<FileSummary> = {}): FileSummary {
  return {
    id: 'f1',
    library_id: 'lib1',
    rel_path: 'IMG_0001.png',
    name: 'IMG_0001.png',
    folder_path: '',
    size_bytes: 2048,
    mod_time: '2026-09-01T00:00:00Z',
    media_type: 'photo',
    mime_type: 'image/png',
    status: 'present',
    content_hash: 'abc',
    ...overrides,
  };
}

export function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

export function noContent(status = 204): Response {
  return new Response(null, { status });
}

export function apiError(status: number, code = 'NOT_FOUND', message = 'missing'): Response {
  return json({ error: { code, message, request_id: 'test' } }, status);
}

/** A handler returns a response, or `undefined` to fall through to the next rule. */
export type RouteHandler = (url: string, init: RequestInit | undefined) => Response | undefined;

/**
 * Install a `fetch` mock.
 *
 * Rules are tried in order; the first non-`undefined` response wins. Anything
 * that matches nothing gets a 404 in the API's own error shape, so a test that
 * forgets an endpoint fails on the assertion that needed it rather than on an
 * unhandled rejection.
 *
 * `auth/status` and `libraries` are answered by default because the providers
 * need them; override them by putting your own rules first.
 */
export function mockApi(
  rules: RouteHandler[] = [],
  options: { user?: User; libraries?: Library[]; authenticated?: boolean } = {},
): ReturnType<typeof vi.fn> {
  const user = options.user ?? admin;
  const libraries = options.libraries ?? [libraryFixture()];

  const fn = vi.fn(async (input: URL | RequestInfo, init?: RequestInit) => {
    const url = String(input);
    for (const rule of rules) {
      const response = rule(url, init);
      if (response) return response;
    }
    if (url.endsWith('/api/v1/auth/status')) {
      return json({
        bootstrap_required: false,
        authenticated: options.authenticated ?? true,
        ...(options.authenticated === false ? {} : { user }),
      });
    }
    if (url.endsWith('/api/v1/libraries') || url.includes('/api/v1/libraries?')) {
      return json({ libraries });
    }
    return apiError(404);
  });
  globalThis.fetch = fn as unknown as typeof fetch;
  return fn;
}

/** True when a recorded call matches a method and a URL substring. */
export function called(fn: ReturnType<typeof vi.fn>, method: string, urlPart: string): boolean {
  return fn.mock.calls.some(([input, init]) => {
    const verb = (init as RequestInit | undefined)?.method ?? 'GET';
    return verb === method && String(input).includes(urlPart);
  });
}

/** The JSON body of the first recorded call matching a method and a substring. */
export function bodyOf(fn: ReturnType<typeof vi.fn>, method: string, urlPart: string): unknown {
  const call = fn.mock.calls.find(([input, init]) => {
    const verb = (init as RequestInit | undefined)?.method ?? 'GET';
    return verb === method && String(input).includes(urlPart);
  });
  const body = (call?.[1] as RequestInit | undefined)?.body;
  return body === undefined || body === null ? undefined : JSON.parse(String(body));
}

export interface RenderPageOptions {
  user?: User;
  libraries?: Library[];
  route?: string;
  /** Render bare, without the auth and library providers. */
  bare?: boolean;
  /** Whether the viewer's details drawer starts open. Defaults to open. */
  viewerPanel?: 'open' | 'closed';
}

/**
 * Render a page the way the app renders it: inside the router, the auth
 * provider, and the shared library list.
 */
export function renderPage(ui: ReactElement, options: RenderPageOptions = {}): RenderResult {
  // The viewer opens its details drawer only when asked; most tests exercise
  // what is in it, so they start with it open, as a returning user's would be.
  try {
    localStorage.setItem('cairn.viewer.panel', options.viewerPanel ?? 'open');
  } catch {
    // No storage: the viewer just starts closed.
  }
  const tree = options.bare ? (
    <MemoryRouter initialEntries={[options.route ?? '/']}>{ui}</MemoryRouter>
  ) : (
    <MemoryRouter initialEntries={[options.route ?? '/']}>
      <Providers>{ui}</Providers>
    </MemoryRouter>
  );
  return render(tree);
}

/** The `fetch` that was installed before any test ran. */
export const originalFetch = globalThis.fetch;
