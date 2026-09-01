import ReactEChartsCore from 'echarts-for-react/lib/core';
import { useMemo } from 'react';

import type { QueryResult } from '../../api/types';
import { buildChartOption, type ChartConfig } from './chartOptions';
import { echarts } from './echarts';

/** Renders a query result as an ECharts chart under the given mapping. */
export function ResultChart({ result, config }: { result: QueryResult; config: ChartConfig }) {
  const option = useMemo(() => buildChartOption(result, config), [result, config]);
  return (
    <ReactEChartsCore
      echarts={echarts}
      option={option}
      notMerge
      lazyUpdate
      style={{ height: '100%', width: '100%' }}
    />
  );
}
