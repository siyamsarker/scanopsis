// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { lazy, Suspense } from 'react';
import { Route, Routes } from 'react-router';
import { HomePage } from './routes/HomePage';
import { LoginPage } from './routes/LoginPage';
import { NotFoundPage } from './routes/NotFoundPage';
import { PasswordPage } from './routes/PasswordPage';
import { ProtectedLayout } from './routes/ProtectedLayout';
import { SetupGate } from './routes/SetupGate';
import { SetupPage } from './routes/SetupPage';
import { UsersPage } from './routes/UsersPage';
import { VerifyPage } from './routes/VerifyPage';

// The enrolment page and the QR code library it needs load on demand.
const EnrollPage = lazy(() =>
  import('./routes/EnrollPage').then((m) => ({ default: m.EnrollPage })),
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
            <Route index element={<HomePage />} />
            <Route path="admin/users" element={<UsersPage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Route>
      </Routes>
    </Suspense>
  );
}
