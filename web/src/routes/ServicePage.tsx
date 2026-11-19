// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ArrowRight, ExternalLink } from 'lucide-react';
import { Link, useParams, useSearchParams } from 'react-router';
import { ApiError } from '../api/client';
import { useOverview, useScanLinks, useService, useSonar } from '../api/service';
import { Freshness } from '../components/Freshness';
import { Mono } from '../components/Mono';
import { Select } from '../components/inputs';
import { PageHeader } from '../components/layout';
import { EmptyState, Skeleton } from '../components/states';
import { safeExternalUrl } from '../format';
import { primaryButtonClass } from '../ui';
import { SonarPosture } from './service/SonarPosture';
import {
  CoverageChart,
  DAY_OPTIONS,
  RecentScans,
  TrendsChart,
  type Days,
} from './service/Timeline';
import { TrivyPosture } from './service/TrivyPosture';
import { SectionError } from './service/parts';
import { findingsHref, projectUrlFrom } from './service/links';

function NotFound() {
  return (
    <EmptyState
      title="Service not found"
      body="This service does not exist, or it was deleted."
      action={
        <Link to="/" className="text-brand underline underline-offset-2">
          Back to the portfolio
        </Link>
      }
    />
  );
}

function Header({ id, refParam }: { id: number; refParam: string | undefined }) {
  const [params, setParams] = useSearchParams();
  const service = useService(id);
  const overview = useOverview(id, refParam);
  const sonar = useSonar(id);

  const artifacts = overview.data?.artifacts ?? [];
  const latestScan = artifacts.reduce<(typeof artifacts)[number] | undefined>(
    (best, scan) => (!best || scan.scanned_at > best.scanned_at ? scan : best),
    undefined,
  );
  const scanLinks = useScanLinks(latestScan?.id);
  const gitlab = projectUrlFrom(
    scanLinks.data?.pipeline_url ?? scanLinks.data?.commit_url ?? scanLinks.data?.job_url,
  );
  const effectiveRef = refParam ?? overview.data?.ref ?? undefined;
  const refs = overview.data?.refs ?? [];
  const sonarLatest = sonar.data?.latest;
  const sonarUrl = safeExternalUrl(sonarLatest?.project_url);

  if (service.isPending) {
    return (
      <div className="pb-6" role="status" aria-label="Loading service">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="mt-2 h-6 w-48" />
        <Skeleton className="mt-3 h-5 w-80 max-w-full" />
      </div>
    );
  }
  if (service.isError) {
    return (
      <div className="pb-6">
        <SectionError
          what="The service"
          error={service.error}
          onRetry={() => void service.refetch()}
          retrying={service.isFetching}
        />
      </div>
    );
  }
  const s = service.data;

  function selectRef(next: string) {
    const copy = new URLSearchParams(params);
    copy.set('ref', next);
    setParams(copy, { replace: true });
  }

  return (
    <>
      <PageHeader
        eyebrow={
          <Link to="/" className="underline underline-offset-2 hover:text-text">
            Portfolio
          </Link>
        }
        title={s.name}
        subtitle={
          <span className="inline-flex flex-wrap items-center gap-x-2">
            <Mono value={s.gitlab_project_path} />
            {gitlab ? (
              <a
                href={gitlab}
                target="_blank"
                rel="noopener noreferrer"
                aria-label={`Open ${s.gitlab_project_path} in GitLab`}
                className="inline-flex text-text-muted hover:text-text"
              >
                <ExternalLink size={14} strokeWidth={1.5} aria-hidden="true" />
              </a>
            ) : null}
          </span>
        }
        actions={
          <>
            {refs.length > 0 ? (
              <Select
                label="Git ref"
                value={effectiveRef ?? refs[0]}
                onChange={selectRef}
                options={refs.map((r) => ({ value: r, label: r }))}
              />
            ) : null}
            <Link to={findingsHref(id, effectiveRef)} className={`${primaryButtonClass} self-end`}>
              Explore findings
              <ArrowRight size={16} strokeWidth={1.5} aria-hidden="true" />
            </Link>
          </>
        }
      />
      <div className="-mt-3 flex flex-wrap gap-x-6 gap-y-1 pb-6">
        {overview.data?.data_at ? (
          <Freshness
            at={overview.data.data_at}
            prefix="Trivy scanned"
            link={
              latestScan
                ? {
                    label: `Pipeline #${latestScan.gitlab_pipeline_id}`,
                    to: `/scans/${latestScan.id}`,
                  }
                : undefined
            }
          />
        ) : null}
        {sonarLatest ? (
          <Freshness
            at={sonarLatest.analysed_at}
            prefix="SonarQube analysed"
            link={sonarUrl ? { label: 'Open project', href: sonarUrl } : undefined}
          />
        ) : null}
      </div>
    </>
  );
}

function Dashboard({ id }: { id: number }) {
  const [params, setParams] = useSearchParams();
  const refParam = params.get('ref') || undefined;
  const daysParam = params.get('days');
  const days: Days = DAY_OPTIONS.find((d) => d === daysParam) ?? '90';
  const service = useService(id);

  if (service.error instanceof ApiError && service.error.status === 404) return <NotFound />;

  function setDays(next: Days) {
    const copy = new URLSearchParams(params);
    copy.set('days', next);
    setParams(copy, { replace: true });
  }

  return (
    <div>
      <Header id={id} refParam={refParam} />
      <div className="grid gap-8 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:items-start">
        <section aria-label="Trivy posture" className="min-w-0 space-y-4">
          <h2 className="text-lg font-semibold tracking-tight text-text">Trivy posture</h2>
          <TrivyPosture serviceId={id} gitRef={refParam} />
        </section>
        <section aria-label="SonarQube posture" className="min-w-0 space-y-4">
          <h2 className="text-lg font-semibold tracking-tight text-text">SonarQube posture</h2>
          <SonarPosture serviceId={id} />
        </section>
      </div>
      <section aria-label="Pipeline timeline" className="mt-10 space-y-4">
        <h2 className="text-lg font-semibold tracking-tight text-text">Pipeline timeline</h2>
        <div className="grid gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
          <TrendsChart serviceId={id} gitRef={refParam} days={days} onDaysChange={setDays} />
          <CoverageChart serviceId={id} />
        </div>
        <div className="pt-4">
          <RecentScans serviceId={id} gitRef={refParam} />
        </div>
      </section>
    </div>
  );
}

export function ServicePage() {
  const { id: raw } = useParams();
  const id = Number(raw);
  if (!Number.isSafeInteger(id) || id < 1) return <NotFound />;
  return <Dashboard id={id} />;
}
