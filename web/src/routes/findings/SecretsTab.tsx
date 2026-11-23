// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { KeyRound } from 'lucide-react';
import { ApiError } from '../../api/client';
import { useSecrets } from '../../api/findings';
import { SeverityMark } from '../../components/SeverityMark';
import { SEVERITY_LABEL, severityKey } from '../../components/severityModel';
import { EmptyState, ErrorState, SkeletonTable } from '../../components/states';
import { artifactLabel, countOf } from '../../format';
import { SEVERITY_RANK } from './model';

/** "12–14", or "12" for a single line. */
export function lineRange(start: number, end: number) {
  if (!start) return '–';
  return end > start ? `${start}–${end}` : String(start);
}

/** "config.yml:12–14"; the file alone when no line was reported. */
export function location(target: string, start: number, end: number) {
  return start ? `${target}:${lineRange(start, end)}` : target;
}

const GRID =
  'grid grid-cols-[1.25rem_minmax(0,1fr)] gap-x-3 sm:grid-cols-[6rem_minmax(0,1fr)] md:grid-cols-[6rem_minmax(0,1.2fr)_minmax(0,1fr)_9rem] md:gap-x-4';

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
  if (query.isPending) {
    return (
      <div className="overflow-hidden rounded-panel border border-line">
        <SkeletonTable columns={4} />
      </div>
    );
  }
  const rows = [...query.data.items].sort(
    (a, b) =>
      SEVERITY_RANK[severityKey(b.severity)] - SEVERITY_RANK[severityKey(a.severity)] ||
      a.target.localeCompare(b.target) ||
      a.start_line - b.start_line,
  );
  if (rows.length === 0) {
    return (
      <div className="panel">
        <EmptyState
          compact
          art="clean"
          title="No secrets found in the current scans"
          body="Trivy's secret scan of the source tree reported nothing."
        />
      </div>
    );
  }
  const files = new Set(rows.map((s) => s.target)).size;
  return (
    <div className="space-y-3">
      <p className="flex flex-wrap items-center gap-x-2 text-body-s text-text-muted">
        <KeyRound size={16} strokeWidth={1.75} aria-hidden="true" className="text-text-faint" />
        {countOf(rows.length, 'secret')} in {countOf(files, 'file')}. Matched values are never
        stored: rotate the credential, then remove it from the history.
      </p>
      <div role="table" aria-label="Secret findings">
        <div role="rowgroup">
          <div
            role="row"
            className={`${GRID} border-b border-line px-3 py-2 text-caption font-medium text-text-muted sm:px-4`}
          >
            <span role="columnheader" className="max-sm:sr-only">
              Severity
            </span>
            <span role="columnheader">Rule and location</span>
            <span role="columnheader" className="hidden md:block">
              Match
            </span>
            <span role="columnheader" className="hidden md:block">
              Found in
            </span>
          </div>
        </div>
        <div role="rowgroup">
          {rows.map((s) => {
            const sev = severityKey(s.severity);
            return (
              <div
                key={s.id}
                role="row"
                className={`${GRID} min-h-16 items-center border-b border-line/70 px-3 py-2.5 sm:px-4 ${s.suppressed ? 'opacity-70' : ''}`}
              >
                <span role="cell" className="flex items-center gap-2">
                  <SeverityMark severity={sev} size={13} />
                  <span className={`tone-${sev} tone-ink text-small font-medium max-sm:sr-only`}>
                    {SEVERITY_LABEL[sev]}
                  </span>
                </span>
                <span role="cell" className="min-w-0">
                  <span className="flex min-w-0 items-baseline gap-2">
                    <span className="truncate text-body-s font-medium text-text">
                      {s.title || s.rule_id}
                    </span>
                    <span className="shrink-0 font-mono text-small text-text-muted">
                      {s.rule_id}
                    </span>
                    {s.suppressed ? <span className="tag shrink-0">Suppressed</span> : null}
                  </span>
                  <span
                    className="mt-1 block truncate font-mono text-small text-text-muted"
                    title={location(s.target, s.start_line, s.end_line)}
                  >
                    {s.target}
                    {s.start_line ? (
                      <span className="text-text">:{lineRange(s.start_line, s.end_line)}</span>
                    ) : null}
                  </span>
                </span>
                <span role="cell" className="hidden min-w-0 md:block">
                  <span
                    className="inline-flex items-center gap-2 rounded-tag border border-dashed border-line-strong bg-sunken px-2 py-0.5 font-mono text-small text-text-muted"
                    title="The matched value is not stored"
                  >
                    <span aria-hidden="true">••••••••••</span>
                    <span className="sr-only">Value not stored</span>
                  </span>
                  {s.category ? (
                    <span className="ml-2 text-small text-text-faint">{s.category}</span>
                  ) : null}
                </span>
                <span role="cell" className="hidden truncate text-small text-text-muted md:block">
                  {artifactLabel(s.artifact_name, 'secret-fs')}
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
