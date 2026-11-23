// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ChevronRight, Clock, Wrench } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import type { PortfolioHistory, PortfolioItem } from '../../api/portfolio';
import { SeverityMark, StatusMark } from '../../components/SeverityMark';
import { Tooltip } from '../../components/Tooltip';
import { countOf, formatInt, plural } from '../../format';
import {
  Delta,
  explainRisk,
  RISK_SOURCE,
  SeverityBar,
  Sparkline,
  SEVERITIES,
  useWidth,
  type SeverityCounts,
} from '../../viz';
import {
  biggestMover,
  portfolioRisk,
  STALE_AFTER_DAYS,
  type FilterKey,
  type HistoryIndex,
} from './model';

function totalCounts(items: PortfolioItem[]): SeverityCounts {
  const out: SeverityCounts = {};
  for (const s of SEVERITIES) out[s] = items.reduce((sum, item) => sum + item.counts[s], 0);
  return out;
}

interface Signal {
  filter: FilterKey;
  count: number;
  mark: ReactNode;
  /** What the count means, after the number. */
  text: string;
  /** What is shown instead when nothing needs attention. */
  calm: string;
  /** The calm state is a fact, not a good sign (nothing to judge yet). */
  calmNeutral?: boolean;
}

function SignalRow({
  signal,
  active,
  onFilter,
}: {
  signal: Signal;
  active: boolean;
  onFilter: (filter: FilterKey) => void;
}) {
  if (signal.count === 0) {
    return (
      <li className="flex min-h-12 items-center gap-3 px-3 text-body-s text-text-muted">
        <StatusMark status={signal.calmNeutral ? 'none' : 'ok'} size={16} />
        <span>{signal.calm}</span>
      </li>
    );
  }
  return (
    <li>
      <button
        type="button"
        aria-pressed={active}
        onClick={() => onFilter(active ? 'all' : signal.filter)}
        className={`group grid min-h-12 w-full grid-cols-[1.25rem_auto_minmax(0,1fr)_auto] items-center gap-x-3 rounded-inner px-3 text-left transition-[background-color,box-shadow] duration-micro ease-enter hover:bg-wash pointer-coarse:min-h-14 ${
          active ? 'lens-ring' : ''
        }`}
      >
        <span className="flex justify-center">{signal.mark}</span>
        <span className="text-h3 font-semibold tracking-tight text-text tabular-nums">
          {formatInt(signal.count)}
        </span>{' '}
        <span className="text-body-s text-text-muted group-hover:text-text">{signal.text}</span>
        <ChevronRight
          size={16}
          strokeWidth={1.75}
          aria-hidden="true"
          className={`text-text-faint transition-transform duration-micro group-hover:translate-x-0.5 ${
            active ? 'rotate-90 text-brand' : ''
          }`}
        />
      </button>
    </li>
  );
}

/**
 * The portfolio at a glance: the risk of every service together with its
 * week, and the few things that need attention. Each signal is also a
 * filter: choosing it narrows the services below.
 */
export function SummaryBand({
  items,
  history,
  index,
  counts,
  filter,
  onFilter,
}: {
  items: PortfolioItem[];
  history?: PortfolioHistory;
  index: HistoryIndex;
  counts: Record<FilterKey, number>;
  filter: FilterKey;
  onFilter: (filter: FilterKey) => void;
}) {
  const risk = portfolioRisk(items, history);
  const total = totalCounts(items);
  const open = SEVERITIES.reduce((sum, s) => sum + (total[s] ?? 0), 0);
  const fixable = items.reduce((sum, item) => sum + item.fixable, 0);
  const analysed = items.filter((item) => item.last_analysis_at !== null).length;
  const mover = biggestMover(items, index);
  const services = items.length;
  const [box, width] = useWidth<HTMLDivElement>(320);

  const signals: Signal[] = [
    {
      filter: 'gate-failed',
      count: counts['gate-failed'],
      mark: <StatusMark status="fail" size={16} />,
      text: `of ${formatInt(analysed)} analysed ${plural(analysed, 'service')} ${plural(counts['gate-failed'], 'fails its', 'fail their')} quality gate`,
      calm:
        analysed > 0
          ? 'Every analysed service passes its quality gate'
          : 'No SonarQube analysis yet',
      calmNeutral: analysed === 0,
    },
    {
      filter: 'critical',
      count: counts.critical,
      mark: <SeverityMark severity="critical" size={14} />,
      text: `${plural(counts.critical, 'service has', 'services have')} critical findings`,
      calm: 'No service has a critical finding',
    },
    {
      filter: 'stale',
      count: counts.stale,
      mark: <Clock size={16} strokeWidth={1.75} aria-hidden="true" className="text-warn" />,
      text: `${plural(counts.stale, 'service has', 'services have')} no scan in ${STALE_AFTER_DAYS} days`,
      calm: `Every service was scanned in the last ${STALE_AFTER_DAYS} days`,
    },
    {
      filter: 'fixable',
      count: fixable,
      mark: <Wrench size={15} strokeWidth={1.75} aria-hidden="true" className="text-ok" />,
      text: `${plural(fixable, 'finding has', 'findings have')} a fixed version, in ${countOf(counts.fixable, 'service')}`,
      calm: 'No finding has a fixed version yet',
    },
  ];

  return (
    <section
      aria-label="Summary"
      className="panel mb-8 grid grid-cols-1 overflow-hidden lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]"
    >
      <div className="flex min-w-0 flex-col gap-4 p-5 sm:p-6">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-body-s font-medium text-text-muted">Portfolio risk</h2>
          <Tooltip
            content={`${explainRisk(total)} Points per open finding follow the ${RISK_SOURCE}.`}
          >
            <button
              type="button"
              className="rounded-[4px] text-caption text-text-faint underline decoration-dotted underline-offset-2 hover:text-text"
            >
              How it is counted
            </button>
          </Tooltip>
        </div>
        <div className="flex flex-wrap items-end gap-x-5 gap-y-2">
          <p
            className="text-display font-semibold tracking-tight text-text tabular-nums"
            data-testid="portfolio-risk"
          >
            {formatInt(risk.now)}
          </p>
          {risk.weekDelta !== undefined ? (
            <p className="pb-2 text-body-s text-text-muted">
              <Delta
                value={risk.weekDelta}
                unit="risk points"
                since="since 7 days ago"
                className="mr-1.5 font-medium"
              />
              <span aria-hidden="true">in 7 days</span>
            </p>
          ) : null}
        </div>
        <div ref={box} className="h-11">
          {risk.series.length > 1 ? (
            <Sparkline
              values={risk.series}
              width={Math.max(120, Math.floor(width))}
              height={44}
              tone="risk"
              fill
              label="Portfolio risk, each day of the last week"
            />
          ) : null}
        </div>
        <div>
          <SeverityBar
            counts={total}
            size="md"
            label="Open findings of every service"
            legend={false}
          />
          <p className="mt-2 text-body-s text-text-muted tabular-nums">
            {countOf(open, 'open finding')} in {countOf(services, 'service')}
          </p>
        </div>
      </div>

      <div className="flex flex-col border-t border-line bg-sunken/60 p-3 sm:p-4 lg:border-t-0 lg:border-l">
        <h2 className="px-3 pt-2 pb-1 text-body-s font-medium text-text-muted">Needs attention</h2>
        <ul className="flex flex-col gap-0.5">
          {signals.map((signal) => (
            <SignalRow
              key={signal.filter}
              signal={signal}
              active={filter === signal.filter}
              onFilter={onFilter}
            />
          ))}
        </ul>
        {mover ? (
          <p className="mt-auto border-t border-line px-3 pt-3 pb-1 text-body-s text-text-muted">
            Biggest change in the latest pipeline:{' '}
            <Link
              to={`/services/${mover.item.service.id}`}
              className="font-medium text-text underline decoration-line-strong underline-offset-2 hover:decoration-text"
            >
              {mover.item.service.name}
            </Link>{' '}
            <Delta
              value={mover.change.delta}
              unit="risk points"
              since={`since pipeline #${mover.change.sincePipeline}`}
            />
          </p>
        ) : null}
      </div>
    </section>
  );
}
