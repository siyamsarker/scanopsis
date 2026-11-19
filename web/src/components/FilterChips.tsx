// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Check } from 'lucide-react';
import { formatInt } from '../format';
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
 * button (aria-pressed) and a selected chip shows a check mark as well as a
 * stronger border. Multi-select by default; `single` makes selecting one clear
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
            className={`inline-flex min-h-8 items-center gap-2 rounded-full border px-3 text-sm font-medium transition-[background-color,border-color] duration-150 ease-out ${
              on
                ? 'border-text bg-surface-raised text-text'
                : 'border-border-strong bg-transparent text-text-muted hover:border-text-muted hover:text-text'
            }`}
          >
            {on ? <Check size={14} strokeWidth={1.5} aria-hidden="true" /> : null}
            {option.severity ? (
              <span aria-hidden="true" className={`tone-${option.severity} chart-dot`} />
            ) : null}
            {option.label}
            {option.count !== undefined ? (
              <span className="tabular-nums text-text-muted">{formatInt(option.count)}</span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
