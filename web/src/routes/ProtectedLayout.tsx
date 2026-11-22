// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { LogOut, UserRound } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link, Navigate, NavLink, Outlet, useLocation, useNavigate } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { useMe } from '../api/hooks';
import { nextStep, stepUrl } from '../auth/session';
import { Wordmark } from '../components/ApertureMark';
import { ThemeSwitch } from '../components/ThemeSwitch';
import { primaryButtonClass, secondaryButtonClass } from '../ui';

const navLinkClass = ({ isActive }: { isActive: boolean }) =>
  `rounded-control px-3 py-2 transition-colors duration-150 ease-out hover:bg-surface-raised ${
    isActive
      ? 'bg-surface-raised text-text shadow-[inset_0_-2px_0_var(--brand)]'
      : 'text-text-muted'
  }`;

/** Empties the query cache once, so data of an ended session is never kept. */
function ClearCache() {
  const queryClient = useQueryClient();
  useEffect(() => {
    queryClient.clear();
  }, [queryClient]);
  return null;
}

export function ProtectedLayout() {
  const me = useMe();
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [signingOut, setSigningOut] = useState(false);
  const [signOutError, setSignOutError] = useState<string | null>(null);

  if (signingOut) return null;

  if (me.isPending) {
    return (
      <p role="status" className="p-6 text-text-muted">
        Loading…
      </p>
    );
  }

  if (me.isError) {
    return (
      <div role="alert" className="mx-auto max-w-2xl p-6">
        <p className="text-text">
          Scanopsis could not load your account. Check your connection and try again.
        </p>
        <button
          type="button"
          className={`${primaryButtonClass} mt-4`}
          onClick={() => void me.refetch()}
        >
          Try again
        </button>
      </div>
    );
  }

  const user = me.data;
  if (user === null) {
    const returnTo = encodeURIComponent(location.pathname + location.search);
    return (
      <>
        <ClearCache />
        <Navigate to={`/login?return_to=${returnTo}`} replace />
      </>
    );
  }

  const step = nextStep(user);
  if (step) {
    // The session is not complete: no application page renders until it is.
    return <Navigate to={stepUrl(step, location.pathname + location.search)} replace />;
  }

  function finishSignOut() {
    setSigningOut(true);
    queryClient.clear();
    void navigate('/login', { replace: true });
  }

  async function signOut() {
    setSignOutError(null);
    try {
      unwrap(await api.POST('/api/v1/auth/logout'));
    } catch (err) {
      // 401 means the session is already gone, which is what signing out wants.
      if (err instanceof ApiError && err.status === 401) {
        finishSignOut();
        return;
      }
      setSignOutError('Signing out failed. Check your connection and try again.');
      return;
    }
    finishSignOut();
  }

  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-10 border-b border-border bg-surface/90 backdrop-blur">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2 sm:px-6">
          <Link to="/" className="rounded-control py-1 text-lg">
            <Wordmark />
          </Link>
          <nav
            aria-label="Main"
            className="order-last -mx-1 flex w-full gap-1 overflow-x-auto px-1 sm:order-none sm:w-auto sm:shrink-0"
          >
            <NavLink to="/" end className={navLinkClass}>
              Portfolio
            </NavLink>
            <NavLink to="/search" className={navLinkClass}>
              Search
            </NavLink>
            {user.role === 'admin' && (
              <>
                <NavLink to="/admin/services" className={navLinkClass}>
                  Services
                </NavLink>
                <NavLink to="/admin/users" className={navLinkClass}>
                  Users
                </NavLink>
                <NavLink to="/admin/integrations" className={navLinkClass}>
                  Integrations
                </NavLink>
                <NavLink to="/admin/audit" className={navLinkClass}>
                  Audit log
                </NavLink>
                <NavLink to="/settings" className={navLinkClass}>
                  Settings
                </NavLink>
              </>
            )}
          </nav>
          <div className="ml-auto flex items-center gap-2 sm:gap-3">
            <Link
              to="/account"
              title={`Signed in as ${user.name || user.username}`}
              aria-label={`Your account: ${user.name || user.username}, ${user.role === 'admin' ? 'Admin' : 'Viewer'}`}
              className="flex min-h-10 min-w-0 items-center gap-2 rounded-control px-2 text-text transition-colors duration-150 ease-out hover:bg-surface-raised"
            >
              <UserRound
                size={18}
                strokeWidth={1.5}
                aria-hidden="true"
                className="shrink-0 text-text-muted"
              />
              <span className="sr-only max-w-48 truncate xl:not-sr-only xl:block">
                {user.name || user.username}
              </span>
              <span className="hidden rounded-control border border-border px-1.5 py-0.5 text-xs text-text-muted sm:inline">
                {user.role === 'admin' ? 'Admin' : 'Viewer'}
              </span>
            </Link>
            <ThemeSwitch />
            <button type="button" className={secondaryButtonClass} onClick={() => void signOut()}>
              <LogOut size={16} strokeWidth={1.5} aria-hidden="true" />
              <span className="sr-only xl:not-sr-only">Sign out</span>
            </button>
          </div>
        </div>
        {signOutError && (
          <p role="alert" className="mx-auto max-w-7xl px-4 pb-3 text-text sm:px-6">
            {signOutError}
          </p>
        )}
      </header>
      <main className="rise mx-auto max-w-7xl px-4 py-8 sm:px-6 sm:py-10">
        <Outlet />
      </main>
    </div>
  );
}
