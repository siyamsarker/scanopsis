// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import * as RadixSelect from '@radix-ui/react-select';
import { Check, ChevronDown, Search, X } from 'lucide-react';
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { usePrimarySearch } from './shell/shortcuts';

export interface SelectOption {
  value: string;
  label: string;
  /** A second line or trailing text in the list only ("12 scans"). */
  hint?: string;
  disabled?: boolean;
}

// Radix Select reserves the empty string for "no value"; options may use it.
const EMPTY = '\u0000empty';
const toRadix = (value: string) => (value === '' ? EMPTY : value);
const fromRadix = (value: string) => (value === EMPTY ? '' : value);

const triggerClass =
  'group inline-flex min-h-10 w-full min-w-0 items-center justify-between gap-2 rounded-inner border border-line-strong bg-sunken pr-2.5 pl-3 text-left text-body-s text-text ' +
  'transition-[border-color,box-shadow] duration-micro ease-enter hover:border-text-faint ' +
  'data-[state=open]:border-brand data-disabled:cursor-not-allowed data-disabled:opacity-60 ' +
  'data-placeholder:text-text-faint pointer-coarse:min-h-11';

/**
 * A select with a visible label (or a screen-reader-only one with `hideLabel`).
 * The list is a styled listbox in a portal: arrow keys move, typing jumps to an
 * option, Enter or Space chooses, Escape closes and returns focus. The trigger
 * is a combobox named by the label.
 */
export function Select({
  label,
  hideLabel = false,
  value,
  onChange,
  options,
  disabled,
  placeholder,
  size = 'md',
  className = '',
  triggerClassName = '',
}: {
  label: string;
  hideLabel?: boolean;
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  disabled?: boolean;
  /** Shown when the value matches no option. */
  placeholder?: string;
  /** sm is 32px high (36 on touch), for table rows and toolbars. */
  size?: 'sm' | 'md';
  className?: string;
  triggerClassName?: string;
}) {
  const id = useId();
  const known = options.some((option) => option.value === value);
  return (
    <div className={`flex flex-col gap-1.5 ${className}`}>
      <label
        htmlFor={id}
        className={hideLabel ? 'sr-only' : 'text-body-s font-medium text-text-muted'}
      >
        {label}
      </label>
      <RadixSelect.Root
        value={known ? toRadix(value) : undefined}
        onValueChange={(next) => onChange(fromRadix(next))}
        disabled={disabled}
      >
        <RadixSelect.Trigger
          id={id}
          className={`${triggerClass} ${size === 'sm' ? 'min-h-8 text-body-s' : ''} ${triggerClassName}`}
        >
          <span className="min-w-0 truncate">
            <RadixSelect.Value placeholder={placeholder ?? 'Choose'} />
          </span>
          <RadixSelect.Icon asChild>
            <ChevronDown
              size={16}
              strokeWidth={1.75}
              aria-hidden="true"
              className="shrink-0 text-text-muted transition-transform duration-micro group-data-[state=open]:rotate-180"
            />
          </RadixSelect.Icon>
        </RadixSelect.Trigger>
        <RadixSelect.Portal>
          <RadixSelect.Content
            position="popper"
            sideOffset={6}
            collisionPadding={8}
            className="raised anim-pop z-50 max-h-[min(22rem,var(--radix-select-content-available-height))] min-w-(--radix-select-trigger-width) origin-(--radix-select-content-transform-origin) overflow-hidden"
          >
            <RadixSelect.Viewport className="p-1">
              {options.map((option) => (
                <RadixSelect.Item
                  key={option.value}
                  value={toRadix(option.value)}
                  disabled={option.disabled}
                  className="relative flex min-h-9 cursor-default items-center gap-2 rounded-[7px] pr-9 pl-2.5 text-body-s text-text outline-none select-none data-disabled:opacity-50 data-highlighted:bg-wash data-[state=checked]:font-medium pointer-coarse:min-h-11"
                >
                  <RadixSelect.ItemText>{option.label}</RadixSelect.ItemText>
                  {option.hint ? (
                    <span className="ml-auto text-caption text-text-faint">{option.hint}</span>
                  ) : null}
                  <RadixSelect.ItemIndicator className="absolute right-2.5 inline-flex">
                    <Check size={16} strokeWidth={2} aria-hidden="true" className="text-brand" />
                  </RadixSelect.ItemIndicator>
                </RadixSelect.Item>
              ))}
            </RadixSelect.Viewport>
          </RadixSelect.Content>
        </RadixSelect.Portal>
      </RadixSelect.Root>
    </div>
  );
}

/**
 * A search box that reports typing after a pause (`delayMs`, 250 by default) so
 * lists do not refetch on every key. Enter reports at once, and the clear button
 * empties the box and reports immediately. `value` lets the parent reset it.
 *
 * The first search box on a page is what the `/` shortcut focuses; pass
 * `primary` to choose a specific one when a page has several.
 */
export function SearchInput({
  label,
  value,
  defaultValue = '',
  onChange,
  delayMs = 250,
  placeholder,
  hideLabel = true,
  primary = false,
  shortcutHint = true,
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
  /** The page's main search: the `/` shortcut focuses this one. */
  primary?: boolean;
  /** Show the "/" key hint while the box is empty. */
  shortcutHint?: boolean;
  className?: string;
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(value ?? defaultValue);
  const timer = useRef<number | undefined>(undefined);
  const lastReported = useRef(value ?? defaultValue);
  usePrimarySearch(input, primary);

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
      event.stopPropagation();
      setText('');
      report('');
    }
  }

  return (
    <div className={`flex flex-col gap-1.5 ${className}`}>
      <label
        htmlFor={id}
        className={hideLabel ? 'sr-only' : 'text-body-s font-medium text-text-muted'}
      >
        {label}
      </label>
      <span className="group/search relative inline-flex">
        <Search
          size={16}
          strokeWidth={1.75}
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-text-muted"
        />
        <input
          ref={input}
          id={id}
          type="search"
          data-search-field=""
          value={text}
          placeholder={placeholder}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => onType(event.target.value)}
          onKeyDown={onKeyDown}
          className="min-h-10 w-full appearance-none rounded-inner border border-line-strong bg-sunken py-2 pr-9 pl-9 text-body-s text-text transition-[border-color,box-shadow] duration-micro ease-enter placeholder:text-text-faint hover:border-text-faint pointer-coarse:min-h-11 [&::-webkit-search-cancel-button]:hidden"
        />
        {text ? (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => {
              setText('');
              report('');
              input.current?.focus();
            }}
            className="absolute top-1/2 right-1.5 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-[7px] text-text-muted hover:bg-wash hover:text-text"
          >
            <X size={14} strokeWidth={1.75} aria-hidden="true" />
          </button>
        ) : shortcutHint ? (
          <Kbd className="pointer-events-none absolute top-1/2 right-2.5 hidden -translate-y-1/2 transition-opacity group-focus-within/search:opacity-0 sm:inline-flex pointer-coarse:hidden">
            /
          </Kbd>
        ) : null}
      </span>
    </div>
  );
}

/** A keyboard key, drawn as a small keycap. */
export function Kbd({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <kbd
      className={`inline-flex h-5 min-w-5 items-center justify-center rounded-[5px] border border-line bg-surface-raised px-1 font-sans text-caption font-medium text-text-muted shadow-[0_1px_0_var(--line)] ${className}`}
    >
      {children}
    </kbd>
  );
}
