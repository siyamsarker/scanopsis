// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { headingClass } from '../ui';

export function HomePage() {
  return (
    <>
      <h1 className={headingClass}>Services</h1>
      <p className="mt-4 text-text-muted">
        No services yet. Services appear here after the first pipeline uploads a scan.
      </p>
    </>
  );
}
