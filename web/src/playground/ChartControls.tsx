import type { QueryResult } from '../api/types';
import { numericColumns, type ChartConfig, type ChartType, type OhlcMapping } from './chart/chartOptions';

const CHART_TYPES: { value: ChartType; label: string }[] = [
  { value: 'line', label: 'Line' },
  { value: 'bar', label: 'Bar' },
  { value: 'candlestick', label: 'Candlestick' },
];

interface ChartControlsProps {
  result: QueryResult;
  config: ChartConfig;
  onChange: (next: ChartConfig) => void;
}

/** Controls for mapping result columns onto the chart axes and series. */
export function ChartControls({ result, config, onChange }: ChartControlsProps) {
  const numeric = numericColumns(result);

  const toggleSeries = (col: number) => {
    const selected = new Set(config.ys);
    if (selected.has(col)) {
      selected.delete(col);
    } else {
      selected.add(col);
    }
    onChange({ ...config, ys: [...selected].sort((a, b) => a - b) });
  };

  return (
    <div className="chart-controls">
      <label className="control">
        <span>Chart</span>
        <select
          value={config.type}
          onChange={(e) => onChange({ ...config, type: e.target.value as ChartType })}
        >
          {CHART_TYPES.map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </select>
      </label>

      <label className="control">
        <span>X axis</span>
        <select value={config.x} onChange={(e) => onChange({ ...config, x: Number(e.target.value) })}>
          {result.columns.map((col, i) => (
            <option key={i} value={i}>
              {col.name}
            </option>
          ))}
        </select>
      </label>

      {config.type === 'candlestick' ? (
        numeric.length >= 4 && (
          <OhlcControls result={result} config={config} numeric={numeric} onChange={onChange} />
        )
      ) : (
        <fieldset className="control series">
          <legend>Series</legend>
          {numeric.length === 0 ? (
            <span className="muted">no numeric columns</span>
          ) : (
            numeric.map((i) => (
              <label key={i} className="checkbox">
                <input type="checkbox" checked={config.ys.includes(i)} onChange={() => toggleSeries(i)} />
                {result.columns[i].name}
              </label>
            ))
          )}
        </fieldset>
      )}
    </div>
  );
}

const OHLC_KEYS: (keyof OhlcMapping)[] = ['open', 'high', 'low', 'close'];

interface OhlcControlsProps {
  result: QueryResult;
  config: ChartConfig;
  numeric: number[];
  onChange: (next: ChartConfig) => void;
}

/** Four selects binding the candlestick's price points to numeric columns. */
function OhlcControls({ result, config, numeric, onChange }: OhlcControlsProps) {
  return (
    <div className="ohlc-controls">
      {OHLC_KEYS.map((key) => (
        <label key={key} className="control">
          <span>{key[0].toUpperCase() + key.slice(1)}</span>
          <select
            value={config.ohlc[key]}
            onChange={(e) => onChange({ ...config, ohlc: { ...config.ohlc, [key]: Number(e.target.value) } })}
          >
            {numeric.map((i) => (
              <option key={i} value={i}>
                {result.columns[i].name}
              </option>
            ))}
          </select>
        </label>
      ))}
    </div>
  );
}
