// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Check } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { useSonarIntegration } from '../../api/admin';
import { useMe, useSettings } from '../../api/hooks';
import { TargetLink } from '../../components/Freshness';
import { StateArt } from '../../components/illustrations';
import { CI_GUIDE_URL } from './ServiceCard';

export const SONARQUBE_GUIDE_URL =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md';

const linkClass =
  'relative font-medium text-text underline decoration-line-strong underline-offset-2 hover:decoration-text';

export interface SetupStep {
  title: string;
  body: ReactNode;
  /** True when done, false when not, undefined when the viewer cannot tell. */
  done?: boolean;
  optional?: boolean;
}

/** The first step that is not done is the one to do now. */
export function currentStep(steps: SetupStep[]): number {
  return steps.findIndex((s) => s.done !== true);
}

export function StepList({ steps }: { steps: SetupStep[] }) {
  const current = currentStep(steps);
  return (
    <ol className="w-full max-w-lg text-left">
      {steps.map((step, i) => {
        const state = step.done ? 'done' : i === current ? 'current' : 'todo';
        return (
          <li key={step.title} className="relative flex gap-4 pb-6 last:pb-0">
            {i < steps.length - 1 ? (
              <span
                aria-hidden="true"
                className={`absolute top-9 bottom-1 left-4 w-px ${step.done ? 'bg-ok' : 'bg-line'}`}
              />
            ) : null}
            <span
              aria-hidden="true"
              className={`relative flex size-8 shrink-0 items-center justify-center rounded-full text-body-s font-semibold tabular-nums ${
                state === 'done'
                  ? 'bg-ok text-bg'
                  : state === 'current'
                    ? 'lens-ring bg-surface-raised text-text'
                    : 'border border-line-strong bg-surface text-text-muted'
              }`}
            >
              {state === 'done' ? <Check size={16} strokeWidth={2.5} /> : i + 1}
            </span>
            <span className="min-w-0 pt-1">
              <span className="flex flex-wrap items-center gap-x-2 text-body font-medium text-text">
                <span className="sr-only">
                  Step {i + 1}
                  {step.done === true ? ', done' : step.done === false ? ', to do' : ''}:{' '}
                </span>
                {step.title}
                {step.optional ? (
                  <span className="text-caption font-normal text-text-faint">Optional</span>
                ) : null}
              </span>
              <span className="mt-1 block text-body-s text-text-muted">{step.body}</span>
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function hasValue(value: unknown) {
  return typeof value === 'string' ? value.trim() !== '' : value != null;
}

/**
 * A fresh install: no service has uploaded a scan yet. Lists what to set up,
 * in order, and for administrators which steps are already done.
 */
export function SetupSteps() {
  const me = useMe();
  const admin = me.data?.role === 'admin';
  const settings = useSettings(admin);
  const sonar = useSonarIntegration(admin);
  const gitlabUrl = settings.data?.find((s) => s.key === 'gitlab.url')?.value;

  const steps: SetupStep[] = [
    {
      title: 'Add the GitLab URL',
      done: admin && settings.data ? hasValue(gitlabUrl) : undefined,
      body: admin ? (
        <>
          Uploads are verified against it. Set it in{' '}
          <Link to="/settings" className={linkClass}>
            Settings
          </Link>
          .
        </>
      ) : (
        'An administrator sets it in Settings; uploads are verified against it.'
      ),
    },
    {
      title: 'Add the upload step to a pipeline',
      done: false,
      body: (
        <>
          The service appears here after its first Trivy upload.{' '}
          <TargetLink
            target={{ label: 'Read the GitLab CI guide', href: CI_GUIDE_URL }}
            className={linkClass}
          />
        </>
      ),
    },
    {
      title: 'Connect SonarQube',
      optional: true,
      done: admin && sonar.data ? sonar.data.configured : undefined,
      body: (
        <>
          Adds quality gates, ratings and coverage.{' '}
          {admin ? (
            <>
              <Link to="/admin/integrations" className={linkClass}>
                Open Integrations
              </Link>{' '}
              or{' '}
            </>
          ) : null}
          <TargetLink
            target={{ label: 'read the SonarQube guide', href: SONARQUBE_GUIDE_URL }}
            className={linkClass}
          />
        </>
      ),
    },
  ];
  const done = steps.filter((s) => s.done).length;

  return (
    <section
      aria-labelledby="setup-title"
      className="panel grid items-center gap-8 p-6 sm:p-10 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]"
    >
      <div className="flex flex-col items-start gap-3">
        <div className="w-40">
          <StateArt art="pipeline" />
        </div>
        <h2 id="setup-title" className="text-h2 font-semibold tracking-tight text-text">
          No services yet
        </h2>
        <p className="max-w-sm text-body-s text-text-muted">
          Services appear here after a pipeline uploads its first Trivy report. Three steps get you
          there.
        </p>
        {admin ? (
          <p className="text-body-s text-text-muted tabular-nums" role="status">
            {done} of {steps.length} done
          </p>
        ) : null}
      </div>
      <StepList steps={steps} />
    </section>
  );
}
