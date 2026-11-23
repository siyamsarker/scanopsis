// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { domAnimation, LazyMotion, m, MotionConfig } from 'motion/react';
import { useRef, type ReactNode } from 'react';

/**
 * Motion for the whole app: only the small "dom animation" feature set is
 * loaded, and people who ask for reduced motion get no transforms (opacity
 * fades only).
 */
export function MotionRoot({ children }: { children: ReactNode }) {
  return (
    <LazyMotion features={domAnimation} strict>
      <MotionConfig reducedMotion="user">{children}</MotionConfig>
    </LazyMotion>
  );
}

/**
 * The content of a page fades in with a 4px rise when the path changes (give
 * it `key={pathname}`). It ends at transform "none", so nothing inside is left
 * in a transformed containing block.
 */
export function RouteTransition({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  return (
    <m.div
      ref={ref}
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.2, ease: [0.2, 0.8, 0.2, 1] }}
      onAnimationComplete={() => {
        // Through the CSSOM: no style attribute in markup (CSP).
        ref.current?.style.removeProperty('transform');
        ref.current?.style.removeProperty('opacity');
      }}
    >
      {children}
    </m.div>
  );
}
