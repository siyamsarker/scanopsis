// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ExternalLink, RefreshCw } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import {
  INTEGRATION_REFRESH_MS,
  useSonarIntegration,
  useTrivyIntegration,
  type SonarIntegration,
  type TrivyIntegration,
} from '../api/admin';
import { describeError } from '../api/errors';
import { StatusPill } from '../components/badges';
import { StatusMark, type StatusKind } from '../components/SeverityMark';
import { Card, CardHeader } from '../components/layout';
import { EmptyState, ErrorState, Skeleton, SkeletonTable } from '../components/states';
import { countOf, formatInt } from '../format';
import { buttonClass, linkClass } from '../ui';
import { TrendArea } from '../viz';
import { AdminGate } from './admin/AdminGate';
import { FlatTable, When, td, th } from './admin/kit';
import { rejectionLabels, rejectionReasons } from './admin/labels';

const trivyGuide =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md';
const sonarGuide =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md';

type Rejection = TrivyIntegration['recent_rejections'][number];
type Delivery = SonarIntegration['recent_deliveries'][number];

/** A verdict: whether the integration works, in one sentence, and why not when it does not. */
export interface Verdict {
  status: StatusKind;
  title: string;
  reason: ReactNode;
}

function mostFrequent(values: string[]): string | undefined {
  const counts = new Map<string, number>();
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1);
  return [...counts.entries()].sort((a, b) => b[1] - a[1])[0]?.[0];
}

const settingsLink = (
  <Link to="/settings" className={linkClass}>
    Settings
  </Link>
);

export function uploadsVerdict(d: TrivyIntegration): Verdict {
  if (!d.configured) {
    return {
      status: 'fail',
      title: 'Scan uploads need attention',
      reason: (
        <>
          Uploads are refused until the GitLab URL is set in {settingsLink}, so CI ID tokens can be
          verified.
        </>
      ),
    };
  }
  if (d.rejections_24h > 0) {
    const reason = mostFrequent(d.recent_rejections.map((r) => r.reason));
    const label = reason ? (rejectionLabels[reason] ?? reason) : undefined;
    const all = d.uploads_24h === 0;
    return {
      status: all ? 'fail' : 'warn',
      title: all ? 'Every recent upload was refused' : 'Some uploads were refused',
      reason: (
        <>
          {countOf(d.rejections_24h, 'upload')} refused in the last 24 hours
          {label ? (
            <>
              , most often for <span className="font-medium text-text">{label.toLowerCase()}</span>
            </>
          ) : null}
          . The list below says which projects.
        </>
      ),
    };
  }
  if (d.last_upload_at === null) {
    return {
      status: 'none',
      title: 'Ready, waiting for the first upload',
      reason: 'Add the Scanopsis job to a GitLab pipeline; its first Trivy report shows here.',
    };
  }
  return {
    status: 'ok',
    title: 'Scan uploads are arriving',
    reason: 'Trivy reports from GitLab CI are accepted and verified.',
  };
}

export function sonarVerdict(d: SonarIntegration): Verdict {
  if (!d.configured) {
    return {
      status: 'fail',
      title: 'SonarQube needs attention',
      reason: <>Set the SonarQube URL, token and webhook secret in {settingsLink}.</>,
    };
  }
  if (d.last_sync_error) {
    return {
      status: 'fail',
      title: 'The last sync with SonarQube failed',
      reason: <span className="font-mono text-small break-words">{d.last_sync_error}</span>,
    };
  }
  const failed = d.recent_deliveries.filter((x) => x.outcome === 'failed').length;
  if (failed > 0) {
    return {
      status: 'warn',
      title: 'Some analyses could not be read',
      reason: `${countOf(failed, 'recent delivery', 'recent deliveries')} failed. The detail column says why.`,
    };
  }
  const unlinked = d.recent_deliveries.filter((x) => x.outcome === 'unlinked').length;
  if (unlinked > 0) {
    return {
      status: 'warn',
      title: 'Analyses arrive for projects no service is linked to',
      reason: `${countOf(unlinked, 'recent delivery', 'recent deliveries')} matched no service. Link the key on the service, or set sonar.analysis.gitlabProjectId in the scanner.`,
    };
  }
  if (d.last_webhook_at === null) {
    return {
      status: 'none',
      title: 'Ready, waiting for the first webhook',
      reason: 'Add the Scanopsis webhook in SonarQube; the next analysis shows here.',
    };
  }
  return {
    status: 'ok',
    title: 'SonarQube analyses are arriving',
    reason: 'Webhooks are signed and every recent analysis was linked.',
  };
}

const bandTone: Record<StatusKind, string> = {
  ok: 'tone-ok',
  warn: 'tone-warn',
  fail: 'tone-fail',
  none: 'tone-neutral',
};

/** A full-width tinted strip: the integration's verdict, its reason and its key facts. */
function VerdictBand({
  name,
  verdict,
  facts,
  guide,
}: {
  name: string;
  verdict: Verdict;
  facts: { label: string; value: ReactNode }[];
  guide: string;
}) {
  return (
    <section
      aria-label={name}
      className={`${bandTone[verdict.status]} relative overflow-hidden rounded-panel border border-[color-mix(in_srgb,var(--tone)_30%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_7%,var(--surface))]`}
    >
      <span aria-hidden="true" className="tone-bg absolute inset-y-0 left-0 w-1" />
      <div className="flex flex-col gap-4 py-5 pr-5 pl-6 sm:pr-6 sm:pl-7 lg:flex-row lg:items-center lg:gap-8">
        <div className="flex min-w-0 flex-1 items-start gap-3.5">
          <StatusMark status={verdict.status} size={22} className="mt-1" />
          <div className="min-w-0">
            <p className="text-caption font-medium text-text-muted">{name}</p>
            <h2 className="text-h3 font-semibold tracking-tight text-text">{verdict.title}</h2>
            <p className="mt-1 max-w-prose text-body-s text-text-muted">{verdict.reason}</p>
          </div>
        </div>
        <dl className="grid shrink-0 grid-cols-2 gap-x-8 gap-y-3 pl-9 sm:grid-cols-3 lg:pl-0">
          {facts.map((fact) => (
            <div key={fact.label} className="min-w-0">
              <dt className="text-caption text-text-muted">{fact.label}</dt>
              <dd className="mt-0.5 text-body-s font-medium whitespace-nowrap text-text tabular-nums">
                {fact.value}
              </dd>
            </div>
          ))}
        </dl>
        <a
          href={guide}
          target="_blank"
          rel="noreferrer"
          className={`${buttonClass('ghost', 'sm')} self-start pl-9 lg:self-center lg:pl-3`}
        >
          Setup guide
          <ExternalLink size={14} strokeWidth={1.75} aria-hidden="true" />
          <span className="sr-only">(opens in a new tab)</span>
        </a>
      </div>
    </section>
  );
}

function BandSkeleton() {
  return (
    <div className="panel flex items-center gap-4 p-6" role="status" aria-label="Loading">
      <Skeleton className="size-6 rounded-full" />
      <div className="flex-1">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="mt-2 h-6 w-72 max-w-full" />
      </div>
    </div>
  );
}

function never(value: string | null, prefix?: string) {
  return value ? (
    <When at={value} prefix={prefix} />
  ) : (
    <span className="text-text-muted">Never</span>
  );
}

function UploadsChart({ daily }: { daily: TrivyIntegration['daily'] }) {
  const uploads = daily.reduce((n, d) => n + d.uploads, 0);
  const rejections = daily.reduce((n, d) => n + d.rejections, 0);
  return (
    <Card as="section" aria-labelledby="uploads-chart" className="[--viz-ground:var(--surface)]">
      <CardHeader
        id="uploads-chart"
        title="Uploads per day"
        description="The last 30 days, by UTC day."
        actions={
          <p className="text-right">
            <span className="text-h3 font-semibold tracking-tight text-text tabular-nums">
              {formatInt(uploads)}
            </span>{' '}
            <span className="text-body-s text-text-muted">
              {uploads === 1 ? 'upload' : 'uploads'}, {formatInt(rejections)} refused
            </span>
          </p>
        }
      />
      <TrendArea
        points={daily.map((d) => ({ at: d.day, value: d.uploads }))}
        label="Accepted uploads per day"
        unit="uploads"
        timeZone="UTC"
        height={180}
        emptyMessage="No uploads in the last 30 days."
      />
      <div className="mt-4 border-t border-line pt-4">
        <p className="mb-1 text-small font-medium text-text-muted">Refused per day</p>
        <TrendArea
          points={daily.map((d) => ({ at: d.day, value: d.rejections }))}
          label="Refused uploads per day"
          unit="refused"
          timeZone="UTC"
          tone="neutral"
          height={88}
          animate={false}
          emptyMessage="Nothing was refused."
        />
      </div>
    </Card>
  );
}

/** Rows shown before "Show all"; the rest are a click away. */
const FIRST_ROWS = 6;

function ShowAll({ total, onClick }: { total: number; onClick: () => void }) {
  return (
    <div className="border-t border-line px-4 py-2.5">
      <button type="button" className={buttonClass('ghost', 'sm')} onClick={onClick}>
        Show all {total}
      </button>
    </div>
  );
}

function RejectionsTable({ rows: all }: { rows: Rejection[] }) {
  const [expanded, setExpanded] = useState(false);
  const rows = expanded ? all : all.slice(0, FIRST_ROWS);
  if (all.length === 0) {
    return (
      <EmptyState
        compact
        art="clean"
        headingLevel={3}
        title="Nothing refused"
        body="Refused uploads and the reason for each appear here."
      />
    );
  }
  const table = (
    <FlatTable
      caption="Recently refused uploads"
      head={
        <>
          <th scope="col" className={th}>
            When
          </th>
          <th scope="col" className={th}>
            Project
          </th>
          <th scope="col" className={th}>
            Reason
          </th>
        </>
      }
    >
      {rows.map((r, i) => (
        <tr key={`${r.at}|${i}`} className="hover-tint">
          <td className={`${td} whitespace-nowrap text-text-muted sm:w-36`}>
            <When at={r.at} />
          </td>
          <td className={`${td} max-w-0 w-[34%]`}>
            {r.service_id !== null && r.service_name ? (
              <>
                <Link
                  to={`/services/${r.service_id}`}
                  className="block truncate font-medium text-text underline-offset-[3px] hover:underline"
                >
                  {r.service_name}
                </Link>
                <span className="block truncate font-mono text-small text-text-muted">
                  {r.project_path}
                </span>
              </>
            ) : r.project_path ? (
              <span
                className="block truncate font-mono text-small text-text"
                title={r.project_path}
              >
                {r.project_path}
              </span>
            ) : (
              <span className="text-text-muted">Not known</span>
            )}
          </td>
          <td className={td}>
            <span className="block font-medium text-text">
              {rejectionLabels[r.reason] ?? r.reason}
            </span>
            <span className="hidden text-small text-text-muted sm:block">
              {rejectionReasons[r.reason] ?? 'This reason code is not recognised.'}
            </span>
          </td>
        </tr>
      ))}
    </FlatTable>
  );
  return (
    <>
      {table}
      {!expanded && all.length > FIRST_ROWS ? (
        <ShowAll total={all.length} onClick={() => setExpanded(true)} />
      ) : null}
    </>
  );
}

const outcomes: Record<string, { status: StatusKind; text: string }> = {
  pending: { status: 'none', text: 'Pending' },
  linked: { status: 'ok', text: 'Linked' },
  unlinked: { status: 'warn', text: 'No service' },
  failed: { status: 'fail', text: 'Failed' },
};

function DeliveriesTable({ rows }: { rows: Delivery[] }) {
  if (rows.length === 0) {
    return (
      <EmptyState
        compact
        art="pipeline"
        headingLevel={3}
        title="No deliveries yet"
        body="Each webhook SonarQube sends after an analysis appears here."
      />
    );
  }
  return (
    <FlatTable
      caption="Recent SonarQube deliveries"
      head={
        <>
          <th scope="col" className={th}>
            When
          </th>
          <th scope="col" className={th}>
            Service
          </th>
          <th scope="col" className={th}>
            Outcome
          </th>
          <th scope="col" className={`${th} hidden md:table-cell`}>
            Detail
          </th>
        </>
      }
    >
      {rows.map((r) => {
        const o = outcomes[r.outcome] ?? { status: 'none' as const, text: r.outcome };
        return (
          <tr key={`${r.task_id}|${r.received_at}`} className="hover-tint">
            <td className={`${td} whitespace-nowrap text-text-muted sm:w-36`}>
              <When at={r.received_at} />
            </td>
            <td className={`${td} max-w-0 w-[34%]`}>
              {r.service_id !== null ? (
                <Link
                  to={`/services/${r.service_id}`}
                  className="block truncate font-medium text-text underline-offset-[3px] hover:underline"
                >
                  {r.service_name ?? r.project_key}
                </Link>
              ) : (
                <span className="block text-text-muted">No service</span>
              )}
              <span
                className="block truncate font-mono text-small text-text-muted"
                title={`Task ${r.task_id}`}
              >
                {r.project_key}
              </span>
            </td>
            <td className={`${td} whitespace-nowrap`}>
              <StatusPill status={o.status}>{o.text}</StatusPill>
            </td>
            <td className={`${td} hidden text-text-muted md:table-cell`}>{r.detail || '–'}</td>
          </tr>
        );
      })}
    </FlatTable>
  );
}

/**
 * Says when the page last updated and when it updates next: a ring that fills
 * over the refresh interval, and a button to update now.
 */
function AutoRefresh() {
  const trivy = useTrivyIntegration(true);
  const sonar = useSonarIntegration(true);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const busy = trivy.isFetching || sonar.isFetching;
  const updated = Math.min(trivy.dataUpdatedAt || now, sonar.dataUpdatedAt || now);
  const elapsed = Math.max(0, now - updated);
  const share = Math.min(1, elapsed / INTEGRATION_REFRESH_MS);
  const seconds = Math.round(elapsed / 1000);
  const r = 7;
  const c = 2 * Math.PI * r;
  return (
    <div className="flex items-center gap-3">
      <span className="flex items-center gap-2 text-small text-text-muted" aria-live="off">
        <svg viewBox="0 0 18 18" width={18} height={18} aria-hidden="true" className="-rotate-90">
          <circle cx="9" cy="9" r={r} fill="none" stroke="var(--line)" strokeWidth="2" />
          <circle
            cx="9"
            cy="9"
            r={r}
            fill="none"
            stroke="var(--brand)"
            strokeWidth="2"
            strokeLinecap="round"
            strokeDasharray={c}
            strokeDashoffset={c * (1 - share)}
            className={busy ? 'opacity-40' : undefined}
          />
        </svg>
        <span className="tabular-nums">
          {busy ? 'Updating' : seconds < 5 ? 'Updated just now' : `Updated ${seconds} s ago`}
        </span>
        <span className="sr-only">. The page updates every 30 seconds.</span>
      </span>
      <button
        type="button"
        className={buttonClass('secondary', 'sm')}
        disabled={busy}
        onClick={() => void Promise.all([trivy.refetch(), sonar.refetch()])}
      >
        <RefreshCw
          size={14}
          strokeWidth={1.75}
          aria-hidden="true"
          className={busy ? 'animate-spin' : undefined}
        />
        Refresh
      </button>
    </div>
  );
}

function Integrations() {
  const trivy = useTrivyIntegration(true);
  const sonar = useSonarIntegration(true);
  const t = trivy.data;
  const s = sonar.data;
  return (
    <div className="flex flex-col gap-4">
      {trivy.isPending ? (
        <BandSkeleton />
      ) : trivy.isError ? (
        <Card>
          <ErrorState
            compact
            title="Scan uploads could not be loaded"
            message={describeError(trivy.error)}
            onRetry={() => void trivy.refetch()}
            retrying={trivy.isFetching}
          />
        </Card>
      ) : (
        <VerdictBand
          name="Scan uploads from GitLab CI"
          verdict={uploadsVerdict(trivy.data)}
          guide={trivyGuide}
          facts={[
            { label: 'Last upload', value: never(trivy.data.last_upload_at) },
            { label: 'Accepted, 24 h', value: formatInt(trivy.data.uploads_24h) },
            { label: 'Refused, 24 h', value: formatInt(trivy.data.rejections_24h) },
          ]}
        />
      )}
      {sonar.isPending ? (
        <BandSkeleton />
      ) : sonar.isError ? (
        <Card>
          <ErrorState
            compact
            title="SonarQube could not be loaded"
            message={describeError(sonar.error)}
            onRetry={() => void sonar.refetch()}
            retrying={sonar.isFetching}
          />
        </Card>
      ) : (
        <VerdictBand
          name="SonarQube"
          verdict={sonarVerdict(sonar.data)}
          guide={sonarGuide}
          facts={[
            {
              label: 'Server',
              value: sonar.data.server_version ? (
                <span className="font-mono">{sonar.data.server_version}</span>
              ) : (
                <span className="text-text-muted">Unknown</span>
              ),
            },
            { label: 'Last webhook', value: never(sonar.data.last_webhook_at) },
            { label: 'Last sync', value: never(sonar.data.last_sync_at) },
          ]}
        />
      )}

      <div className="mt-4">
        {t ? (
          <UploadsChart daily={t.daily} />
        ) : trivy.isPending ? (
          <Skeleton className="h-80" />
        ) : null}
      </div>

      <div className="mt-4 grid gap-6">
        <Card as="section" padded={false} aria-labelledby="rejections" className="overflow-hidden">
          <CardHeader
            id="rejections"
            padded
            title="Refused uploads"
            description="The 20 most recent, with the reason for each."
          />
          {t ? (
            <RejectionsTable rows={t.recent_rejections} />
          ) : (
            <SkeletonTable rows={3} columns={3} />
          )}
        </Card>
        <Card as="section" padded={false} aria-labelledby="deliveries" className="overflow-hidden">
          <CardHeader
            id="deliveries"
            padded
            title="SonarQube deliveries"
            description="Webhooks received after each analysis, newest first."
          />
          {s ? (
            <DeliveriesTable rows={s.recent_deliveries} />
          ) : (
            <SkeletonTable rows={3} columns={4} />
          )}
        </Card>
      </div>
    </div>
  );
}

export function IntegrationsPage() {
  return (
    <AdminGate
      title="Integrations"
      subtitle="Whether scan reports and analyses are arriving, and why not when they are not."
      actions={<AutoRefresh />}
    >
      <Integrations />
    </AdminGate>
  );
}
