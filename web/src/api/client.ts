/**
 * API client for Cairn's JSON API v1.
 *
 * The frontend is deliberately a pure client of the backend HTTP API. Every
 * request goes through this module, which understands the API error envelope
 * and turns failures into typed {@link ApiError} objects.
 */

export const API_BASE = '/api/v1';

/** Error envelope returned by the server. */
interface ApiErrorBody {
  code: string;
  message: string;
  details?: Record<string, unknown>;
  request_id: string;
}

interface ApiErrorEnvelope {
  error: ApiErrorBody;
}

/** Name of the window event the API layer fires when the server rejects a
 * request with 403. The app shell listens for it and routes to the
 * access-denied page, so permission failures anywhere in the UI surface
 * consistently without every page handling the status itself. */
export const FORBIDDEN_EVENT = 'cairn:forbidden';

/** Name of the window event fired when the server rejects a request with 401,
 * which for Cairn means the session is missing or has expired. The app shell
 * re-reads the auth state and returns the visitor to the sign-in page. */
export const UNAUTHORIZED_EVENT = 'cairn:unauthorized';

/** Thrown for any non-2xx API response. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;
  readonly details: Record<string, unknown> | undefined;

  constructor(body: ApiErrorBody, status: number) {
    super(body.message);
    this.name = 'ApiError';
    this.status = status;
    this.code = body.code;
    this.requestId = body.request_id;
    this.details = body.details;
  }
}

/** A typed JSON request against the API. */
export async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: HeadersInit = { Accept: 'application/json' };
  let body: BodyInit | null = null;
  if (init?.body !== undefined && init.body !== null) {
    headers['Content-Type'] = 'application/json';
    body = init.body;
  }

  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: { ...headers, ...(init?.headers ?? {}) },
    body,
  });

  if (!response.ok) {
    const body = await readErrorEnvelope(response);
    notifyAuthFailure(response.status, body);
    throw new ApiError(body, response.status);
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

/**
 * Broadcast the auth/permission failures the shell reacts to globally. Kept
 * out of the React tree because the API client is deliberately framework-free;
 * every request (JSON or upload) funnels through here so a 401 or 403 anywhere
 * has the same effect on the session.
 */
function notifyAuthFailure(status: number, body: ApiErrorBody) {
  if (typeof window === 'undefined') return;
  const name = status === 403 ? FORBIDDEN_EVENT : status === 401 ? UNAUTHORIZED_EVENT : null;
  if (!name) return;
  window.dispatchEvent(
    new CustomEvent(name, { detail: { code: body.code, message: body.message } }),
  );
}

async function readErrorEnvelope(response: Response): Promise<ApiErrorBody> {
  try {
    const envelope = (await response.json()) as ApiErrorEnvelope;
    if (envelope?.error?.code) {
      return envelope.error;
    }
  } catch {
    // fall through to the fallback below
  }
  return {
    code: 'UNKNOWN',
    message: `Request failed with status ${response.status}.`,
    request_id: '',
  };
}

export const apiGet = <T>(path: string) => apiRequest<T>(path);

const withJsonBody = (method: string, body: unknown): RequestInit =>
  body === undefined ? { method } : { method, body: JSON.stringify(body) };

export const apiPost = <T>(path: string, body?: unknown) =>
  apiRequest<T>(path, withJsonBody('POST', body));
export const apiPatch = <T>(path: string, body?: unknown) =>
  apiRequest<T>(path, withJsonBody('PATCH', body));
export const apiPut = <T>(path: string, body?: unknown) =>
  apiRequest<T>(path, withJsonBody('PUT', body));

/**
 * Some deletes need a body. The file soft-delete is one: it acts on the
 * `path` in the payload rather than the id in the URL (the id path segment is
 * still supplied for symmetry with the rest of the file routes), because the
 * server resolves and re-authorizes the resource from the path.
 */
export const apiDelete = <T>(path: string, body?: unknown) =>
  apiRequest<T>(path, withJsonBody('DELETE', body));

/** Build a query string from defined values, skipping empties. */
export function query(
  params: Record<string, string | number | boolean | undefined | null>,
): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : '';
}

/** Multipart upload. The browser sets the multipart boundary, so no
 * Content-Type header is sent here. `path` is the destination relative path
 * (falls back to the file name server-side). */
export async function apiUpload<T>(endpoint: string, file: File, path?: string): Promise<T> {
  const form = new FormData();
  if (path) form.append('path', path);
  form.append('file', file);

  const response = await fetch(`${API_BASE}${endpoint}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
    body: form,
  });

  if (!response.ok) {
    const body = await readErrorEnvelope(response);
    notifyAuthFailure(response.status, body);
    throw new ApiError(body, response.status);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export interface UploadProgress {
  loaded: number;
  total: number;
}

/**
 * A multipart upload that reports progress and can be cancelled.
 *
 * `fetch` cannot report upload progress, so this one request uses
 * XMLHttpRequest. Errors are the same {@link ApiError} every other call
 * throws, and an abort rejects with a `DOMException` named `AbortError`.
 */
export function apiUploadWithProgress<T>(
  endpoint: string,
  file: File,
  path: string | undefined,
  { onProgress, signal }: { onProgress?: (p: UploadProgress) => void; signal?: AbortSignal } = {},
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${API_BASE}${endpoint}`);
    xhr.setRequestHeader('Accept', 'application/json');
    xhr.withCredentials = true;

    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress?.({ loaded: event.loaded, total: event.total });
    };
    xhr.onload = () => {
      const parsed = parseJson(xhr.responseText);
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve((xhr.status === 204 ? undefined : parsed) as T);
        return;
      }
      const envelope = parsed as ApiErrorEnvelope | null;
      const body: ApiErrorBody = envelope?.error?.code
        ? envelope.error
        : { code: 'UNKNOWN', message: `Request failed with status ${xhr.status}.`, request_id: '' };
      notifyAuthFailure(xhr.status, body);
      reject(new ApiError(body, xhr.status));
    };
    xhr.onerror = () => reject(new Error('The connection to the server was lost.'));
    xhr.onabort = () => reject(new DOMException('Upload cancelled.', 'AbortError'));

    if (signal) {
      if (signal.aborted) {
        reject(new DOMException('Upload cancelled.', 'AbortError'));
        return;
      }
      signal.addEventListener('abort', () => xhr.abort(), { once: true });
    }

    const form = new FormData();
    if (path) form.append('path', path);
    form.append('file', file);
    xhr.send(form);
  });
}

function parseJson(text: string): unknown {
  try {
    return text ? JSON.parse(text) : null;
  } catch {
    return null;
  }
}
