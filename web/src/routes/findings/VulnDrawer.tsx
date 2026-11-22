// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ExternalLink } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import type { Vulnerability } from '../../api/findings';
import { Mono } from '../../components/Mono';
import { SeverityBadge } from '../../components/Severity';
import { formatExactTime, relativeTime, safeExternalUrl } from '../../format';
import { Sheet } from '../../components/Dialog';

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="border-b border-line py-3 last:border-b-0">
      <dt className="text-caption text-text-faint">{label}</dt>
      <dd className="mt-0.5 min-w-0 text-body-s break-words text-text">{children}</dd>
    </div>
  );
}

/** The advisory link for a vulnerability: external for http(s) URLs, plain text otherwise. */
export function AdvisoryLink({ vuln }: { vuln: Vulnerability }) {
  const href = safeExternalUrl(vuln.primary_url);
  if (!href) return <Mono value={vuln.vuln_id} />;
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex max-w-full items-center gap-1 underline decoration-border-strong underline-offset-2 hover:decoration-text"
    >
      <Mono value={vuln.vuln_id} />
      <ExternalLink size={12} strokeWidth={1.5} aria-hidden="true" className="shrink-0" />
      <span className="sr-only">(opens in a new tab)</span>
    </a>
  );
}

/**
 * A side panel with every field of one finding, in a portal. Focus moves to
 * the close button on open, Tab stays inside, Escape or the scrim closes, and
 * focus returns to the control that opened it.
 */
export function VulnDrawer({ vuln, onClose }: { vuln: Vulnerability; onClose: () => void }) {
  const fixed = vuln.fixed_version.trim();
  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={<Mono value={vuln.vuln_id} />}
      description={vuln.title || 'No title reported'}
      closeLabel="Close details"
    >
      <dl>
        <Field label="Severity">
          <SeverityBadge severity={vuln.severity} />
        </Field>
        {vuln.suppressed ? (
          <Field label="Suppressed">
            {vuln.suppression ? vuln.suppression : 'Suppressed, no reason recorded'}
          </Field>
        ) : null}
        <Field label="Advisory">
          <AdvisoryLink vuln={vuln} />
        </Field>
        <Field label="Package">
          <Mono value={vuln.pkg_name} />
        </Field>
        <Field label="Package type">{vuln.pkg_type || '–'}</Field>
        {vuln.pkg_path ? (
          <Field label="Package path">
            <Mono value={vuln.pkg_path} />
          </Field>
        ) : null}
        <Field label="Installed version">
          <Mono value={vuln.installed_version} />
        </Field>
        <Field label="Fixed version">{fixed ? <Mono value={fixed} /> : <span>No fix</span>}</Field>
        <Field label="Status">{vuln.status || '–'}</Field>
        <Field label="Class">{vuln.class || '–'}</Field>
        <Field label="Target">
          <Mono value={vuln.target} />
        </Field>
        <Field label="Artifact">
          <Mono value={vuln.artifact_name} />
        </Field>
        <Field label="First seen">
          <time dateTime={vuln.first_seen_at} title={formatExactTime(vuln.first_seen_at)}>
            {relativeTime(vuln.first_seen_at)}
          </time>{' '}
          <span className="text-text-muted">({formatExactTime(vuln.first_seen_at)})</span>
        </Field>
        <Field label="Scan">
          <Link
            to={`/scans/${vuln.scan_id}`}
            className="underline decoration-border-strong underline-offset-2 hover:decoration-text"
          >
            Scan {vuln.scan_id}
          </Link>
        </Field>
      </dl>
    </Sheet>
  );
}
