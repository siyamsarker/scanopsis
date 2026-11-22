// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Check } from 'lucide-react';
import { formatInt } from '../format';
import { SeverityMark } from './SeverityMark';
import type { SeverityKey } from './severityModel';

export interface ChipOption {
  value: string;
  label: string;
  /** How many results this filter leaves; shown in tabular figures. */
  count?: number;
  /** A severity marker before the label, for severity facets. */
  severity?: SeverityKey;
}

/**
 * Toggle chips for facets ("Critical 12", "Fixable 340"). Each chip is a toggle
 * button (aria-pressed). A selected chip is "in focus": a lens-gradient ring, a
 * raised fill and a check mark, so the state never depends on colour alone. Multi-select by default; `single` makes selecting one clear
 * the others, and pressing the selected chip again clears it.
 */
export function FilterChips({
  options,
  selected,
  onChange,
  label,
  single = false,
  className = '',
}: {
  options: ChipOption[];
  selected: string[];
  onChange: (selected: string[]) => void;
  /** Names the group: "Filter by severity". */
  label: string;
  single?: boolean;
  className?: string;
}) {
  function toggle(value: string) {
    if (selected.includes(value)) onChange(selected.filter((item) => item !== value));
    else onChange(single ? [value] : [...selected, value]);
  }

  return (
    <div role="group" aria-label={label} className={`flex flex-wrap gap-2 ${className}`}>
      {options.map((option) => {
        const on = selected.includes(option.value);
        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={on}
            onClick={() => toggle(option.value)}
            className={`inline-flex min-h-8 items-center gap-1.5 rounded-full px-3 text-body-s font-medium transition-[background-color,border-color,color,box-shadow] duration-micro ease-enter pointer-coarse:min-h-11 ${
              on
                ? 'lens-ring text-text shadow-[0_0_0_3px_color-mix(in_srgb,var(--lens-a)_14%,transparent)]'
                : 'border border-line bg-transparent text-text-muted hover:border-line-strong hover:bg-wash hover:text-text'
            }`}
          >
            {on ? (
              <Check
                size={14}
                strokeWidth={2.25}
                aria-hidden="true"
                className="-ml-0.5 text-brand"
              />
            ) : null}
            {option.severity ? <SeverityMark severity={option.severity} size={10} /> : null}
            {option.label}
            {option.count !== undefined ? (
              <span className={`tabular-nums ${on ? 'text-text-muted' : 'text-text-faint'}`}>
                {formatInt(option.count)}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
