// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ApertureMark } from '../components/ApertureMark';
import { cardClass, headingClass } from '../ui';

export function HomePage() {
  return (
    <>
      <h1 className={headingClass}>Services</h1>
      <section
        aria-label="Empty state"
        className={`${cardClass} mt-6 flex flex-col items-center px-6 py-12 text-center sm:py-16`}
      >
        <span className="flex size-16 items-center justify-center rounded-full border border-border bg-bg text-brand">
          <ApertureMark size={36} />
        </span>
        <p className="mt-6 max-w-md text-lg text-text">
          No services yet. Services appear here after the first pipeline uploads a scan.
        </p>
        <p className="mt-3 max-w-md text-text-muted">
          Results from <span className="font-mono text-text">SonarQube</span> and{' '}
          <span className="font-mono text-text">Trivy</span> show up as soon as a scan arrives.
        </p>
      </section>
    </>
  );
}
