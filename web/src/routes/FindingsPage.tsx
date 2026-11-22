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
import { countOf, formatExactTime, relativeTime } from '../format';
import { linkClass } from '../ui';
import { parseFilters } from './findings/model';
import { refChoices } from './service/refSource';
import { FixTab } from './findings/FixTab';
import { SecretsTab } from './findings/SecretsTab';
import { VulnerabilitiesTab } from './findings/VulnerabilitiesTab';
import './findings/findings.css';

const TABS = ['vulnerabilities', 'fix', 'secrets'] as const;
type TabId = (typeof TABS)[number];

function NotFound() {
  return (
    <ErrorState
      title="Service not found"
      message="This service does not exist or was deleted."
      className="panel"
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
  // Only the vulnerabilities tab narrows what is loaded; the upgrade plan always
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
      <section>
        <PageHeader title="Findings" />
        <NotFound />
      </section>
    );
  }
  const failure = service.error ?? overview.error;
  if (failure) {
    return (
      <section>
        <PageHeader title="Findings" />
        <ErrorState
          message={failure instanceof ApiError ? failure.message : 'Try again in a moment.'}
          onRetry={() => {
            void service.refetch();
            void overview.refetch();
          }}
          className="panel"
        />
      </section>
    );
  }
  if (!service.data || !overview.data) {
    return (
      <section aria-busy="true">
        <header className="pb-8">
          <Skeleton className="h-5 w-48" />
          <Skeleton className="mt-2 h-8 w-40" />
          <Skeleton className="mt-3 h-4 w-72" />
        </header>
        <div className="overflow-hidden rounded-panel border border-line">
          <SkeletonTable columns={4} />
        </div>
      </section>
    );
  }

  const data = overview.data;
  const newest = [...data.artifacts].sort(
    (a, b) => Date.parse(b.scanned_at) - Date.parse(a.scanned_at),
  )[0];
  const c = data.counts;
  const open = c.critical + c.high + c.medium + c.low + c.unknown;

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

  const images = data.artifacts.filter((a) => a.kind === 'image').length;
  return (
    <section>
      <PageHeader
        breadcrumb={[
          { label: 'Portfolio', to: '/' },
          { label: service.data.name, to: `/services/${id}` },
          { label: 'Findings' },
        ]}
        title="Findings"
        subtitle={
          gitRef === undefined || !newest ? undefined : (
            <>
              Latest scan of {countOf(images, 'image')} on{' '}
              <span className="font-mono text-text">{gitRef}</span>:{' '}
              <Link to={`/scans/${newest.id}`} className={linkClass}>
                pipeline #{newest.gitlab_pipeline_id}
              </Link>
              ,{' '}
              <time dateTime={newest.scanned_at} title={formatExactTime(newest.scanned_at)}>
                {relativeTime(newest.scanned_at)}
              </time>
            </>
          )
        }
        actions={
          data.refs.length > 0 ? (
            <Select
              label="Ref"
              value={gitRef ?? ''}
              onChange={(value) => setParam('ref', value)}
              options={refChoices(data.refs, gitRef).map((r) => ({ value: r, label: r }))}
            />
          ) : undefined
        }
      />
      {gitRef === undefined || data.artifacts.length === 0 ? (
        <div className="panel">
          <EmptyState
            art="pipeline"
            title={gitRef === undefined ? 'No scans yet' : `No scans of ${gitRef} yet`}
            body={
              gitRef === undefined
                ? 'Findings appear here after the first pipeline uploads a scan.'
                : 'Findings appear here after a pipeline on this branch uploads a scan.'
            }
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
              { id: 'vulnerabilities', label: 'Vulnerabilities', count: open },
              { id: 'fix', label: 'Fix by upgrading', count: data.fixable },
              { id: 'secrets', label: 'Secrets', count: data.secrets },
            ]}
          />
          <TabPanel idBase="findings" tab="vulnerabilities" value={tab}>
            <VulnerabilitiesTab
              serviceId={id}
              rows={vulns.rows}
              loading={vulns.loading}
              loadingMore={vulns.loadingMore}
              truncated={vulns.truncated}
              error={vulns.error}
              onRetry={vulns.retry}
            />
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
          <TabPanel idBase="findings" tab="secrets" value={tab}>
            <SecretsTab serviceId={id} gitRef={gitRef} />
          </TabPanel>
        </>
      )}
    </section>
  );
}
