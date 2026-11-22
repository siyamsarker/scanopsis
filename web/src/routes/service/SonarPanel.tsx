// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ExternalLink } from 'lucide-react';
import { Link } from 'react-router';
import { useMe } from '../../api/hooks';
import { useSonar, type SonarAnalysis } from '../../api/service';
import { GateBadge, RatingBadge } from '../../components/badges';
import { StatusMark } from '../../components/SeverityMark';
import { EmptyState, Skeleton } from '../../components/states';
import { formatExactTime, formatPercent, relativeTime, safeExternalUrl } from '../../format';
import { Delta } from '../../viz';
import { conditionRequirement, conditionValue, metricName } from './conditions';
import { coverageValue, measureDelta, scopeOf } from './model';
import { SectionError } from './parts';

export const SONARQUBE_GUIDE =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md';

const SCOPE_TEXT = { new: 'New code', overall: 'Overall' } as const;

function statusOf(status: string) {
  if (status === 'ERROR') return 'fail' as const;
  if (status === 'WARN') return 'warn' as const;
  if (status === 'OK') return 'ok' as const;
  return 'none' as const;
}

const STATUS_TEXT = { fail: 'Failed', warn: 'Warning', ok: 'Passed', none: 'No value' };

function Conditions({ latest }: { latest: SonarAnalysis }) {
  // Failed first, then new code before overall, so the reason for a red gate leads.
  const rank = (status: string) => ({ ERROR: 0, WARN: 1 })[status] ?? 2;
  const rows = [...latest.conditions].sort(
    (a, b) =>
      rank(a.status) - rank(b.status) ||
      (scopeOf(a.metric) === scopeOf(b.metric) ? 0 : scopeOf(a.metric) === 'new' ? -1 : 1),
  );
  if (rows.length === 0) {
    return <p className="text-body-s text-text-muted">The quality gate reported no conditions.</p>;
  }
  return (
    <table className="w-full text-left text-body-s">
      <caption className="sr-only">Quality gate conditions</caption>
      <thead className="text-caption text-text-faint">
        <tr>
          <th scope="col" className="pb-2 font-medium">
            Condition
          </th>
          <th scope="col" className="pb-2 font-medium">
            Applies to
          </th>
          <th scope="col" className="pb-2 text-right font-medium">
            Value
          </th>
        </tr>
      </thead>
      <tbody>
        {rows.map((c) => {
          const status = statusOf(c.status);
          const scope = scopeOf(c.metric);
          return (
            <tr key={c.metric} className="border-t border-line align-top">
              <th scope="row" className="py-2.5 pr-3 font-normal">
                <span className="flex items-start gap-2">
                  <span className="pt-0.5">
                    <StatusMark status={status} size={14} />
                  </span>
                  <span>
                    <span className="sr-only">{STATUS_TEXT[status]}: </span>
                    {metricName(c.metric) ?? <span className="font-mono">{c.metric}</span>}
                    <span className="block text-caption text-text-muted">
                      needs {conditionRequirement(c)}
                    </span>
                  </span>
                </span>
              </th>
              <td className="py-2.5 pr-3">
                <span
                  className={`tag ${scope === 'new' ? 'tone-brand' : 'tone-neutral'} text-caption`}
                >
                  {SCOPE_TEXT[scope]}
                </span>
              </td>
              <td
                className={`py-2.5 text-right font-medium tabular-nums ${status === 'fail' ? 'tone-fail tone-ink' : 'text-text'}`}
              >
                {conditionValue(c)}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function Measure({
  label,
  scope,
  value,
  delta,
  better,
}: {
  label: string;
  scope: keyof typeof SCOPE_TEXT;
  value: number | null | undefined;
  delta?: number;
  better: 'up' | 'down';
}) {
  return (
    <div className="min-w-0">
      <dt className="flex flex-wrap items-center gap-2 text-body-s text-text-muted">
        {label}
        <span className={`tag ${scope === 'new' ? 'tone-brand' : 'tone-neutral'} text-caption`}>
          {SCOPE_TEXT[scope]}
        </span>
      </dt>
      <dd className="mt-1 flex items-baseline gap-2">
        <span
          className={`text-display-s font-semibold tracking-tight tabular-nums ${value == null ? 'text-text-faint' : 'text-text'}`}
        >
          {value == null ? '–' : formatPercent(value)}
        </span>
        {value == null ? (
          <span className="text-body-s text-text-muted">not reported</span>
        ) : delta !== undefined ? (
          <span className="text-body-s">
            <Delta
              value={delta}
              better={better}
              unit="percentage points"
              since="since the analysis before"
              format={(v) => `${v.toFixed(1)}`}
            />
          </span>
        ) : null}
      </dd>
    </div>
  );
}

function Analysis({ latest, previous }: { latest: SonarAnalysis; previous?: SonarAnalysis }) {
  const url = safeExternalUrl(latest.project_url);
  const newCoverage = coverageValue(latest, 'new_coverage');
  return (
    <section aria-labelledby="code-quality" className="panel p-5 sm:p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id="code-quality" className="text-body font-semibold tracking-tight text-text">
            Code quality
          </h2>
          <p className="mt-0.5 text-body-s text-text-muted">
            SonarQube analysis{' '}
            <time
              dateTime={latest.analysed_at}
              title={formatExactTime(new Date(latest.analysed_at))}
            >
              {relativeTime(latest.analysed_at)}
            </time>
            {latest.gitlab_pipeline_id != null ? ` in pipeline #${latest.gitlab_pipeline_id}` : ''}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <GateBadge status={latest.quality_gate_status} />
          {url ? (
            <a
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-body-s font-medium text-text underline decoration-line-strong underline-offset-2 hover:decoration-text"
            >
              Open in SonarQube
              <ExternalLink size={14} strokeWidth={1.75} aria-hidden="true" />
            </a>
          ) : null}
        </div>
      </div>

      <div className="mt-6 grid gap-8 lg:grid-cols-[minmax(0,7fr)_minmax(0,5fr)]">
        <div className="min-w-0">
          <h3 className="mb-2 text-body-s font-medium text-text">Quality gate conditions</h3>
          <Conditions latest={latest} />
        </div>
        <div className="min-w-0 space-y-6">
          <dl className="grid grid-cols-2 gap-x-6 gap-y-5">
            <Measure
              label="Coverage"
              scope="overall"
              value={latest.coverage}
              delta={measureDelta(latest.coverage, previous?.coverage)}
              better="up"
            />
            <Measure
              label="Coverage"
              scope="new"
              value={newCoverage}
              delta={measureDelta(
                newCoverage,
                previous ? coverageValue(previous, 'new_coverage') : undefined,
              )}
              better="up"
            />
            <Measure
              label="Duplication"
              scope="overall"
              value={latest.duplicated_lines_density}
              delta={measureDelta(
                latest.duplicated_lines_density,
                previous?.duplicated_lines_density,
              )}
              better="down"
            />
          </dl>
          <div className="border-t border-line pt-4">
            <h3 className="mb-3 text-body-s font-medium text-text">
              Ratings <span className="font-normal text-text-muted">(overall code)</span>
            </h3>
            <div className="grid grid-cols-1 gap-3 min-[420px]:grid-cols-2">
              <RatingBadge rating={latest.reliability_rating} label="Reliability" />
              <RatingBadge rating={latest.security_rating} label="Security" />
              <RatingBadge rating={latest.maintainability_rating} label="Maintainability" />
              <RatingBadge rating={latest.security_review_rating} label="Security review" />
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

export function NoSonar({ linked }: { linked: boolean }) {
  const me = useMe();
  const admin = me.data?.role === 'admin';
  return (
    <section aria-labelledby="code-quality" className="panel">
      <h2 id="code-quality" className="sr-only">
        Code quality
      </h2>
      <EmptyState
        compact
        headingLevel={3}
        art="pipeline"
        title={linked ? 'No SonarQube analysis yet' : 'No SonarQube project linked'}
        body={
          linked
            ? 'The project is linked. Quality gate, ratings and coverage appear after the next pipeline reports an analysis.'
            : 'Link a SonarQube project key through the pipeline properties, or set the key on the Services page, to see the quality gate, ratings and coverage here.'
        }
        action={
          <>
            {admin && !linked ? (
              <Link
                to="/admin/services"
                className="font-medium text-text underline underline-offset-2"
              >
                Open the Services page
              </Link>
            ) : null}
            <a
              href={SONARQUBE_GUIDE}
              target="_blank"
              rel="noopener noreferrer"
              className="font-medium text-text underline underline-offset-2"
            >
              Read the SonarQube guide
            </a>
          </>
        }
      />
    </section>
  );
}

export function SonarPanel({ serviceId }: { serviceId: number }) {
  const query = useSonar(serviceId);
  if (query.isPending) {
    return (
      <div role="status" aria-label="Loading code quality" className="panel space-y-4 p-6">
        <Skeleton className="h-5 w-32" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }
  if (query.isError) {
    return (
      <SectionError
        what="Code quality"
        error={query.error}
        onRetry={() => void query.refetch()}
        retrying={query.isFetching}
      />
    );
  }
  const { latest, history, sonar_project_key: key } = query.data;
  if (!latest) return <NoSonar linked={Boolean(key)} />;
  const previous = history.find((a) => a.id !== latest.id && a.analysed_at <= latest.analysed_at);
  return <Analysis latest={latest} previous={previous} />;
}
