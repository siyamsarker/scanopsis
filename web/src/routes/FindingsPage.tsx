// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Link, useParams, useSearchParams } from 'react-router';
import { ApiError } from '../api/client';
import {
  MAX_ROWS,
  useAllVulnerabilities,
  useFindingsOverview,
  useFindingsService,
  type ServerFilters,
} from '../api/findings';
import { Select } from '../components/inputs';
import { PageHeader } from '../components/layout';
import { EmptyState, ErrorState, Skeleton, SkeletonTable } from '../components/states';
import { TabPanel, Tabs } from '../components/Tabs';
import { parseFilters } from './findings/model';
import { FixTab } from './findings/FixTab';
import { SecretsTab } from './findings/SecretsTab';
import { VulnerabilitiesTab } from './findings/VulnerabilitiesTab';

const TABS = ['vulnerabilities', 'secrets', 'fix'] as const;
type TabId = (typeof TABS)[number];

function NotFound() {
  return (
    <ErrorState
      title="Service not found"
      message="This service does not exist or was deleted."
      className="rounded-card border border-border bg-surface"
    />
  );
}

/** `rowCeiling` is the number of rows after which loading stops; only tests change it. */
export function FindingsPage({ rowCeiling = MAX_ROWS }: { rowCeiling?: number } = {}) {
  const { id: idParam } = useParams();
  const id = /^\d+$/.test(idParam ?? '') ? Number(idParam) : NaN;
  const [params, setParams] = useSearchParams();
  const urlRef = params.get('ref');
  const tabParam = params.get('tab');
  const tab: TabId = (TABS as readonly string[]).includes(tabParam ?? '')
    ? (tabParam as TabId)
    : 'vulnerabilities';
  const includeSuppressed = params.get('suppressed') === 'true';

  const service = useFindingsService(id);
  const overview = useFindingsOverview(id, urlRef ?? undefined);
  const gitRef = overview.data?.ref ?? undefined;
  // Only the vulnerabilities tab narrows what is loaded; the upgrade list always
  // works on the unfiltered state, so its totals never depend on a leftover filter.
  const urlFilters = parseFilters(params);
  const serverFilters: ServerFilters | undefined =
    tab === 'vulnerabilities'
      ? {
          severity: urlFilters.severity.map((s) => s.toUpperCase()).join(',') || undefined,
          fixable: urlFilters.fixable === null ? undefined : urlFilters.fixable === 'true',
          q: urlFilters.q || undefined,
        }
      : undefined;
  const vulns = useAllVulnerabilities(
    id,
    gitRef,
    includeSuppressed,
    tab === 'vulnerabilities' || tab === 'fix',
    serverFilters,
    rowCeiling,
  );

  const notFound = [service.error, overview.error].some(
    (e) => e instanceof ApiError && e.status === 404,
  );
  if (Number.isNaN(id) || notFound) {
    return (
      <section className="mx-auto max-w-7xl p-6">
        <PageHeader title="Findings" />
        <NotFound />
      </section>
    );
  }
  const failure = service.error ?? overview.error;
  if (failure) {
    return (
      <section className="mx-auto max-w-7xl p-6">
        <PageHeader title="Findings" />
        <ErrorState
          message={failure instanceof ApiError ? failure.message : 'Try again in a moment.'}
          onRetry={() => {
            void service.refetch();
            void overview.refetch();
          }}
          className="rounded-card border border-border bg-surface"
        />
      </section>
    );
  }
  if (!service.data || !overview.data) {
    return (
      <section className="mx-auto max-w-7xl p-6" aria-busy="true">
        <header className="pb-6">
          <Skeleton className="h-5 w-32" />
          <Skeleton className="mt-2 h-8 w-40" />
          <Skeleton className="mt-3 h-4 w-48" />
        </header>
        <div className="overflow-hidden rounded-card border border-border bg-surface">
          <SkeletonTable columns={5} />
        </div>
      </section>
    );
  }

  const data = overview.data;
  const newest = [...data.artifacts].sort(
    (a, b) => Date.parse(b.scanned_at) - Date.parse(a.scanned_at),
  )[0];

  function setParam(name: string, value: string | null) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(name, value);
        else next.delete(name);
        return next;
      },
      { replace: true },
    );
  }

  return (
    <section className="mx-auto max-w-7xl p-6">
      <PageHeader
        eyebrow={
          <Link
            to={`/services/${id}`}
            className="underline decoration-border-strong underline-offset-2 hover:decoration-text"
          >
            {service.data.name}
          </Link>
        }
        title="Findings"
        freshness={
          data.data_at
            ? {
                at: data.data_at,
                prefix: 'Scanned',
                link: newest
                  ? { label: `Pipeline ${newest.gitlab_pipeline_id}`, to: `/scans/${newest.id}` }
                  : undefined,
              }
            : undefined
        }
        actions={
          data.refs.length > 0 ? (
            <Select
              label="Ref"
              value={gitRef ?? ''}
              onChange={(value) => setParam('ref', value)}
              options={data.refs.map((r) => ({ value: r, label: r }))}
            />
          ) : undefined
        }
      />
      {gitRef === undefined ? (
        <div className="rounded-card border border-border bg-surface">
          <EmptyState
            title="No scans yet"
            body="Findings appear here after the first pipeline uploads a scan."
          />
        </div>
      ) : (
        <>
          <Tabs
            idBase="findings"
            label="Findings sections"
            value={tab}
            onChange={(next) => setParam('tab', next === 'vulnerabilities' ? null : next)}
            tabs={[
              { id: 'vulnerabilities', label: 'Vulnerabilities' },
              { id: 'secrets', label: 'Secrets', count: data.secrets },
              { id: 'fix', label: 'Fix by upgrading' },
            ]}
          />
          <TabPanel idBase="findings" tab="vulnerabilities" value={tab}>
            <VulnerabilitiesTab
              rows={vulns.rows}
              loading={vulns.loading}
              loadingMore={vulns.loadingMore}
              truncated={vulns.truncated}
              error={vulns.error}
              onRetry={vulns.retry}
            />
          </TabPanel>
          <TabPanel idBase="findings" tab="secrets" value={tab}>
            <SecretsTab serviceId={id} gitRef={gitRef} />
          </TabPanel>
          <TabPanel idBase="findings" tab="fix" value={tab}>
            <FixTab
              serviceId={id}
              urlRef={urlRef}
              rows={vulns.rows}
              loading={vulns.loading}
              loadingMore={vulns.loadingMore}
              truncated={vulns.truncated}
              error={vulns.error}
              onRetry={vulns.retry}
            />
          </TabPanel>
        </>
      )}
    </section>
  );
}
