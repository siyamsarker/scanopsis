// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Monitor, Moon, Sun, type LucideIcon } from 'lucide-react';
import { useState } from 'react';
import { readStoredTheme, setTheme, type Theme } from '../theme';

const options: { value: Theme; label: string; Icon: LucideIcon }[] = [
  { value: 'system', label: 'System', Icon: Monitor },
  { value: 'light', label: 'Light', Icon: Sun },
  { value: 'dark', label: 'Dark', Icon: Moon },
];

/** Three-way theme choice. Each button reports whether it is the current choice. */
export function ThemeSwitch() {
  const [theme, setCurrent] = useState<Theme>(readStoredTheme);

  function choose(value: Theme) {
    setTheme(value);
    setCurrent(value);
  }

  return (
    <div
      role="group"
      aria-label="Theme"
      className="inline-flex rounded-control border border-border-strong bg-bg p-0.5"
    >
      {options.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          aria-pressed={theme === value}
          aria-label={label}
          title={label}
          onClick={() => choose(value)}
          className={`inline-flex size-9 items-center justify-center rounded-[6px] transition-colors duration-150 ease-out ${
            theme === value
              ? 'bg-surface-raised text-text shadow-sm ring-1 ring-border-strong'
              : 'text-text-muted hover:text-text'
          }`}
        >
          <Icon size={16} strokeWidth={1.5} aria-hidden="true" />
        </button>
      ))}
    </div>
  );
}
