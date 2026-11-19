// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';
import { Navigate, useLocation, useSearchParams } from 'react-router';
import { useMe } from '../api/hooks';
import { ErrorNote } from '../components/ErrorNote';
import { safeReturnTo } from './returnTo';
import { nextStep, stepUrl, type SessionStep } from './session';

/**
 * Renders a session step only while it is the step the session is at. Other
 * states are sent where they belong. hold keeps the page in place, for the
 * moment after the last step when the codes are still on screen.
 */
export function StepGuard({
  step,
  hold = false,
  children,
}: {
  step: SessionStep;
  hold?: boolean;
  children: ReactNode;
}) {
  const me = useMe();
  const location = useLocation();
  const [params] = useSearchParams();
  const returnTo = safeReturnTo(params.get('return_to'));

  if (hold && me.data) return children;
  if (me.isPending) {
    return (
      <p role="status" className="p-6 text-text-muted">
        Loading…
      </p>
    );
  }
  if (me.isError) {
    return (
      <div className="p-6">
        <ErrorNote>Scanopsis could not load your account. Reload the page to try again.</ErrorNote>
      </div>
    );
  }
  if (me.data === null) {
    const back = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?return_to=${back}`} replace />;
  }
  const expected = nextStep(me.data);
  if (expected === step) return children;
  return <Navigate to={expected ? stepUrl(expected, returnTo) : returnTo} replace />;
}
