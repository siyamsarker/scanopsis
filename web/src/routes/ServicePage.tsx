// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Link, useParams, useSearchParams } from 'react-router';
import { ApiError } from '../api/client';
import { useOverview, useService, useSonar, useTrends, type Overview } from '../api/service';
import { Breadcrumb } from '../components/layout';
import { EmptyState, Skeleton } from '../components/states';
import type { GateState } from '../viz';
import { useFirstView } from './firstView';
import { FixFirst } from './service/FixFirst';
import { compareBase } from './service/model';
import { OpenFindings } from './service/OpenFindings';
import { SectionError } from './service/parts';
import { DAY_OPTIONS, PipelinesSection, type Days, type Lanes } from './service/PipelinesSection';
import { RecentScans } from './service/RecentScans';
import { SonarPanel } from './service/SonarPanel';
import { VerdictBand } from './service/VerdictBand';

const GITLAB_CI_GUIDE =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md';

function NotFound() {
  return (
    <EmptyState
      art="lost"
      title="Service not found"
      body="This service does not exist, or it was deleted."
      action={
        <Link to="/" className="font-medium text-text underline underline-offset-2">
          Back to the portfolio
        </Link>
      }
    />
  );
}

/** The ref has no scans: say why and what makes them appear. */
function NoScans({ overview }: { overview: Overview }) {
  const chosen = overview.ref_source === 'default';
  const title = overview.ref ? `No scans of ${overview.ref} yet` : 'No Trivy scans yet';
  const body = overview.ref
    ? (chosen
        ? 'An administrator chose this branch for the dashboards. '
        : 'No pipeline on this branch has uploaded a report. ') +
      (overview.refs.length > 0
        ? 'Other scanned branches are in the branch list above.'
        : 'Scans appear here after a pipeline on it uploads a Trivy report.')
    : 'Scans appear here after a pipeline of this project uploads its first Trivy report.';
  return (
    <section aria-label="Trivy scans" className="panel mb-10">
      <EmptyState
        compact
        art="pipeline"
        title={title}
        body={body}
        action={
          <a
            className="font-medium text-text underline underline-offset-2"
            href={GITLAB_CI_GUIDE}
            target="_blank"
            rel="noopener noreferrer"
          >
            Read the GitLab CI guide
          </a>
        }
      />
    </section>
  );
}

function BandSkeleton() {
  return (
    <div
      role="status"
      aria-label="Loading service"
      className="-mx-4 mb-10 flex items-center gap-8 border-y border-line px-4 py-8 sm:-mx-6 sm:px-6 lg:-mx-8 lg:px-8"
    >
      <Skeleton className="size-[168px] shrink-0 rounded-full" />
      <div className="flex-1 space-y-3">
        <Skeleton className="h-8 w-64 max-w-full" />
        <Skeleton className="h-4 w-48" />
        <Skeleton className="h-7 w-full max-w-xl" />
        <Skeleton className="h-10 w-72 max-w-full" />
      </div>
    </div>
  );
}

function baseOf(overview: Overview, points: Parameters<typeof compareBase>[1]) {
  const base = compareBase(overview, points);
  return { sincePipeline: base.pipeline, firstScan: base.firstScan };
}

function gateOf(status: string | undefined): GateState | undefined {
  if (status === undefined) return undefined;
  if (status === 'OK') return 'pass';
  if (status === 'ERROR') return 'fail';
  return 'none';
}

function Dashboard({ id }: { id: number }) {
  const [params, setParams] = useSearchParams();
  const refParam = params.get('ref') || undefined;
  const days: Days = DAY_OPTIONS.find((d) => d === params.get('days')) ?? '90';
  const lanes: Lanes = params.get('branches') === 'all' ? 'all' : 'ref';
  const service = useService(id);
  const overview = useOverview(id, refParam);
  const sonar = useSonar(id);
  const trends = useTrends(id, refParam, Number(days), lanes === 'all');
  const first = useFirstView(`service-${id}`);

  if (service.error instanceof ApiError && service.error.status === 404) return <NotFound />;

  function setParam(name: string, value: string | null) {
    const copy = new URLSearchParams(params);
    if (value === null) copy.delete(name);
    else copy.set(name, value);
    setParams(copy, { replace: true });
  }

  const o = overview.data;
  const scanned = o !== undefined && o.artifacts.length > 0;
  const latestSonar = sonar.data?.latest ?? null;

  return (
    <div>
      <Breadcrumb
        className="mb-4"
        items={[{ label: 'Portfolio', to: '/' }, { label: service.data?.name ?? 'Service' }]}
      />
      {service.isPending || overview.isPending ? (
        <BandSkeleton />
      ) : service.isError ? (
        <div className="mb-10">
          <SectionError
            what="The service"
            error={service.error}
            onRetry={() => void service.refetch()}
            retrying={service.isFetching}
          />
        </div>
      ) : (
        <VerdictBand
          service={service.data}
          overview={o}
          sonar={latestSonar}
          gate={gateOf(latestSonar?.quality_gate_status)}
          animate={first}
          verdict={{
            gate: gateOf(latestSonar?.quality_gate_status),
            counts: scanned ? o.counts : null,
            ref: o?.ref,
            newFindings: o?.new_since_previous ?? 0,
            fixed: o?.fixed_since_previous ?? 0,
            ...(o && trends.data ? baseOf(o, trends.data.points) : {}),
          }}
        />
      )}

      {overview.isError ? (
        <div className="mb-10">
          <SectionError
            what="Trivy findings"
            error={overview.error}
            onRetry={() => void overview.refetch()}
            retrying={overview.isFetching}
          />
        </div>
      ) : o && !scanned ? (
        <NoScans overview={o} />
      ) : null}

      {o && scanned ? (
        <>
          <PipelinesSection
            serviceId={id}
            gitRef={refParam}
            shownRef={o.ref}
            days={days}
            lanes={lanes}
            onDaysChange={(next) => setParam('days', next === '90' ? null : next)}
            onLanesChange={(next) => setParam('branches', next === 'all' ? 'all' : null)}
            animate={first}
          />
          <div className="mb-12 grid items-start gap-6 lg:grid-cols-12">
            <div className="min-w-0 lg:col-span-7">
              <FixFirst overview={o} serviceId={id} />
            </div>
            <div className="min-w-0 lg:col-span-5">
              <OpenFindings overview={o} serviceId={id} />
            </div>
          </div>
        </>
      ) : null}

      <div className="mb-12">
        <SonarPanel serviceId={id} />
      </div>

      {o && scanned ? <RecentScans serviceId={id} gitRef={refParam} /> : null}
    </div>
  );
}

export function ServicePage() {
  const { id: raw } = useParams();
  const id = Number(raw);
  if (!Number.isSafeInteger(id) || id < 1) return <NotFound />;
  return <Dashboard id={id} />;
}
