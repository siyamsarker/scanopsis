// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { NO_VALUE, safeExternalUrl } from '../../format';
import type { Scan } from '../../api/scan';
import { Freshness } from '../../components/Freshness';
import { Mono } from '../../components/Mono';
import { Card, PageHeader } from '../../components/layout';
import { Skeleton } from '../../components/states';

export function kindLabel(kind: Scan['kind']) {
  return kind === 'image' ? 'Image' : 'Secret scan of the source tree';
}

function MaybeLink({ href, children }: { href: string | null; children: ReactNode }) {
  const safe = safeExternalUrl(href);
  if (!safe) return <span>{children}</span>;
  return (
    <a
      href={safe}
      target="_blank"
      rel="noopener noreferrer"
      className="underline decoration-border-strong underline-offset-2 hover:decoration-text"
    >
      {children}
    </a>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-sm text-text-muted">{label}</dt>
      <dd className="mt-0.5 min-w-0 break-words text-text">{children}</dd>
    </div>
  );
}

export function ScanHeader({ scan, serviceName }: { scan: Scan; serviceName: string }) {
  return (
    <>
      <PageHeader
        eyebrow={
          <nav aria-label="Breadcrumb">
            <ol className="flex flex-wrap items-center gap-x-2">
              <li>
                <Link to="/" className="hover:text-text">
                  Portfolio
                </Link>
              </li>
              <li aria-hidden="true">›</li>
              <li>
                <Link to={`/services/${scan.service_id}`} className="hover:text-text">
                  {serviceName}
                </Link>
              </li>
              <li aria-hidden="true">›</li>
              <li aria-current="page">Scan</li>
            </ol>
          </nav>
        }
        title={<Mono value={scan.artifact_name} className="text-[0.9em]" />}
        subtitle={kindLabel(scan.kind)}
      />
      <Card className="mb-8">
        <dl className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
          <Fact label="Pipeline">
            <MaybeLink href={scan.pipeline_url}>Pipeline #{scan.gitlab_pipeline_id}</MaybeLink>
          </Fact>
          <Fact label="Job">
            <MaybeLink href={scan.job_url}>Job #{scan.gitlab_job_id}</MaybeLink>
          </Fact>
          <Fact label="Commit">
            <MaybeLink href={scan.commit_url}>
              <Mono value={scan.commit_sha} kind="sha" />
            </MaybeLink>
          </Fact>
          <Fact label="Ref">
            <Mono value={scan.ref} />
            {scan.ref_protected ? <span className="chip ml-2">protected</span> : null}
          </Fact>
          <Fact label="Trivy version">
            <Mono value={scan.trivy_version || NO_VALUE} />
          </Fact>
          <Fact label="Scanned at">
            <Freshness at={scan.scanned_at} prefix="" className="text-base" />
          </Fact>
          <Fact label="Received at">
            <Freshness at={scan.received_at} prefix="" className="text-base" />
          </Fact>
        </dl>
      </Card>
    </>
  );
}

export function ScanHeaderSkeleton() {
  return (
    <div aria-hidden="true">
      <div className="pb-6">
        <Skeleton className="h-5 w-64" />
        <Skeleton className="mt-2 h-8 w-80 max-w-full" />
        <Skeleton className="mt-2 h-6 w-48" />
      </div>
      <Skeleton className="mb-8 h-40 w-full rounded-card" />
    </div>
  );
}
