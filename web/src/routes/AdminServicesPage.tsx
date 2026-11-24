// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Plus } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import { useAdminServices, type Service } from '../api/admin';
import { usePortfolio } from '../api/portfolio';
import { Pagination } from '../components/Pagination';
import { StatusMark } from '../components/SeverityMark';
import { Tabs } from '../components/Tabs';
import { toast } from '../components/Toast';
import { Tooltip } from '../components/Tooltip';
import { SearchInput } from '../components/inputs';
import { EmptyState, ErrorState, SkeletonTable } from '../components/states';
import { buttonClass } from '../ui';
import { AddServiceDialog } from './admin/AddServiceForm';
import { AdminGate } from './admin/AdminGate';
import { DashboardBranch } from './admin/DashboardBranch';
import { ServiceActions } from './admin/ServiceActions';
import { FlatTable, When, td, th } from './admin/kit';

/** What the portfolio knows about a service: the branch it shows, and its last analysis. */
interface Known {
  latest?: string;
  analysedAt: string | null;
  scannedAt: string | null;
}

/** The SonarQube key and whether analyses arrive for it. */
function SonarLink({ service, known }: { service: Service; known?: Known }) {
  const key = service.sonar_project_key;
  const declared = service.sonar_key_declared ?? null;
  if (key === null && declared) {
    const tip =
      'The pipeline declared this key in an upload. It is set when a SonarQube analysis of the key names this GitLab project. Setting a key here cancels it.';
    return (
      <span className="flex min-w-0 items-center gap-1.5 text-text-muted">
        <Tooltip content={tip}>
          <button type="button" className="inline-flex shrink-0 rounded-full" aria-label={tip}>
            <StatusMark status="none" size={13} />
          </button>
        </Tooltip>
        <span className="min-w-0 truncate" title={`Waiting for SonarQube project ${declared}`}>
          Waiting for SonarQube project <span className="font-mono text-small">{declared}</span>
        </span>
      </span>
    );
  }
  if (key === null) {
    return (
      <span className="inline-flex items-center gap-1.5 whitespace-nowrap text-text-muted">
        <StatusMark status="none" size={13} />
        Not linked
      </span>
    );
  }
  const analysed = known?.analysedAt ?? null;
  const status = analysed ? 'ok' : 'warn';
  const tip = analysed
    ? 'Linked: analyses arrive for this key.'
    : service.archived
      ? 'Linked. Archived services are not on the portfolio.'
      : 'Linked, but no analysis has arrived yet. Check the webhook in SonarQube.';
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <Tooltip content={tip}>
        <button type="button" className="inline-flex shrink-0 rounded-full" aria-label={tip}>
          <StatusMark status={service.archived ? 'none' : status} size={13} />
        </button>
      </Tooltip>
      <span className="min-w-0 truncate font-mono text-small text-text" title={key}>
        {key}
      </span>
    </span>
  );
}

/** Days after which a service's latest scan counts as stale. */
const STALE_DAYS = 14;

/** When the newest scan arrived, flagged when it is old, and how the service was added. */
function LastScan({
  service,
  known,
  waiting,
}: {
  service: Service;
  known?: Known;
  waiting: boolean;
}) {
  const at = known?.scannedAt ?? null;
  const [now] = useState(() => Date.now());
  const stale = at !== null && now - new Date(at).getTime() > STALE_DAYS * 86_400_000;
  const added = service.created_via === 'ingest' ? 'Added by its first upload' : 'Added by hand';
  return (
    <>
      <span className="flex items-center gap-1.5 text-text">
        {at ? (
          <>
            {stale ? <StatusMark status="warn" size={12} /> : null}
            <When at={at} />
            {stale ? <span className="sr-only">, older than {STALE_DAYS} days</span> : null}
          </>
        ) : (
          <span className="text-text-muted">
            {waiting ? 'Waiting for the first upload' : 'No scans'}
          </span>
        )}
      </span>
      <span className="block text-small text-text-muted">
        {added}, <When at={service.created_at} />
      </span>
    </>
  );
}

function ServiceRow({ service, known }: { service: Service; known?: Known }) {
  const [branchOpen, setBranchOpen] = useState(false);
  const waiting = service.gitlab_project_id === null;
  return (
    <tr className="hover-tint">
      <td className={`${td} w-[48%] max-w-0 sm:w-[34%] sm:min-w-48`}>
        <Link
          to={`/services/${service.id}`}
          className="block truncate font-medium text-text decoration-line-strong underline-offset-[3px] hover:underline"
        >
          {service.name}
        </Link>
        <span
          className="mt-0.5 block truncate font-mono text-small text-text-muted"
          title={service.gitlab_project_path}
        >
          {service.gitlab_project_path}
        </span>
      </td>
      <td className={`${td} max-w-0 sm:w-56 sm:max-w-none`}>
        <DashboardBranch
          service={service}
          latest={known?.latest}
          open={branchOpen}
          onOpenChange={setBranchOpen}
        />
      </td>
      <td className={`${td} hidden max-w-0 w-[24%] md:table-cell`}>
        <SonarLink service={service} known={known} />
      </td>
      <td className={`${td} hidden whitespace-nowrap lg:table-cell`}>
        <LastScan service={service} known={known} waiting={waiting} />
      </td>
      <td className={`${td} w-12 pr-2 text-right`}>
        <ServiceActions service={service} onChooseBranch={() => setBranchOpen(true)} />
      </td>
    </tr>
  );
}

function ServicesList({ q, archived }: { q: string; archived: boolean }) {
  const services = useAdminServices({ q, archived }, true);
  const rows = services.data?.pages.flatMap((page) => page.items) ?? [];
  // The portfolio lists active services with the branch they show and their
  // last SonarQube analysis; archived services are not on it.
  const portfolio = usePortfolio();
  const known = new Map<number, Known>();
  for (const item of portfolio.data?.items ?? []) {
    known.set(item.service.id, {
      latest: item.ref_source === 'latest' && item.ref ? item.ref : undefined,
      analysedAt: item.last_analysis_at,
      scannedAt: item.last_scan_at,
    });
  }

  if (services.isError) {
    return (
      <ErrorState
        title="Services could not be loaded"
        message={services.error.message}
        onRetry={() => void services.refetch()}
        retrying={services.isFetching}
      />
    );
  }
  if (services.isPending) return <SkeletonTable rows={6} columns={4} />;
  if (rows.length === 0) {
    return (
      <EmptyState
        compact
        art={q ? 'search' : 'services'}
        title={q ? 'No services match' : archived ? 'No archived services' : 'No services yet'}
        body={
          q
            ? 'Try another part of the name or the project path.'
            : archived
              ? 'Services you archive appear here, with their history kept.'
              : 'Add a service, or let the first Trivy upload from a GitLab pipeline create it.'
        }
      />
    );
  }
  return (
    <>
      <FlatTable
        caption={archived ? 'Archived services' : 'Active services'}
        head={
          <>
            <th scope="col" className={th}>
              Service
            </th>
            <th scope="col" className={th}>
              Dashboard branch
            </th>
            <th scope="col" className={`${th} hidden md:table-cell`}>
              SonarQube key
            </th>
            <th scope="col" className={`${th} hidden lg:table-cell`}>
              Last scan
            </th>
            <th scope="col" className={`${th} text-right`}>
              <span className="sr-only">Actions</span>
            </th>
          </>
        }
      >
        {rows.map((service) => (
          <ServiceRow key={service.id} service={service} known={known.get(service.id)} />
        ))}
      </FlatTable>
      <Pagination
        className="border-t border-line px-4 py-3"
        shown={rows.length}
        hasMore={services.hasNextPage}
        loading={services.isFetchingNextPage}
        onLoadMore={() => void services.fetchNextPage()}
        noun="service"
      />
    </>
  );
}

export function AdminServicesPage() {
  const [adding, setAdding] = useState(false);
  const [q, setQ] = useState('');
  const [tab, setTab] = useState<'active' | 'archived'>('active');
  return (
    <AdminGate
      title="Services"
      subtitle="Every GitLab project Scanopsis follows: its SonarQube link, the branch its dashboards show, and whether it is archived."
      actions={
        <button type="button" className={buttonClass('primary')} onClick={() => setAdding(true)}>
          <Plus size={16} strokeWidth={2} aria-hidden="true" />
          Add service
        </button>
      }
    >
      <div className="panel overflow-hidden">
        <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3 px-4 pt-2 sm:px-5">
          <Tabs
            label="Show services"
            idBase="services"
            value={tab}
            onChange={(id) => setTab(id as 'active' | 'archived')}
            tabs={[
              { id: 'active', label: 'Active' },
              { id: 'archived', label: 'Archived' },
            ]}
            className="border-b-0"
          />
          <SearchInput
            label="Search services"
            placeholder="Search name or path"
            onChange={setQ}
            primary
            className="mb-2 w-full sm:w-72"
          />
        </div>
        <div
          role="tabpanel"
          id={`services-panel-${tab}`}
          aria-labelledby={`services-tab-${tab}`}
          className="border-t border-line"
        >
          <ServicesList q={q} archived={tab === 'archived'} />
        </div>
      </div>
      <AddServiceDialog
        open={adding}
        onOpenChange={setAdding}
        onCreated={(service) => {
          setAdding(false);
          toast.success('Service added', {
            description: `${service.name} is ready for its first upload.`,
          });
        }}
      />
    </AdminGate>
  );
}
