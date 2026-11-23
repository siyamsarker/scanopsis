// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import * as RadixDialog from '@radix-ui/react-dialog';
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import { fetchRestoreStatus } from '../api/backups';
import { ApiError, type Me } from '../api/client';
import { meKey, setupKey } from '../api/hooks';
import { hideRestoreOverlay } from '../api/restoreMode';
import { overlayView, type OverlayView } from '../routes/admin/backups/model';
import { buttonClass } from '../ui';
import { Wordmark } from './ApertureMark';
import { StatusMark } from './SeverityMark';
import { LensSpinner } from './states';

/** The heading of each view; it also names the screen for screen readers. */
const titles: Record<OverlayView['kind'] | 'waiting', string> = {
  waiting: 'Restoring a backup',
  running: 'Restoring a backup',
  done: 'Restore finished',
  failed: 'The restore did not finish',
  restart: 'Restart Scanopsis to finish the restore',
  idle: 'No restore is running',
};

/** A thin bar of the restore's progress; the width is set through the style object. */
function Progress({ percent }: { percent: number }) {
  const value = Math.max(0, Math.min(100, Math.round(percent)));
  return (
    <div className="mt-6 w-full">
      <div
        role="progressbar"
        aria-label="Restore progress"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={value}
        className="h-1.5 w-full overflow-hidden rounded-full bg-sunken shadow-[inset_0_0_0_1px_var(--line)]"
      >
        <div
          className="h-full rounded-full bg-brand transition-[width] duration-layout ease-enter"
          style={{ width: `${value}%` }}
        />
      </div>
      <p className="mt-2 text-right text-small text-text-muted tabular-nums">{value}%</p>
    </div>
  );
}

/**
 * The full-screen restore screen. It follows GET /api/v1/restore/status, the
 * one request the server answers while it restores, and shows the server's
 * own sentence for each state. Focus stays inside; Escape does not close it
 * while the restore runs.
 *
 * - done: "Go to sign-in" (`onSignIn`), since every session ended;
 * - failed before anything changed: the server's sentence, then `onBack` with `backLabel`;
 * - restored but waiting for a restart: the server's instruction, and no way
 *   back, because the server turns every request away until it restarts.
 */
export function RestoreOverlay({
  onSignIn,
  onBack,
  backLabel = 'Back to Backups',
  onReload = () => window.location.assign('/'),
  pollMs = 1000,
}: {
  onSignIn: () => void;
  /** Leave the screen after a restore that failed; without it no button is offered. */
  onBack?: () => void;
  backLabel?: string;
  /** When no restore runs any more (the server restarted). */
  onReload?: () => void;
  /** How often the status is read while the restore runs. */
  pollMs?: number;
}) {
  const [view, setView] = useState<OverlayView | null>(null);
  // Why the last status request failed: no answer, or an error answer.
  const [trouble, setTrouble] = useState<'network' | 'error' | null>(null);
  const headingId = useId();
  const action = useRef<HTMLButtonElement>(null);
  const content = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();
    async function poll() {
      try {
        const next = overlayView(await fetchRestoreStatus(controller.signal));
        if (stopped) return;
        setTrouble(null);
        setView(next);
        // A finished restore does not change again; a restart is waited for, slowly.
        if (next.kind === 'done' || next.kind === 'failed' || next.kind === 'idle') return;
        timer = setTimeout(() => void poll(), next.kind === 'running' ? pollMs : pollMs * 5);
      } catch (err) {
        if (stopped) return;
        // The server may be restarting: keep asking.
        setTrouble(err instanceof ApiError ? 'error' : 'network');
        timer = setTimeout(() => void poll(), pollMs * 2);
      }
    }
    void poll();
    return () => {
      stopped = true;
      controller.abort();
      clearTimeout(timer);
    };
  }, [pollMs]);

  const kind = view?.kind ?? 'waiting';
  // The way out, once there is one, takes the focus.
  useEffect(() => {
    action.current?.focus();
  }, [kind]);

  const button =
    kind === 'done' ? (
      <button ref={action} type="button" className={buttonClass('primary')} onClick={onSignIn}>
        Go to sign-in
      </button>
    ) : kind === 'failed' && onBack ? (
      <button ref={action} type="button" className={buttonClass('secondary')} onClick={onBack}>
        {backLabel}
      </button>
    ) : kind === 'idle' ? (
      <button ref={action} type="button" className={buttonClass('primary')} onClick={onReload}>
        Reload the page
      </button>
    ) : null;

  return (
    <RadixDialog.Root open>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className="fixed inset-0 z-[80] bg-bg" />
        <RadixDialog.Content
          ref={content}
          role="alertdialog"
          aria-labelledby={headingId}
          aria-describedby={undefined}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            content.current?.focus();
          }}
          onEscapeKeyDown={(event) => {
            event.preventDefault();
            if (kind === 'failed') onBack?.();
          }}
          className="fixed inset-0 z-[80] flex flex-col items-center overflow-y-auto bg-bg px-4 pt-[max(1.5rem,env(safe-area-inset-top))] pb-[max(1.5rem,env(safe-area-inset-bottom))] outline-none sm:px-6"
        >
          <Wordmark size={22} className="self-start sm:self-center" />
          <div className="flex w-full max-w-md flex-1 flex-col items-center justify-center py-10 text-center">
            <div className="flex size-20 items-center justify-center" aria-hidden="true">
              {kind === 'done' ? (
                <StatusMark status="ok" size={44} />
              ) : kind === 'failed' ? (
                <StatusMark status="fail" size={44} />
              ) : kind === 'restart' ? (
                <StatusMark status="warn" size={44} />
              ) : kind === 'idle' ? (
                <StatusMark status="none" size={44} />
              ) : (
                <LensSpinner size={72} />
              )}
            </div>
            <RadixDialog.Title asChild>
              <h2
                id={headingId}
                className="mt-6 text-h2 font-semibold tracking-tight text-balance text-text"
              >
                {titles[kind]}
              </h2>
            </RadixDialog.Title>
            <div aria-live="polite" className="mt-2 w-full text-body text-text-muted">
              {view === null ? <p>Checking the restore.</p> : null}
              {view?.kind === 'running' ? (
                <>
                  {/* The server's sentence, unless it only repeats the heading. */}
                  {view.message.replace(/\.$/, '') !== titles.running ? (
                    <p>{view.message}</p>
                  ) : null}
                  <p className="mt-1 text-body-s">
                    Keep this page open. It shows the result when the restore ends.
                  </p>
                </>
              ) : null}
              {view?.kind === 'done' ? <p>Sign in with an account from the backup.</p> : null}
              {view?.kind === 'failed' ? <p>{view.message}</p> : null}
              {view?.kind === 'restart' ? <p>{view.message}</p> : null}
              {view?.kind === 'idle' ? (
                <p>Scanopsis is not restoring a backup any more. Reload the page to go on.</p>
              ) : null}
              {trouble ? (
                <p className="mt-3 text-body-s">
                  {trouble === 'network'
                    ? 'Scanopsis is not answering right now. Trying again.'
                    : 'Scanopsis answered with an error. Trying again.'}
                </p>
              ) : null}
            </div>
            {view?.kind === 'running' ? <Progress percent={view.percent} /> : null}
            {button ? <div className="mt-8">{button}</div> : null}
          </div>
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  );
}

/**
 * The restore screen for the signed-in application: after a restore everyone
 * signs in again. A failed restore leads admins back to the Backups page and
 * everyone else to the portfolio. The role comes from the cache, since the
 * server answers no account request while it restores.
 */
export function RestoreOverlayHost() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const isAdmin = queryClient.getQueryData<Me | null>(meKey)?.role === 'admin';
  // A restore started on the setup screen leads back to it: no account exists yet.
  const onSetup = queryClient.getQueryData<{ required: boolean }>(setupKey)?.required === true;
  return (
    <RestoreOverlay
      backLabel={onSetup ? 'Back to setup' : isAdmin ? 'Back to Backups' : 'Back to the portfolio'}
      onSignIn={() => {
        hideRestoreOverlay();
        queryClient.clear();
        void navigate('/login', { replace: true });
      }}
      onBack={() => {
        hideRestoreOverlay();
        void queryClient.invalidateQueries();
        void navigate(onSetup ? '/setup' : isAdmin ? '/admin/backups' : '/');
      }}
    />
  );
}

export default RestoreOverlayHost;
