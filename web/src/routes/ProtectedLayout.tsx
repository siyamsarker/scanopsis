// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { lazy, Suspense, useEffect, useState } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router';
import { useMe } from '../api/hooks';
import { nextStep, stepUrl } from '../auth/session';
import { ErrorState, PageLoader } from '../components/states';
import { PhoneBar } from '../components/shell/PhoneBar';
import { RouteTransition } from '../components/shell/RouteTransition';
import { useGlobalShortcuts } from '../components/shell/shortcuts';
import { ShortcutsDialog } from '../components/shell/ShortcutsDialog';
import { Sidebar, useSidebarCollapsed } from '../components/shell/Sidebar';
import { useSignOut } from '../components/shell/useSignOut';

// The palette (and cmdk) loads the first time it is opened.
const CommandPalette = lazy(() => import('../components/shell/CommandPalette'));

/** Empties the query cache once, so data of an ended session is never kept. */
function ClearCache() {
  const queryClient = useQueryClient();
  useEffect(() => {
    queryClient.clear();
  }, [queryClient]);
  return null;
}

/**
 * The application shell: sidebar (or the phone top bar), the page in one
 * content container, the command palette and the shortcuts. Renders only for
 * a complete session; otherwise it sends the person to the right step.
 */
export function ProtectedLayout() {
  const me = useMe();
  const location = useLocation();
  const { signOut, signingOut } = useSignOut();
  const [collapsed, setCollapsed] = useSidebarCollapsed();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [paletteLoaded, setPaletteLoaded] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);

  function openPalette() {
    setPaletteLoaded(true);
    setPaletteOpen(true);
  }

  useGlobalShortcuts({
    onPalette: () => {
      if (me.data) openPalette();
    },
    onShortcuts: () => {
      if (me.data) setShortcutsOpen(true);
    },
  });

  if (signingOut) return null;

  if (me.isPending) return <PageLoader label="Loading your workspace" fullScreen />;

  if (me.isError) {
    return (
      <main className="mx-auto flex min-h-dvh max-w-2xl items-center px-4">
        <ErrorState
          title="Your account could not be loaded"
          message="Scanopsis could not load your account. Check your connection and try again."
          onRetry={() => void me.refetch()}
          retrying={me.isRefetching}
          className="w-full"
        />
      </main>
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

  const showShortcuts = () => setShortcutsOpen(true);

  return (
    <div className="flex min-h-dvh">
      <a
        href="#main"
        className="fixed top-2 left-2 z-[70] -translate-y-20 rounded-inner bg-brand px-3 py-2 text-body-s font-medium text-brand-ink focus:translate-y-0"
      >
        Skip to content
      </a>
      <Sidebar
        user={user}
        collapsed={collapsed}
        onCollapsedChange={setCollapsed}
        onPalette={openPalette}
        onShortcuts={showShortcuts}
        onSignOut={signOut}
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <PhoneBar
          user={user}
          onPalette={openPalette}
          onShortcuts={showShortcuts}
          onSignOut={signOut}
        />
        <main
          id="main"
          tabIndex={-1}
          className="mx-auto w-full max-w-[1344px] flex-1 px-4 pt-6 pb-16 sm:px-6 sm:pt-8 lg:px-8 lg:pt-10"
        >
          <Suspense fallback={<PageLoader />}>
            <RouteTransition key={location.pathname}>
              <Outlet />
            </RouteTransition>
          </Suspense>
        </main>
      </div>
      {paletteLoaded ? (
        <Suspense fallback={null}>
          <CommandPalette
            open={paletteOpen}
            onOpenChange={setPaletteOpen}
            isAdmin={user.role === 'admin'}
            onShortcuts={showShortcuts}
            onSignOut={signOut}
          />
        </Suspense>
      ) : null}
      <ShortcutsDialog open={shortcutsOpen} onOpenChange={setShortcutsOpen} />
    </div>
  );
}
