// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { meKey, useMe } from '../api/hooks';
import { StepGuard } from '../auth/StepGuard';
import { AuthShell } from '../components/AuthShell';
import { Spinner } from '../components/ConfirmDialog';
import { SignOutButton } from '../components/SignOutButton';
import { buttonClass } from '../ui';
import { Problem } from './admin/kit';
import { PasswordField, PasswordRules } from './auth/fields';

function PasswordForm() {
  const queryClient = useQueryClient();
  const me = useMe();
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
      lead="You signed in with a temporary password. Replace it with one only you know to continue."
    >
      <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
        <PasswordField
          id="current"
          label="Current password"
          autoComplete="current-password"
          required
          value={current}
          onChange={setCurrent}
          hint="The temporary password from your administrator."
        />
        <PasswordField
          id="new"
          label="New password"
          autoComplete="new-password"
          required
          minLength={12}
          value={next}
          onChange={setNext}
          hint={<PasswordRules password={next} username={me.data?.username} />}
        />
        <PasswordField
          id="confirm"
          label="Confirm new password"
          autoComplete="new-password"
          required
          value={confirm}
          onChange={(v) => {
            setConfirm(v);
            setMismatch(false);
          }}
          error={mismatch ? 'The passwords do not match.' : undefined}
        />
        {error ? <Problem>{error}</Problem> : null}
        <button
          type="submit"
          disabled={pending}
          aria-busy={pending || undefined}
          className={`${buttonClass('primary', 'lg')} mt-2 w-full`}
        >
          {pending ? <Spinner /> : null}
          Change password
        </button>
      </form>
      <p className="text-body-s text-text-muted">
        Not you? <SignOutButton />
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
