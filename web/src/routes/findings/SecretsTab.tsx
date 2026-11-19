// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ApiError } from '../../api/client';
import type { SecretFinding } from '../../api/findings';
import { useSecrets } from '../../api/findings';
import { DataTable } from '../../components/DataTable';
import { Mono } from '../../components/Mono';
import { SeverityBadge } from '../../components/Severity';
import { severityKey } from '../../components/severityModel';
import { EmptyState, ErrorState } from '../../components/states';
import type { Column } from '../../components/tableCore';
import { SEVERITY_RANK } from './model';

/** "12–14", or "12" for a single line. */
export function lineRange(start: number, end: number) {
  if (!start) return '–';
  return end > start ? `${start}–${end}` : String(start);
}

const columns: Column<SecretFinding>[] = [
  {
    key: 'severity',
    header: 'Severity',
    sortKey: 'severity',
    sortValue: (s) => SEVERITY_RANK[severityKey(s.severity)],
    cell: (s) => <SeverityBadge severity={s.severity} />,
  },
  { key: 'rule', header: 'Rule', cell: (s) => <Mono value={s.rule_id} /> },
  { key: 'category', header: 'Category', cell: (s) => s.category || '–' },
  { key: 'target', header: 'Target', cell: (s) => <Mono value={s.target} /> },
  {
    key: 'lines',
    header: 'Lines',
    cell: (s) => <span className="tabular-nums">{lineRange(s.start_line, s.end_line)}</span>,
  },
  { key: 'artifact', header: 'Artifact', cell: (s) => s.artifact_name },
  {
    key: 'suppressed',
    header: 'Suppressed',
    cell: (s) =>
      s.suppressed ? <span className="chip tone-neutral">Suppressed</span> : <span>No</span>,
  },
];

export function SecretsTab({
  serviceId,
  gitRef,
}: {
  serviceId: number;
  gitRef: string | undefined;
}) {
  const query = useSecrets(serviceId, gitRef, true);
  if (query.isError) {
    return (
      <ErrorState
        title="Secret findings could not be loaded"
        message={query.error instanceof ApiError ? query.error.message : 'Try again in a moment.'}
        onRetry={() => void query.refetch()}
        retrying={query.isFetching}
      />
    );
  }
  const rows = [...(query.data?.items ?? [])].sort(
    (a, b) =>
      SEVERITY_RANK[severityKey(b.severity)] - SEVERITY_RANK[severityKey(a.severity)] ||
      a.target.localeCompare(b.target) ||
      a.start_line - b.start_line,
  );
  return (
    <DataTable
      caption="Secret findings"
      columns={columns}
      rows={rows}
      rowKey={(s) => s.id}
      loading={query.isPending}
      empty={<EmptyState compact title="No secrets found in the current scans" />}
    />
  );
}
