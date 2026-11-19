// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router';
import { useSetupStatus } from '../api/hooks';

/**
 * While no administrator exists, every route shows the setup page. A failed
 * status request does not block the app: the server still enforces everything.
 */
export function SetupGate() {
  const status = useSetupStatus();
  const location = useLocation();
  // The cache is emptied on sign-in; keep the last answer so the page does not flash.
  const [known, setKnown] = useState<boolean | undefined>(undefined);
  if (status.data && status.data.required !== known) setKnown(status.data.required);
  const required = status.data?.required ?? known;

  if (required === undefined && status.isPending) {
    return (
      <p role="status" className="p-6 text-text-muted">
        Loading…
      </p>
    );
  }
  const onSetup = location.pathname === '/setup';
  if (required === true && !onSetup) return <Navigate to="/setup" replace />;
  if (required === false && onSetup) return <Navigate to="/login" replace />;
  return <Outlet />;
}
