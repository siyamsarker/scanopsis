// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import * as RadixDialog from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import { useRef, type ComponentPropsWithoutRef, type ReactNode } from 'react';
import { iconButtonClass } from '../ui';

/*
 * Dialogs and sheets. Both render in a portal at document level, so no
 * transform, overflow or stacking context on the page can confine them. Focus
 * moves inside on open, Tab stays inside, Escape closes, and focus returns to
 * the control that opened them.
 */

type FocusEvent = Event;

export interface DialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The dialog's name: a plain question or action ("Edit payments-api"). */
  title: ReactNode;
  /** One or two sentences under the title; read by screen readers as the description. */
  description?: ReactNode;
  children?: ReactNode;
  /** Buttons, right-aligned at the bottom. Put the primary action last. */
  footer?: ReactNode;
  /** sm 400px, md 480px (default), lg 640px. */
  size?: 'sm' | 'md' | 'lg';
  /** "alertdialog" for confirmations that interrupt; "dialog" otherwise. */
  role?: 'dialog' | 'alertdialog';
  /** Hide the close button in the corner (confirmations offer Cancel instead). */
  hideClose?: boolean;
  /** Do not close on a click outside (confirmations, dirty forms). */
  modalStrict?: boolean;
  /** Called before focus moves into the dialog; preventDefault() to place focus yourself. */
  onOpenAutoFocus?: (event: FocusEvent) => void;
  /** Called before focus returns on close; preventDefault() to return it elsewhere. */
  onCloseAutoFocus?: (event: FocusEvent) => void;
  /** Called on Escape; preventDefault() keeps the dialog open. */
  onEscapeKeyDown?: (event: KeyboardEvent) => void;
  className?: string;
}

/**
 * Focus handlers that return focus to whatever had it when the overlay opened
 * (Radix only does this for its own Trigger). A caller's handler runs first
 * and can preventDefault() to place focus itself.
 */
export function useReturnFocus(
  onOpenAutoFocus?: (event: FocusEvent) => void,
  onCloseAutoFocus?: (event: FocusEvent) => void,
) {
  const opener = useRef<HTMLElement | null>(null);
  return {
    onOpenAutoFocus(event: FocusEvent) {
      opener.current =
        document.activeElement instanceof HTMLElement ? document.activeElement : null;
      onOpenAutoFocus?.(event);
    },
    onCloseAutoFocus(event: FocusEvent) {
      onCloseAutoFocus?.(event);
      if (event.defaultPrevented) return;
      const target = opener.current;
      if (target?.isConnected) {
        event.preventDefault();
        target.focus();
      }
    },
  };
}

const dialogSizes = { sm: 'sm:max-w-[400px]', md: 'sm:max-w-[480px]', lg: 'sm:max-w-[640px]' };

function Scrim() {
  return <RadixDialog.Overlay className="anim-fade fixed inset-0 z-40 bg-(--scrim)" />;
}

/** The dialog's close button in the top right corner. */
function CornerClose({ label = 'Close' }: { label?: string }) {
  return (
    <RadixDialog.Close
      className={`${iconButtonClass('ghost', 'sm')} -mt-1 -mr-2`}
      aria-label={label}
    >
      <X size={18} strokeWidth={1.75} aria-hidden="true" />
    </RadixDialog.Close>
  );
}

/** A centred modal dialog. On a phone it rises from the bottom edge. */
export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  size = 'md',
  role = 'dialog',
  hideClose = false,
  modalStrict = false,
  onOpenAutoFocus,
  onCloseAutoFocus,
  onEscapeKeyDown,
  className = '',
}: DialogProps) {
  const focus = useReturnFocus(onOpenAutoFocus, onCloseAutoFocus);
  return (
    <RadixDialog.Root open={open} onOpenChange={onOpenChange}>
      <RadixDialog.Portal>
        <Scrim />
        <div className="pointer-events-none fixed inset-0 z-50 flex items-end justify-center p-3 sm:items-center sm:p-6">
          <RadixDialog.Content
            role={role}
            onOpenAutoFocus={focus.onOpenAutoFocus}
            onCloseAutoFocus={focus.onCloseAutoFocus}
            onEscapeKeyDown={onEscapeKeyDown}
            onPointerDownOutside={modalStrict ? (event) => event.preventDefault() : undefined}
            onInteractOutside={modalStrict ? (event) => event.preventDefault() : undefined}
            {...(description ? {} : { 'aria-describedby': undefined })}
            className={`overlay anim-pop pointer-events-auto flex max-h-[calc(100dvh-1.5rem)] w-full flex-col rounded-panel ${dialogSizes[size]} ${className}`}
          >
            <div className="flex items-start gap-4 px-6 pt-5 pb-1">
              <div className="min-w-0 flex-1">
                <RadixDialog.Title className="text-h3 font-semibold tracking-tight text-text">
                  {title}
                </RadixDialog.Title>
                {description ? (
                  <RadixDialog.Description className="mt-1 text-body-s text-text-muted">
                    {description}
                  </RadixDialog.Description>
                ) : null}
              </div>
              {hideClose ? null : <CornerClose />}
            </div>
            {children ? (
              <div className="min-h-0 flex-1 overflow-y-auto px-6 pt-3 pb-1 text-body-s text-text-muted">
                {children}
              </div>
            ) : null}
            {footer ? (
              <div className="flex flex-col-reverse gap-2 px-6 pt-5 pb-6 sm:flex-row sm:justify-end">
                {footer}
              </div>
            ) : (
              <div className="pb-5" />
            )}
          </RadixDialog.Content>
        </div>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  );
}

export interface SheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  /** Sticky footer, for actions. */
  footer?: ReactNode;
  /** Right (default) for details; left for navigation. */
  side?: 'left' | 'right';
  /** Width from the sm breakpoint up: md 480px (default), lg 640px. Phones always get the full screen. */
  size?: 'sm' | 'md' | 'lg';
  /** Show the title visually (default) or only to screen readers. */
  hideTitle?: boolean;
  /** Content above the title row, beside the close button (a logo, for navigation). */
  header?: ReactNode;
  closeLabel?: string;
  onOpenAutoFocus?: (event: FocusEvent) => void;
  onCloseAutoFocus?: (event: FocusEvent) => void;
  className?: string;
}

const sheetSizes = { sm: 'sm:max-w-[360px]', md: 'sm:max-w-[480px]', lg: 'sm:max-w-[640px]' };

/**
 * A panel that slides in from the side: details of a row, filters, navigation
 * on a phone. Full-screen below the sm breakpoint.
 */
export function Sheet({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  side = 'right',
  size = 'md',
  hideTitle = false,
  header,
  closeLabel = 'Close',
  onOpenAutoFocus,
  onCloseAutoFocus,
  className = '',
}: SheetProps) {
  const focus = useReturnFocus(onOpenAutoFocus, onCloseAutoFocus);
  const edge =
    side === 'right'
      ? 'right-0 anim-sheet-right sm:border-l sm:rounded-l-panel'
      : 'left-0 anim-sheet-left sm:border-r sm:rounded-r-panel';
  return (
    <RadixDialog.Root open={open} onOpenChange={onOpenChange}>
      <RadixDialog.Portal>
        <Scrim />
        <RadixDialog.Content
          onOpenAutoFocus={focus.onOpenAutoFocus}
          onCloseAutoFocus={focus.onCloseAutoFocus}
          {...(description ? {} : { 'aria-describedby': undefined })}
          className={`fixed inset-y-0 z-50 flex w-full flex-col border-line bg-surface-overlay shadow-3 ${edge} ${sheetSizes[size]} ${className}`}
        >
          <div className="flex items-start gap-4 border-b border-line px-5 pt-[max(1rem,env(safe-area-inset-top))] pb-4 sm:px-6">
            <div className="min-w-0 flex-1">
              {header}
              <RadixDialog.Title
                className={hideTitle ? 'sr-only' : 'text-h3 font-semibold tracking-tight text-text'}
              >
                {title}
              </RadixDialog.Title>
              {description ? (
                <RadixDialog.Description className="mt-1 text-body-s text-text-muted">
                  {description}
                </RadixDialog.Description>
              ) : null}
            </div>
            <CornerClose label={closeLabel} />
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4 sm:px-6">{children}</div>
          {footer ? (
            <div className="flex flex-wrap justify-end gap-2 border-t border-line px-5 pt-4 pb-[max(1rem,env(safe-area-inset-bottom))] sm:px-6">
              {footer}
            </div>
          ) : null}
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  );
}

/** A button that closes the surrounding Dialog or Sheet. */
export function DialogClose(props: ComponentPropsWithoutRef<typeof RadixDialog.Close>) {
  return <RadixDialog.Close {...props} />;
}
