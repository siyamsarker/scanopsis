// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { meKey } from '../api/hooks';
import { StepGuard } from '../auth/StepGuard';
import { AuthShell } from '../components/AuthShell';
import { ErrorNote } from '../components/ErrorNote';
import { Field } from '../components/Field';
import { SignOutButton } from '../components/SignOutButton';
import { primaryButtonClass } from '../ui';
import { passwordRules } from './SetupPage';

function PasswordForm() {
  const queryClient = useQueryClient();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [pending, setPending] = useState(false);
  const [mismatch, setMismatch] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    if (next !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    setPending(true);
    try {
      unwrap(
        await api.POST('/api/v1/me/password', {
          body: { current_password: current, new_password: next },
        }),
      );
      // The step guard continues to the original destination once the flag is cleared.
      await queryClient.invalidateQueries({ queryKey: meKey });
    } catch (err) {
      setError(
        err instanceof ApiError && err.code === 'invalid_credentials'
          ? 'The current password is not correct. Use the temporary password you were given.'
          : describeError(err),
      );
      setPending(false);
    }
  }

  return (
    <AuthShell
      title="Choose a new password"
      lead="You signed in with a temporary password. Choose your own to continue."
    >
      <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
        <Field
          id="current"
          label="Current password"
          type="password"
          autoComplete="current-password"
          required
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
        <Field
          id="new"
          label="New password"
          type="password"
          autoComplete="new-password"
          required
          minLength={12}
          hint={passwordRules}
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
        <Field
          id="confirm"
          label="Confirm new password"
          type="password"
          autoComplete="new-password"
          required
          error={mismatch ? 'The passwords do not match.' : undefined}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {error && <ErrorNote>{error}</ErrorNote>}
        <button type="submit" disabled={pending} className={primaryButtonClass}>
          Change password
        </button>
      </form>
      <p className="text-text-muted">
        <SignOutButton />
      </p>
    </AuthShell>
  );
}

export function PasswordPage() {
  return (
    <StepGuard step="/password">
      <PasswordForm />
    </StepGuard>
  );
}
