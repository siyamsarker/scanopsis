// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { Download } from 'lucide-react';
import { lazy, Suspense, useEffect, useRef, useState, type FormEvent } from 'react';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { meKey } from '../api/hooks';
import { StepGuard } from '../auth/StepGuard';
import { AuthShell } from '../components/AuthShell';
import { CopyButton } from '../components/CopyButton';
import { ErrorNote } from '../components/ErrorNote';
import { Field } from '../components/Field';
import { SignOutButton } from '../components/SignOutButton';
import { monoInputClass, primaryButtonClass, secondaryButtonClass } from '../ui';

// The QR library is only needed here, so it loads on demand.
const QrCode = lazy(() => import('../components/QrCode'));

interface Enrollment {
  secret: string;
  otpauth_uri: string;
}

function QrPlaceholder() {
  return <div className="aspect-square w-full max-w-52 rounded-control bg-surface-raised" />;
}

function ScanStep({ enrollment, onNext }: { enrollment: Enrollment; onNext: () => void }) {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col items-start gap-6 sm:flex-row">
        <Suspense fallback={<QrPlaceholder />}>
          <QrCode value={enrollment.otpauth_uri} label="QR code for your authenticator app" />
        </Suspense>
        <div className="flex min-w-0 flex-col gap-2">
          <p className="text-text-muted">Cannot scan the code? Enter this secret in the app.</p>
          <p
            aria-label="Secret"
            className="break-all rounded-control border border-border bg-bg px-3 py-2 font-mono text-text"
          >
            {enrollment.secret}
          </p>
          <div>
            <CopyButton text={enrollment.secret} label="Copy secret" />
          </div>
        </div>
      </div>
      <button type="button" className={primaryButtonClass} onClick={onNext}>
        Next
      </button>
    </div>
  );
}

function CodeStep({ onConfirmed }: { onConfirmed: (codes: string[]) => void }) {
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setPending(true);
    setError(null);
    try {
      const result = unwrap(
        await api.POST('/api/v1/auth/mfa/enrollment/confirm', {
          body: { code: code.replace(/\s/g, '') },
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
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
      <Field
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
      {error && <ErrorNote>{error}</ErrorNote>}
      <button type="submit" disabled={pending} className={primaryButtonClass}>
        Confirm
      </button>
    </form>
  );
}

function RecoveryStep({ codes, onContinue }: { codes: string[]; onContinue: () => Promise<void> }) {
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  const text = codes.join('\n');

  function download() {
    const blob = new Blob([`Scanopsis recovery codes\n\n${text}\n`], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'scanopsis-recovery-codes.txt';
    link.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="flex flex-col gap-6">
      <ul
        aria-label="Recovery codes"
        className="grid grid-cols-1 gap-2 rounded-control border border-border bg-bg p-4 font-mono text-text sm:grid-cols-2"
      >
        {codes.map((code) => (
          <li key={code}>{code}</li>
        ))}
      </ul>
      <div className="flex flex-wrap gap-2">
        <CopyButton text={text} label="Copy all" />
        <button type="button" className={secondaryButtonClass} onClick={download}>
          <Download size={16} strokeWidth={1.5} aria-hidden="true" />
          Download as text file
        </button>
      </div>
      <label className="flex items-start gap-3 text-text">
        <input
          type="checkbox"
          checked={saved}
          onChange={(e) => setSaved(e.target.checked)}
          className="mt-0.5 size-4 accent-brand"
        />
        I have saved these codes
      </label>
      <button
        type="button"
        disabled={!saved || busy}
        className={primaryButtonClass}
        onClick={() => {
          setBusy(true);
          void onContinue().finally(() => setBusy(false));
        }}
      >
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

  const titles = {
    scan: 'Set up two-factor sign-in',
    code: 'Enter the code from the app',
    recovery: 'Save your recovery codes',
  };
  const leads = {
    scan: 'Scan this QR code with an authenticator app such as Aegis, 2FAS or Google Authenticator.',
    code: 'The app shows a new 6-digit code every 30 seconds.',
    recovery:
      'Each code works once, in place of an app code. They are shown only now, so keep them somewhere safe.',
  };

  let body;
  if (step === 'recovery' && codes) {
    body = <RecoveryStep codes={codes} onContinue={finish} />;
  } else if (error) {
    body = (
      <div className="flex flex-col items-start gap-4">
        <ErrorNote>{error}</ErrorNote>
        <button type="button" className={secondaryButtonClass} onClick={() => void start()}>
          Try again
        </button>
      </div>
    );
  } else if (!enrollment) {
    body = (
      <div role="status" className="text-text-muted">
        <QrPlaceholder />
        <span className="sr-only">Preparing your secret…</span>
      </div>
    );
  } else if (step === 'scan') {
    body = <ScanStep enrollment={enrollment} onNext={() => setStep('code')} />;
  } else {
    body = (
      <CodeStep
        onConfirmed={(next) => {
          setCodes(next);
          setStep('recovery');
          // The secret is no longer needed on screen.
          setEnrollment(null);
        }}
      />
    );
  }

  return (
    <AuthShell title={titles[step]} lead={leads[step]} wide>
      {body}
      <p className="text-text-muted">
        <SignOutButton />
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
