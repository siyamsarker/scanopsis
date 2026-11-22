// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Deterministic sample data for the gallery and the tests. A seeded generator
 * keeps every render (and every screenshot) identical.
 */

import type { HeatRow } from './HeatStrip';
import type { GateState, SeverityCounts } from './severity';
import type { PipelinePoint } from './stripLayout';
import type { TrendPoint } from './TrendArea';

export function seeded(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export const SAMPLE_NOW = Date.UTC(2026, 10, 19, 12, 0);
const HOUR = 3_600_000;

export interface PipelineOptions {
  seed?: number;
  refs?: string[];
  /** Insert a pause of this many days before the pipeline at 60% of the run. */
  pauseDays?: number;
  coverage?: boolean;
  end?: number;
  spacingHours?: number;
}

export function samplePipelines(count: number, options: PipelineOptions = {}): PipelinePoint[] {
  const {
    seed = 7,
    refs = ['main', 'feature/token-refresh', 'release/2.4'],
    pauseDays = 9,
    coverage = true,
    end = SAMPLE_NOW,
    spacingHours = 20,
  } = options;
  const rand = seeded(seed);
  const out: PipelinePoint[] = [];
  let t = end;
  let cov = 71;
  const pauseAt = Math.floor(count * 0.4);
  for (let i = count - 1; i >= 0; i--) {
    const r = rand();
    const ref = r < 0.62 ? refs[0] : r < 0.86 ? (refs[1] ?? refs[0]) : (refs[2] ?? refs[0]);
    const spike = rand() < 0.08;
    const newBySeverity: SeverityCounts = {
      critical: rand() < (spike ? 0.6 : 0.08) ? 1 + Math.floor(rand() * 2) : 0,
      high: Math.floor(rand() * (spike ? 14 : 4)),
      medium: Math.floor(rand() * (spike ? 22 : 6)),
      low: Math.floor(rand() * 5),
      unknown: rand() < 0.1 ? 1 : 0,
    };
    const fixedCount = Math.floor(rand() * (rand() < 0.1 ? 26 : 7));
    const gate: GateState = rand() < 0.14 ? 'fail' : rand() < 0.06 ? 'none' : 'pass';
    cov = Math.max(52, Math.min(88, cov + (rand() - 0.55) * 2.4));
    out.push({
      pipelineId: 1000 + i,
      at: new Date(t).toISOString(),
      ref,
      artifact: 'registry.example.com/payments-api:' + (1000 + i),
      newBySeverity,
      fixedCount,
      gate,
      coverage: coverage && rand() > 0.1 ? Math.round(cov * 10) / 10 : null,
    });
    t -= spacingHours * HOUR * (0.4 + rand() * 1.2);
    if (i === pauseAt) t -= pauseDays * 24 * HOUR;
  }
  return out.reverse();
}

const NAMES = [
  'payments-api',
  'auth-service',
  'billing-worker',
  'etl-runner',
  'api-gateway',
  'search-indexer',
  'notifications',
  'ledger',
  'web-frontend',
  'scope-demo',
  'image-resizer',
  'audit-trail',
];

export function sampleServices(count = NAMES.length, seed = 11): HeatRow[] {
  const rand = seeded(seed);
  const out: HeatRow[] = [];
  for (let i = 0; i < count; i++) {
    const base = NAMES[i % NAMES.length];
    const name = i < NAMES.length ? base : `${base}-${Math.floor(i / NAMES.length) + 1}`;
    const heavy = rand() < 0.3;
    const noScan = i % 17 === 9;
    const counts: SeverityCounts | null = noScan
      ? null
      : {
          critical: rand() < (heavy ? 0.7 : 0.15) ? Math.floor(rand() * 6) + 1 : 0,
          high: Math.floor(rand() * (heavy ? 60 : 12)),
          medium: Math.floor(rand() * (heavy ? 140 : 30)),
          low: Math.floor(rand() * 70),
          unknown: rand() < 0.2 ? Math.floor(rand() * 4) : 0,
        };
    const g = rand();
    out.push({
      id: i + 1,
      name,
      path: `platform/${name}`,
      counts,
      gate: noScan ? undefined : g < 0.22 ? 'fail' : g < 0.3 ? 'none' : 'pass',
      scannedAt: noScan
        ? null
        : new Date(SAMPLE_NOW - rand() * rand() * 30 * 24 * HOUR).toISOString(),
      href: `/services/${i + 1}`,
    });
  }
  return out;
}

export function sampleUploads(days = 60, seed = 5): TrendPoint[] {
  const rand = seeded(seed);
  const out: TrendPoint[] = [];
  for (let d = days - 1; d >= 0; d--) {
    const t = SAMPLE_NOW - d * 24 * HOUR;
    const weekday = new Date(t).getUTCDay();
    const weekend = weekday === 0 || weekday === 6;
    const down = d === 21 || d === 22;
    out.push({
      at: new Date(t).toISOString(),
      value: down
        ? null
        : Math.round((weekend ? 8 : 34) + rand() * (weekend ? 10 : 30) + (days - d) * 0.3),
    });
  }
  return out;
}
