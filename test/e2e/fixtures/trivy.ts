// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Real Trivy reports, the numbers the tests expect from them, and the upload
// to Scanopsis with a CI ID token minted by the fake GitLab.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import type { APIRequestContext, APIResponse } from '@playwright/test';

export const publicUrl = 'http://localhost:18080';
export const gitlabUrl = 'http://fake-gitlab:19000';
// Where the tests reach the fake GitLab from the host.
const fakeUrl = 'http://localhost:19000';

export type SeverityName = 'critical' | 'high' | 'medium' | 'low' | 'unknown';
export type Counts = Record<SeverityName, number>;

interface RawVuln {
  VulnerabilityID: string;
  PkgName: string;
  InstalledVersion?: string;
  FixedVersion?: string;
  Severity: string;
  PrimaryURL?: string;
}
interface RawResult {
  Target: string;
  Vulnerabilities?: RawVuln[];
  Secrets?: { RuleID: string }[];
}
interface RawReport {
  CreatedAt: string;
  ArtifactName: string;
  Results: RawResult[];
}

export interface Finding {
  vulnId: string;
  pkg: string;
  target: string;
  installed: string;
  fixed: string;
  severity: SeverityName;
  url: string;
}

/** A fixture file and the numbers computed from its JSON. */
export interface Report {
  name: string;
  body: Buffer;
  /** CreatedAt of the report, in milliseconds since the epoch. */
  createdAt: number;
  artifact: string;
  /** The artifact the dashboards show: an image name without its tag and digest. */
  key: string;
  /** Unsuppressed vulnerabilities, the ones every dashboard number counts. */
  findings: Finding[];
  counts: Counts;
  fixable: number;
  secretRules: string[];
}

/** An image name without its digest and tag; a registry port is not a tag. */
function imageKey(name: string): string {
  const bare = name.split('@')[0];
  const slash = bare.lastIndexOf('/');
  const colon = bare.indexOf(':', slash + 1);
  return colon === -1 ? bare : bare.slice(0, colon);
}

function load(name: string): Report {
  const body = readFileSync(fileURLToPath(new URL(`./trivy/${name}.json`, import.meta.url)));
  const raw = JSON.parse(body.toString('utf8')) as RawReport;
  const findings: Finding[] = [];
  const secretRules: string[] = [];
  for (const result of raw.Results) {
    for (const v of result.Vulnerabilities ?? []) {
      findings.push({
        vulnId: v.VulnerabilityID,
        pkg: v.PkgName,
        target: result.Target,
        installed: v.InstalledVersion ?? '',
        fixed: v.FixedVersion ?? '',
        severity: v.Severity.toLowerCase() as SeverityName,
        url: v.PrimaryURL ?? '',
      });
    }
    for (const s of result.Secrets ?? []) secretRules.push(s.RuleID);
  }
  return {
    name,
    body,
    createdAt: Date.parse(raw.CreatedAt),
    artifact: raw.ArtifactName,
    key: imageKey(raw.ArtifactName),
    findings,
    counts: countBySeverity(findings),
    fixable: findings.filter((f) => f.fixed !== '').length,
    secretRules,
  };
}

export function countBySeverity(findings: Finding[]): Counts {
  const counts: Counts = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };
  for (const f of findings) counts[f.severity] += 1;
  return counts;
}

export const imageFull = load('image-full');
export const imageSuppressed = load('image-suppressed');
export const secretFs = load('secret-fs');

/** The key that identifies a finding between two scans. */
export const findingKey = (f: Finding) => `${f.vulnId}\u0000${f.pkg}\u0000${f.target}`;

/** How the dashboard words a severity summary: "2 critical, 44 high". */
export function summary(counts: Counts): string {
  return (['critical', 'high', 'medium', 'low', 'unknown'] as const)
    .filter((k) => counts[k] > 0)
    .map((k) => `${counts[k]} ${k}`)
    .join(', ');
}

export const sum = (counts: Counts) => Object.values(counts).reduce((a, b) => a + b, 0);

export interface Job {
  projectId: number;
  projectPath: string;
  pipelineId: number;
  jobId: number;
  audience?: string;
  ref?: string;
  /** Whether the ref is protected; true unless set. */
  refProtected?: boolean;
}

/** Asks the fake GitLab to sign a CI ID token for the job. */
export async function mintToken(request: APIRequestContext, job: Job): Promise<string> {
  const response = await request.post(`${fakeUrl}/test/ci-token`, {
    data: {
      aud: job.audience ?? publicUrl,
      project_id: String(job.projectId),
      project_path: job.projectPath,
      namespace_path: job.projectPath.split('/').slice(0, -1).join('/'),
      pipeline_id: String(job.pipelineId),
      job_id: String(job.jobId),
      ref: job.ref ?? 'main',
      ref_protected: String(job.refProtected ?? true),
      sha: '0123456789abcdef0123456789abcdef01234567',
    },
  });
  if (!response.ok()) throw new Error(`minting a token failed: ${response.status()}`);
  return ((await response.json()) as { token: string }).token;
}

export interface Uploaded {
  scan_id: number;
  service_id: number;
  service_created: boolean;
  replaced: boolean;
  counts: Counts;
  fixable: number;
  secrets: number;
  suppressed: number;
}

/** Posts a report to the upload endpoint; the caller checks the response. */
export async function upload(
  request: APIRequestContext,
  report: Report,
  job: Job,
  kind?: 'secret-fs',
): Promise<APIResponse> {
  const token = await mintToken(request, job);
  return request.post(`${publicUrl}/api/v1/ingest/trivy${kind ? `?kind=${kind}` : ''}`, {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data: report.body,
  });
}
