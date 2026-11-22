// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Monitor, ShieldAlert, Smartphone } from 'lucide-react';
import { useRef, useState } from 'react';
import { describeError } from '../api/errors';
import {
  useEndOtherSessions,
  useEndSession,
  useMe,
  useMySessions,
  type MySession,
} from '../api/hooks';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ErrorNote } from '../components/ErrorNote';
import { Card, PageHeader, Section } from '../components/layout';
import { ErrorState, Skeleton } from '../components/states';
import { secondaryButtonClass } from '../ui';
import { formatRelative } from './UsersPage';

const badgeClass = 'rounded-control border px-1.5 py-0.5 text-xs whitespace-nowrap';

/** A short name for a User-Agent header, such as "Firefox on Linux". */
export function describeAgent(ua: string): { label: string; mobile: boolean } {
  const browser = /Edg\//.test(ua)
    ? 'Edge'
    : /Firefox\/|FxiOS\//.test(ua)
      ? 'Firefox'
      : /OPR\//.test(ua)
        ? 'Opera'
        : /Chrome\/|CriOS\//.test(ua)
          ? 'Chrome'
          : /Safari\//.test(ua)
            ? 'Safari'
            : null;
  const os = /iPhone|iPad|iPod/.test(ua)
    ? 'iOS'
    : /Android/.test(ua)
      ? 'Android'
      : /Windows/.test(ua)
        ? 'Windows'
        : /CrOS/.test(ua)
          ? 'ChromeOS'
          : /Mac OS X|Macintosh/.test(ua)
            ? 'macOS'
            : /Linux/.test(ua)
              ? 'Linux'
              : null;
  const mobile = /Mobile|iPhone|Android/.test(ua);
  if (browser && os) return { label: `${browser} on ${os}`, mobile };
  if (browser ?? os) return { label: (browser ?? os)!, mobile };
  return { label: ua === '' ? 'Unknown browser' : ua.split(/[\s/]/)[0], mobile };
}

function When({ iso, prefix }: { iso: string; prefix: string }) {
  return (
    <span>
      {prefix}{' '}
      <time dateTime={iso} title={new Date(iso).toLocaleString()}>
        {formatRelative(iso)}
      </time>
    </span>
  );
}

function SessionRow({ session }: { session: MySession }) {
  const end = useEndSession();
  const agent = describeAgent(session.user_agent);
  const Icon = agent.mobile ? Smartphone : Monitor;
  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-3 border-t border-border px-6 py-4 first:border-t-0">
      <span className="flex size-10 shrink-0 items-center justify-center rounded-full border border-border bg-bg text-text-muted">
        <Icon size={18} strokeWidth={1.5} aria-hidden="true" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2 text-text">
          <span className="font-medium" title={session.user_agent || undefined}>
            {agent.label}
          </span>
          {session.current && (
            <span className={`${badgeClass} border-brand text-brand`}>This browser</span>
          )}
          {!session.mfa_verified && (
            <span className={`${badgeClass} inline-flex items-center gap-1 border-warn text-text`}>
              <ShieldAlert size={12} strokeWidth={1.5} aria-hidden="true" className="text-warn" />
              Second factor not completed
            </span>
          )}
        </p>
        <p className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-sm text-text-muted">
          <span className="font-mono text-[13px]">
            {session.client_ip || 'Address not recorded'}
          </span>
          <When prefix="Signed in" iso={session.created_at} />
          <When prefix="Last active" iso={session.last_seen_at} />
        </p>
        {end.isError && (
          <div className="mt-2">
            <ErrorNote>{describeError(end.error)}</ErrorNote>
          </div>
        )}
      </div>
      {!session.current && (
        <button
          type="button"
          className={secondaryButtonClass}
          disabled={end.isPending}
          onClick={() => end.mutate(session.id)}
        >
          End session{' '}
          <span className="sr-only">
            {[agent.label, session.client_ip].filter(Boolean).join(', ')}
          </span>
        </button>
      )}
    </li>
  );
}

function Sessions() {
  const sessions = useMySessions();
  const endOthers = useEndOtherSessions();
  const [confirming, setConfirming] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);

  const others = sessions.data?.filter((s) => !s.current).length ?? 0;
  let status = '';
  if (endOthers.isSuccess) {
    status =
      endOthers.data === 0
        ? 'There were no other sessions to end.'
        : `Ended ${endOthers.data} other ${endOthers.data === 1 ? 'session' : 'sessions'}.`;
  }

  return (
    <Section
      title="Sessions"
      description="Where you are signed in. A session ends when its browser closes, after a time without use, or at the latest a fixed time after sign-in."
      actions={
        <button
          ref={trigger}
          type="button"
          className={secondaryButtonClass}
          disabled={others === 0 || endOthers.isPending}
          onClick={() => setConfirming(true)}
        >
          End all other sessions
        </button>
      }
    >
      <div aria-live="polite" className="min-h-6 pb-2 text-text-muted">
        {endOthers.isError ? <ErrorNote>{describeError(endOthers.error)}</ErrorNote> : status}
      </div>
      <Card padded={false}>
        {sessions.isPending ? (
          <div role="status" aria-label="Loading sessions" className="flex flex-col gap-3 p-6">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : sessions.isError ? (
          <ErrorState
            compact
            title="Your sessions could not be loaded"
            message={describeError(sessions.error)}
            onRetry={() => void sessions.refetch()}
            retrying={sessions.isFetching}
          />
        ) : (
          <ul aria-label="Sessions">
            {sessions.data.map((s) => (
              <SessionRow key={s.id} session={s} />
            ))}
          </ul>
        )}
      </Card>
      {confirming && (
        <ConfirmDialog
          title="End all other sessions?"
          confirmLabel="End other sessions"
          pending={endOthers.isPending}
          onConfirm={() =>
            endOthers.mutate(undefined, {
              onSettled: () => setConfirming(false),
            })
          }
          onCancel={() => setConfirming(false)}
          returnFocusTo={trigger}
        >
          <p>
            Every other browser signed in to your account is signed out now. This browser stays
            signed in.
          </p>
        </ConfirmDialog>
      )}
    </Section>
  );
}

export function AccountPage() {
  const me = useMe();
  const user = me.data;
  return (
    <>
      <PageHeader
        title="Account"
        subtitle={
          user
            ? `${user.name || user.username} · ${user.source === 'gitlab' ? 'GitLab' : 'Local'} account · ${user.role === 'admin' ? 'Admin' : 'Viewer'}`
            : undefined
        }
      />
      <Sessions />
    </>
  );
}
