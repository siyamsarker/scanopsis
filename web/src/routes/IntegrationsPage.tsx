// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import {
  CircleAlert,
  CircleCheck,
  CircleX,
  Clock,
  CircleDashed,
  RefreshCw,
  TriangleAlert,
  Unlink,
  type LucideIcon,
} from 'lucide-react';
import { Link } from 'react-router';
import { useSonarIntegration, useTrivyIntegration } from '../api/admin';
import { DataTable } from '../components/DataTable';
import type { Column } from '../components/tableCore';
import { Freshness, TargetLink } from '../components/Freshness';
import { Mono } from '../components/Mono';
import { StatTile } from '../components/StatTile';
import { Card, CardHeader, Grid } from '../components/layout';
import { EmptyState, ErrorState, Skeleton } from '../components/states';
import { formatExactTime, relativeTime } from '../format';
import { secondaryButtonClass } from '../ui';
import { AdminGate } from './admin/AdminGate';
import { rejectionReasons } from './admin/labels';

const trivyGuide =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md';
const sonarGuide =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md';
const linkClass =
  'text-text underline decoration-border-strong underline-offset-2 hover:decoration-text';

function Status({ ok, children }: { ok: boolean; children: React.ReactNode }) {
  const Icon = ok ? CircleCheck : TriangleAlert;
  return (
    <span className={`chip ${ok ? 'tone-pass' : 'tone-warn'}`}>
      <Icon size={14} strokeWidth={1.5} aria-hidden="true" className="shrink-0 tone-fg" />
      {children}
    </span>
  );
}

function When({ at }: { at: string }) {
  return (
    <time dateTime={at} title={formatExactTime(at)}>
      {relativeTime(at)}
    </time>
  );
}

function Never({ label, at }: { label: string; at: string | null }) {
  return at ? (
    <Freshness at={at} prefix={label} />
  ) : (
    <span className="inline-flex items-center gap-2 text-sm text-text-muted">
      <Clock size={14} strokeWidth={1.5} aria-hidden="true" />
      {label} never
    </span>
  );
}

function CardSkeleton() {
  return (
    <Card aria-label="Loading">
      <Skeleton className="h-6 w-40" />
      <Skeleton className="mt-4 h-6 w-64" />
      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <Skeleton className="h-28" />
        <Skeleton className="h-28" />
      </div>
      <Skeleton className="mt-4 h-48 w-full" />
    </Card>
  );
}

function Reason({ code }: { code: string }) {
  const text = rejectionReasons[code];
  return (
    <div>
      <Mono value={code} />
      <p className="text-sm text-text-muted">{text ?? 'This reason code is not recognised.'}</p>
    </div>
  );
}

type Rejection = { at: string; reason: string; project_path: string | null };

const rejectionColumns: Column<Rejection>[] = [
  { key: 'at', header: 'Time', cell: (r) => <When at={r.at} /> },
  { key: 'reason', header: 'Reason', cell: (r) => <Reason code={r.reason} /> },
  {
    key: 'project',
    header: 'Project path',
    cell: (r) =>
      r.project_path ? (
        <Mono value={r.project_path} />
      ) : (
        <span className="text-text-muted">Unknown</span>
      ),
  },
];

function TrivyCard() {
  const trivy = useTrivyIntegration(true);
  if (trivy.isPending) return <CardSkeleton />;
  if (trivy.isError) {
    return (
      <Card>
        <ErrorState
          title="Scan uploads could not be loaded"
          message={trivy.error.message}
          onRetry={() => void trivy.refetch()}
          retrying={trivy.isFetching}
          compact
        />
      </Card>
    );
  }
  const d = trivy.data;
  return (
    <Card as="section" aria-label="Scan uploads">
      <CardHeader
        title="Scan uploads"
        description="Trivy reports sent from GitLab CI."
        actions={
          <TargetLink
            target={{ label: 'GitLab CI guide', href: trivyGuide }}
            className={linkClass}
          />
        }
      />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        {d.configured ? (
          <Status ok>Ready</Status>
        ) : (
          <Status ok={false}>
            Not configured — set the GitLab URL in{' '}
            <Link to="/settings" className={linkClass}>
              Settings
            </Link>
          </Status>
        )}
        <Never label="Last upload" at={d.last_upload_at} />
      </div>
      <Grid cols={2} className="mt-4">
        <StatTile label="Uploads, last 24 hours" value={d.uploads_24h} tone="pass" />
        <StatTile
          label="Recorded rejections, last 24 hours"
          value={d.rejections_24h}
          tone={d.rejections_24h > 0 ? 'warn' : undefined}
        />
      </Grid>
      <h3 className="mt-6 mb-3 text-sm font-semibold text-text">Recent rejections</h3>
      <DataTable
        caption="Recent rejected uploads"
        columns={rejectionColumns}
        rows={d.recent_rejections}
        rowKey={(r) => `${r.at}|${r.reason}|${r.project_path ?? ''}`}
        manualSort
        scrollClass="max-h-96"
        empty={
          <EmptyState
            compact
            headingLevel={4}
            title="No rejections"
            body="No rejected upload was recorded recently."
          />
        }
      />
    </Card>
  );
}

const outcomes: Record<string, { Icon: LucideIcon; tone: string; text: string }> = {
  pending: { Icon: Clock, tone: 'neutral', text: 'Pending' },
  linked: { Icon: CircleCheck, tone: 'pass', text: 'Linked' },
  unlinked: { Icon: Unlink, tone: 'warn', text: 'Unlinked' },
  failed: { Icon: CircleX, tone: 'fail', text: 'Failed' },
};

function Outcome({ value }: { value: string }) {
  const o = outcomes[value] ?? { Icon: CircleDashed, tone: 'neutral', text: value };
  return (
    <span className={`chip tone-${o.tone}`}>
      <o.Icon size={14} strokeWidth={1.5} aria-hidden="true" className="shrink-0 tone-fg" />
      {o.text}
    </span>
  );
}

type Delivery = {
  task_id: string;
  project_key: string;
  received_at: string;
  outcome: string;
  service_id: number | null;
  detail: string;
};

const deliveryColumns: Column<Delivery>[] = [
  { key: 'received', header: 'Received', cell: (r) => <When at={r.received_at} /> },
  { key: 'task', header: 'Task ID', cell: (r) => <Mono value={r.task_id} truncate /> },
  { key: 'key', header: 'Project key', cell: (r) => <Mono value={r.project_key} /> },
  { key: 'outcome', header: 'Outcome', cell: (r) => <Outcome value={r.outcome} /> },
  {
    key: 'service',
    header: 'Service',
    cell: (r) =>
      r.service_id === null ? (
        <span className="text-text-muted">None</span>
      ) : (
        <Link to={`/services/${r.service_id}`} className={linkClass}>
          Service {r.service_id}
        </Link>
      ),
  },
  { key: 'detail', header: 'Detail', cell: (r) => r.detail },
];

function SonarCard() {
  const sonar = useSonarIntegration(true);
  if (sonar.isPending) return <CardSkeleton />;
  if (sonar.isError) {
    return (
      <Card>
        <ErrorState
          title="SonarQube could not be loaded"
          message={sonar.error.message}
          onRetry={() => void sonar.refetch()}
          retrying={sonar.isFetching}
          compact
        />
      </Card>
    );
  }
  const d = sonar.data;
  return (
    <Card as="section" aria-label="SonarQube">
      <CardHeader
        title="SonarQube"
        description="Analyses delivered by webhook and synced from the server."
        actions={
          <TargetLink
            target={{ label: 'SonarQube guide', href: sonarGuide }}
            className={linkClass}
          />
        }
      />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        {d.configured ? (
          <Status ok>Ready</Status>
        ) : (
          <Status ok={false}>
            Not configured — see{' '}
            <Link to="/settings" className={linkClass}>
              Settings
            </Link>
          </Status>
        )}
        <span className="text-sm text-text-muted">
          Server version{' '}
          {d.server_version ? <Mono value={d.server_version} className="text-text" /> : 'unknown'}
        </span>
        <Never label="Last webhook" at={d.last_webhook_at} />
        <Never label="Last sync" at={d.last_sync_at} />
      </div>
      {d.last_sync_error && (
        <div
          role="alert"
          className="mt-4 flex items-start gap-3 rounded-control border border-border-strong bg-bg p-4"
        >
          <CircleAlert
            size={20}
            strokeWidth={1.5}
            aria-hidden="true"
            className="mt-0.5 shrink-0 text-fail"
          />
          <div className="min-w-0">
            <p className="font-medium text-text">The last sync failed</p>
            <p className="mt-1 font-mono text-sm break-words text-text-muted">
              {d.last_sync_error}
            </p>
          </div>
        </div>
      )}
      <h3 className="mt-6 mb-3 text-sm font-semibold text-text">Recent deliveries</h3>
      <DataTable
        caption="Recent SonarQube deliveries"
        columns={deliveryColumns}
        rows={d.recent_deliveries}
        rowKey={(r) => `${r.task_id}|${r.received_at}`}
        manualSort
        scrollClass="max-h-96"
        empty={
          <EmptyState
            compact
            headingLevel={4}
            title="No deliveries yet"
            body="Webhook deliveries from SonarQube appear here."
          />
        }
      />
    </Card>
  );
}

function RefreshButton() {
  const trivy = useTrivyIntegration(true);
  const sonar = useSonarIntegration(true);
  const busy = trivy.isFetching || sonar.isFetching;
  return (
    <button
      type="button"
      className={secondaryButtonClass}
      disabled={busy}
      onClick={() => void Promise.all([trivy.refetch(), sonar.refetch()])}
    >
      <RefreshCw
        size={16}
        strokeWidth={1.5}
        aria-hidden="true"
        className={busy ? 'animate-spin' : undefined}
      />
      Refresh
    </button>
  );
}

export function IntegrationsPage() {
  return (
    <AdminGate
      title="Integrations"
      subtitle="Whether scans and analyses are arriving. This page refreshes every 30 seconds."
      actions={<RefreshButton />}
    >
      <div className="grid gap-6">
        <TrivyCard />
        <SonarCard />
      </div>
    </AdminGate>
  );
}
