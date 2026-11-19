// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { BarChart, LineChart, TreemapChart } from 'echarts/charts';
import {
  AriaComponent,
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
} from 'echarts/components';
import * as echarts from 'echarts/core';
import { CanvasRenderer } from 'echarts/renderers';
import { useEffect, useRef, useState } from 'react';
import type { ChartItemClick } from './Chart';
import type { ChartOption } from './options';

// Only the modules the dashboards use; this keeps the chart chunk small.
echarts.use([
  BarChart,
  LineChart,
  TreemapChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  TitleComponent,
  DataZoomComponent,
  AriaComponent,
  CanvasRenderer,
]);

function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(
    () =>
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const query = window.matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReduced(query.matches);
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);
  return reduced;
}

/**
 * The echarts-backed canvas. Loaded lazily by Chart; import Chart instead.
 * Draws `option`, redraws when it changes (a new palette gives a new option),
 * resizes with its container and disposes on unmount.
 */
export default function ChartCore({
  option,
  ariaLabel,
  onItemClick,
}: {
  option: ChartOption;
  ariaLabel: string;
  onItemClick?: (click: ChartItemClick) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const chart = useRef<echarts.ECharts | null>(null);
  const click = useRef(onItemClick);
  const reduced = useReducedMotion();

  useEffect(() => {
    click.current = onItemClick;
  }, [onItemClick]);

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const instance = echarts.init(el, undefined, { renderer: 'canvas' });
    chart.current = instance;
    instance.on('click', (params) => {
      const p = params as { dataIndex: number; name: string; seriesName?: string; data: unknown };
      click.current?.({
        dataIndex: p.dataIndex,
        name: p.name,
        seriesName: p.seriesName,
        data: p.data,
      });
    });
    const observer =
      typeof ResizeObserver === 'function' ? new ResizeObserver(() => instance.resize()) : null;
    observer?.observe(el);
    return () => {
      observer?.disconnect();
      instance.dispose();
      chart.current = null;
    };
  }, []);

  useEffect(() => {
    const aria = (option as { aria?: object }).aria ?? {};
    chart.current?.setOption(
      {
        ...option,
        animation: !reduced,
        aria: { ...aria, enabled: true, label: { description: ariaLabel } },
      },
      { notMerge: true },
    );
  }, [option, ariaLabel, reduced]);

  return <div ref={host} className="size-full" />;
}
