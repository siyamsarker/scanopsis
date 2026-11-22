// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { KeyRound, Smartphone } from 'lucide-react';
import { useCallback, useRef, useState, type FormEvent } from 'react';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { meKey } from '../api/hooks';
import { StepGuard } from '../auth/StepGuard';
import { AuthShell } from '../components/AuthShell';
import { Spinner } from '../components/ConfirmDialog';
import { SignOutButton } from '../components/SignOutButton';
import { buttonClass, monoInputClass } from '../ui';
import { CodeField, AuthField } from './auth/fields';

function VerifyForm() {
  const queryClient = useQueryClient();
  const [recovery, setRecovery] = useState(false);
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const busy = useRef(false);

  const verify = useCallback(
    async (value: string, usingRecovery: boolean) => {
      if (busy.current) return;
      busy.current = true;
      setPending(true);
      setError(undefined);
      try {
        unwrap(await api.POST('/api/v1/auth/mfa/verify', { body: { code: value.trim() } }));
        // The step guard sends the user on once the session state says it is complete.
        await queryClient.invalidateQueries({ queryKey: meKey });
      } catch (err) {
        setError(
          err instanceof ApiError && err.code === 'mfa_locked'
            ? 'Too many wrong codes. Ask an administrator to reset two-factor authentication.'
            : err instanceof ApiError && err.code === 'invalid_code'
              ? usingRecovery
                ? 'That recovery code is not correct, or it was already used.'
                : 'That code is not correct. Wait for a new one in the app and try again.'
              : describeError(err),
        );
        setPending(false);
        busy.current = false;
      }
    },
    [queryClient],
  );

  function submit(event: FormEvent) {
    event.preventDefault();
    void verify(code, recovery);
  }

  function switchMode() {
    setRecovery(!recovery);
    setCode('');
    setError(undefined);
  }

  return (
    <AuthShell
      title={recovery ? 'Use a recovery code' : 'Enter your code'}
      lead={
        recovery
          ? 'Type one of the recovery codes you saved when you set up two-factor sign-in. Each works once.'
          : 'Open your authenticator app and type the 6-digit code it shows for Scanopsis.'
      }
    >
      <form onSubmit={submit} className="flex flex-col gap-5">
        {recovery ? (
          <AuthField
            key="recovery"
            id="code"
            label="Recovery code"
            type="text"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            required
            placeholder="xxxxx-xxxxx-xxxxx-xxxxx"
            value={code}
            onChange={(e) => {
              setCode(e.target.value);
              setError(undefined);
            }}
            error={error}
            className={`${monoInputClass} text-body`}
          />
        ) : (
          <CodeField
            key="totp"
            id="code"
            label="6-digit code"
            value={code}
            onChange={(v) => {
              setCode(v);
              setError(undefined);
            }}
            onComplete={(v) => void verify(v, false)}
            error={error}
          />
        )}
        <button
          type="submit"
          disabled={pending}
          aria-busy={pending || undefined}
          className={`${buttonClass('primary', 'lg')} w-full`}
        >
          {pending ? <Spinner /> : null}
          Verify
        </button>
      </form>
      <div className="flex flex-wrap items-center justify-between gap-3 text-body-s text-text-muted">
        <button type="button" className={buttonClass('ghost', 'sm')} onClick={switchMode}>
          {recovery ? (
            <Smartphone size={14} strokeWidth={1.75} aria-hidden="true" />
          ) : (
            <KeyRound size={14} strokeWidth={1.75} aria-hidden="true" />
          )}
          {recovery ? 'Use an authenticator code' : 'Use a recovery code'}
        </button>
        <SignOutButton />
      </div>
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
