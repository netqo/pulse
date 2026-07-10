import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { ApiError, executeQuery, loadQuery, saveQuery } from '../api/client';
import type { QueryResult } from '../api/types';
import { ResultsPanel } from './ResultsPanel';
import { SqlEditor } from './SqlEditor';

const DEFAULT_QUERY = `SELECT symbol, base_asset, quote_asset
FROM instruments
ORDER BY symbol;`;

/**
 * Playground is the query workspace: a SQL editor over a results panel, with
 * save and share. It runs the current query against the sandbox (cancelling any
 * in-flight request when a new run starts), saves it for a shareable URL, and
 * loads a shared query when the route carries an id.
 */
export function Playground() {
  const { id } = useParams();
  const navigate = useNavigate();

  const [query, setQuery] = useState(DEFAULT_QUERY);
  const [title, setTitle] = useState('');
  const [result, setResult] = useState<QueryResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [shareUrl, setShareUrl] = useState<string | null>(null);
  const inFlight = useRef<AbortController | null>(null);

  const execute = useCallback(async (sql: string) => {
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;

    setRunning(true);
    setError(null);
    try {
      const next = await executeQuery(sql, controller.signal);
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
  }, []);

  const run = useCallback(() => execute(query), [execute, query]);

  // Load a shared query when the URL carries an id. The editor is populated but
  // not run: the visitor triggers the query themselves.
  useEffect(() => {
    if (!id) {
      return;
    }
    const controller = new AbortController();
    void (async () => {
      try {
        const saved = await loadQuery(id, controller.signal);
        setQuery(saved.query);
        setTitle(saved.title ?? '');
        setShareUrl(`${window.location.origin}/q/${id}`);
      } catch (err) {
        if (controller.signal.aborted) {
          return;
        }
        setResult(null);
        setError(err instanceof ApiError ? formatError(err) : 'Failed to load the saved query.');
      }
    })();
    return () => controller.abort();
  }, [id]);

  useEffect(() => () => inFlight.current?.abort(), []);

  const onQueryChange = useCallback((value: string) => {
    setQuery(value);
    setShareUrl(null);
    setSaveError(null);
  }, []);

  const onTitleChange = useCallback((value: string) => {
    setTitle(value);
    setShareUrl(null);
    setSaveError(null);
  }, []);

  const save = useCallback(async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const saved = await saveQuery({ query, title: title.trim() || undefined });
      setShareUrl(`${window.location.origin}/q/${saved.id}`);
      navigate(`/q/${saved.id}`);
    } catch (err) {
      setSaveError(err instanceof ApiError ? formatError(err) : 'Failed to save the query.');
    } finally {
      setSaving(false);
    }
  }, [query, title, navigate]);

  const hasQuery = query.trim().length > 0;

  return (
    <section className="playground">
      <div className="editor-pane">
        <SqlEditor value={query} onChange={onQueryChange} onRun={run} disabled={running || !hasQuery} />
        <div className="toolbar">
          <button type="button" onClick={run} disabled={running || !hasQuery} aria-busy={running}>
            {running ? 'Running...' : 'Run'}
          </button>
          <span className="hint">Ctrl / Cmd + Enter</span>
          <input
            className="title-input"
            type="text"
            placeholder="Untitled query"
            value={title}
            onChange={(e) => onTitleChange(e.target.value)}
            aria-label="Query title"
          />
          <button
            type="button"
            className="secondary"
            onClick={save}
            disabled={saving || !hasQuery}
            aria-busy={saving}
          >
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
        {saveError && (
          <div className="save-error" role="alert">
            {saveError}
          </div>
        )}
        {shareUrl && <ShareLink url={shareUrl} />}
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

/** ShareLink shows a saved query's URL with a one-click copy. */
function ShareLink({ url }: { url: string }) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState(false);

  // Reset the "Copied" label whenever the URL changes (a new save).
  useEffect(() => setCopied(false), [url]);

  const copy = useCallback(async () => {
    const ok = await copyToClipboard(url, inputRef.current);
    setCopied(ok);
  }, [url]);

  return (
    <div className="share-link">
      <span className="share-label">Shareable link</span>
      <input
        ref={inputRef}
        type="text"
        readOnly
        value={url}
        onFocus={(e) => e.target.select()}
        aria-label="Shareable link"
      />
      <button type="button" onClick={copy}>
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  );
}

/**
 * Copies text to the clipboard, returning whether it succeeded. Uses the async
 * Clipboard API when available (secure contexts: HTTPS or localhost) and falls
 * back to selecting the input and execCommand('copy'), which also works over
 * plain HTTP on a LAN address where navigator.clipboard is undefined.
 */
async function copyToClipboard(text: string, input: HTMLInputElement | null): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Fall through to the execCommand path below.
    }
  }
  if (!input) {
    return false;
  }
  input.focus();
  input.select();
  input.setSelectionRange(0, text.length);
  try {
    return document.execCommand('copy');
  } catch {
    return false;
  }
}

function formatError(err: ApiError): string {
  return err.code ? `${err.message} (${err.code})` : err.message;
}
