// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Monitor, Moon, Sun, type LucideIcon } from 'lucide-react';
import { useTheme, type Theme } from '../theme';

export const themeOptions: { value: Theme; label: string; Icon: LucideIcon }[] = [
  { value: 'system', label: 'System', Icon: Monitor },
  { value: 'light', label: 'Light', Icon: Sun },
  { value: 'dark', label: 'Dark', Icon: Moon },
];

/** Three-way theme choice. Each button reports whether it is the current choice. */
export function ThemeSwitch({ className = '' }: { className?: string }) {
  const [theme, setTheme] = useTheme();

  return (
    <div
      role="group"
      aria-label="Theme"
      className={`inline-flex gap-0.5 rounded-inner border border-line bg-sunken p-0.5 ${className}`}
    >
      {themeOptions.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          aria-pressed={theme === value}
          aria-label={label}
          title={label}
          onClick={() => setTheme(value)}
          className={`inline-flex size-8 items-center justify-center rounded-[8px] transition-colors duration-micro ease-enter pointer-coarse:size-10 ${
            theme === value
              ? 'bg-surface-raised text-text shadow-[0_0_0_1px_var(--line),var(--elev-1)]'
              : 'text-text-muted hover:bg-wash hover:text-text'
          }`}
        >
          <Icon size={16} strokeWidth={1.75} aria-hidden="true" />
        </button>
      ))}
    </div>
  );
}
