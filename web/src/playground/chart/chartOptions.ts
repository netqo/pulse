import type { EChartsOption } from 'echarts';

import type { Cell, QueryResult } from '../../api/types';

/** The chart shapes offered by the result panel. */
export type ChartType = 'line' | 'bar' | 'candlestick';

/** Column indices mapped to a candlestick's four price points. */
export interface OhlcMapping {
  open: number;
  high: number;
  low: number;
  close: number;
}

/**
 * ChartConfig describes how a result maps onto a chart: the x-axis column, the
 * value columns (line/bar), and the OHLC columns (candlestick). Columns are
 * referenced by index because result column names are not guaranteed unique.
 */
export interface ChartConfig {
  type: ChartType;
  x: number;
  ys: number[];
  ohlc: OhlcMapping;
}

/** Which representation of a result is on screen. */
export type ResultView = 'table' | 'chart';

/**
 * SavedChartConfig is the persisted visualization state of a query: the chosen
 * view and, when charting, the column mapping. It is stored opaquely by the API
 * and restored when a shared query is loaded.
 */
export interface SavedChartConfig {
  view: ResultView;
  config: ChartConfig | null;
}

/** Converts a cell to a finite number, or null when it is not numeric. */
export function toNumber(cell: Cell): number | null {
  if (typeof cell === 'number') {
    return Number.isFinite(cell) ? cell : null;
  }
  if (typeof cell === 'string' && cell.trim() !== '') {
    const n = Number(cell);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

/** Reports whether a column has at least one value and every value is numeric. */
export function columnIsNumeric(result: QueryResult, col: number): boolean {
  let sawValue = false;
  for (const row of result.rows) {
    const cell = row[col];
    if (cell === null) {
      continue;
    }
    if (toNumber(cell) === null) {
      return false;
    }
    sawValue = true;
  }
  return sawValue;
}

/** Indices of every numeric column, left to right. */
export function numericColumns(result: QueryResult): number[] {
  return result.columns.map((_, i) => i).filter((i) => columnIsNumeric(result, i));
}

/**
 * defaultChartConfig picks a sensible initial mapping: a non-numeric column for
 * the x-axis when one exists (typically a timestamp or label), the first other
 * numeric column as the series, and OHLC columns detected by name. Returns null
 * when the result has no numeric column to plot.
 */
export function defaultChartConfig(result: QueryResult): ChartConfig | null {
  const numeric = numericColumns(result);
  if (numeric.length === 0) {
    return null;
  }
  const numericSet = new Set(numeric);
  let x = result.columns.findIndex((_, i) => !numericSet.has(i));
  if (x < 0) {
    x = 0;
  }
  const ys = numeric.filter((i) => i !== x);
  return {
    type: 'line',
    x,
    ys: ys.length > 0 ? [ys[0]] : [numeric[0]],
    ohlc: detectOhlc(result, numeric, x),
  };
}

// detectOhlc maps open/high/low/close by column name, falling back to the first
// available numeric columns for any that are not found by name.
function detectOhlc(result: QueryResult, numeric: number[], x: number): OhlcMapping {
  const byName = (keyword: string) =>
    result.columns.findIndex((c) => c.name.toLowerCase().includes(keyword));
  const fallback = numeric.filter((i) => i !== x);
  const pick = (keyword: string, nth: number) => {
    const named = byName(keyword);
    return named >= 0 ? named : (fallback[nth] ?? numeric[nth] ?? numeric[0]);
  };
  return {
    open: pick('open', 0),
    high: pick('high', 1),
    low: pick('low', 2),
    close: pick('close', 3),
  };
}

/**
 * chartConfigIssue returns a human-readable reason the current mapping cannot
 * produce a meaningful chart, or null when it can. It gates the confusing case
 * of a candlestick over a result without four numeric columns to map to OHLC.
 */
export function chartConfigIssue(result: QueryResult, config: ChartConfig): string | null {
  const numeric = numericColumns(result);
  if (config.type === 'candlestick') {
    if (numeric.length < 4) {
      return `Candlestick needs four numeric columns for open, high, low and close; this result has ${numeric.length}. Use line or bar, or run a query that returns OHLC columns.`;
    }
    return null;
  }
  if (config.ys.length === 0) {
    return 'Select at least one series column to plot.';
  }
  return null;
}

/**
 * isChartConfigCompatible reports whether a mapping can be applied to a result:
 * every referenced column index must be within the result's columns. A null
 * config (no chart) is always compatible. Used to decide whether a restored
 * chart mapping still fits, or the defaults should be used instead.
 */
export function isChartConfigCompatible(result: QueryResult, config: ChartConfig | null): boolean {
  if (config === null) {
    return true;
  }
  const cols = result.columns.length;
  const inRange = (i: number) => Number.isInteger(i) && i >= 0 && i < cols;
  const { open, high, low, close } = config.ohlc;
  return inRange(config.x) && config.ys.every(inRange) && [open, high, low, close].every(inRange);
}

/**
 * parseSavedChartConfig validates untrusted persisted state (as returned by the
 * API) into a SavedChartConfig, or null when it is absent or malformed. This
 * guards against stale or hand-edited stored values reaching the chart layer.
 */
export function parseSavedChartConfig(raw: unknown): SavedChartConfig | null {
  if (raw === null || typeof raw !== 'object') {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const view = record.view === 'chart' ? 'chart' : record.view === 'table' ? 'table' : null;
  if (view === null) {
    return null;
  }
  return { view, config: parseChartConfig(record.config) };
}

function parseChartConfig(raw: unknown): ChartConfig | null {
  if (raw === null || raw === undefined || typeof raw !== 'object') {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const type = record.type;
  if (type !== 'line' && type !== 'bar' && type !== 'candlestick') {
    return null;
  }
  if (typeof record.x !== 'number') {
    return null;
  }
  if (!Array.isArray(record.ys) || !record.ys.every((n) => typeof n === 'number')) {
    return null;
  }
  const ohlc = parseOhlc(record.ohlc);
  if (ohlc === null) {
    return null;
  }
  return { type, x: record.x, ys: record.ys as number[], ohlc };
}

function parseOhlc(raw: unknown): OhlcMapping | null {
  if (raw === null || typeof raw !== 'object') {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const keys: (keyof OhlcMapping)[] = ['open', 'high', 'low', 'close'];
  if (keys.some((k) => typeof record[k] !== 'number')) {
    return null;
  }
  return {
    open: record.open as number,
    high: record.high as number,
    low: record.low as number,
    close: record.close as number,
  };
}

/** Builds the ECharts option for a result under the given mapping. */
export function buildChartOption(result: QueryResult, config: ChartConfig): EChartsOption {
  const categories = result.rows.map((row) => cellToLabel(row[config.x]));
  const base = baseOption(categories);

  if (config.type === 'candlestick') {
    // ECharts candlestick datum order is [open, close, low, high].
    const data = result.rows.map((row) => [
      toNumber(row[config.ohlc.open]),
      toNumber(row[config.ohlc.close]),
      toNumber(row[config.ohlc.low]),
      toNumber(row[config.ohlc.high]),
    ]);
    return {
      ...base,
      series: [
        {
          type: 'candlestick',
          data,
          itemStyle: {
            color: '#bca9ae',
            color0: '#735960',
            borderColor: '#bca9ae',
            borderColor0: '#735960',
          },
        },
      ],
    };
  }

  const type = config.type;
  return {
    ...base,
    legend: { data: config.ys.map((c) => result.columns[c].name), textStyle: { color: '#a68c93' } },
    series: config.ys.map((col) => ({
      name: result.columns[col].name,
      type,
      showSymbol: false,
      data: result.rows.map((row) => toNumber(row[col])),
    })),
  };
}

function cellToLabel(cell: Cell): string {
  return cell === null ? '' : String(cell);
}

// baseOption holds the shared axes, grid, zoom and dusty-mauve theming.
function baseOption(categories: string[]): EChartsOption {
  return {
    backgroundColor: 'transparent',
    color: ['#bca9ae', '#a68c93', '#d3c5c9', '#906f78'],
    textStyle: { color: '#e9e2e4' },
    tooltip: { trigger: 'axis' },
    grid: { left: 16, right: 24, top: 24, bottom: 64, containLabel: true },
    xAxis: {
      type: 'category',
      data: categories,
      axisLine: { lineStyle: { color: '#564348' } },
      axisLabel: { color: '#a68c93' },
    },
    yAxis: {
      type: 'value',
      scale: true,
      axisLine: { lineStyle: { color: '#564348' } },
      axisLabel: { color: '#a68c93' },
      splitLine: { lineStyle: { color: '#3a2c30' } },
    },
    dataZoom: [
      { type: 'inside' },
      { type: 'slider', height: 18, bottom: 28, borderColor: '#3a2c30', fillerColor: '#3a2c3088' },
    ],
  };
}
