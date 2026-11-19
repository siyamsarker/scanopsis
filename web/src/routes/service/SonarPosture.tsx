// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ExternalLink } from 'lucide-react';
import { Link } from 'react-router';
import { useMe } from '../../api/hooks';
import { useSonar, type SonarAnalysis } from '../../api/service';
import { GateBadge, RatingBadge } from '../../components/badges';
import { Card, CardHeader } from '../../components/layout';
import { EmptyState, Skeleton } from '../../components/states';
import { formatPercent, safeExternalUrl } from '../../format';
import { SectionError } from './parts';

export const SONARQUBE_GUIDE =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md';

const NOT_AVAILABLE = 'Not available';

const percentOrNA = (value: number | null) =>
  value === null ? NOT_AVAILABLE : formatPercent(value);

/** "fails when below 80" for the operator SonarQube reports (LT, GT, EQ, NE). */
function failsWhen(operator: string, threshold: string): string {
  switch (operator) {
    case 'LT':
      return `fails when below ${threshold}`;
    case 'GT':
      return `fails when above ${threshold}`;
    case 'EQ':
      return `fails when equal to ${threshold}`;
    case 'NE':
      return `fails when not equal to ${threshold}`;
    default:
      return `threshold ${operator} ${threshold}`;
  }
}

function Measure({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-sm text-text-muted">{label}</dt>
      <dd
        className={`mt-0.5 text-2xl font-semibold tracking-tight tabular-nums ${
          value === NOT_AVAILABLE ? 'text-text-muted' : 'text-text'
        }`}
      >
        {value}
      </dd>
    </div>
  );
}

function Analysis({ latest }: { latest: SonarAnalysis }) {
  const failed = latest.conditions.filter((c) => c.status === 'ERROR' || c.status === 'WARN');
  const url = safeExternalUrl(latest.project_url);
  return (
    <Card className="space-y-6">
      <CardHeader title="Quality gate" description="Latest SonarQube analysis" />
      <div>
        <GateBadge status={latest.quality_gate_status} className="px-4 py-2 text-lg" />
        {failed.length > 0 ? (
          <ul aria-label="Failed conditions" className="mt-4 space-y-2">
            {failed.map((c) => (
              <li
                key={c.metric}
                className="rounded-control border border-border bg-bg px-3 py-2 text-sm"
              >
                <span className="font-mono text-text">{c.metric}</span>
                <span className="mt-0.5 block text-text-muted">
                  Value {c.value ?? NOT_AVAILABLE}, {failsWhen(c.operator, c.threshold)}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
      </div>
      <div className="grid grid-cols-1 gap-3 border-t border-border pt-4 min-[420px]:grid-cols-2">
        <RatingBadge rating={latest.reliability_rating} label="Reliability" />
        <RatingBadge rating={latest.security_rating} label="Security" />
        <RatingBadge rating={latest.maintainability_rating} label="Maintainability" />
        <RatingBadge rating={latest.security_review_rating} label="Security review" />
      </div>
      <dl className="grid grid-cols-2 gap-4 border-t border-border pt-4">
        <Measure label="Coverage" value={percentOrNA(latest.coverage)} />
        <Measure label="Duplication" value={percentOrNA(latest.duplicated_lines_density)} />
      </dl>
      {url ? (
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1.5 text-brand underline underline-offset-2"
        >
          Open in SonarQube
          <ExternalLink size={14} strokeWidth={1.5} aria-hidden="true" />
        </a>
      ) : null}
    </Card>
  );
}

export function NoSonar({ linked }: { linked: boolean }) {
  const me = useMe();
  const admin = me.data?.role === 'admin';
  return (
    <Card padded={false}>
      <EmptyState
        title={linked ? 'No SonarQube analysis yet' : 'No SonarQube project linked'}
        body={
          <>
            <p>
              {linked
                ? 'The project is linked. Results appear after the next pipeline reports an analysis.'
                : 'Link a SonarQube project key through the pipeline properties, or set the key on the Services page.'}
            </p>
            {admin ? (
              <p className="mt-2">
                <Link to="/admin/services" className="text-brand underline underline-offset-2">
                  Open the Services page
                </Link>
              </p>
            ) : null}
          </>
        }
        action={
          <a
            href={SONARQUBE_GUIDE}
            target="_blank"
            rel="noopener noreferrer"
            className="text-brand underline underline-offset-2"
          >
            Read the SonarQube guide
          </a>
        }
      />
    </Card>
  );
}

export function SonarPosture({ serviceId }: { serviceId: number }) {
  const query = useSonar(serviceId);
  if (query.isPending) {
    return (
      <div role="status" aria-label="Loading SonarQube posture">
        <Card className="space-y-6">
          <Skeleton className="h-5 w-32" />
          <Skeleton className="h-10 w-28" />
          <div className="grid grid-cols-2 gap-3">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-7 w-full" />
            ))}
          </div>
          <div className="grid grid-cols-2 gap-4">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        </Card>
      </div>
    );
  }
  if (query.isError) {
    return (
      <SectionError
        what="SonarQube posture"
        error={query.error}
        onRetry={() => void query.refetch()}
        retrying={query.isFetching}
      />
    );
  }
  const { latest, sonar_project_key: key } = query.data;
  if (!latest) return <NoSonar linked={Boolean(key)} />;
  return <Analysis latest={latest} />;
}
