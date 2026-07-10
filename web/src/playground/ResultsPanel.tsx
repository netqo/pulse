import type { QueryResult } from '../api/types';
import { ChartControls } from './ChartControls';
import { ResultChart } from './chart/ResultChart';
import { chartConfigIssue, type ChartConfig, type ResultView } from './chart/chartOptions';
import { ResultsTable } from './ResultsTable';

interface ResultsPanelProps {
  result: QueryResult;
  view: ResultView;
  onViewChange: (view: ResultView) => void;
  config: ChartConfig | null;
  onConfigChange: (config: ChartConfig) => void;
}

/**
 * ResultsPanel shows a result as either a table or a chart. It is controlled:
 * the parent owns the view and chart mapping so both can be persisted and
 * restored with a saved query.
 */
export function ResultsPanel({ result, view, onViewChange, config, onConfigChange }: ResultsPanelProps) {
  return (
    <div className="results-panel">
      <div className="view-toggle" role="tablist" aria-label="Result view">
        <button
          type="button"
          role="tab"
          aria-selected={view === 'table'}
          className={view === 'table' ? 'active' : ''}
          onClick={() => onViewChange('table')}
        >
          Table
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={view === 'chart'}
          className={view === 'chart' ? 'active' : ''}
          onClick={() => onViewChange('chart')}
        >
          Chart
        </button>
      </div>

      {view === 'table' && <ResultsTable result={result} />}
      {view === 'chart' &&
        (config ? (
          <ChartArea result={result} config={config} onChange={onConfigChange} />
        ) : (
          <p className="muted">This result has no numeric column to chart.</p>
        ))}
    </div>
  );
}

interface ChartAreaProps {
  result: QueryResult;
  config: ChartConfig;
  onChange: (next: ChartConfig) => void;
}

// ChartArea renders the mapping controls plus either the chart or a hint when
// the current mapping cannot produce a meaningful one.
function ChartArea({ result, config, onChange }: ChartAreaProps) {
  const issue = chartConfigIssue(result, config);
  return (
    <div className="chart-area">
      <ChartControls result={result} config={config} onChange={onChange} />
      {issue ? (
        <p className="muted chart-hint">{issue}</p>
      ) : (
        <div className="chart-canvas">
          <ResultChart result={result} config={config} />
        </div>
      )}
    </div>
  );
}
