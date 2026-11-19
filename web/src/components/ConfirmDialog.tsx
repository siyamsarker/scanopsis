// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import {
  useEffect,
  useId,
  useRef,
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
} from 'react';
import { cardClass, primaryButtonClass, secondaryButtonClass } from '../ui';

/**
 * A modal confirmation. Focus starts on the safe choice, Tab stays inside the
 * dialog, Escape cancels, and focus returns to the control that opened it.
 */
export function ConfirmDialog({
  title,
  children,
  confirmLabel,
  pending = false,
  onConfirm,
  onCancel,
  returnFocusTo,
}: {
  title: string;
  children: ReactNode;
  confirmLabel: string;
  pending?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  /** Where focus goes on close; defaults to the control that had focus when the dialog opened. */
  returnFocusTo?: RefObject<HTMLElement | null>;
}) {
  const titleId = useId();
  const cancel = useRef<HTMLButtonElement>(null);
  const confirm = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const target = returnFocusTo?.current ?? (document.activeElement as HTMLElement | null);
    cancel.current?.focus();
    return () => target?.focus();
  }, [returnFocusTo]);

  function onKeyDown(event: KeyboardEvent) {
    if (event.key === 'Escape' && !pending) {
      event.stopPropagation();
      onCancel();
    } else if (event.key === 'Tab') {
      const first = cancel.current;
      const last = confirm.current?.disabled ? first : confirm.current;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    }
  }

  return (
    <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/60 p-4">
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
        className={`${cardClass} rise w-full max-w-md p-6`}
      >
        <h2 id={titleId} className="text-lg font-semibold text-text">
          {title}
        </h2>
        <div className="mt-2 text-text-muted">{children}</div>
        <div className="mt-6 flex flex-wrap justify-end gap-2">
          <button
            ref={cancel}
            type="button"
            className={secondaryButtonClass}
            disabled={pending}
            onClick={onCancel}
          >
            Cancel
          </button>
          <button
            ref={confirm}
            type="button"
            className={primaryButtonClass}
            disabled={pending}
            onClick={onConfirm}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
