// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Link } from 'react-router';
import { headingClass, linkClass } from '../ui';

export function NotFoundPage() {
  return (
    <>
      <h1 className={headingClass}>Page not found</h1>
      <p className="mt-4 text-text-muted">
        The page you asked for does not exist. Check the address, or go back to the{' '}
        <Link to="/" className={linkClass}>
          home page
        </Link>
        .
      </p>
    </>
  );
}
