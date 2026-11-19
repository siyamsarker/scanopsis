// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { InputHTMLAttributes, ReactNode } from 'react';
import { inputClass } from '../ui';

interface FieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  id: string;
  label: string;
  hint?: ReactNode;
  error?: string;
}

/** A labelled input with an optional hint and a field-level error tied to it for assistive technology. */
export function Field({ id, label, hint, error, className, ...input }: FieldProps) {
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="font-medium text-text">
        {label}
      </label>
      <input
        id={id}
        name={id}
        aria-describedby={[hintId, errorId].filter(Boolean).join(' ') || undefined}
        aria-invalid={error ? true : undefined}
        className={className ?? inputClass}
        {...input}
      />
      {hint && (
        <p id={hintId} className="text-text-muted">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="text-text">
          {error}
        </p>
      )}
    </div>
  );
}
