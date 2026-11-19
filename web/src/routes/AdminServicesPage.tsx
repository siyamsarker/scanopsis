// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Plus } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import { useAdminServices, type Service } from '../api/admin';
import { usePortfolio } from '../api/portfolio';
import { DataTable } from '../components/DataTable';
import type { Column } from '../components/tableCore';
import { Mono } from '../components/Mono';
import { Pagination } from '../components/Pagination';
import { Segmented } from '../components/Segmented';
import { SearchInput } from '../components/inputs';
import { EmptyState, ErrorState } from '../components/states';
import { formatExactTime, relativeTime } from '../format';
import { primaryButtonClass } from '../ui';
import { AddServiceForm } from './admin/AddServiceForm';
import { AdminGate } from './admin/AdminGate';
import { DashboardBranch } from './admin/DashboardBranch';
import { ServiceActions } from './admin/ServiceActions';

function ServicesList({ onNotice }: { onNotice: (message: string) => void }) {
  const [q, setQ] = useState('');
  const [archived, setArchived] = useState(false);
  const services = useAdminServices({ q, archived }, true);
  const rows = services.data?.pages.flatMap((page) => page.items) ?? [];
  // The branch each service shows while none is chosen. The portfolio lists active
  // services only, and the label simply omits the branch when it is not known.
  const portfolio = usePortfolio();
  const latest = new Map<number, string>();
  for (const item of portfolio.data?.items ?? []) {
    if (item.ref_source === 'latest' && item.ref) latest.set(item.service.id, item.ref);
  }

  const columns: Column<Service>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (s) => (
        <Link
          to={`/services/${s.id}`}
          className="font-medium text-text underline decoration-border-strong underline-offset-2 hover:decoration-text"
        >
          {s.name}
        </Link>
      ),
    },
    {
      key: 'path',
      header: 'GitLab project path',
      cell: (s) => <Mono value={s.gitlab_project_path} />,
    },
    {
      key: 'project-id',
      header: 'GitLab project ID',
      cell: (s) =>
        s.gitlab_project_id === null ? (
          <span className="text-text-muted">Not linked yet</span>
        ) : (
          <Mono value={String(s.gitlab_project_id)} />
        ),
    },
    {
      key: 'sonar',
      header: 'SonarQube key',
      cell: (s) =>
        s.sonar_project_key === null ? (
          <span className="text-text-muted">Not linked</span>
        ) : (
          <Mono value={s.sonar_project_key} />
        ),
    },
    {
      key: 'dashboard-branch',
      header: 'Dashboard branch',
      cell: (s) => <DashboardBranch service={s} latest={latest.get(s.id)} onNotice={onNotice} />,
    },
    {
      key: 'via',
      header: 'Created via',
      cell: (s) => (s.created_via === 'ingest' ? 'Upload' : 'Manual'),
    },
    {
      key: 'created',
      header: 'Created',
      cell: (s) => (
        <time dateTime={s.created_at} title={formatExactTime(s.created_at)}>
          {relativeTime(s.created_at)}
        </time>
      ),
    },
    {
      key: 'actions',
      header: 'Actions',
      cell: (s) => <ServiceActions service={s} onNotice={onNotice} />,
    },
  ];

  return (
    <>
      {services.isError ? (
        <ErrorState
          message={services.error.message}
          onRetry={() => void services.refetch()}
          retrying={services.isFetching}
        />
      ) : (
        <DataTable
          caption={archived ? 'Archived services' : 'Active services'}
          columns={columns}
          rows={rows}
          rowKey={(s) => s.id}
          manualSort
          loading={services.isPending}
          scrollClass="max-h-none"
          toolbar={
            <div className="flex flex-wrap items-center gap-3">
              <SearchInput
                label="Search services"
                placeholder="Search name or path"
                onChange={setQ}
                className="w-64 max-w-full"
              />
              <Segmented
                label="Show services"
                value={archived ? 'archived' : 'active'}
                onChange={(v) => setArchived(v === 'archived')}
                options={[
                  { value: 'active', label: 'Active' },
                  { value: 'archived', label: 'Archived' },
                ]}
              />
            </div>
          }
          empty={
            <EmptyState
              compact
              title={
                q ? 'No services match' : archived ? 'No archived services' : 'No services yet'
              }
              body={
                q
                  ? 'Try a different name or project path.'
                  : archived
                    ? 'Services you archive appear here.'
                    : 'Add a service, or upload a Trivy report from its GitLab pipeline.'
              }
            />
          }
        />
      )}
      {!services.isPending && !services.isError && rows.length > 0 && (
        <Pagination
          className="mt-4"
          shown={rows.length}
          hasMore={services.hasNextPage}
          loading={services.isFetchingNextPage}
          onLoadMore={() => void services.fetchNextPage()}
          noun="service"
        />
      )}
    </>
  );
}

export function AdminServicesPage() {
  const [adding, setAdding] = useState(false);
  const [notice, setNotice] = useState('');
  return (
    <AdminGate
      title="Services"
      subtitle="Add services, link them to SonarQube, choose the branch their dashboards show, archive or delete them."
      actions={
        !adding && (
          <button type="button" className={primaryButtonClass} onClick={() => setAdding(true)}>
            <Plus size={16} strokeWidth={1.5} aria-hidden="true" />
            Add service
          </button>
        )
      }
    >
      <p aria-live="polite" className="mb-4 min-h-6 text-text-muted">
        {notice}
      </p>
      {adding && (
        <AddServiceForm
          onCancel={() => setAdding(false)}
          onCreated={(service) => {
            setAdding(false);
            setNotice(`Added ${service.name}.`);
          }}
        />
      )}
      <ServicesList onNotice={setNotice} />
    </AdminGate>
  );
}
