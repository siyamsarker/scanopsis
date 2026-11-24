// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Lock } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import type { Scan } from '../../api/scan';
import { ArtifactName } from '../../components/ArtifactName';
import { CopyIconButton } from '../../components/CopyIconButton';
import { Mono } from '../../components/Mono';
import { PageHeader } from '../../components/layout';
import { Skeleton } from '../../components/states';
import { NO_VALUE, formatExactTime, relativeTime, scannedName, shortSha } from '../../format';
import { buttonClass } from '../../ui';
import { SafeExternal } from '../findings/VulnDrawer';
import { findingsHref } from './Posture';

export function kindLabel(kind: Scan['kind']) {
  return kind === 'image' ? 'Image' : 'Secret scan of the source tree';
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0 border-line py-3 sm:border-l sm:pl-4 sm:first:border-l-0 sm:first:pl-0 lg:py-0">
      <dt className="text-small text-text-muted">{label}</dt>
      <dd className="mt-1 min-w-0 truncate text-body-s text-text">{children}</dd>
    </div>
  );
}

function When({ at }: { at: string }) {
  return (
    <time dateTime={at} title={formatExactTime(at)}>
      {relativeTime(at)}
    </time>
  );
}

export function ScanHeader({ scan, serviceName }: { scan: Scan; serviceName: string }) {
  // The image is named by key; the tag this scan was of follows the kind.
  const tagged = scannedName(scan);
  return (
    <>
      <PageHeader
        breadcrumb={[
          { label: 'Portfolio', to: '/' },
          { label: serviceName, to: `/services/${scan.service_id}` },
          { label: `Pipeline #${scan.gitlab_pipeline_id}` },
        ]}
        title={<ArtifactName artifact={scan} className="text-[0.9em]" />}
        subtitle={
          <>
            {kindLabel(scan.kind)}
            {tagged ? (
              <>
                {' '}
                <Mono value={tagged} className="text-text" />
              </>
            ) : null}{' '}
            scanned <When at={scan.scanned_at} /> on{' '}
            <span className="font-mono text-text">{scan.ref}</span>
            {scan.ref_protected ? ', a protected ref' : ''}
          </>
        }
        actions={
          <Link
            to={
              scan.kind === 'image'
                ? findingsHref(scan, '')
                : `/services/${scan.service_id}/findings?ref=${encodeURIComponent(scan.ref)}&tab=secrets`
            }
            className={buttonClass('secondary')}
          >
            Open current findings
          </Link>
        }
      />
      <dl className="mb-8 grid grid-cols-2 gap-x-4 border-y border-line py-1 sm:grid-cols-3 sm:gap-y-4 sm:py-4 lg:grid-cols-[repeat(6,auto)] lg:justify-between">
        <Fact label="Pipeline">
          <SafeExternal href={scan.pipeline_url}>#{scan.gitlab_pipeline_id}</SafeExternal>
        </Fact>
        <Fact label="Job">
          <SafeExternal href={scan.job_url}>#{scan.gitlab_job_id}</SafeExternal>
        </Fact>
        <Fact label="Commit">
          <span className="inline-flex max-w-full items-center gap-1">
            <SafeExternal href={scan.commit_url}>
              <span className="font-mono" title={scan.commit_sha}>
                {shortSha(scan.commit_sha)}
              </span>
            </SafeExternal>
            <CopyIconButton text={scan.commit_sha} label="Copy commit" />
          </span>
        </Fact>
        <Fact label="Ref">
          <span className="inline-flex max-w-full items-center gap-1.5">
            <span className="truncate font-mono">{scan.ref}</span>
            {scan.ref_protected ? (
              <span className="tag shrink-0" title="Protected ref">
                <Lock size={11} strokeWidth={2} aria-hidden="true" />
                protected
              </span>
            ) : null}
          </span>
        </Fact>
        <Fact label="Trivy">
          <span className="font-mono">{scan.trivy_version || NO_VALUE}</span>
        </Fact>
        <Fact label="Received">
          <When at={scan.received_at} />
        </Fact>
      </dl>
    </>
  );
}

export function ScanHeaderSkeleton() {
  return (
    <div aria-hidden="true">
      <div className="pb-8">
        <Skeleton className="h-5 w-64" />
        <Skeleton className="mt-2 h-8 w-80 max-w-full" />
        <Skeleton className="mt-2 h-5 w-72" />
      </div>
      <Skeleton className="mb-8 h-16 w-full" />
    </div>
  );
}
