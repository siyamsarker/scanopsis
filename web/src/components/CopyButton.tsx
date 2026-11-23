// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Check, Copy } from 'lucide-react';
import { useState } from 'react';
import { secondaryButtonClass } from '../ui';

/** Copies text to the clipboard and says so. Fails quietly when the browser refuses. */
export function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }

  return (
    <>
      <button type="button" className={secondaryButtonClass} onClick={() => void copy()}>
        {copied ? (
          <Check size={16} strokeWidth={1.5} aria-hidden="true" />
        ) : (
          <Copy size={16} strokeWidth={1.5} aria-hidden="true" />
        )}
        {copied ? 'Copied' : label}
      </button>
      <span role="status" className="sr-only">
        {copied ? 'Copied to the clipboard' : ''}
      </span>
    </>
  );
}
