// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useSearchParams } from 'react-router';
import { useAuditLog, type AuditEvent } from '../api/admin';
import { DataTable } from '../components/DataTable';
import type { Column } from '../components/tableCore';
import { Mono } from '../components/Mono';
import { Pagination } from '../components/Pagination';
import { SearchInput, Select } from '../components/inputs';
import { EmptyState, ErrorState } from '../components/states';
import { formatExactTime, relativeTime } from '../format';
import { AdminGate } from './admin/AdminGate';
import { AuditDetails } from './admin/AuditDetails';
import { auditActions } from './admin/labels';

const columns: Column<AuditEvent>[] = [
  {
    key: 'time',
    header: 'Time',
    cell: (e) => (
      <time dateTime={e.at} title={formatExactTime(e.at)}>
        <span className="block">{relativeTime(e.at)}</span>
        <span className="block text-sm text-text-muted">{formatExactTime(e.at)}</span>
      </time>
    ),
  },
  { key: 'actor', header: 'Actor', cell: (e) => <Mono value={e.actor} /> },
  {
    key: 'action',
    header: 'Action',
    cell: (e) => (
      <div>
        <Mono value={e.action} />
        {auditActions[e.action] && (
          <p className="text-sm text-text-muted">{auditActions[e.action]}</p>
        )}
      </div>
    ),
  },
  { key: 'detail', header: 'Details', cell: (e) => <AuditDetails detail={e.detail} /> },
];

const actionOptions = [
  { value: '', label: 'All actions' },
  ...Object.entries(auditActions).map(([value, label]) => ({
    value,
    label: `${label} (${value})`,
  })),
];

function AuditLog() {
  const [params, setParams] = useSearchParams();
  const action = params.get('action') ?? '';
  const actor = params.get('actor') ?? '';
  const audit = useAuditLog({ action, actor }, true);
  const rows = audit.data?.pages.flatMap((page) => page.items) ?? [];

  function set(name: string, value: string) {
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

  if (audit.isError) {
    return (
      <ErrorState
        message={audit.error.message}
        onRetry={() => void audit.refetch()}
        retrying={audit.isFetching}
      />
    );
  }
  return (
    <>
      <DataTable
        caption="Audit log"
        columns={columns}
        rows={rows}
        rowKey={(e) => e.id}
        manualSort
        loading={audit.isPending}
        scrollClass="max-h-none"
        toolbar={
          <div className="flex flex-wrap items-end gap-3">
            <Select
              label="Action"
              hideLabel
              value={action}
              onChange={(v) => set('action', v)}
              options={actionOptions}
            />
            <SearchInput
              label="Filter by actor"
              placeholder="Filter by actor"
              defaultValue={actor}
              onChange={(v) => set('actor', v)}
              className="w-64 max-w-full"
            />
          </div>
        }
        empty={
          <EmptyState
            compact
            title="No events"
            body={
              action || actor
                ? 'No events match these filters.'
                : 'Events appear as people use Scanopsis.'
            }
          />
        }
      />
      {!audit.isPending && rows.length > 0 && (
        <Pagination
          className="mt-4"
          shown={rows.length}
          hasMore={audit.hasNextPage}
          loading={audit.isFetchingNextPage}
          onLoadMore={() => void audit.fetchNextPage()}
          noun="event"
        />
      )}
    </>
  );
}

export function AuditPage() {
  return (
    <AdminGate title="Audit log" subtitle="Who did what, newest first.">
      <AuditLog />
    </AdminGate>
  );
}
