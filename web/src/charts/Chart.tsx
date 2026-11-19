// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Component, Suspense, lazy, useMemo, type ReactNode } from 'react';
import { EmptyState, ErrorState, SkeletonChart } from '../components/states';
import { formatInt } from '../format';
import type { ChartFactory, ChartOption } from './options';
import { usePalette } from './palette';
import { summarizeOption } from './summary';

/**
 * The echarts code lives in ChartCore, which is loaded with a dynamic import the
 * first time a chart is shown. Nothing from echarts is in the initial bundle.
 */
const ChartCore = lazy(() => import('./ChartCore'));

export interface ChartItemClick {
  /** Index of the clicked bar group, point or tile. */
  dataIndex: number;
  name: string;
  seriesName?: string;
  /** The raw data item; for treemap tiles it has `id`. */
  data: unknown;
}

export interface ChartProps {
  /** A finished option or, usually, the result of stackedSeverityBars, lineSeries or treemap. */
  option: ChartOption | ChartFactory;
  /**
   * What the chart answers, in one sentence: "Findings per scan, last 30 scans".
   * It names the chart for screen readers and captions the hidden data table.
   */
  ariaLabel: string;
  /** Tailwind height of the chart box. Loading, empty and error states use the same box. */
  heightClass?: string;
  status?: 'ready' | 'loading' | 'error' | 'empty';
  emptyTitle?: string;
  emptyBody?: string;
  errorMessage?: string;
  onRetry?: () => void;
  /** Mouse clicks on bars and tiles. Offer the same destinations as links elsewhere: the canvas is not keyboard-operable. */
  onItemClick?: (click: ChartItemClick) => void;
  className?: string;
}

class ChunkBoundary extends Component<
  { children: ReactNode; fallback: (retry: () => void) => ReactNode },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed
      ? this.props.fallback(() => this.setState({ failed: false }))
      : this.props.children;
  }
}

function HiddenTable({ option, caption }: { option: ChartOption; caption: string }) {
  const { columns, rows } = useMemo(() => summarizeOption(option), [option]);
  return (
    <table className="sr-only">
      <caption>{caption}</caption>
      <thead>
        <tr>
          {columns.map((column, i) => (
            <th key={i} scope="col">
              {column}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, r) => (
          <tr key={r}>
            {row.map((cell, c) =>
              c === 0 ? (
                <th key={c} scope="row">
                  {cell}
                </th>
              ) : (
                <td key={c}>{typeof cell === 'number' ? formatInt(cell) : cell}</td>
              ),
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/**
 * A themed, resizing, reduced-motion-aware chart. Colours are read from the CSS
 * tokens at run time and re-read when the theme changes; animation is off under
 * prefers-reduced-motion; the canvas follows its container's size. A skeleton of
 * the same size shows while the chart code loads, and a visually hidden data
 * table carries the numbers for screen readers.
 */
export function Chart({
  option,
  ariaLabel,
  heightClass = 'h-72',
  status = 'ready',
  emptyTitle = 'No data to chart yet',
  emptyBody,
  errorMessage = 'The chart data could not be loaded.',
  onRetry,
  onItemClick,
  className = '',
}: ChartProps) {
  const palette = usePalette();
  const resolved = useMemo(
    () => (typeof option === 'function' ? option(palette) : option),
    [option, palette],
  );
  const box = `${heightClass} ${className}`;

  if (status === 'loading') return <SkeletonChart className={box} />;
  if (status === 'empty') {
    return (
      <div className={`flex items-center justify-center ${box}`}>
        <EmptyState compact title={emptyTitle} body={emptyBody} headingLevel={3} />
      </div>
    );
  }
  if (status === 'error') {
    return (
      <div className={`flex items-center justify-center ${box}`}>
        <ErrorState compact message={errorMessage} onRetry={onRetry} />
      </div>
    );
  }

  return (
    <div className={`relative ${box}`}>
      <ChunkBoundary
        fallback={(retry) => (
          <ErrorState compact message="The chart could not be loaded." onRetry={retry} />
        )}
      >
        <Suspense fallback={<SkeletonChart className="h-full" />}>
          <div role="img" aria-label={ariaLabel} className="size-full">
            <ChartCore option={resolved} ariaLabel={ariaLabel} onItemClick={onItemClick} />
          </div>
        </Suspense>
      </ChunkBoundary>
      <HiddenTable option={resolved} caption={ariaLabel} />
    </div>
  );
}
