// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ExternalLink, X } from 'lucide-react';
import { useEffect, useId, useRef, type KeyboardEvent, type ReactNode } from 'react';
import { Link } from 'react-router';
import type { Vulnerability } from '../../api/findings';
import { Mono } from '../../components/Mono';
import { SeverityBadge } from '../../components/Severity';
import { formatExactTime, relativeTime, safeExternalUrl } from '../../format';
import { cardClass } from '../../ui';

const FOCUSABLE = 'a[href], button:not([disabled]), input, select, textarea, [tabindex="0"]';

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="border-b border-border/70 py-3 last:border-b-0">
      <dt className="text-sm text-text-muted">{label}</dt>
      <dd className="mt-0.5 min-w-0 break-words text-text">{children}</dd>
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
 * A side panel with every field of one finding. Focus moves to the close
 * button on open, Tab stays inside, Escape or the backdrop closes, and focus
 * returns to the control that opened it.
 */
export function VulnDrawer({ vuln, onClose }: { vuln: Vulnerability; onClose: () => void }) {
  const titleId = useId();
  const panel = useRef<HTMLDivElement>(null);
  const close = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    close.current?.focus();
    return () => opener?.focus();
  }, []);

  function onKeyDown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      onClose();
    } else if (event.key === 'Tab') {
      const items = panel.current?.querySelectorAll<HTMLElement>(FOCUSABLE);
      if (!items || items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }
  }

  const fixed = vuln.fixed_version.trim();
  return (
    <div className="fixed inset-0 z-20 flex justify-end bg-black/60" onMouseDown={onClose}>
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
        onMouseDown={(event) => event.stopPropagation()}
        className={`${cardClass} rise h-full w-full max-w-lg overflow-y-auto rounded-r-none p-6`}
      >
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h2 id={titleId} className="text-lg font-semibold tracking-tight text-text">
              <Mono value={vuln.vuln_id} />
            </h2>
            <p className="mt-1 text-text-muted">{vuln.title || 'No title reported'}</p>
          </div>
          <button
            ref={close}
            type="button"
            aria-label="Close details"
            onClick={onClose}
            className="inline-flex size-8 shrink-0 items-center justify-center rounded text-text-muted hover:bg-surface-raised hover:text-text"
          >
            <X size={16} strokeWidth={1.5} aria-hidden="true" />
          </button>
        </div>
        <dl className="mt-4">
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
          <Field label="Fixed version">
            {fixed ? <Mono value={fixed} /> : <span>No fix</span>}
          </Field>
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
      </div>
    </div>
  );
}
