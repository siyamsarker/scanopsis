// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useRef, type KeyboardEvent, type ReactNode } from 'react';
import { nextIndex } from './roving';

export interface SegmentedOption<T extends string> {
  value: T;
  label: ReactNode;
}

/**
 * A small single-choice switch (radio group): "Table | Treemap", "Comfortable |
 * Compact". One option is always selected. Tab enters the selected option and the
 * arrow keys, Home and End move and select.
 */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  label,
  className = '',
}: {
  options: SegmentedOption<T>[];
  value: T;
  onChange: (value: T) => void;
  /** Names the group for screen readers: "Row density". */
  label: string;
  className?: string;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const current = Math.max(
    0,
    options.findIndex((option) => option.value === value),
  );

  function onKeyDown(event: KeyboardEvent) {
    const next = nextIndex(event, current, options.length);
    if (next === null) return;
    event.preventDefault();
    onChange(options[next].value);
    refs.current[next]?.focus();
  }

  return (
    <div
      role="radiogroup"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={`inline-flex gap-0.5 rounded-inner border border-line bg-sunken p-0.5 ${className}`}
    >
      {options.map((option, index) => {
        const selected = index === current;
        return (
          <button
            key={option.value}
            ref={(el) => {
              refs.current[index] = el;
            }}
            type="button"
            role="radio"
            data-roving
            aria-checked={selected}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(option.value)}
            className={`inline-flex min-h-8 items-center justify-center gap-2 rounded-[8px] px-3 text-body-s font-medium transition-[background-color,color,box-shadow] duration-micro ease-enter pointer-coarse:min-h-10 [&_svg]:shrink-0 ${
              selected
                ? 'bg-surface-raised text-text shadow-[0_0_0_1px_var(--line),var(--elev-1)]'
                : 'text-text-muted hover:bg-wash hover:text-text'
            }`}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}
