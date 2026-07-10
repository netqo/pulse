import type { QueryResult } from './types';

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

  const body: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const err = (body ?? {}) as ErrorBody;
    throw new ApiError(err.error ?? `request failed with status ${res.status}`, err.code, res.status);
  }
  if (body === null || typeof body !== 'object' || !Array.isArray((body as QueryResult).columns)) {
    throw new ApiError('received an invalid response from the server');
  }
  return body as QueryResult;
}
