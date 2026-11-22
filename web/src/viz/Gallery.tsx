// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Development only: every component of the visual kit with sample data, in
 * the states that matter (empty, one point, many points, extreme values). Not
 * routed; `npm run dev` serves it at /gallery.html. Pass `theme` or use the
 * switch to see both palettes.
 */

import { useMemo, useState, type ReactNode } from 'react';
import { DistributionMini, SeverityBar } from './SeverityBar';
import { HeatStrip } from './HeatStrip';
import { PipelineSpark } from './PipelineSpark';
import { PipelineStrip } from './PipelineStrip';
import { riskDelta, riskPoints } from './risk';
import { RiskIris } from './RiskIris';
import { samplePipelines, sampleServices, sampleUploads, SAMPLE_NOW } from './samples';
import type { SeverityCounts } from './severity';
import { Delta, Sparkline } from './Sparkline';
import type { PipelinePoint } from './stripLayout';
import { TrendArea } from './TrendArea';
import './gallery.css';

export type GalleryTheme = 'dark' | 'light';

const PAYMENTS: SeverityCounts = { critical: 2, high: 44, medium: 57, low: 29, unknown: 3 };
const AUTH: SeverityCounts = { critical: 0, high: 3, medium: 12, low: 9, unknown: 0 };
const EXTREME: SeverityCounts = { critical: 1, high: 0, medium: 0, low: 12_480, unknown: 0 };
const ZERO: SeverityCounts = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };

const ONE_POINT: PipelinePoint[] = [
  {
    pipelineId: 1002,
    at: '2026-11-19T09:12:00Z',
    ref: 'main',
    artifact: 'python:3.9-slim-buster',
    newBySeverity: { critical: 1, high: 4, medium: 6 },
    fixedCount: 3,
    gate: 'fail',
    coverage: 61.4,
  },
];

const SPIKE: PipelinePoint[] = samplePipelines(24, { seed: 3, refs: ['main'], pauseDays: 0 }).map(
  (p, i) =>
    i === 17
      ? { ...p, newBySeverity: { critical: 12, high: 240, medium: 610, low: 380, unknown: 9 } }
      : i === 20
        ? { ...p, fixedCount: 1180 }
        : p,
);

/** Risk after each pipeline, starting from 260: new findings add, fixes remove. */
function runningRisk(points: PipelinePoint[]): number[] {
  return points.reduce<number[]>((acc, p) => {
    const prev = acc.length > 0 ? acc[acc.length - 1] : 260;
    acc.push(Math.max(0, prev + riskPoints(p.newBySeverity) - p.fixedCount * 3));
    return acc;
  }, []);
}

function Section({ title, lede, children }: { title: string; lede: string; children: ReactNode }) {
  return (
    <section aria-label={title}>
      <h2>{title}</h2>
      <p className="lede">{lede}</p>
      {children}
    </section>
  );
}

function Panel({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <div className="viz-gallery-panel">
      {title && <h3>{title}</h3>}
      {children}
    </div>
  );
}

export default function Gallery({ theme: initial = 'dark' }: { theme?: GalleryTheme }) {
  const [theme, setTheme] = useState<GalleryTheme>(initial);
  const [selected, setSelected] = useState('Nothing selected yet');
  const many = useMemo(() => samplePipelines(96), []);
  const maxed = useMemo(() => samplePipelines(300, { seed: 19, spacingHours: 6 }), []);
  const services = useMemo(() => sampleServices(12), []);
  const lots = useMemo(() => sampleServices(60, 23), []);
  const uploads = useMemo(() => sampleUploads(60), []);
  const riskSeries = useMemo(() => runningRisk(many.slice(-24)), [many]);
  const delta = riskDelta(AUTH, PAYMENTS);

  return (
    <div className="viz-gallery" data-viz-theme={theme}>
      <div className="viz-gallery-inner">
        <header>
          <div>
            <h1>Visual kit</h1>
            <p className="lede">Charts and figures with sample data. Selected: {selected}</p>
          </div>
          <div className="viz-gallery-switch" role="group" aria-label="Theme">
            {(['dark', 'light'] as const).map((t) => (
              <button key={t} type="button" aria-pressed={theme === t} onClick={() => setTheme(t)}>
                {t === 'dark' ? 'Dark' : 'Light'}
              </button>
            ))}
          </div>
        </header>

        <Section
          title="Risk iris"
          lede="Ring segments are each severity's share of the risk points; the hairline outside is the quality gate."
        >
          <Panel>
            <div className="viz-gallery-iris-row">
              <figure>
                <RiskIris counts={PAYMENTS} gate="pass" size={168} name="payments-api" />
                <figcaption>168, gate passed</figcaption>
              </figure>
              <figure>
                <RiskIris counts={PAYMENTS} gate="fail" size={96} name="payments-api" />
                <figcaption>96, gate failed</figcaption>
              </figure>
              <figure>
                <RiskIris counts={AUTH} gate="none" size={96} name="auth-service" />
                <figcaption>96, no gate</figcaption>
              </figure>
              <figure>
                <RiskIris counts={ZERO} gate="pass" size={96} name="ledger" />
                <figcaption>Zero risk</figcaption>
              </figure>
              <figure>
                <RiskIris counts={null} size={96} name="new-service" />
                <figcaption>No scan yet</figcaption>
              </figure>
              <figure>
                <RiskIris counts={EXTREME} gate="fail" size={96} name="legacy-monolith" />
                <figcaption>Extreme</figcaption>
              </figure>
              <figure>
                <RiskIris counts={PAYMENTS} gate="pass" size={40} />
                <figcaption>40</figcaption>
              </figure>
              <figure>
                <RiskIris counts={EXTREME} gate="fail" size={40} />
                <figcaption>40, extreme</figcaption>
              </figure>
              <figure>
                <RiskIris counts={ZERO} size={40} />
                <figcaption>40, zero</figcaption>
              </figure>
            </div>
          </Panel>
        </Section>

        <Section
          title="Pipeline strip"
          lede="One column per pipeline: new findings above the baseline, fixed below, gate dots on top, coverage on its own track."
        >
          <div className="viz-gallery-stack">
            <Panel title="96 pipelines on three branches, a nine-day pause, coverage gate 80%">
              <PipelineStrip
                points={many}
                gateCoverageThreshold={80}
                onSelect={(p) => setSelected(`pipeline #${p.pipelineId}`)}
                label="payments-api pipelines"
              />
            </Panel>
            <Panel title="Split by branch">
              <PipelineStrip points={many.slice(-40)} defaultSplit label="Pipelines by branch" />
            </Panel>
            <Panel title="300 pipelines (oldest left out when they do not fit)">
              <PipelineStrip points={maxed} gateCoverageThreshold={70} label="300 pipelines" />
            </Panel>
            <div className="viz-gallery-grid">
              <Panel title="One pipeline">
                <PipelineStrip points={ONE_POINT} gateCoverageThreshold={80} label="One pipeline" />
              </Panel>
              <Panel title="Extreme spike and a big clean-up">
                <PipelineStrip points={SPIKE} label="Spike" lanes={false} />
              </Panel>
              <Panel title="No pipelines">
                <PipelineStrip points={[]} label="Empty" />
              </Panel>
            </div>
          </div>
        </Section>

        <Section title="Cards" lede="Pipeline spark, sparkline and delta in card and tile sizes.">
          <div className="viz-gallery-grid">
            <div className="viz-gallery-card">
              <RiskIris counts={PAYMENTS} gate="pass" size={40} />
              <div>
                <div className="name">payments-api</div>
                <div className="meta">Last 30 pipelines</div>
              </div>
              <PipelineSpark points={many} label="payments-api" />
            </div>
            <div className="viz-gallery-card">
              <RiskIris counts={AUTH} gate="fail" size={40} />
              <div>
                <div className="name">auth-service</div>
                <div className="meta">One pipeline</div>
              </div>
              <PipelineSpark points={ONE_POINT} label="auth-service" />
            </div>
            <div className="viz-gallery-card">
              <RiskIris counts={null} size={40} />
              <div>
                <div className="name">new-service</div>
                <div className="meta">No pipelines</div>
              </div>
              <PipelineSpark points={[]} label="new-service" />
            </div>
          </div>
          <div className="viz-gallery-grid viz-gallery-gap">
            <Panel>
              <div className="viz-gallery-tile">
                <span className="label">Risk points</span>
                <span className="value">
                  {riskPoints(PAYMENTS)}
                  <Delta value={delta.delta} unit="risk points" since="since the previous scan" />
                </span>
                <Sparkline values={riskSeries} label="Risk points" tone="risk" fill />
              </div>
            </Panel>
            <Panel>
              <div className="viz-gallery-tile">
                <span className="label">Open findings</span>
                <span className="value">
                  135
                  <Delta value={-12} unit="findings" since="this week" />
                </span>
                <Sparkline
                  values={[150, 149, 151, 147, 140, 141, 138, 135]}
                  label="Open findings"
                />
              </div>
            </Panel>
            <Panel>
              <div className="viz-gallery-tile">
                <span className="label">Coverage</span>
                <span className="value">
                  64.2%
                  <Delta value={0} better="up" format={(v) => `${v} pts`} />
                </span>
                <Sparkline values={[64.2]} label="Coverage" tone="ok" />
              </div>
            </Panel>
            <Panel>
              <div className="viz-gallery-tile">
                <span className="label">Uploads, with a gap</span>
                <span className="value">
                  41
                  <Delta value={7} better="up" />
                </span>
                <Sparkline values={[30, 32, null, null, 35, 41]} label="Uploads" tone="brand" />
              </div>
            </Panel>
          </div>
        </Section>

        <Section
          title="Severity bar and distribution"
          lede="One whole split by severity; segments can link to their findings."
        >
          <div className="viz-gallery-grid">
            <Panel title="Large, linked">
              <SeverityBar
                counts={PAYMENTS}
                size="lg"
                hrefFor={(s) => `#findings-${s}`}
                label="Open vulnerabilities"
              />
            </Panel>
            <Panel title="Medium and small, compared against 200">
              <div className="viz-gallery-stack">
                <SeverityBar counts={PAYMENTS} size="md" max={200} />
                <SeverityBar counts={AUTH} size="md" max={200} />
                <SeverityBar counts={AUTH} size="sm" max={200} />
              </div>
            </Panel>
            <Panel title="Extreme and empty">
              <div className="viz-gallery-stack">
                <SeverityBar counts={EXTREME} size="lg" />
                <SeverityBar counts={ZERO} size="lg" />
                <SeverityBar counts={ZERO} size="md" />
              </div>
            </Panel>
            <Panel title="Distribution mini, shared maximum">
              <table className="viz-gallery-mini-table">
                <tbody>
                  {[
                    ['payments-api', PAYMENTS],
                    ['auth-service', AUTH],
                    ['legacy-monolith', EXTREME],
                    ['ledger', ZERO],
                  ].map(([name, counts]) => (
                    <tr key={name as string}>
                      <td>{name as string}</td>
                      <td>
                        <DistributionMini counts={counts as SeverityCounts} max={200} showTotal />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Panel>
          </div>
        </Section>

        <Section
          title="Heat strip"
          lede="Services ranked by risk; cell intensity grows with the count per severity."
        >
          <div className="viz-gallery-stack">
            <Panel title="Twelve services">
              <HeatStrip
                rows={services}
                now={SAMPLE_NOW}
                onSelect={(row) => setSelected(row.name)}
              />
            </Panel>
            <Panel title="Sixty services in a 420 px window (sticky header)">
              <HeatStrip
                rows={lots}
                now={SAMPLE_NOW}
                maxHeight={420}
                onSelect={(row) => setSelected(row.name)}
              />
            </Panel>
            <Panel title="No services">
              <HeatStrip rows={[]} />
            </Panel>
          </div>
        </Section>

        <Section title="Trend area" lede="A plain time series with an optional reference line.">
          <div className="viz-gallery-stack">
            <Panel title="Uploads per day, two days missing">
              <TrendArea
                points={uploads}
                label="Uploads per day"
                unit="uploads"
                reference={{ value: 80, label: 'Rate limit 80 a day' }}
              />
            </Panel>
            <div className="viz-gallery-grid">
              <Panel title="One day">
                <TrendArea
                  points={uploads.slice(-1)}
                  label="Uploads today"
                  unit="uploads"
                  height={140}
                />
              </Panel>
              <Panel title="No data">
                <TrendArea points={[]} label="Uploads per day" height={140} />
              </Panel>
            </div>
          </div>
        </Section>
      </div>
    </div>
  );
}
