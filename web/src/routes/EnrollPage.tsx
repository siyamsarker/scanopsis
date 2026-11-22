// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Check, Copy, Download } from 'lucide-react';
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { useLocation } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { meKey, useMe } from '../api/hooks';
import { StepGuard } from '../auth/StepGuard';
import { AuthShell } from '../components/AuthShell';
import { Spinner } from '../components/ConfirmDialog';
import { SignOutButton } from '../components/SignOutButton';
import { Skeleton } from '../components/states';
import { buttonClass } from '../ui';
import { Problem } from './admin/kit';
import { CodeField, SecretGroups } from './auth/fields';
import { setupSteps } from './SetupPage';

// The QR library is only needed here, so it loads on demand.
const QrCode = lazy(() => import('../components/QrCode'));

interface Enrollment {
  secret: string;
  otpauth_uri: string;
}

function QrPlaceholder() {
  return <Skeleton className="aspect-square w-full max-w-52" />;
}

function ScanStep({ enrollment, onNext }: { enrollment: Enrollment; onNext: () => void }) {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-6 sm:flex-row sm:items-center">
        <div className="w-44 shrink-0 rounded-panel border border-line bg-surface p-3 shadow-1 sm:w-48">
          <Suspense fallback={<QrPlaceholder />}>
            <QrCode value={enrollment.otpauth_uri} label="QR code for your authenticator app" />
          </Suspense>
        </div>
        <ol className="flex flex-col gap-3 text-body-s text-text-muted">
          <li className="flex gap-2.5">
            <span className="flex size-5 shrink-0 items-center justify-center rounded-full border border-line-strong text-caption font-semibold text-text">
              1
            </span>
            Open an authenticator app, such as Aegis, 2FAS or Google Authenticator.
          </li>
          <li className="flex gap-2.5">
            <span className="flex size-5 shrink-0 items-center justify-center rounded-full border border-line-strong text-caption font-semibold text-text">
              2
            </span>
            Add an account and scan the code. The app then shows a new 6-digit code every 30
            seconds.
          </li>
        </ol>
      </div>
      <SecretGroups secret={enrollment.secret} />
      <button type="button" className={`${buttonClass('primary', 'lg')} w-full`} onClick={onNext}>
        Next
      </button>
    </div>
  );
}

function CodeStep({
  onConfirmed,
  onBack,
}: {
  onConfirmed: (codes: string[]) => void;
  onBack: () => void;
}) {
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const busy = useRef(false);

  const confirm = useCallback(
    async (value: string) => {
      if (busy.current) return;
      busy.current = true;
      setPending(true);
      setError(undefined);
      try {
        const result = unwrap(
          await api.POST('/api/v1/auth/mfa/enrollment/confirm', {
            body: { code: value.replace(/\s/g, '') },
          }),
        );
        onConfirmed(result.recovery_codes);
      } catch (err) {
        setError(
          err instanceof ApiError && err.code === 'invalid_code'
            ? 'That code is not correct. Wait for a new one in the app and try again.'
            : describeError(err),
        );
        setPending(false);
        busy.current = false;
      }
    },
    [onConfirmed],
  );

  function submit(event: FormEvent) {
    event.preventDefault();
    void confirm(code);
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-5">
      <CodeField
        id="code"
        label="6-digit code"
        value={code}
        onChange={(v) => {
          setCode(v);
          setError(undefined);
        }}
        onComplete={(v) => void confirm(v)}
        error={error}
      />
      <button
        type="submit"
        disabled={pending}
        aria-busy={pending || undefined}
        className={`${buttonClass('primary', 'lg')} w-full`}
      >
        {pending ? <Spinner /> : null}
        Confirm
      </button>
      <button type="button" className={`${buttonClass('ghost', 'sm')} self-start`} onClick={onBack}>
        <ArrowLeft size={14} strokeWidth={1.75} aria-hidden="true" />
        Back to the QR code
      </button>
    </form>
  );
}

function RecoverySheet({ codes, username }: { codes: string[]; username?: string }) {
  const [copied, setCopied] = useState(false);
  const text = codes.join('\n');
  const today = new Intl.DateTimeFormat('en-GB', { dateStyle: 'long' }).format(new Date());
  const host = window.location.host;

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }

  function download() {
    const lines = [
      'Scanopsis recovery codes',
      '',
      `Account: ${username ?? ''}`,
      `Server: ${host}`,
      `Created: ${today}`,
      '',
      'Each code works once, in place of a code from your authenticator app.',
      '',
      ...codes.map((code, i) => `${String(i + 1).padStart(2, ' ')}. ${code}`),
      '',
    ];
    const blob = new Blob([lines.join('\n')], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'scanopsis-recovery-codes.txt';
    link.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="overflow-hidden rounded-panel border border-line bg-surface shadow-2">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-dashed border-line-strong px-5 py-3">
        <p className="text-body-s font-semibold text-text">Recovery codes</p>
        <p className="text-caption text-text-muted">
          {username ? <span className="font-mono">{username}</span> : null}
          {username ? ' on ' : null}
          <span className="font-mono">{host}</span>, {today}
        </p>
      </div>
      <ol
        aria-label="Recovery codes"
        className="grid list-[decimal-leading-zero] grid-cols-1 gap-x-8 gap-y-2 py-4 pr-5 pl-12 font-mono text-body-s text-text marker:text-caption marker:text-text-faint sm:grid-cols-2 sm:text-body"
      >
        {codes.map((code) => (
          <li key={code} className="whitespace-nowrap tabular-nums">
            {code}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2 border-t border-line bg-sunken px-4 py-3 print:hidden">
        <button
          type="button"
          className={buttonClass('secondary', 'sm')}
          onClick={() => void copy()}
        >
          {copied ? (
            <Check size={14} strokeWidth={2} aria-hidden="true" />
          ) : (
            <Copy size={14} strokeWidth={1.75} aria-hidden="true" />
          )}
          {copied ? 'Copied' : 'Copy all'}
        </button>
        <button type="button" className={buttonClass('secondary', 'sm')} onClick={download}>
          <Download size={14} strokeWidth={1.75} aria-hidden="true" />
          Download as text file
        </button>
        <span role="status" className="sr-only">
          {copied ? 'Copied to the clipboard' : ''}
        </span>
      </div>
    </div>
  );
}

function RecoveryStep({ codes, onContinue }: { codes: string[]; onContinue: () => Promise<void> }) {
  const me = useMe();
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col gap-6">
      <RecoverySheet codes={codes} username={me.data?.username} />
      <label className="flex cursor-pointer items-start gap-3 text-body-s text-text print:hidden">
        <input
          type="checkbox"
          checked={saved}
          onChange={(e) => setSaved(e.target.checked)}
          className="mt-0.5 size-4 accent-(--brand)"
        />
        I have saved these codes
      </label>
      <button
        type="button"
        disabled={!saved || busy}
        aria-busy={busy || undefined}
        className={`${buttonClass('primary', 'lg')} w-full print:hidden`}
        onClick={() => {
          setBusy(true);
          void onContinue().finally(() => setBusy(false));
        }}
      >
        {busy ? <Spinner /> : null}
        Continue
      </button>
    </div>
  );
}

function Enroll({
  codes,
  setCodes,
}: {
  codes: string[] | null;
  setCodes: (codes: string[] | null) => void;
}) {
  const queryClient = useQueryClient();
  const location = useLocation();
  const fromSetup = (location.state as { fromSetup?: boolean } | null)?.fromSetup === true;
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [step, setStep] = useState<'scan' | 'code' | 'recovery'>('scan');
  const started = useRef(false);

  async function start() {
    setError(null);
    try {
      setEnrollment(unwrap(await api.POST('/api/v1/auth/mfa/enrollment')));
    } catch (err) {
      if (err instanceof ApiError && err.code === 'mfa_already_enrolled') {
        await queryClient.invalidateQueries({ queryKey: meKey });
        return;
      }
      setError(describeError(err));
    }
  }

  useEffect(() => {
    // One request per visit: starting again would replace the secret on screen.
    if (started.current) return;
    started.current = true;
    void start();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function finish() {
    // The codes stay on screen until the session state says enrolment is done.
    await queryClient.refetchQueries({ queryKey: meKey });
    setCodes(null);
  }

  const onConfirmed = useCallback(
    (next: string[]) => {
      setCodes(next);
      setStep('recovery');
      // The secret is no longer needed on screen.
      setEnrollment(null);
    },
    [setCodes],
  );

  const titles = {
    scan: 'Set up two-factor sign-in',
    code: 'Enter the code from the app',
    recovery: 'Save your recovery codes',
  };
  const leads = {
    scan: 'Every sign-in to Scanopsis asks for a code from an app on your phone, as well as your password or GitLab.',
    code: 'Type the 6-digit code the app shows for Scanopsis. It confirms as soon as six digits are in.',
    recovery:
      'If you lose your phone, each of these codes signs you in once. They are shown only now: download or copy them and keep them somewhere safe.',
  };
  const index = ['scan', 'code', 'recovery'].indexOf(step);
  const steps = fromSetup
    ? { labels: setupSteps, current: step === 'recovery' ? 2 : 1 }
    : { labels: ['Scan the code', 'Enter a code', 'Save recovery codes'], current: index };

  let body;
  if (step === 'recovery' && codes) {
    body = <RecoveryStep codes={codes} onContinue={finish} />;
  } else if (error) {
    body = (
      <div className="flex flex-col items-start gap-4">
        <Problem>{error}</Problem>
        <button type="button" className={buttonClass('secondary')} onClick={() => void start()}>
          Try again
        </button>
      </div>
    );
  } else if (!enrollment) {
    body = (
      <div role="status" className="flex gap-6">
        <QrPlaceholder />
        <span className="sr-only">Preparing your key…</span>
      </div>
    );
  } else if (step === 'scan') {
    body = <ScanStep enrollment={enrollment} onNext={() => setStep('code')} />;
  } else {
    body = <CodeStep onConfirmed={onConfirmed} onBack={() => setStep('scan')} />;
  }

  return (
    <AuthShell title={titles[step]} lead={leads[step]} wide steps={steps}>
      {body}
      <p className="text-body-s text-text-muted print:hidden">
        Not you, or not now? <SignOutButton />
      </p>
    </AuthShell>
  );
}

export function EnrollPage() {
  // Held here so the guard lets the codes stay on screen until they are acknowledged.
  const [codes, setCodes] = useState<string[] | null>(null);
  return (
    <StepGuard step="/2fa/setup" hold={codes !== null}>
      <Enroll codes={codes} setCodes={setCodes} />
    </StepGuard>
  );
}
