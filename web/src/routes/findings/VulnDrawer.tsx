// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ArrowRight, ExternalLink } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { useVulnerabilitySearch, type Vulnerability } from '../../api/findings';
import { useScan } from '../../api/scan';
import { CopyIconButton } from '../../components/CopyIconButton';
import { Sheet } from '../../components/Dialog';
import { Mono } from '../../components/Mono';
import { SeverityMark } from '../../components/SeverityMark';
import { SEVERITY_LABEL, severityKey } from '../../components/severityModel';
import { Skeleton } from '../../components/states';
import { countOf, formatExactTime, relativeTime, safeExternalUrl } from '../../format';
import { linkClass } from '../../ui';
import { RISK_WEIGHTS } from '../../viz';
import { suggestedFix, upgradeCommand } from './model';

/** An external link, only for http(s) URLs; anything else is not linked. */
export function SafeExternal({
  href,
  children,
  className = linkClass,
}: {
  href: string | null | undefined;
  children: ReactNode;
  className?: string;
}) {
  const safe = safeExternalUrl(href);
  if (!safe) return <span>{children}</span>;
  return (
    <a href={safe} target="_blank" rel="noopener noreferrer" className={className}>
      {children}
      <ExternalLink
        size={12}
        strokeWidth={1.75}
        aria-hidden="true"
        className="ml-1 inline-block shrink-0 align-[-1px]"
      />
      <span className="sr-only"> (opens in a new tab)</span>
    </a>
  );
}

/** The advisory link for a vulnerability: external for http(s) URLs, plain text otherwise. */
export function AdvisoryLink({ vuln }: { vuln: Vulnerability }) {
  const href = safeExternalUrl(vuln.primary_url);
  if (!href) return <Mono value={vuln.vuln_id} />;
  return (
    <SafeExternal href={href}>
      <span className="font-mono">{vuln.vuln_id}</span>
    </SafeExternal>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[7.5rem_minmax(0,1fr)] gap-3 border-b border-line py-2.5 last:border-b-0">
      <dt className="text-small text-text-muted">{label}</dt>
      <dd className="min-w-0 text-body-s break-words text-text">{children}</dd>
    </div>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mt-6">
      <h3 className="mb-2 text-body-s font-semibold text-text">{title}</h3>
      {children}
    </section>
  );
}

/** Where else the same ID is open: other services, through the search API. */
function Elsewhere({ vulnId, serviceId }: { vulnId: string; serviceId: number }) {
  const search = useVulnerabilitySearch(vulnId);
  const searchHref = `/search?${new URLSearchParams({ id: vulnId })}`;
  if (search.isPending) return <Skeleton className="h-12 w-full" />;
  if (search.isError) {
    return (
      <p className="text-body-s text-text-muted">
        Other services could not be checked.{' '}
        <Link to={searchHref} className={linkClass}>
          Search for it
        </Link>
      </p>
    );
  }
  const services = new Map<number, string>();
  for (const hit of search.data.items) services.set(hit.service.id, hit.service.name);
  services.delete(serviceId);
  const others = search.data.truncated
    ? Math.max(0, search.data.total_services - 1)
    : services.size;
  const names = [...services.entries()].slice(0, 4);
  return (
    <div className="rounded-inner border border-line bg-sunken px-4 py-3">
      <p className="text-body-s text-text">
        {others === 0 ? (
          'No other service has it in its current scans.'
        ) : (
          <>
            Affects <span className="font-semibold">{countOf(others, 'other service')}</span>
            {names.length > 0 ? ': ' : ''}
            {names.map(([id, name], index) => (
              <span key={id}>
                {index > 0 ? ', ' : ''}
                <Link
                  to={`/services/${id}/findings?${new URLSearchParams({ q: vulnId })}`}
                  className={linkClass}
                >
                  {name}
                </Link>
              </span>
            ))}
            {others > names.length ? ` and ${others - names.length} more` : ''}.
          </>
        )}
      </p>
      <p className="mt-1 text-small text-text-muted">
        Dashboard branches and protected refs.{' '}
        <Link to={searchHref} className={linkClass}>
          Open in search
        </Link>
      </p>
    </div>
  );
}

function Pipeline({ scanId }: { scanId: number }) {
  const scan = useScan(scanId);
  if (scan.isPending) return <Skeleton className="h-5 w-48" />;
  if (scan.isError) return <span className="text-text-muted">Not available</span>;
  const s = scan.data;
  return (
    <span className="flex flex-wrap gap-x-3 gap-y-1">
      <SafeExternal href={s.pipeline_url}>Pipeline #{s.gitlab_pipeline_id}</SafeExternal>
      <SafeExternal href={s.job_url}>Job #{s.gitlab_job_id}</SafeExternal>
      <Link to={`/scans/${s.id}`} className={linkClass}>
        Scan details
      </Link>
    </span>
  );
}

/**
 * Everything about one finding in a side sheet: what it is, how to fix it,
 * where else it is, and where it came from. Focus moves into the sheet,
 * Escape closes it, and focus returns to the row.
 */
export function VulnDrawer({
  serviceId,
  vuln,
  onClose,
}: {
  serviceId: number;
  vuln: Vulnerability;
  onClose: () => void;
}) {
  const sev = severityKey(vuln.severity);
  const fix = suggestedFix(vuln.installed_version, vuln.fixed_version);
  const command = fix ? upgradeCommand(vuln.pkg_type, vuln.pkg_name, fix) : null;
  const allFixes = vuln.fixed_version
    .split(',')
    .map((v) => v.trim())
    .filter(Boolean);

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      size="lg"
      title={
        <span className="inline-flex items-center gap-2">
          <span className="font-mono">{vuln.vuln_id}</span>
          <CopyIconButton text={vuln.vuln_id} label={`Copy ${vuln.vuln_id}`} />
        </span>
      }
      description={vuln.title || 'No title reported'}
      closeLabel="Close details"
    >
      <div
        data-severity={sev}
        className={`tone-${sev} sev-band -mx-5 -mt-4 flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-3 sm:-mx-6 sm:px-6`}
      >
        <span className="inline-flex items-center gap-2">
          <SeverityMark severity={sev} size={16} />
          <span className="tone-ink font-semibold">{SEVERITY_LABEL[sev]}</span>
        </span>
        <span className="text-small text-text-muted tabular-nums">
          {RISK_WEIGHTS[sev]} risk points
        </span>
        {vuln.suppressed ? (
          <span className="tag">Suppressed: {vuln.suppression || 'no reason recorded'}</span>
        ) : null}
        {safeExternalUrl(vuln.primary_url) ? (
          <span className="ml-auto text-small">
            <SafeExternal href={vuln.primary_url}>Advisory</SafeExternal>
          </span>
        ) : null}
      </div>

      <Block title="Fix">
        <div className="rounded-inner border border-line bg-sunken p-4">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2 font-mono text-body-s">
            <span className="text-text">{vuln.pkg_name}</span>
            <span className="rounded-tag border border-line px-2 py-0.5 text-text-muted">
              {vuln.installed_version || 'unknown version'}
            </span>
            <ArrowRight
              size={16}
              strokeWidth={1.75}
              aria-hidden="true"
              className="text-text-faint"
            />
            <span className="sr-only">fixed in</span>
            {fix ? (
              <span className="tone-ok rounded-tag border border-[color-mix(in_srgb,var(--ok)_40%,transparent)] bg-[color-mix(in_srgb,var(--ok)_12%,transparent)] px-2 py-0.5 tone-ink">
                {fix}
              </span>
            ) : (
              <span className="font-sans text-text-muted">No fixed version yet</span>
            )}
          </div>
          {allFixes.length > 1 ? (
            <p className="mt-2 text-small text-text-muted">
              Fixed in {allFixes.join(', ')}; {fix} is the lowest above the installed version.
            </p>
          ) : null}
          {command ? (
            <div className="mt-3 flex items-center gap-2 rounded-[8px] border border-line bg-surface px-3 py-2">
              <code
                className="min-w-0 flex-1 font-mono text-small break-all text-text"
                title={command}
              >
                {command}
              </code>
              <CopyIconButton text={command} label="Copy upgrade command" />
            </div>
          ) : null}
          {!fix ? (
            <p className="mt-2 text-small text-text-muted">
              Watch the advisory, or remove or replace the package.
            </p>
          ) : null}
        </div>
      </Block>

      <Block title="Elsewhere">
        <Elsewhere vulnId={vuln.vuln_id} serviceId={serviceId} />
      </Block>

      <Block title="Details">
        <dl>
          <Fact label="Image">{vuln.artifact_name}</Fact>
          <Fact label="Target">
            <Mono value={vuln.target} />
          </Fact>
          {vuln.pkg_path ? (
            <Fact label="Package path">
              <Mono value={vuln.pkg_path} />
            </Fact>
          ) : null}
          <Fact label="Package type">{vuln.pkg_type || '–'}</Fact>
          <Fact label="Class">{vuln.class || '–'}</Fact>
          <Fact label="Status">{vuln.status || '–'}</Fact>
          <Fact label="First seen">
            <time dateTime={vuln.first_seen_at} title={formatExactTime(vuln.first_seen_at)}>
              {relativeTime(vuln.first_seen_at)}
            </time>{' '}
            <span className="text-text-muted">({formatExactTime(vuln.first_seen_at)})</span>
          </Fact>
          <Fact label="Found by">
            <Pipeline scanId={vuln.scan_id} />
          </Fact>
        </dl>
      </Block>
    </Sheet>
  );
}
