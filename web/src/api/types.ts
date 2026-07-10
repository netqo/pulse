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
