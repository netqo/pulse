import type { QueryResult } from '../api/types';
import { ChartControls } from './ChartControls';
import { ResultChart } from './chart/ResultChart';
import {
  chartConfigIssue,
  isChartConfigCompatible,
  type ChartConfig,
  type ResultView,
} from './chart/chartOptions';
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
          id="tab-table"
          role="tab"
          aria-selected={view === 'table'}
          aria-controls="result-view"
          className={view === 'table' ? 'active' : ''}
          onClick={() => onViewChange('table')}
        >
          Table
        </button>
        <button
          type="button"
          id="tab-chart"
          role="tab"
          aria-selected={view === 'chart'}
          aria-controls="result-view"
          className={view === 'chart' ? 'active' : ''}
          onClick={() => onViewChange('chart')}
        >
          Chart
        </button>
      </div>

      <div id="result-view" role="tabpanel" aria-labelledby={view === 'table' ? 'tab-table' : 'tab-chart'}>
        {view === 'table' && <ResultsTable result={result} />}
        {view === 'chart' &&
          // config is null only when the result has no numeric column. When it is
          // non-null but incompatible, we are in the brief frame after a new result
          // committed but before the parent's reset effect ran; render nothing that
          // frame rather than indexing out-of-range columns (which would throw).
          (config === null ? (
            <p className="muted">This result has no numeric column to chart.</p>
          ) : isChartConfigCompatible(result, config) ? (
            <ChartArea result={result} config={config} onChange={onConfigChange} />
          ) : null)}
      </div>
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
