import { useEffect, useMemo, useState } from 'react';

import type { QueryResult } from '../api/types';
import { ChartControls } from './ChartControls';
import { ResultChart } from './chart/ResultChart';
import { chartConfigIssue, defaultChartConfig, type ChartConfig } from './chart/chartOptions';
import { ResultsTable } from './ResultsTable';

type View = 'table' | 'chart';

/**
 * ResultsPanel shows a result as either a table or a chart. The view choice
 * persists across runs; the chart mapping resets to sensible defaults whenever a
 * new result arrives, since its columns may differ.
 */
export function ResultsPanel({ result }: { result: QueryResult }) {
  const [view, setView] = useState<View>('table');
  const initialConfig = useMemo(() => defaultChartConfig(result), [result]);
  const [config, setConfig] = useState<ChartConfig | null>(initialConfig);

  useEffect(() => {
    setConfig(initialConfig);
  }, [initialConfig]);

  return (
    <div className="results-panel">
      <div className="view-toggle" role="tablist" aria-label="Result view">
        <button
          type="button"
          role="tab"
          aria-selected={view === 'table'}
          className={view === 'table' ? 'active' : ''}
          onClick={() => setView('table')}
        >
          Table
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={view === 'chart'}
          className={view === 'chart' ? 'active' : ''}
          onClick={() => setView('chart')}
        >
          Chart
        </button>
      </div>

      {view === 'table' && <ResultsTable result={result} />}
      {view === 'chart' &&
        (config ? (
          <ChartArea result={result} config={config} onChange={setConfig} />
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
