// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { SonarAnalysis } from '../../api/service';
import {
  coverageSource,
  coverageValue,
  measureDelta,
  pipelineKey,
  pipelinePoints,
  compareBase,
  scopeOf,
  verdictSentence,
  verdictTone,
  type TrendPoint,
} from './model';
import { conditionRequirement, conditionValue } from './conditions';

const zero = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };

function point(scan: number, over: Partial<TrendPoint> = {}): TrendPoint {
  return {
    scan_id: scan,
    kind: 'image',
    ref: 'main',
    scanned_at: `2026-11-1${scan}T10:00:00Z`,
    gitlab_pipeline_id: 800 + scan,
    commit_sha: `sha${scan}`,
    artifact_name: 'api',
    counts: zero,
    fixable: 0,
    previous_scan_id: scan - 1,
    new: zero,
    fixed: 0,
    ...over,
  };
}

function analysis(id: number, over: Partial<SonarAnalysis> = {}): SonarAnalysis {
  return {
    id,
    analysed_at: `2026-11-1${id}T11:00:00Z`,
    revision: '',
    quality_gate_status: 'OK',
    conditions: [],
    reliability_rating: null,
    security_rating: null,
    maintainability_rating: null,
    security_review_rating: null,
    coverage: null,
    duplicated_lines_density: null,
    measures: {},
    mode: 'mqr',
    gitlab_pipeline_id: null,
    project_url: null,
    ...over,
  };
}

describe('pipelinePoints', () => {
  it('sums the image scans of a pipeline and leaves secret scans out', () => {
    const strip = pipelinePoints(
      [
        point(1, { previous_scan_id: null, new: { ...zero, high: 40 } }),
        point(2, { new: { ...zero, critical: 1 }, fixed: 2 }),
        point(3, {
          gitlab_pipeline_id: 802,
          artifact_name: 'worker',
          new: { ...zero, high: 2 },
          fixed: 1,
        }),
        point(4, { gitlab_pipeline_id: 802, kind: 'secret-fs', new: { ...zero, low: 9 } }),
      ],
      [],
      { metric: 'coverage', label: 'Coverage' },
    );
    expect(strip.points).toHaveLength(2);
    const [first, second] = strip.points;
    // A first scan is the starting point, not 40 new findings.
    expect(first.newBySeverity).toEqual({});
    expect(first.label).toBe('#801, first scan');
    expect(second.newBySeverity).toEqual({ critical: 1, high: 2, medium: 0, low: 0, unknown: 0 });
    expect(second.fixedCount).toBe(3);
    expect(second.artifact).toBe('2 artifacts');
    expect(strip.totals).toEqual({ newFindings: 3, fixed: 3, pipelines: 2, baselines: 1 });
    // The scan to open is the one that changed the most risk.
    expect(strip.scanOf.get(pipelineKey('main', 802))).toBe(2);
  });

  it('takes the gate and coverage from the analysis of the pipeline, else of the commit', () => {
    const strip = pipelinePoints(
      [point(1), point(2), point(3)],
      [
        analysis(1, { gitlab_pipeline_id: 801, quality_gate_status: 'ERROR', coverage: 61 }),
        analysis(2, { revision: 'sha2', quality_gate_status: 'OK', coverage: 64.5 }),
      ],
      { metric: 'coverage', label: 'Coverage' },
    );
    expect(strip.points.map((p) => [p.gate, p.coverage])).toEqual([
      ['fail', 61],
      ['pass', 64.5],
      [undefined, null],
    ]);
  });

  it('keeps branches apart', () => {
    const strip = pipelinePoints(
      [point(1), point(2, { ref: 'dev', gitlab_pipeline_id: 801 })],
      [],
      { metric: 'coverage', label: 'Coverage' },
    );
    expect(strip.points.map((p) => p.ref)).toEqual(['main', 'dev']);
  });
});

describe('coverage', () => {
  const gate = (metric: string, threshold: string) =>
    analysis(9, {
      conditions: [{ metric, operator: 'LT', threshold, value: '50', status: 'ERROR' }],
      coverage: 70,
      measures: { new_coverage: '55.5' },
    });

  it('draws coverage on new code when the gate judges it', () => {
    const latest = gate('new_coverage', '80');
    expect(coverageSource(latest, [latest])).toEqual({
      metric: 'new_coverage',
      label: 'Coverage on new code',
      threshold: 80,
    });
    expect(coverageValue(latest, 'new_coverage')).toBe(55.5);
    expect(coverageValue(analysis(1), 'new_coverage')).toBeUndefined();
  });

  it('falls back to overall coverage, with its threshold when the gate has one', () => {
    const latest = gate('coverage', '75');
    expect(coverageSource(latest, [latest])).toEqual({
      metric: 'coverage',
      label: 'Coverage',
      threshold: 75,
    });
    // A new-code condition without any new-code measure draws overall coverage.
    const bare = analysis(9, {
      conditions: [
        {
          metric: 'new_coverage',
          operator: 'LT',
          threshold: '80',
          value: null,
          status: 'NO_VALUE',
        },
      ],
    });
    expect(coverageSource(bare, [bare]).metric).toBe('coverage');
    expect(coverageSource(null, []).threshold).toBeUndefined();
  });
});

describe('verdict', () => {
  const counts = (critical: number, high = 0, low = 0) => ({ ...zero, critical, high, low });

  it('says gate, worst findings and the change since the previous pipeline', () => {
    expect(
      verdictSentence({
        gate: 'fail',
        counts: counts(1, 43),
        newFindings: 0,
        fixed: 3,
        sincePipeline: 1002,
      }),
    ).toBe('Failing its quality gate; 1 critical; 3 fixed and 0 new since pipeline #1002.');
    expect(verdictSentence({ gate: 'pass', counts: counts(0, 2), newFindings: 1, fixed: 0 })).toBe(
      'Passing its quality gate; no critical, 2 high; 0 fixed and 1 new since the previous scan.',
    );
    expect(verdictSentence({ counts: counts(0, 0, 4), newFindings: 0, fixed: 0 })).toBe(
      'No critical or high findings; nothing changed since the previous scan.',
    );
    expect(verdictSentence({ counts: zero, newFindings: 0, fixed: 5 })).toBe(
      'No open findings; 5 fixed and 0 new since the previous scan.',
    );
    expect(
      verdictSentence({ gate: 'fail', counts: null, newFindings: 0, fixed: 0, ref: 'release/9' }),
    ).toBe('Failing its quality gate; no scans of release/9 yet.');
    expect(verdictSentence({ counts: null, newFindings: 0, fixed: 0, ref: null })).toBe(
      'No scans yet.',
    );
  });

  it('picks a tone', () => {
    expect(verdictTone({ gate: 'pass', counts: counts(1) })).toBe('fail');
    expect(verdictTone({ gate: 'fail', counts: zero })).toBe('fail');
    expect(verdictTone({ counts: counts(0, 3) })).toBe('warn');
    expect(verdictTone({ gate: 'pass', counts: zero })).toBe('ok');
    expect(verdictTone({ counts: null })).toBe('none');
  });

  it('finds the pipeline of the previous scan of the newest image', () => {
    const overview = {
      artifacts: [
        { id: 3, kind: 'image', scanned_at: '2026-11-13T10:00:00Z' },
        { id: 9, kind: 'secret-fs', scanned_at: '2026-11-14T10:00:00Z' },
      ],
    } as Parameters<typeof compareBase>[0];
    expect(compareBase(overview, [point(1), point(2), point(3)])).toEqual({
      pipeline: 802,
      firstScan: false,
    });
    expect(compareBase(overview, [point(3)])).toEqual({ pipeline: undefined, firstScan: false });
    expect(compareBase({ artifacts: [] }, [point(3)])).toEqual({
      pipeline: undefined,
      firstScan: false,
    });
    expect(compareBase(overview, [point(3, { previous_scan_id: null })])).toEqual({
      pipeline: undefined,
      firstScan: true,
    });
  });

  it('says a first scan is the first, not that everything is new', () => {
    expect(
      verdictSentence({ counts: counts(2), newFindings: 138, fixed: 0, firstScan: true }),
    ).toBe('2 critical; first scan on this branch.');
  });
});

describe('SonarQube helpers', () => {
  it('labels new code and overall conditions', () => {
    expect(scopeOf('new_coverage')).toBe('new');
    expect(scopeOf('coverage')).toBe('overall');
  });

  it('rounds measure changes and skips missing values', () => {
    expect(measureDelta(57.94, 59.4)).toBe(-1.5);
    expect(measureDelta(null, 3)).toBeUndefined();
    expect(measureDelta(3, undefined)).toBeUndefined();
  });

  it('splits a condition into value and requirement', () => {
    const c = {
      metric: 'new_coverage',
      operator: 'LT',
      threshold: '80',
      value: '57.9',
      status: 'ERROR',
    };
    expect(conditionValue(c)).toBe('57.9%');
    expect(conditionRequirement(c)).toBe('at least 80%');
    expect(
      conditionRequirement({
        ...c,
        metric: 'new_reliability_rating',
        operator: 'GT',
        threshold: '1',
      }),
    ).toBe('at most A');
    expect(conditionRequirement({ ...c, operator: 'EQ' })).toBe('not 80%');
    expect(conditionRequirement({ ...c, operator: 'NE' })).toBe('exactly 80%');
    expect(conditionRequirement({ ...c, operator: 'ODD' })).toBe('ODD 80%');
    expect(conditionValue({ ...c, value: null, status: 'NO_VALUE' })).toBe('Not available');
  });
});
