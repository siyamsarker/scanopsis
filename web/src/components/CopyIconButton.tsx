// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Check, Copy } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

/**
 * A compact icon button that copies text. The accessible name says what is
 * copied ("Copy commit"), and a polite live region confirms the copy. Fails
 * quietly when the browser refuses clipboard access. For a labelled button use
 * CopyButton instead.
 */
export function CopyIconButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }

  const Icon = copied ? Check : Copy;
  return (
    <>
      <button
        type="button"
        aria-label={label}
        title={copied ? 'Copied' : label}
        onClick={() => void copy()}
        className="inline-flex size-6 shrink-0 items-center justify-center rounded text-text-muted transition-colors duration-150 ease-out hover:bg-surface-raised hover:text-text"
      >
        <Icon size={14} strokeWidth={1.5} aria-hidden="true" />
      </button>
      <span role="status" className="sr-only">
        {copied ? 'Copied to the clipboard' : ''}
      </span>
    </>
  );
}
