// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleAlert } from 'lucide-react';

export function ErrorNote({ children, id }: { children: string; id?: string }) {
  return (
    <p id={id} role="alert" className="flex items-start gap-2 text-text">
      <CircleAlert
        size={16}
        strokeWidth={1.5}
        aria-hidden="true"
        className="mt-0.5 shrink-0 text-fail"
      />
      <span>{children}</span>
    </p>
  );
}
