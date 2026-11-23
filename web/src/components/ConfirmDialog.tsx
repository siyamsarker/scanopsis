// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useRef, type ReactNode, type RefObject } from 'react';
import { buttonClass } from '../ui';
import { Dialog } from './Dialog';

/**
 * A modal confirmation (role alertdialog). Focus starts on the safe choice,
 * Tab stays inside, Escape cancels (not while pending), and focus returns to
 * the control that opened it. Render it while the question is open; it is
 * shown as soon as it mounts.
 *
 * `tone="danger"` is for destructive actions: the confirm button turns
 * critical. Name the object and the consequence in the title and the body
 * ("Delete alice?" / "This removes alice with their sessions...").
 */
export function ConfirmDialog({
  title,
  children,
  confirmLabel,
  cancelLabel = 'Cancel',
  tone = 'default',
  pending = false,
  onConfirm,
  onCancel,
  returnFocusTo,
}: {
  title: string;
  children: ReactNode;
  /** The action, as a verb: "Delete user", "Archive". */
  confirmLabel: string;
  cancelLabel?: string;
  tone?: 'default' | 'danger';
  pending?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  /** Where focus goes on close; defaults to the control that had focus when the dialog opened. */
  returnFocusTo?: RefObject<HTMLElement | null>;
}) {
  const cancel = useRef<HTMLButtonElement>(null);

  return (
    <Dialog
      open
      role="alertdialog"
      hideClose
      modalStrict
      size="sm"
      title={title}
      onOpenChange={(open) => {
        if (!open && !pending) onCancel();
      }}
      onOpenAutoFocus={(event) => {
        event.preventDefault();
        cancel.current?.focus();
      }}
      onCloseAutoFocus={(event) => {
        const target = returnFocusTo?.current;
        if (target) {
          event.preventDefault();
          target.focus();
        }
      }}
      onEscapeKeyDown={(event) => {
        if (pending) event.preventDefault();
      }}
      footer={
        <>
          <button
            ref={cancel}
            type="button"
            className={buttonClass('secondary')}
            disabled={pending}
            onClick={onCancel}
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            className={buttonClass(tone === 'danger' ? 'danger' : 'primary')}
            disabled={pending}
            aria-busy={pending || undefined}
            onClick={onConfirm}
          >
            {pending ? <Spinner /> : null}
            {confirmLabel}
          </button>
        </>
      }
    >
      <div className="text-body-s text-text-muted [&_p+p]:mt-2">{children}</div>
    </Dialog>
  );
}

/** A small spinning arc for buttons that wait on the server. */
export function Spinner({ className = '' }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      width={16}
      height={16}
      fill="none"
      aria-hidden="true"
      className={`animate-spin ${className}`}
    >
      <circle cx="8" cy="8" r="6" stroke="currentColor" strokeOpacity="0.25" strokeWidth="2" />
      <path d="M14 8a6 6 0 0 0-6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}
