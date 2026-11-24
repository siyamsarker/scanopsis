// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Info } from 'lucide-react';
import type { Vulnerability } from '../../api/findings';
import { Tooltip } from '../../components/Tooltip';
import { countOf, formatInt, formatPercent } from '../../format';
import { buttonClass } from '../../ui';
import { RISK_SOURCE, SeverityBar, riskTerms } from '../../viz';
import { artifactIdOf, countSeverities, isFixable } from './model';
import type { SeverityCountValues } from '../../components/severityModel';
import { riskPoints } from '../../viz';

function RiskTip({ counts }: { counts: SeverityCountValues }) {
  const terms = riskTerms(counts).filter((t) => t.count > 0);
  return (
    <span className="block space-y-1">
      <span className="block font-medium">Risk points weigh each finding by severity</span>
      {terms.length === 0 ? (
        <span className="block text-text-muted">No findings in focus.</span>
      ) : (
        terms.map((t) => (
          <span key={t.severity} className="flex justify-between gap-4 tabular-nums">
            <span>
              {formatInt(t.count)} {t.severity} × {t.weight}
            </span>
            <span>{formatInt(t.points)}</span>
          </span>
        ))
      )}
      <span className="block text-text-muted">{RISK_SOURCE}</span>
    </span>
  );
}

/** A thin meter of a share, with its value as text beside it. */
export function ShareMeter({ value, label }: { value: number; label: string }) {
  const pct = Math.max(0, Math.min(1, value));
  return (
    <span
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct * 100)}
      className="relative inline-block h-1.5 w-16 overflow-hidden rounded-full bg-line"
    >
      <svg
        className="absolute inset-0 size-full"
        viewBox="0 0 100 6"
        preserveAspectRatio="none"
        aria-hidden="true"
      >
        <rect width={pct * 100} height="6" className="fill-ok" />
      </svg>
    </span>
  );
}

/**
 * The live summary of what the filters keep: how many findings, their
 * severities against everything loaded, their risk points and how much of it
 * an upgrade can fix. When a filter is active the summary is "in focus": the
 * lens gradient marks its edge and the bar shows the selection against the
 * whole.
 */
export function LensSummary({
  all,
  selected,
  focused,
  onClear,
  incomplete,
}: {
  all: Vulnerability[];
  selected: Vulnerability[];
  focused: boolean;
  onClear: () => void;
  /** More findings are still loading or loading stopped: totals are lower bounds. */
  incomplete?: boolean;
}) {
  const counts = countSeverities(selected);
  const risk = riskPoints(counts);
  const allRisk = riskPoints(countSeverities(all));
  const fixable = selected.filter(isFixable).length;
  const packages = new Set(selected.map((v) => v.pkg_name)).size;
  const images = new Set(selected.map(artifactIdOf)).size;
  const share = selected.length ? fixable / selected.length : 0;
  const atLeast = incomplete ? 'at least ' : '';

  return (
    <section
      aria-label="Selection summary"
      data-focused={focused || undefined}
      className="lens-summary relative grid gap-x-8 gap-y-4 px-5 py-4 sm:px-6 lg:grid-cols-[minmax(0,15rem)_minmax(0,1fr)]"
    >
      <div className="min-w-0">
        <p className="flex items-baseline gap-2">
          <span className="text-display-s font-semibold tracking-tight text-text tabular-nums">
            {formatInt(selected.length)}
          </span>
          <span className="text-body-s text-text-muted">
            {focused ? (
              <>
                of {atLeast}
                {formatInt(all.length)} in focus
              </>
            ) : (
              <>{selected.length === 1 ? 'vulnerability' : 'vulnerabilities'}</>
            )}
          </span>
        </p>
        {focused ? (
          <button
            type="button"
            onClick={onClear}
            className={`${buttonClass('ghost', 'sm')} -ml-2 mt-1`}
          >
            Clear filters
          </button>
        ) : (
          <p className="mt-1 text-small text-text-muted">
            {incomplete ? 'Counting as findings arrive' : 'In the current scans of this ref'}
          </p>
        )}
      </div>
      <p role="status" className="sr-only">
        {countOf(selected.length, 'vulnerability', 'vulnerabilities')}, {formatInt(fixable)} fixable
      </p>
      <div className="min-w-0 space-y-3 self-center">
        <SeverityBar
          counts={counts}
          size="md"
          max={focused ? all.length : undefined}
          label={focused ? 'Findings in focus' : 'Findings'}
        />
        <dl className="flex flex-wrap items-center gap-x-6 gap-y-2 text-body-s">
          <div className="flex items-center gap-2">
            <dt className="text-text-muted">Risk</dt>
            <dd className="tabular-nums">
              <span className="font-semibold text-text">{formatInt(risk)}</span>
              {focused ? <span className="text-text-muted"> of {formatInt(allRisk)}</span> : null}
              <span className="text-text-muted"> pts</span>
            </dd>
            <Tooltip content={<RiskTip counts={counts} />}>
              <button
                type="button"
                aria-label="How risk points are counted"
                className="inline-flex size-6 items-center justify-center rounded text-text-faint hover:text-text"
              >
                <Info size={14} strokeWidth={1.75} aria-hidden="true" />
              </button>
            </Tooltip>
          </div>
          <div className="flex items-center gap-2">
            <dt className="text-text-muted">Fixable</dt>
            <dd className="flex items-center gap-2 tabular-nums">
              <ShareMeter value={share} label="Share with a fixed version" />
              <span className="font-semibold text-text">
                {formatPercent(share * 100, { digits: 0 })}
              </span>
              <span className="text-text-muted">({formatInt(fixable)})</span>
            </dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="text-text-muted">Packages</dt>
            <dd className="font-semibold text-text tabular-nums">{formatInt(packages)}</dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="text-text-muted">Images</dt>
            <dd className="font-semibold text-text tabular-nums">{formatInt(images)}</dd>
          </div>
        </dl>
      </div>
    </section>
  );
}
