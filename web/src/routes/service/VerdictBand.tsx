// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ArrowRight, ExternalLink, GitBranch } from 'lucide-react';
import { useSearchParams } from 'react-router';
import { Link } from 'react-router';
import type { Overview, Service, SonarAnalysis } from '../../api/service';
import { useScanLinks } from '../../api/service';
import { Select } from '../../components/inputs';
import { formatExactTime, relativeTime, safeExternalUrl } from '../../format';
import { buttonClass } from '../../ui';
import { RiskIris, type GateState } from '../../viz';
import { projectUrlFrom, findingsHref } from './links';
import { verdictSentence, verdictTone, type VerdictInput } from './model';
import { describeRefSource, refChoices } from './refSource';

const TONE_CLASS = {
  fail: 'tone-fail',
  warn: 'tone-high',
  ok: 'tone-ok',
  none: 'tone-neutral',
} as const;

function When({ at, children }: { at: string; children: string }) {
  return (
    <>
      {children}{' '}
      <time dateTime={at} title={formatExactTime(new Date(at))} className="text-text">
        {relativeTime(at)}
      </time>
    </>
  );
}

/**
 * The top of the service page, edge to edge: the risk iris with the gate
 * ring, the name, one sentence that says where the service stands, the
 * branch being shown and the ways out (findings, the pipeline, SonarQube).
 */
export function VerdictBand({
  service,
  overview,
  sonar,
  gate,
  verdict,
  animate,
}: {
  service: Service;
  overview?: Overview;
  sonar?: SonarAnalysis | null;
  gate?: GateState;
  verdict: VerdictInput;
  animate: boolean;
}) {
  const [params, setParams] = useSearchParams();
  const refParam = params.get('ref') || undefined;
  const effectiveRef = refParam ?? overview?.ref ?? undefined;
  const refs = refChoices(overview?.refs ?? [], effectiveRef);

  const latest = (overview?.artifacts ?? []).reduce<Overview['artifacts'][number] | undefined>(
    (best, scan) => (!best || scan.scanned_at > best.scanned_at ? scan : best),
    undefined,
  );
  const links = useScanLinks(latest?.id);
  const pipelineUrl = safeExternalUrl(links.data?.pipeline_url);
  const gitlab = projectUrlFrom(
    links.data?.pipeline_url ?? links.data?.commit_url ?? links.data?.job_url,
  );
  const sonarUrl = safeExternalUrl(sonar?.project_url);
  const tone = verdictTone(verdict);

  function selectRef(next: string) {
    const copy = new URLSearchParams(params);
    copy.set('ref', next);
    setParams(copy, { replace: true });
  }

  return (
    <section
      aria-labelledby="service-title"
      className={`${TONE_CLASS[tone]} relative -mx-4 mb-10 border-y border-[color-mix(in_srgb,var(--tone)_26%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_6%,var(--bg))] px-4 py-6 sm:-mx-6 sm:px-6 sm:py-8 lg:-mx-8 lg:px-8`}
      data-verdict={tone}
    >
      <div className="flex flex-col gap-6 md:flex-row md:items-center md:gap-8">
        <RiskIris
          counts={verdict.counts}
          gate={gate}
          size={168}
          name={service.name}
          caption="risk points"
          animate={animate}
          className="shrink-0 self-start md:self-center"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
            <div className="min-w-0">
              <h1
                id="service-title"
                className="text-h2 font-semibold tracking-tight text-balance text-text"
              >
                {service.name}
              </h1>
              <p className="mt-0.5 flex flex-wrap items-center gap-x-2 font-mono text-small text-text-muted">
                {service.gitlab_project_path}
                {gitlab ? (
                  <a
                    href={gitlab}
                    target="_blank"
                    rel="noopener noreferrer"
                    aria-label={`Open ${service.gitlab_project_path} in GitLab`}
                    className="inline-flex rounded-[4px] text-text-muted hover:text-text"
                  >
                    <ExternalLink size={14} strokeWidth={1.75} aria-hidden="true" />
                  </a>
                ) : null}
              </p>
            </div>
            {refs.length > 0 ? (
              <div className="flex flex-col items-start gap-1 sm:items-end">
                <Select
                  label="Branch"
                  hideLabel
                  size="sm"
                  value={effectiveRef ?? refs[0]}
                  onChange={selectRef}
                  options={refs.map((r) => ({ value: r, label: r }))}
                />
              </div>
            ) : null}
          </div>

          <p
            className="mt-4 max-w-[60ch] text-h3 font-medium tracking-tight text-balance text-text"
            data-testid="verdict"
          >
            {verdictSentence(verdict)}
          </p>

          <div className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-body-s text-text-muted">
            {overview?.ref ? (
              <p className="inline-flex items-center gap-1.5" data-testid="ref-source">
                <GitBranch size={14} strokeWidth={1.75} aria-hidden="true" />
                <span>
                  Showing <span className="font-mono text-text">{overview.ref}</span>,{' '}
                  {describeRefSource(overview.ref_source, overview.ref, service.default_ref)}
                </span>
              </p>
            ) : null}
            {overview?.data_at && latest ? (
              <p>
                <When at={overview.data_at}>Trivy scanned</When> in{' '}
                <Link
                  to={`/scans/${latest.id}`}
                  className="font-mono text-text underline decoration-line-strong underline-offset-2 hover:decoration-text"
                >
                  pipeline #{latest.gitlab_pipeline_id}
                </Link>
              </p>
            ) : null}
            {sonar ? (
              <p>
                <When at={sonar.analysed_at}>SonarQube analysed</When>
              </p>
            ) : null}
          </div>

          <div className="mt-5 flex flex-wrap gap-2">
            {latest ? (
              <Link to={findingsHref(service.id, effectiveRef)} className={buttonClass('primary')}>
                Explore findings
                <ArrowRight size={16} strokeWidth={1.75} aria-hidden="true" />
              </Link>
            ) : null}
            {pipelineUrl && latest ? (
              <a
                href={pipelineUrl}
                target="_blank"
                rel="noopener noreferrer"
                className={buttonClass('secondary')}
              >
                Open pipeline #{latest.gitlab_pipeline_id}
                <ExternalLink size={14} strokeWidth={1.75} aria-hidden="true" />
              </a>
            ) : null}
            {sonarUrl ? (
              <a
                href={sonarUrl}
                target="_blank"
                rel="noopener noreferrer"
                className={buttonClass('secondary')}
              >
                Open in SonarQube
                <ExternalLink size={14} strokeWidth={1.75} aria-hidden="true" />
              </a>
            ) : null}
          </div>
        </div>
      </div>
    </section>
  );
}
