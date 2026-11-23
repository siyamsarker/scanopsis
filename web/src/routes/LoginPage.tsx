// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { useAuthMethods, useMe } from '../api/hooks';
import { safeReturnTo } from '../auth/returnTo';
import { AuthShell } from '../components/AuthShell';
import { Spinner } from '../components/ConfirmDialog';
import { Skeleton } from '../components/states';
import { buttonClass } from '../ui';
import { GitLabGlyph, Problem } from './admin/kit';
import { AuthField, OrDivider, PasswordField } from './auth/fields';

const errorMessages: Record<string, string> = {
  access_denied: 'You cancelled the sign-in on GitLab.',
  group_not_allowed:
    'Your GitLab account is not in a group that may use Scanopsis. Ask an administrator to add your group.',
  login_expired: 'The sign-in took too long or was started in another tab. Try again.',
  account_disabled: 'Your account is disabled. Ask an administrator to enable it.',
  login_failed:
    'Sign-in with GitLab failed. Try again, and contact an administrator if it keeps failing.',
};

function localLoginMessage(err: unknown): string {
  if (err instanceof ApiError && err.status === 401) return 'Wrong username or password.';
  return describeError(err);
}

/**
 * Username and password. When GitLab sign-in is offered too, GitLab is the
 * primary action and this form's button steps back to a secondary one.
 */
function LocalForm({ returnTo, primary }: { returnTo: string; primary: boolean }) {
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
      <AuthField
        id="username"
        label="Username"
        type="text"
        autoComplete="username"
        autoCapitalize="off"
        spellCheck={false}
        required
        value={username}
        onChange={(e) => setUsername(e.target.value)}
      />
      <PasswordField
        id="password"
        label="Password"
        autoComplete="current-password"
        required
        value={password}
        onChange={setPassword}
      />
      {error ? <Problem>{error}</Problem> : null}
      <button
        type="submit"
        disabled={pending}
        aria-busy={pending || undefined}
        className={`${buttonClass(primary ? 'primary' : 'secondary', 'lg')} w-full`}
      >
        {pending ? <Spinner /> : null}
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
      <div role="status" aria-label="Loading sign-in options" className="flex flex-col gap-3">
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (methods.isError) {
    body = (
      <Problem>Scanopsis could not load the sign-in options. Reload the page to try again.</Problem>
    );
  } else if (!methods.data.gitlab && !methods.data.local) {
    body = (
      <p className="text-body-s text-text-muted">
        No sign-in method is enabled on this server. Ask an administrator to configure GitLab or
        local sign-in.
      </p>
    );
  } else {
    body = (
      <div className="flex flex-col gap-6">
        {methods.data.gitlab ? (
          <a
            href={`/api/v1/auth/gitlab/login?return_to=${encodeURIComponent(returnTo)}`}
            className={`${buttonClass('primary', 'lg')} w-full`}
          >
            <GitLabGlyph size={18} />
            Sign in with GitLab
          </a>
        ) : null}
        {methods.data.gitlab && methods.data.local ? (
          <OrDivider>or with a local account</OrDivider>
        ) : null}
        {methods.data.local ? (
          <LocalForm returnTo={returnTo} primary={!methods.data.gitlab} />
        ) : null}
      </div>
    );
  }

  return (
    <AuthShell title="Sign in" lead="to see what every pipeline changed." focusHeading={false}>
      {urlError ? (
        <div className="tone-fail rounded-inner border border-[color-mix(in_srgb,var(--tone)_35%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_8%,var(--surface))] px-4 py-3">
          <Problem>{urlError}</Problem>
        </div>
      ) : null}
      <div className="min-h-24">{body}</div>
    </AuthShell>
  );
}
