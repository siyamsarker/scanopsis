// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Link } from 'react-router';
import { ApertureMark } from '../components/ApertureMark';
import { cardClass, headingClass, linkClass } from '../ui';

export function NotFoundPage() {
  return (
    <section className={`${cardClass} flex flex-col items-start gap-4 p-6 sm:p-8`}>
      <ApertureMark size={32} className="text-text-muted" />
      <p className="font-mono text-text-muted">404</p>
      <h1 className={headingClass}>Page not found</h1>
      <p className="max-w-md text-text-muted">
        The page you asked for does not exist. Check the address, or go back to the{' '}
        <Link to="/" className={linkClass}>
          home page
        </Link>
        .
      </p>
    </section>
  );
}
