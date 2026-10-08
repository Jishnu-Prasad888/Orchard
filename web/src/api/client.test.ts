import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  ApiError,
  FORBIDDEN_EVENT,
  UNAUTHORIZED_EVENT,
  apiDelete,
  apiGet,
  apiPatch,
  apiPost,
} from './client';

const originalFetch = globalThis.fetch;

function mockFetch(response: Response | Promise<Response>) {
  const fn = vi.fn(() => response);
  globalThis.fetch = fn as unknown as typeof fetch;
  return fn;
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('apiGet', () => {
  it('parses a successful JSON response', async () => {
    mockFetch(jsonResponse(200, { status: 'ok' }));
    const result = await apiGet<{ status: string }>('/health');
    expect(result).toEqual({ status: 'ok' });
  });

  it('requests from the /api/v1 base path', async () => {
    const fn = mockFetch(jsonResponse(200, {}));
    await apiGet('/health');
    const [url] = fn.mock.calls[0] as unknown as [string];
    expect(url).toContain('/api/v1/health');
  });

  it('throws a typed ApiError for error envelopes', async () => {
    mockFetch(
      jsonResponse(404, {
        error: { code: 'NOT_FOUND', message: 'Missing.', request_id: 'abc' },
      }),
    );

    const error = await apiGet('/nope').catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    if (!(error instanceof ApiError)) throw new Error('unreachable');
    expect(error.status).toBe(404);
    expect(error.code).toBe('NOT_FOUND');
    expect(error.message).toBe('Missing.');
    expect(error.requestId).toBe('abc');
  });

  it('falls back to an UNKNOWN code when the body is not an envelope', async () => {
    mockFetch(new Response('oops', { status: 500 }));
    const error = await apiGet('/x').catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    if (!(error instanceof ApiError)) throw new Error('unreachable');
    expect(error.code).toBe('UNKNOWN');
    expect(error.status).toBe(500);
  });
});

describe('auth failure broadcasts', () => {
  it('announces a 403 so the shell can show the access-denied page', async () => {
    const events: Array<{ type: string; message: string | undefined }> = [];
    const listener = (event: Event) => {
      const custom = event as CustomEvent<{ message?: string }>;
      events.push({ type: event.type, message: custom.detail?.message });
    };
    window.addEventListener(FORBIDDEN_EVENT, listener);
    mockFetch(
      jsonResponse(403, {
        error: { code: 'FORBIDDEN', message: 'Not allowed.', request_id: 'abc' },
      }),
    );

    await apiGet('/libraries').catch(() => undefined);
    window.removeEventListener(FORBIDDEN_EVENT, listener);

    expect(events).toEqual([{ type: FORBIDDEN_EVENT, message: 'Not allowed.' }]);
  });

  it('announces a 401 so an expired session can be re-checked', async () => {
    const events: string[] = [];
    const listener = (event: Event) => events.push(event.type);
    window.addEventListener(UNAUTHORIZED_EVENT, listener);
    mockFetch(
      jsonResponse(401, {
        error: { code: 'UNAUTHORIZED', message: 'Missing session.', request_id: 'abc' },
      }),
    );

    await apiPost('/auth/login', { username: 'a', password: 'b' }).catch(() => undefined);
    window.removeEventListener(UNAUTHORIZED_EVENT, listener);

    expect(events).toEqual([UNAUTHORIZED_EVENT]);
  });

  it('stays quiet for other failures', async () => {
    const listener = vi.fn();
    window.addEventListener(FORBIDDEN_EVENT, listener);
    window.addEventListener(UNAUTHORIZED_EVENT, listener);
    mockFetch(new Response(null, { status: 500 }));

    await apiGet('/x').catch(() => undefined);
    window.removeEventListener(FORBIDDEN_EVENT, listener);
    window.removeEventListener(UNAUTHORIZED_EVENT, listener);

    expect(listener).not.toHaveBeenCalled();
  });
});

describe('verb helpers', () => {
  it('apiPost sends JSON and requests method POST', async () => {
    const fn = mockFetch(jsonResponse(200, {}));
    await apiPost('/things', { name: 'pebble' });
    const [, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe('POST');
    expect(init.body).toBe(JSON.stringify({ name: 'pebble' }));
  });

  it('apiPost omits a body when none is given', async () => {
    const fn = mockFetch(jsonResponse(200, {}));
    await apiPost('/things');
    const [, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.body ?? undefined).toBeUndefined();
  });

  it('apiPatch requests method PATCH', async () => {
    const fn = mockFetch(jsonResponse(200, {}));
    await apiPatch('/things/1', { color: 'terracotta' });
    const [, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe('PATCH');
  });

  it('apiDelete requests method DELETE', async () => {
    const fn = mockFetch(new Response(null, { status: 204 }));
    await apiDelete<void>('/things/1');
    const [, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe('DELETE');
  });
});
