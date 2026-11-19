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
import { linkClass, monoInputClass, primaryButtonClass } from '../ui';

function VerifyForm() {
  const queryClient = useQueryClient();
  const [recovery, setRecovery] = useState(false);
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setPending(true);
    setError(null);
    try {
      unwrap(await api.POST('/api/v1/auth/mfa/verify', { body: { code: code.trim() } }));
      // The step guard sends the user on once the session state says it is complete.
      await queryClient.invalidateQueries({ queryKey: meKey });
    } catch (err) {
      setError(
        err instanceof ApiError && err.code === 'invalid_code'
          ? recovery
            ? 'That recovery code is not correct, or it was already used.'
            : 'That code is not correct. Wait for a new one in the app and try again.'
          : describeError(err),
      );
      setPending(false);
    }
  }

  function switchMode() {
    setRecovery(!recovery);
    setCode('');
    setError(null);
  }

  return (
    <AuthShell
      title={recovery ? 'Use a recovery code' : 'Enter your code'}
      lead={
        recovery
          ? 'Enter one of the recovery codes you saved. Each works once.'
          : 'Open your authenticator app and enter the 6-digit code for Scanopsis.'
      }
    >
      <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
        {recovery ? (
          <Field
            key="recovery"
            id="code"
            label="Recovery code"
            type="text"
            autoComplete="off"
            spellCheck={false}
            required
            value={code}
            onChange={(e) => setCode(e.target.value)}
            className={monoInputClass}
          />
        ) : (
          <Field
            key="totp"
            id="code"
            label="6-digit code"
            type="text"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9 ]{6,7}"
            required
            value={code}
            onChange={(e) => setCode(e.target.value)}
            className={monoInputClass}
          />
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        <button type="submit" disabled={pending} className={primaryButtonClass}>
          Verify
        </button>
      </form>
      <p className="flex flex-wrap items-center justify-between gap-3 text-text-muted">
        <button type="button" className={linkClass} onClick={switchMode}>
          {recovery ? 'Use an authenticator code' : 'Use a recovery code'}
        </button>
        <span>
          <SignOutButton />
        </span>
      </p>
    </AuthShell>
  );
}

export function VerifyPage() {
  return (
    <StepGuard step="/2fa">
      <VerifyForm />
    </StepGuard>
  );
}
