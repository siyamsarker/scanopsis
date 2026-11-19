// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ChevronDown, Search, X } from 'lucide-react';
import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import { selectClass } from '../ui';

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

/**
 * A native select with a visible label (or a screen-reader-only one with
 * `hideLabel`). Native keeps keyboard, mobile pickers and accessibility intact.
 */
export function Select({
  label,
  hideLabel = false,
  value,
  onChange,
  options,
  disabled,
  className = '',
}: {
  label: string;
  hideLabel?: boolean;
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  disabled?: boolean;
  className?: string;
}) {
  const id = useId();
  return (
    <div className={`flex flex-col gap-1 ${className}`}>
      <label htmlFor={id} className={hideLabel ? 'sr-only' : 'text-sm font-medium text-text-muted'}>
        {label}
      </label>
      <span className="relative inline-flex">
        <select
          id={id}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
          className={`${selectClass} w-full appearance-none pr-9`}
        >
          {options.map((option) => (
            <option key={option.value} value={option.value} disabled={option.disabled}>
              {option.label}
            </option>
          ))}
        </select>
        <ChevronDown
          size={16}
          strokeWidth={1.5}
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-text-muted"
        />
      </span>
    </div>
  );
}

/**
 * A search box that reports typing after a pause (`delayMs`, 250 by default) so
 * lists do not refetch on every key. Enter reports at once, and the clear button
 * empties the box and reports immediately. `value` lets the parent reset it.
 */
export function SearchInput({
  label,
  value,
  defaultValue = '',
  onChange,
  delayMs = 250,
  placeholder,
  hideLabel = true,
  className = '',
}: {
  label: string;
  /** Controlled text, for resetting from outside. Changes typed here are reported via onChange. */
  value?: string;
  defaultValue?: string;
  onChange: (value: string) => void;
  delayMs?: number;
  placeholder?: string;
  hideLabel?: boolean;
  className?: string;
}) {
  const id = useId();
  const [text, setText] = useState(value ?? defaultValue);
  const timer = useRef<number | undefined>(undefined);
  const lastReported = useRef(value ?? defaultValue);

  useEffect(() => {
    if (value !== undefined && value !== lastReported.current) {
      lastReported.current = value;
      setText(value);
    }
  }, [value]);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  function report(next: string) {
    window.clearTimeout(timer.current);
    if (next === lastReported.current) return;
    lastReported.current = next;
    onChange(next);
  }

  function onType(next: string) {
    setText(next);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => report(next), delayMs);
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter') report(text);
    else if (event.key === 'Escape' && text) {
      setText('');
      report('');
    }
  }

  return (
    <div className={`flex flex-col gap-1 ${className}`}>
      <label htmlFor={id} className={hideLabel ? 'sr-only' : 'text-sm font-medium text-text-muted'}>
        {label}
      </label>
      <span className="relative inline-flex">
        <Search
          size={16}
          strokeWidth={1.5}
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-text-muted"
        />
        <input
          id={id}
          type="search"
          value={text}
          placeholder={placeholder}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => onType(event.target.value)}
          onKeyDown={onKeyDown}
          className="min-h-10 w-full appearance-none rounded-control border border-border-strong bg-bg py-2 pr-9 pl-9 text-text transition-colors duration-150 ease-out placeholder:text-text-muted hover:border-text-muted [&::-webkit-search-cancel-button]:hidden"
        />
        {text ? (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => {
              setText('');
              report('');
            }}
            className="absolute top-1/2 right-2 inline-flex size-6 -translate-y-1/2 items-center justify-center rounded text-text-muted hover:bg-surface-raised hover:text-text"
          >
            <X size={14} strokeWidth={1.5} aria-hidden="true" />
          </button>
        ) : null}
      </span>
    </div>
  );
}
