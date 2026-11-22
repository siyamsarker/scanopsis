// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { lazy, Suspense } from 'react';
import { Route, Routes } from 'react-router';
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
const AccountPage = lazy(() =>
  import('./routes/AccountPage').then((m) => ({ default: m.AccountPage })),
);

const loading = (
  <p role="status" className="p-6 text-text-muted">
    Loading…
  </p>
);

export function App() {
  return (
    <Suspense fallback={loading}>
      <Routes>
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
            <Route path="settings" element={<SettingsPage />} />
            <Route path="account" element={<AccountPage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Route>
      </Routes>
    </Suspense>
  );
}
