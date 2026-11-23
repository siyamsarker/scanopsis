// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { lazy, Suspense } from 'react';
import { Route, Routes } from 'react-router';
import { useRestoreOverlay } from './api/restoreMode';
import { MotionRoot } from './components/shell/RouteTransition';
import { PageLoader } from './components/states';
import { Toaster } from './components/Toast';
import { LoginPage } from './routes/LoginPage';
import { NotFoundPage } from './routes/NotFoundPage';
import { PasswordPage } from './routes/PasswordPage';
import { ProtectedLayout } from './routes/ProtectedLayout';
import { SetupGate } from './routes/SetupGate';
import { SetupPage } from './routes/SetupPage';
import { UsersPage } from './routes/UsersPage';
import { VerifyPage } from './routes/VerifyPage';

// Rarely used pages, and the QR code library they need, load on demand.
const EnrollPage = lazy(() =>
  import('./routes/EnrollPage').then((m) => ({ default: m.EnrollPage })),
);
const SettingsPage = lazy(() =>
  import('./routes/SettingsPage').then((m) => ({ default: m.SettingsPage })),
);

// Screens load on demand so that the sign-in pages stay small.
const PortfolioPage = lazy(() =>
  import('./routes/PortfolioPage').then((m) => ({ default: m.PortfolioPage })),
);
const ServicePage = lazy(() =>
  import('./routes/ServicePage').then((m) => ({ default: m.ServicePage })),
);
const FindingsPage = lazy(() =>
  import('./routes/FindingsPage').then((m) => ({ default: m.FindingsPage })),
);
const ScanPage = lazy(() => import('./routes/ScanPage').then((m) => ({ default: m.ScanPage })));
const SearchPage = lazy(() =>
  import('./routes/SearchPage').then((m) => ({ default: m.SearchPage })),
);
const AdminServicesPage = lazy(() =>
  import('./routes/AdminServicesPage').then((m) => ({ default: m.AdminServicesPage })),
);
const IntegrationsPage = lazy(() =>
  import('./routes/IntegrationsPage').then((m) => ({ default: m.IntegrationsPage })),
);
const AuditPage = lazy(() => import('./routes/AuditPage').then((m) => ({ default: m.AuditPage })));
const BackupsPage = lazy(() =>
  import('./routes/admin/BackupsPage').then((m) => ({ default: m.BackupsPage })),
);
// The restore screen loads the first time a restore is seen.
const RestoreOverlayHost = lazy(() => import('./components/RestoreOverlay'));

/** Covers the whole application while a backup is restored. */
function RestoreScreen() {
  const shown = useRestoreOverlay();
  if (!shown) return null;
  return (
    <Suspense fallback={null}>
      <RestoreOverlayHost />
    </Suspense>
  );
}
const AccountPage = lazy(() =>
  import('./routes/AccountPage').then((m) => ({ default: m.AccountPage })),
);

// The design system showcase, in the development server only.
const Showcase = import.meta.env.DEV ? lazy(() => import('./components/Showcase')) : null;

export function App() {
  return (
    <MotionRoot>
      <Suspense fallback={<PageLoader fullScreen />}>
        <Routes>
          {Showcase ? <Route path="/_showcase" element={<Showcase />} /> : null}
          <Route element={<SetupGate />}>
            <Route path="/setup" element={<SetupPage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/2fa/setup" element={<EnrollPage />} />
            <Route path="/2fa" element={<VerifyPage />} />
            <Route path="/password" element={<PasswordPage />} />
            <Route element={<ProtectedLayout />}>
              <Route index element={<PortfolioPage />} />
              <Route path="services/:id" element={<ServicePage />} />
              <Route path="services/:id/findings" element={<FindingsPage />} />
              <Route path="scans/:id" element={<ScanPage />} />
              <Route path="search" element={<SearchPage />} />
              <Route path="admin/services" element={<AdminServicesPage />} />
              <Route path="admin/users" element={<UsersPage />} />
              <Route path="admin/integrations" element={<IntegrationsPage />} />
              <Route path="admin/audit" element={<AuditPage />} />
              <Route path="admin/backups" element={<BackupsPage />} />
              <Route path="settings" element={<SettingsPage />} />
              <Route path="account" element={<AccountPage />} />
              <Route path="*" element={<NotFoundPage />} />
            </Route>
          </Route>
        </Routes>
      </Suspense>
      <RestoreScreen />
      <Toaster />
    </MotionRoot>
  );
}
