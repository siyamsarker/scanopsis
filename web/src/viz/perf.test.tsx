// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { act, fireEvent, render as baseRender } from '@testing-library/react';
import type { ReactElement } from 'react';
import { MemoryRouter } from 'react-router';
import { HeatStrip } from './HeatStrip';
import { PipelineStrip } from './PipelineStrip';
import { SAMPLE_NOW, samplePipelines, sampleServices } from './samples';

const render = (ui: ReactElement) => baseRender(ui, { wrapper: MemoryRouter });

/**
 * Render budgets in jsdom, which is several times slower than a browser: the
 * numbers guard against accidental quadratic work, not frame times.
 */
const STRIP_BUDGET_MS = 400;
const HOVER_BUDGET_MS = 100;
const HEAT_BUDGET_MS = 1000;

function timed(fn: () => void) {
  const start = performance.now();
  fn();
  return performance.now() - start;
}

describe('render budget', () => {
  it('draws a 300-pipeline strip and moves the crosshair quickly', () => {
    const points = samplePipelines(300, { seed: 19, spacingHours: 6 });
    // Warm up module and style caches so the measurement is the render itself.
    render(<PipelineStrip points={points.slice(0, 10)} width={1440} />).unmount();
    let container!: HTMLElement;
    const ms = timed(() => {
      container = render(
        <PipelineStrip points={points} width={1440} gateCoverageThreshold={80} />,
      ).container;
    });
    expect(container.querySelectorAll('.viz-mark')).toHaveLength(300);
    const marks = container.querySelectorAll('.viz-mark');
    const hover = timed(() => {
      act(() => {
        fireEvent.pointerEnter(marks[150]);
      });
    });
    console.info(
      `PipelineStrip, 300 pipelines: render ${ms.toFixed(1)} ms, hover ${hover.toFixed(1)} ms`,
    );
    expect(ms).toBeLessThan(STRIP_BUDGET_MS);
    expect(hover).toBeLessThan(HOVER_BUDGET_MS);
  });

  it('draws a 500-service heat strip', () => {
    const rows = sampleServices(500, 29);
    render(<HeatStrip rows={rows.slice(0, 10)} now={SAMPLE_NOW} />).unmount();
    let container!: HTMLElement;
    const ms = timed(() => {
      container = render(
        <HeatStrip rows={rows} now={SAMPLE_NOW} onSelect={() => {}} maxHeight={600} />,
      ).container;
    });
    expect(container.querySelectorAll('tbody tr')).toHaveLength(500);
    console.info(`HeatStrip, 500 services: render ${ms.toFixed(1)} ms`);
    expect(ms).toBeLessThan(HEAT_BUDGET_MS);
  });
});
