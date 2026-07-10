import type { QueryResult, SavedQuery, SaveQueryInput, SaveQueryResponse } from './types';

/** The JSON error envelope the API returns for a rejected request. */
interface ErrorBody {
  error?: string;
  code?: string;
}

/**
 * ApiError carries the API's error message and, when present, the PostgreSQL
 * error code (for example "42601" for a syntax error) and HTTP status.
 */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly code?: string,
    readonly status?: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

// parseJson reads the JSON body and turns a non-2xx response into an ApiError
// carrying the server's message, PostgreSQL code, and HTTP status.
async function parseJson<T>(res: Response): Promise<T> {
  const body: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const err = (body ?? {}) as ErrorBody;
    throw new ApiError(err.error ?? `request failed with status ${res.status}`, err.code, res.status);
  }
  return body as T;
}

/**
 * Executes read-only SQL against the Playground sandbox and returns the capped
 * result. Rejects with an ApiError for a rejected query (4xx) or a server fault
 * (5xx); the caller's AbortSignal cancels the in-flight request.
 */
export async function executeQuery(query: string, signal?: AbortSignal): Promise<QueryResult> {
  const res = await fetch('/api/v1/playground/query', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ query }),
    signal,
  });
  const result = await parseJson<QueryResult>(res);
  if (result === null || typeof result !== 'object' || !Array.isArray(result.columns)) {
    throw new ApiError('received an invalid response from the server');
  }
  return result;
}

/** Persists a query and returns its generated id and load path. */
export async function saveQuery(input: SaveQueryInput, signal?: AbortSignal): Promise<SaveQueryResponse> {
  const res = await fetch('/api/v1/playground/save', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(input),
    signal,
  });
  return parseJson<SaveQueryResponse>(res);
}

/** Loads a saved query by its id. Rejects with an ApiError on 400/404/5xx. */
export async function loadQuery(id: string, signal?: AbortSignal): Promise<SavedQuery> {
  const res = await fetch(`/api/v1/playground/q/${encodeURIComponent(id)}`, { signal });
  return parseJson<SavedQuery>(res);
}
