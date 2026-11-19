// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { LogOut } from 'lucide-react';
import { useState } from 'react';
import { Link, Navigate, NavLink, Outlet, useLocation, useNavigate } from 'react-router';
import { api, unwrap } from '../api/client';
import { meKey, useMe } from '../api/hooks';
import { primaryButtonClass, secondaryButtonClass } from '../ui';

const navLinkClass = ({ isActive }: { isActive: boolean }) =>
  `rounded-control px-3 py-2 transition-colors duration-150 ease-out hover:bg-surface-raised ${
    isActive ? 'bg-surface-raised text-text' : 'text-text-muted'
  }`;

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
    return <Navigate to={`/login?return_to=${returnTo}`} replace />;
  }

  async function signOut() {
    setSignOutError(null);
    try {
      unwrap(await api.POST('/api/v1/auth/logout'));
    } catch {
      setSignOutError('Signing out failed. Check your connection and try again.');
      return;
    }
    setSigningOut(true);
    queryClient.setQueryData(meKey, null);
    queryClient.removeQueries({ queryKey: ['users'] });
    void navigate('/login', { replace: true });
  }

  return (
    <div className="min-h-screen">
      <header className="border-b border-border bg-surface">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-6 gap-y-2 px-6 py-3">
          <Link to="/" className="text-lg font-semibold tracking-tight text-text">
            scanopsis
          </Link>
          <nav aria-label="Main" className="flex gap-1">
            <NavLink to="/" end className={navLinkClass}>
              Home
            </NavLink>
            {user.role === 'admin' && (
              <NavLink to="/admin/users" className={navLinkClass}>
                Users
              </NavLink>
            )}
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <span className="text-text">
              {user.name || user.username}
              <span className="ml-2 text-text-muted">
                {user.role === 'admin' ? 'Admin' : 'Viewer'}
              </span>
            </span>
            <button type="button" className={secondaryButtonClass} onClick={() => void signOut()}>
              <LogOut size={16} strokeWidth={1.5} aria-hidden="true" />
              Sign out
            </button>
          </div>
        </div>
        {signOutError && (
          <p role="alert" className="mx-auto max-w-6xl px-6 pb-3 text-text">
            {signOutError}
          </p>
        )}
      </header>
      <main className="mx-auto max-w-6xl px-6 py-8">
        <Outlet />
      </main>
    </div>
  );
}
