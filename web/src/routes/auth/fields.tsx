// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/*
 * Form pieces of the sign-in, setup and two-factor pages: a labelled field,
 * a password field that can show its text and warns about Caps Lock, a live
 * list of password rules, a one-time code field and a secret set in groups.
 */

import { Check, Circle, Copy, Eye, EyeOff } from 'lucide-react';
import {
  useEffect,
  useRef,
  useState,
  type InputHTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import { buttonClass, inputClass, monoInputClass } from '../../ui';
import { Problem } from '../admin/kit';

const labelClass = 'text-body-s font-medium text-text';

interface FieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  id: string;
  label: string;
  hint?: ReactNode;
  error?: string;
  /** Content at the right of the label row ("Forgot?" links, a show toggle). */
  aside?: ReactNode;
}

/** A labelled input with an optional hint and an error tied to it for assistive technology. */
export function AuthField({ id, label, hint, error, aside, className, ...input }: FieldProps) {
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={id} className={labelClass}>
          {label}
        </label>
        {aside}
      </div>
      <input
        id={id}
        name={id}
        aria-describedby={[hintId, errorId].filter(Boolean).join(' ') || undefined}
        aria-invalid={error ? true : undefined}
        className={`${className ?? inputClass} min-h-11`}
        {...input}
      />
      {hint ? (
        <div id={hintId} className="text-small text-text-muted">
          {hint}
        </div>
      ) : null}
      {error ? <Problem id={errorId}>{error}</Problem> : null}
    </div>
  );
}

/**
 * A password field with a show/hide toggle and a Caps Lock warning, the two
 * things that make most wrong-password attempts.
 */
export function PasswordField({
  id,
  label,
  hint,
  error,
  value,
  onChange,
  autoComplete,
  ...rest
}: Omit<FieldProps, 'type' | 'onChange' | 'value'> & {
  value: string;
  onChange: (value: string) => void;
}) {
  const [visible, setVisible] = useState(false);
  const [caps, setCaps] = useState(false);
  const capsId = `${id}-caps`;
  function onKey(event: KeyboardEvent<HTMLInputElement>) {
    setCaps(event.getModifierState?.('CapsLock') ?? false);
  }
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className={labelClass}>
        {label}
      </label>
      <span className="relative flex">
        <input
          id={id}
          name={id}
          type={visible ? 'text' : 'password'}
          autoComplete={autoComplete}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKey}
          onKeyUp={onKey}
          onBlur={() => setCaps(false)}
          spellCheck={false}
          aria-describedby={
            [hintId, errorId, caps ? capsId : undefined].filter(Boolean).join(' ') || undefined
          }
          aria-invalid={error ? true : undefined}
          className={`${inputClass} min-h-11 pr-11`}
          {...rest}
        />
        <button
          type="button"
          onClick={() => setVisible((v) => !v)}
          aria-pressed={visible}
          aria-label={visible ? 'Hide characters' : 'Show characters'}
          className="absolute top-1/2 right-1.5 inline-flex size-8 -translate-y-1/2 items-center justify-center rounded-[7px] text-text-muted hover:bg-wash hover:text-text"
        >
          {visible ? (
            <EyeOff size={16} strokeWidth={1.75} aria-hidden="true" />
          ) : (
            <Eye size={16} strokeWidth={1.75} aria-hidden="true" />
          )}
        </button>
      </span>
      {caps ? (
        <p id={capsId} className="tone-warn flex items-center gap-1.5 text-small text-text">
          <span aria-hidden="true" className="tone-bg size-1.5 rounded-full" />
          Caps Lock is on.
        </p>
      ) : null}
      {hint ? (
        <div id={hintId} className="text-small text-text-muted">
          {hint}
        </div>
      ) : null}
      {error ? <Problem id={errorId}>{error}</Problem> : null}
    </div>
  );
}

/** The password rules the server applies, checked as the person types. */
export function passwordChecks(password: string, username = '') {
  const trimmed = password.trim();
  return [
    { label: 'At least 12 characters', ok: password.length >= 12 },
    {
      label: 'Not your username',
      ok: trimmed !== '' && trimmed.toLowerCase() !== username.trim().toLowerCase(),
    },
    { label: 'Not one character repeated', ok: password.length > 0 && new Set(password).size > 1 },
  ];
}

export function PasswordRules({ password, username }: { password: string; username?: string }) {
  const checks = passwordChecks(password, username);
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1">
      {checks.map((check) => (
        <li
          key={check.label}
          className={`flex items-center gap-1.5 ${check.ok ? 'text-text' : 'text-text-muted'}`}
        >
          {check.ok ? (
            <Check size={14} strokeWidth={2.25} aria-hidden="true" className="tone-ok tone-fg" />
          ) : (
            <Circle
              size={10}
              strokeWidth={2}
              aria-hidden="true"
              className="mx-0.5 text-text-faint"
            />
          )}
          {check.label}
          <span className="sr-only">{check.ok ? ': met' : ': not yet'}</span>
        </li>
      ))}
    </ul>
  );
}

/**
 * A field for a 6-digit code from an authenticator app, set large in
 * monospace. Typing the sixth digit submits the form.
 */
export function CodeField({
  id,
  label,
  value,
  onChange,
  onComplete,
  error,
  disabled,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  /** Called with the code when six digits are in. */
  onComplete?: (code: string) => void;
  error?: string;
  disabled?: boolean;
}) {
  const input = useRef<HTMLInputElement>(null);
  const last = useRef('');
  useEffect(() => {
    const digits = value.replace(/\D/g, '');
    if (digits.length === 6 && digits !== last.current) {
      last.current = digits;
      onComplete?.(digits);
    }
    if (digits.length < 6) last.current = '';
  }, [value, onComplete]);
  // After a wrong code, select it so the next one replaces it as it is typed.
  useEffect(() => {
    if (error) input.current?.select();
  }, [error]);
  const errorId = error ? `${id}-error` : undefined;
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className={labelClass}>
        {label}
      </label>
      <input
        ref={input}
        id={id}
        name={id}
        type="text"
        inputMode="numeric"
        autoComplete="one-time-code"
        pattern="[0-9 ]{6,7}"
        maxLength={7}
        required
        disabled={disabled}
        value={value}
        placeholder="000000"
        onChange={(e) => onChange(e.target.value.replace(/[^\d ]/g, ''))}
        aria-invalid={error ? true : undefined}
        aria-describedby={errorId}
        className={`${monoInputClass} min-h-14 pl-[calc(0.75rem+0.45em)] text-center text-h2 tracking-[0.45em] placeholder:tracking-[0.45em] placeholder:text-line-strong`}
      />
      {error ? <Problem id={errorId}>{error}</Problem> : null}
    </div>
  );
}

/** A TOTP secret in groups of four for reading aloud or typing, with a copy button. */
export function SecretGroups({ secret }: { secret: string }) {
  const groups = secret.replace(/\s/g, '').match(/.{1,4}/g) ?? [];
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div className="flex flex-col gap-2">
      <p className="text-small text-text-muted">Cannot scan it? Type this key into the app:</p>
      <div className="flex flex-wrap items-center gap-2">
        <p
          aria-label="Secret"
          data-secret={secret}
          className="flex flex-wrap gap-x-2 gap-y-1 rounded-inner border border-line bg-sunken px-3 py-2 font-mono text-body text-text tabular-nums"
        >
          <span className="sr-only">{secret}</span>
          {groups.map((group, i) => (
            <span key={i} aria-hidden="true">
              {group}
            </span>
          ))}
        </p>
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
          {copied ? 'Copied' : 'Copy key'}
        </button>
        <span role="status" className="sr-only">
          {copied ? 'Copied to the clipboard' : ''}
        </span>
      </div>
    </div>
  );
}

/** A quiet rule with words in the middle: "or with a local account". */
export function OrDivider({ children }: { children: ReactNode }) {
  return (
    <p className="flex items-center gap-3 text-small text-text-muted">
      <span aria-hidden="true" className="h-px flex-1 bg-line" />
      {children}
      <span aria-hidden="true" className="h-px flex-1 bg-line" />
    </p>
  );
}
