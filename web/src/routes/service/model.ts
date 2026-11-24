// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { Overview, SonarAnalysis, Trends } from '../../api/service';
import { formatInt } from '../../format';
import {
  riskPoints,
  SEVERITIES,
  type GateState,
  type PipelinePoint,
  type SeverityCounts,
} from '../../viz';

export type TrendPoint = Trends['points'][number];

/* --------------------------------------------------------------- coverage */

export interface CoverageSource {
  /** The metric drawn on the strip's coverage track. */
  metric: 'coverage' | 'new_coverage';
  label: string;
  /** The gate condition's threshold for that metric, 0 to 100. */
  threshold?: number;
}

function thresholdOf(analysis: SonarAnalysis | null | undefined, metric: string) {
  const condition = analysis?.conditions.find((c) => c.metric === metric);
  if (!condition) return undefined;
  const value = Number.parseFloat(condition.threshold);
  return Number.isFinite(value) ? value : undefined;
}

/** A metric's value in one analysis, as a number; undefined when not reported. */
export function coverageValue(
  analysis: SonarAnalysis,
  metric: CoverageSource['metric'],
): number | undefined {
  if (metric === 'coverage') return analysis.coverage ?? undefined;
  const raw = analysis.measures.new_coverage;
  const value = raw === undefined ? Number.NaN : Number.parseFloat(raw);
  return Number.isFinite(value) ? value : undefined;
}

/**
 * Which coverage the strip draws. When the quality gate judges coverage on
 * new code, that is the line worth comparing with the gate's threshold;
 * otherwise overall coverage, with its threshold when the gate has one.
 */
export function coverageSource(
  latest: SonarAnalysis | null | undefined,
  history: SonarAnalysis[],
): CoverageSource {
  const newThreshold = thresholdOf(latest, 'new_coverage');
  if (
    newThreshold !== undefined &&
    history.some((a) => coverageValue(a, 'new_coverage') !== undefined)
  ) {
    return { metric: 'new_coverage', label: 'Coverage on new code', threshold: newThreshold };
  }
  return { metric: 'coverage', label: 'Coverage', threshold: thresholdOf(latest, 'coverage') };
}

/* -------------------------------------------------------------- pipelines */

export interface StripData {
  points: PipelinePoint[];
  /** The scan to open for a pipeline, by its key (see pipelineKey). */
  scanOf: Map<string, number>;
  /** New and fixed over every pipeline shown. */
  totals: { newFindings: number; fixed: number; pipelines: number; baselines: number };
}

export function pipelineKey(ref: string, pipelineId: string | number) {
  return `${ref}\u0000${pipelineId}`;
}

function gateFrom(status: string): GateState {
  if (status === 'OK') return 'pass';
  if (status === 'ERROR') return 'fail';
  return 'none';
}

/**
 * One column per pipeline of the image scans: the new findings of its scans
 * by severity and their fixed findings, summed over its artifacts. The gate
 * and coverage come from the SonarQube analysis of the same pipeline, or of
 * the same commit when the analysis does not name its pipeline.
 */
export function pipelinePoints(
  trends: TrendPoint[],
  analyses: SonarAnalysis[],
  coverage: CoverageSource,
): StripData {
  const byPipeline = new Map<string, SonarAnalysis>();
  const byCommit = new Map<string, SonarAnalysis>();
  // Oldest first, so the latest analysis of a pipeline or commit wins.
  for (const a of [...analyses].sort((x, y) => x.analysed_at.localeCompare(y.analysed_at))) {
    if (a.gitlab_pipeline_id != null) byPipeline.set(String(a.gitlab_pipeline_id), a);
    if (a.revision) byCommit.set(a.revision, a);
  }

  interface Group {
    ref: string;
    pipelineId: number;
    at: string;
    commit: string;
    artifacts: string[];
    added: SeverityCounts;
    fixed: number;
    /** A scan in it is the first of its artifact: its findings set the baseline. */
    baseline: boolean;
    scan: { id: number; weight: number };
  }
  const groups = new Map<string, Group>();
  for (const p of trends) {
    if (p.kind !== 'image') continue;
    const key = pipelineKey(p.ref, p.gitlab_pipeline_id);
    let g = groups.get(key);
    if (!g) {
      g = {
        ref: p.ref,
        pipelineId: p.gitlab_pipeline_id,
        at: p.scanned_at,
        commit: p.commit_sha,
        artifacts: [],
        added: {},
        fixed: 0,
        baseline: false,
        scan: { id: p.scan_id, weight: -1 },
      };
      groups.set(key, g);
    }
    if (p.scanned_at > g.at) g.at = p.scanned_at;
    if (!g.artifacts.includes(p.artifact_key)) g.artifacts.push(p.artifact_key);
    // The first scan of an artifact has every finding "new"; drawn as new it
    // would dwarf every real change, so it counts as the starting point.
    const first = p.previous_scan_id === null;
    if (first) g.baseline = true;
    else for (const s of SEVERITIES) g.added[s] = (g.added[s] ?? 0) + p.new[s];
    g.fixed += p.fixed;
    // Open the artifact whose scan changed the most risk in this pipeline.
    const weight = (first ? 0 : riskPoints(p.new)) + p.fixed;
    if (weight > g.scan.weight) g.scan = { id: p.scan_id, weight };
  }

  const points: PipelinePoint[] = [];
  const scanOf = new Map<string, number>();
  let newFindings = 0;
  let fixed = 0;
  let baselines = 0;
  for (const [key, g] of groups) {
    if (g.baseline) baselines++;
    const analysis = byPipeline.get(String(g.pipelineId)) ?? byCommit.get(g.commit);
    const value = analysis ? coverageValue(analysis, coverage.metric) : undefined;
    points.push({
      pipelineId: g.pipelineId,
      at: g.at,
      ref: g.ref,
      artifact: g.artifacts.length === 1 ? g.artifacts[0] : `${g.artifacts.length} artifacts`,
      newBySeverity: g.added,
      fixedCount: g.fixed,
      gate: analysis ? gateFrom(analysis.quality_gate_status) : undefined,
      coverage: value ?? null,
      label: g.baseline ? `#${g.pipelineId}, first scan` : undefined,
    });
    scanOf.set(key, g.scan.id);
    newFindings += SEVERITIES.reduce((sum, s) => sum + (g.added[s] ?? 0), 0);
    fixed += g.fixed;
  }
  points.sort((a, b) => String(a.at).localeCompare(String(b.at)));
  return { points, scanOf, totals: { newFindings, fixed, pipelines: points.length, baselines } };
}

/* ---------------------------------------------------------------- verdict */

export type VerdictTone = 'fail' | 'warn' | 'ok' | 'none';

export interface VerdictInput {
  gate?: GateState;
  /** null when the ref has no scans. */
  counts: SeverityCounts | null;
  newFindings: number;
  fixed: number;
  /** The pipeline the changes are counted against, when known. */
  sincePipeline?: number;
  /** Every current image scan is the first of its artifact: nothing to compare with. */
  firstScan?: boolean;
  /** The branch shown; null when the service has no scans at all. */
  ref?: string | null;
}

function changePhrase(input: VerdictInput) {
  if (input.firstScan) return 'first scan on this branch';
  const since =
    input.sincePipeline !== undefined
      ? `since pipeline #${input.sincePipeline}`
      : 'since the previous scan';
  if (input.newFindings === 0 && input.fixed === 0) return `nothing changed ${since}`;
  return `${formatInt(input.fixed)} fixed and ${formatInt(input.newFindings)} new ${since}`;
}

function severityPhrase(counts: SeverityCounts) {
  const critical = counts.critical ?? 0;
  const high = counts.high ?? 0;
  const open = SEVERITIES.reduce((sum, s) => sum + (counts[s] ?? 0), 0);
  if (critical > 0) return `${formatInt(critical)} critical`;
  if (high > 0) return `no critical, ${formatInt(high)} high`;
  if (open > 0) return 'no critical or high findings';
  return 'no open findings';
}

/**
 * One sentence for the top of the service page, e.g. "Failing its quality
 * gate; 1 critical; 3 fixed and 0 new since pipeline #1001."
 */
export function verdictSentence(input: VerdictInput): string {
  const parts: string[] = [];
  if (input.gate === 'fail') parts.push('failing its quality gate');
  else if (input.gate === 'pass') parts.push('passing its quality gate');
  if (input.counts === null) {
    parts.push(input.ref ? `no scans of ${input.ref} yet` : 'no scans yet');
  } else {
    parts.push(severityPhrase(input.counts));
    parts.push(changePhrase(input));
  }
  const text = parts.join('; ');
  return `${text.charAt(0).toUpperCase()}${text.slice(1)}.`;
}

export function verdictTone(input: Pick<VerdictInput, 'gate' | 'counts'>): VerdictTone {
  if (input.counts === null) return input.gate === 'fail' ? 'fail' : 'none';
  if (input.gate === 'fail' || (input.counts.critical ?? 0) > 0) return 'fail';
  if ((input.counts.high ?? 0) > 0) return 'warn';
  return 'ok';
}

export interface CompareBase {
  /** The pipeline of the previous scan of the newest image, when the window holds it. */
  pipeline?: number;
  /** Every current image scan is the first of its artifact on this branch. */
  firstScan: boolean;
}

/**
 * What the overview's "new since previous" compares against: the pipeline
 * of the previous scan of the newest image artifact, or nothing when every
 * current image scan is the first of its artifact (then every finding is
 * "new", which says nothing about change).
 */
export function compareBase(
  overview: Pick<Overview, 'artifacts'>,
  trends: TrendPoint[],
): CompareBase {
  const images = overview.artifacts.filter((a) => a.kind === 'image');
  const points = images.map((a) => trends.find((p) => p.scan_id === a.id));
  const firstScan =
    images.length > 0 && points.every((p) => p !== undefined && p.previous_scan_id === null);
  const newest = images.reduce<(typeof images)[number] | undefined>(
    (best, a) => (!best || a.scanned_at > best.scanned_at ? a : best),
    undefined,
  );
  const point = newest ? trends.find((p) => p.scan_id === newest.id) : undefined;
  const previous =
    point && point.previous_scan_id !== null
      ? trends.find((p) => p.scan_id === point.previous_scan_id)
      : undefined;
  return { pipeline: previous?.gitlab_pipeline_id, firstScan };
}

/* ---------------------------------------------------------- SonarQube */

export type CodeScope = 'new' | 'overall';

/** Conditions on metrics of the new code period start with "new_". */
export function scopeOf(metric: string): CodeScope {
  return metric.startsWith('new_') ? 'new' : 'overall';
}

/** Change of a measure against the analysis before; undefined when either is missing. */
export function measureDelta(
  latest: number | null | undefined,
  previous: number | null | undefined,
): number | undefined {
  if (latest == null || previous == null) return undefined;
  return Math.round((latest - previous) * 10) / 10;
}
