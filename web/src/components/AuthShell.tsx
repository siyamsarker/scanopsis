// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useRef, type ReactNode } from 'react';
import { cardClass, headingClass } from '../ui';
import { Wordmark } from './ApertureMark';
import { ThemeSwitch } from './ThemeSwitch';

/**
 * Centred card used by sign-in and the session steps. The heading takes focus
 * when the title changes, so keyboard and screen reader users land on the new step.
 */
export function AuthShell({
  title,
  lead,
  children,
  wide = false,
  focusHeading = true,
}: {
  title: string;
  lead?: ReactNode;
  children: ReactNode;
  wide?: boolean;
  focusHeading?: boolean;
}) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (focusHeading) heading.current?.focus();
  }, [title, focusHeading]);

  return (
    <main className="brand-glow flex min-h-screen flex-col items-center justify-center gap-6 px-4 py-10 sm:px-6">
      <Wordmark size={28} className="text-xl" />
      <section
        className={`${cardClass} brand-edge rise flex w-full ${wide ? 'max-w-xl' : 'max-w-md'} flex-col gap-6 p-6 sm:p-8`}
      >
        <div>
          <h1 ref={heading} tabIndex={-1} className={headingClass}>
            {title}
          </h1>
          {lead && <p className="mt-1 text-text-muted">{lead}</p>}
        </div>
        {children}
      </section>
      <ThemeSwitch />
    </main>
  );
}
