/** A result column's name and PostgreSQL type, as returned by the API. */
export interface Column {
  name: string;
  type: string;
}

/**
 * The outcome of a sandboxed Playground query. Cells are decoded as JSON, so a
 * value is a string, number, boolean, or null (numeric and timestamp columns
 * arrive as strings to preserve precision).
 */
export interface QueryResult {
  columns: Column[];
  rows: Cell[][];
  row_count: number;
  truncated: boolean;
}

export type Cell = string | number | boolean | null;

/** A persisted, shareable Playground query as returned by the load endpoint. */
export interface SavedQuery {
  id: string;
  title?: string;
  query: string;
  chart_config?: unknown;
  created_at: string;
}

/** The fields sent when saving a query. */
export interface SaveQueryInput {
  query: string;
  title?: string;
  chart_config?: unknown;
}

/** The save endpoint's response: the new id and the path that loads it. */
export interface SaveQueryResponse {
  id: string;
  url: string;
}
