import { useCallback, useEffect, useRef, useState } from 'react';

import { ApiError, executeQuery } from '../api/client';
import type { QueryResult } from '../api/types';
import { ResultsPanel } from './ResultsPanel';
import { SqlEditor } from './SqlEditor';

const DEFAULT_QUERY = `SELECT symbol, base_asset, quote_asset
FROM instruments
ORDER BY symbol;`;

/**
 * Playground is the query workspace: a SQL editor above a results panel. It runs
 * the current query against the sandbox, cancelling any in-flight request when a
 * new run starts or the component unmounts.
 */
export function Playground() {
  const [query, setQuery] = useState(DEFAULT_QUERY);
  const [result, setResult] = useState<QueryResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const inFlight = useRef<AbortController | null>(null);

  const run = useCallback(async () => {
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;

    setRunning(true);
    setError(null);
    try {
      const next = await executeQuery(query, controller.signal);
      // Ignore a response whose request has been superseded by a newer run, so a
      // slow earlier query cannot overwrite a later one's result.
      if (inFlight.current !== controller) {
        return;
      }
      setResult(next);
    } catch (err) {
      if (controller.signal.aborted || inFlight.current !== controller) {
        return;
      }
      setResult(null);
      setError(err instanceof ApiError ? formatError(err) : 'Unexpected error running the query.');
    } finally {
      if (inFlight.current === controller) {
        inFlight.current = null;
        setRunning(false);
      }
    }
  }, [query]);

  useEffect(() => () => inFlight.current?.abort(), []);

  const canRun = !running && query.trim().length > 0;

  return (
    <section className="playground">
      <div className="editor-pane">
        <SqlEditor value={query} onChange={setQuery} onRun={run} disabled={!canRun} />
        <div className="toolbar">
          <button type="button" onClick={run} disabled={!canRun} aria-busy={running}>
            {running ? 'Running...' : 'Run'}
          </button>
          <span className="hint">Ctrl / Cmd + Enter</span>
        </div>
      </div>
      <div className="result-pane">
        {running && (
          <p className="muted" role="status">
            Running query...
          </p>
        )}
        {!running && error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}
        {!running && !error && result && <ResultsPanel result={result} />}
        {!running && !error && !result && <p className="muted">Run a query to see results.</p>}
      </div>
    </section>
  );
}

function formatError(err: ApiError): string {
  return err.code ? `${err.message} (${err.code})` : err.message;
}
