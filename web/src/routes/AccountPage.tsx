// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Monitor, Smartphone } from 'lucide-react';
import { useRef, useState } from 'react';
import type { Me } from '../api/client';
import { describeError } from '../api/errors';
import {
  useEndOtherSessions,
  useEndSession,
  useMe,
  useMySessions,
  type MySession,
} from '../api/hooks';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { StatusPill, Tag } from '../components/badges';
import { PageHeader, Section } from '../components/layout';
import { ErrorState, Skeleton } from '../components/states';
import { toast } from '../components/Toast';
import { countOf } from '../format';
import { useTheme, type Theme } from '../theme';
import { buttonClass } from '../ui';
import { InitialsAvatar, When } from './admin/kit';

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

function Profile({ user }: { user: Me }) {
  const name = user.name || user.username;
  const facts = [
    {
      label: 'Signs in with',
      value: <Tag>{user.source === 'gitlab' ? 'GitLab' : 'Local password'}</Tag>,
    },
    {
      label: 'Role',
      value: (
        <span className="text-body-s font-medium text-text">
          {user.role === 'admin' ? 'Admin' : 'Viewer'}
        </span>
      ),
    },
    {
      label: 'Two-factor',
      value: user.mfa_enrolled ? (
        <StatusPill status="ok">On</StatusPill>
      ) : (
        <StatusPill status="warn">Not set up</StatusPill>
      ),
    },
    {
      label: 'Member since',
      value: <When at={user.created_at} className="text-body-s text-text" />,
    },
  ];
  return (
    <section
      aria-label="Profile"
      className="panel flex flex-col gap-6 p-5 sm:flex-row sm:items-center sm:p-6"
    >
      <div className="flex min-w-0 flex-1 items-center gap-4">
        <InitialsAvatar name={name} size="lg" ring={user.role === 'admin'} />
        <div className="min-w-0">
          <p className="truncate text-h3 font-semibold tracking-tight text-text">{name}</p>
          <p className="truncate text-body-s text-text-muted">
            {user.name ? <span className="font-mono">{user.username} </span> : null}
            {user.email ? <span>{user.email}</span> : null}
            {!user.name && !user.email
              ? user.source === 'gitlab'
                ? 'GitLab account'
                : 'Local account'
              : null}
          </p>
        </div>
      </div>
      <dl className="grid grid-cols-2 gap-x-8 gap-y-3 sm:flex sm:flex-wrap sm:justify-end">
        {facts.map((fact) => (
          <div key={fact.label} className="flex flex-col items-start gap-1">
            <dt className="text-caption text-text-muted">{fact.label}</dt>
            <dd>{fact.value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function SessionRow({
  session,
  onEnd,
}: {
  session: MySession;
  onEnd: (session: MySession, trigger: HTMLButtonElement) => void;
}) {
  const agent = describeAgent(session.user_agent);
  const Icon = agent.mobile ? Smartphone : Monitor;
  return (
    <li
      className={`flex flex-wrap items-center gap-x-4 gap-y-3 px-5 py-4 sm:px-6 ${
        session.current ? 'bg-[color-mix(in_srgb,var(--brand)_6%,transparent)]' : ''
      }`}
    >
      <span
        className={`flex size-10 shrink-0 items-center justify-center rounded-inner border ${
          session.current ? 'border-brand text-brand' : 'border-line bg-sunken text-text-muted'
        }`}
      >
        <Icon size={18} strokeWidth={1.75} aria-hidden="true" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2">
          <span className="font-medium text-text" title={session.user_agent || undefined}>
            {agent.label}
          </span>
          {session.current ? <span className="chip tone-brand">This browser</span> : null}
          {!session.mfa_verified ? (
            <StatusPill status="warn">Second factor not completed</StatusPill>
          ) : null}
        </p>
        <p className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-small text-text-muted">
          <span className="font-mono">{session.client_ip || 'Address not recorded'}</span>
          <When prefix="Signed in" at={session.created_at} />
          <When prefix="Last active" at={session.last_seen_at} />
        </p>
      </div>
      {!session.current ? (
        <button
          type="button"
          className={buttonClass('secondary', 'sm')}
          onClick={(e) => onEnd(session, e.currentTarget)}
        >
          End session{' '}
          <span className="sr-only">
            {[agent.label, session.client_ip].filter(Boolean).join(', ')}
          </span>
        </button>
      ) : null}
    </li>
  );
}

function Sessions() {
  const sessions = useMySessions();
  const end = useEndSession();
  const endOthers = useEndOtherSessions();
  const [ending, setEnding] = useState<MySession | null>(null);
  const [endingAll, setEndingAll] = useState(false);
  const trigger = useRef<HTMLElement | null>(null);
  const allButton = useRef<HTMLButtonElement>(null);

  const others = sessions.data?.filter((s) => !s.current).length ?? 0;

  return (
    <Section
      title="Sessions"
      description="Where you are signed in. A session ends when its browser closes, after a while unused, or at the latest a fixed time after sign-in."
      actions={
        <button
          ref={allButton}
          type="button"
          className={buttonClass('secondary')}
          disabled={others === 0 || endOthers.isPending}
          onClick={() => setEndingAll(true)}
        >
          End all other sessions
        </button>
      }
    >
      <div className="panel overflow-hidden">
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
          <ul aria-label="Sessions" className="divide-y divide-line">
            {sessions.data.map((s) => (
              <SessionRow
                key={s.id}
                session={s}
                onEnd={(session, button) => {
                  trigger.current = button;
                  setEnding(session);
                }}
              />
            ))}
          </ul>
        )}
      </div>
      {ending ? (
        <ConfirmDialog
          title="End this session?"
          confirmLabel="End session"
          tone="danger"
          pending={end.isPending}
          onConfirm={() =>
            end.mutate(ending.id, {
              onSuccess: () => {
                trigger.current = allButton.current;
                toast.success('Session ended', {
                  description: `${describeAgent(ending.user_agent).label} is signed out.`,
                });
                setEnding(null);
              },
              onError: (error) => {
                toast.error('The session could not be ended', {
                  description: describeError(error),
                });
                setEnding(null);
              },
            })
          }
          onCancel={() => setEnding(null)}
          returnFocusTo={trigger}
        >
          <p>
            {describeAgent(ending.user_agent).label}
            {ending.client_ip ? ` at ${ending.client_ip}` : ''} is signed out now and has to sign in
            again.
          </p>
        </ConfirmDialog>
      ) : null}
      {endingAll ? (
        <ConfirmDialog
          title="End all other sessions?"
          confirmLabel="End other sessions"
          tone="danger"
          pending={endOthers.isPending}
          onConfirm={() =>
            endOthers.mutate(undefined, {
              onSuccess: (n) => {
                setEndingAll(false);
                toast.success('Other sessions ended', {
                  description:
                    n === 0
                      ? 'There were no other sessions to end.'
                      : `${countOf(n, 'session')} signed out. This browser stays signed in.`,
                });
              },
              onError: (error) => {
                setEndingAll(false);
                toast.error('The sessions could not be ended', {
                  description: describeError(error),
                });
              },
            })
          }
          onCancel={() => setEndingAll(false)}
          returnFocusTo={allButton}
        >
          <p>
            Every other browser signed in to your account ({countOf(others, 'session')}) is signed
            out now. This browser stays signed in.
          </p>
        </ConfirmDialog>
      ) : null}
    </Section>
  );
}

/** A miniature of the app in one theme, drawn with that theme's own tokens. */
function ThemePreview({ scope }: { scope: 'light' | 'dark' }) {
  return (
    <span
      data-theme-scope={scope}
      aria-hidden="true"
      className="flex h-full w-full gap-1.5 bg-bg p-2"
    >
      <span className="flex w-1/4 flex-col gap-1 rounded-[4px] border border-line bg-surface p-1">
        <span className="h-1 w-3/4 rounded-full bg-[image:var(--lens)]" />
        <span className="h-1 w-full rounded-full bg-line" />
        <span className="h-1 w-2/3 rounded-full bg-line" />
      </span>
      <span className="flex flex-1 flex-col gap-1.5">
        <span className="h-1.5 w-1/2 rounded-full bg-text-muted" />
        <span className="flex flex-1 items-end gap-1 rounded-[4px] border border-line bg-surface p-1.5">
          {['h-[45%]', 'h-[70%]', 'h-[35%]', 'h-[85%]', 'h-[55%]'].map((h) => (
            <span key={h} className={`flex-1 rounded-[2px] bg-brand opacity-80 ${h}`} />
          ))}
        </span>
      </span>
    </span>
  );
}

const themeChoices: { value: Theme; label: string; hint: string }[] = [
  { value: 'system', label: 'System', hint: 'Follows your device' },
  { value: 'light', label: 'Light', hint: 'Always light' },
  { value: 'dark', label: 'Dark', hint: 'Always dark' },
];

function Appearance() {
  const [theme, setTheme] = useTheme();
  return (
    <Section title="Appearance" description="Saved in this browser.">
      <fieldset>
        <legend className="sr-only">Theme</legend>
        <div className="grid max-w-2xl grid-cols-3 gap-3 sm:gap-4">
          {themeChoices.map((choice) => {
            const checked = theme === choice.value;
            return (
              <label
                key={choice.value}
                className={`group relative flex cursor-pointer flex-col overflow-hidden rounded-panel border bg-surface transition-[border-color,box-shadow] duration-micro has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-brand ${
                  checked
                    ? 'border-brand shadow-[0_0_0_3px_color-mix(in_srgb,var(--brand)_18%,transparent)]'
                    : 'border-line hover:border-line-strong'
                }`}
              >
                <input
                  type="radio"
                  name="theme"
                  value={choice.value}
                  checked={checked}
                  onChange={() => setTheme(choice.value)}
                  className="sr-only"
                />
                <span className="flex aspect-[16/10] w-full border-b border-line">
                  {choice.value === 'system' ? (
                    <>
                      <span className="w-1/2 overflow-hidden">
                        <ThemePreview scope="light" />
                      </span>
                      <span className="w-1/2 overflow-hidden">
                        <ThemePreview scope="dark" />
                      </span>
                    </>
                  ) : (
                    <ThemePreview scope={choice.value} />
                  )}
                </span>
                <span className="flex items-center justify-between gap-2 px-3 py-2.5">
                  <span className="min-w-0">
                    <span className="block text-body-s font-medium text-text">{choice.label}</span>
                    <span className="hidden text-caption text-text-muted sm:block">
                      {choice.hint}
                    </span>
                  </span>
                  <span
                    aria-hidden="true"
                    className={`flex size-4 shrink-0 items-center justify-center rounded-full border ${
                      checked ? 'border-brand' : 'border-line-strong'
                    }`}
                  >
                    {checked ? <span className="size-2 rounded-full bg-brand" /> : null}
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>
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
        subtitle="Who you are signed in as, where you are signed in, and how Scanopsis looks for you."
      />
      {user ? <Profile user={user} /> : <Skeleton className="h-28 w-full" />}
      <div className="mt-10">
        <Sessions />
      </div>
      <Appearance />
    </>
  );
}
