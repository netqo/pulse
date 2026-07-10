import type { Cell as CellValue, QueryResult } from '../api/types';

/** Renders a query result as a scrollable table with a row-count summary. */
export function ResultsTable({ result }: { result: QueryResult }) {
  if (result.columns.length === 0) {
    return <p className="muted">Query returned no columns.</p>;
  }

  return (
    <div className="results">
      <div className="results-meta">
        <span>
          {result.row_count} row{result.row_count === 1 ? '' : 's'}
        </span>
        {result.truncated && <span className="badge" title="More rows exist than the sandbox returns">truncated</span>}
      </div>
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              {result.columns.map((col, i) => (
                <th key={i} scope="col">
                  <span className="col-name">{col.name}</span>
                  <span className="col-type">{col.type}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {result.rows.map((row, i) => (
              <tr key={i}>
                {row.map((cell, j) => (
                  <Cell key={j} value={cell} />
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/**
 * Cell renders one value. Non-null values carry the full text as a title so a
 * value truncated by the column's max width is still readable on hover; NULL is
 * shown distinctly.
 */
function Cell({ value }: { value: CellValue }) {
  if (value === null) {
    return (
      <td>
        <span className="null">NULL</span>
      </td>
    );
  }
  const text = String(value);
  return <td title={text}>{text}</td>;
}
