// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { LogIn } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { useAuthMethods, useMe } from '../api/hooks';
import { safeReturnTo } from '../auth/returnTo';
import { AuthShell } from '../components/AuthShell';
import { ErrorNote } from '../components/ErrorNote';
import { inputClass, primaryButtonClass } from '../ui';

const errorMessages: Record<string, string> = {
  access_denied: 'You cancelled the sign-in on GitLab.',
  group_not_allowed:
    'Your GitLab account is not in a group that may use Scanopsis. Ask an administrator to add your group.',
  login_expired: 'The sign-in took too long or was started in another tab. Try again.',
  login_failed:
    'Sign-in with GitLab failed. Try again, and contact an administrator if it keeps failing.',
};

function localLoginMessage(err: unknown): string {
  if (err instanceof ApiError && err.status === 401) return 'Wrong username or password.';
  return describeError(err);
}

function LocalForm({ returnTo }: { returnTo: string }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setPending(true);
    setError(null);
    try {
      unwrap(await api.POST('/api/v1/auth/local/login', { body: { username, password } }));
      // Nothing cached before sign-in may be shown to the new principal.
      queryClient.clear();
      void navigate(returnTo, { replace: true });
    } catch (err) {
      setError(localLoginMessage(err));
      setPending(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <label htmlFor="username" className="font-medium text-text">
          Username
        </label>
        <input
          id="username"
          name="username"
          type="text"
          autoComplete="username"
          required
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          className={inputClass}
        />
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor="password" className="font-medium text-text">
          Password
        </label>
        <input
          id="password"
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className={inputClass}
        />
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <button type="submit" disabled={pending} className={primaryButtonClass}>
        Sign in
      </button>
    </form>
  );
}

export function LoginPage() {
  const [params] = useSearchParams();
  const returnTo = safeReturnTo(params.get('return_to'));
  const me = useMe();
  const methods = useAuthMethods();

  if (me.data) return <Navigate to={returnTo} replace />;

  const errorCode = params.get('error');
  const urlError =
    errorCode && Object.hasOwn(errorMessages, errorCode) ? errorMessages[errorCode] : undefined;

  let body;
  if (me.isPending || methods.isPending) {
    body = (
      <p role="status" className="text-text-muted">
        Loading…
      </p>
    );
  } else if (methods.isError) {
    body = (
      <ErrorNote>
        Scanopsis could not load the sign-in options. Reload the page to try again.
      </ErrorNote>
    );
  } else if (!methods.data.gitlab && !methods.data.local) {
    body = (
      <p className="text-text-muted">
        No sign-in method is enabled on this server. Ask an administrator to configure GitLab or
        local sign-in.
      </p>
    );
  } else {
    body = (
      <div className="flex flex-col gap-6">
        {methods.data.gitlab && (
          <a
            href={`/api/v1/auth/gitlab/login?return_to=${encodeURIComponent(returnTo)}`}
            className={primaryButtonClass}
          >
            <LogIn size={16} strokeWidth={1.5} aria-hidden="true" />
            Sign in with GitLab
          </a>
        )}
        {methods.data.gitlab && methods.data.local && (
          <p className="text-center text-text-muted">or use a local account</p>
        )}
        {methods.data.local && <LocalForm returnTo={returnTo} />}
      </div>
    );
  }

  return (
    <AuthShell title="Sign in" lead="to see your SonarQube and Trivy results." focusHeading={false}>
      {urlError && <ErrorNote>{urlError}</ErrorNote>}
      <div className="min-h-24">{body}</div>
    </AuthShell>
  );
}
