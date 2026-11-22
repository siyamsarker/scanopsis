// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';

const seen = new Set<string>();

/**
 * True on the first visit to a page in this session, so the signature draw
 * (iris arcs, strip columns) plays once rather than on every return.
 */
export function useFirstView(key: string): boolean {
  const [first] = useState(() => !seen.has(key));
  useEffect(() => {
    seen.add(key);
  }, [key]);
  return first;
}
