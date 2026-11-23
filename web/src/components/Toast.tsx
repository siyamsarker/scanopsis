// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import * as RadixToast from '@radix-ui/react-toast';
import { CircleAlert, CircleCheck, Info, X } from 'lucide-react';
import { useSyncExternalStore, type ReactNode } from 'react';

/*
 * Toasts confirm a finished action with the same verb as its button
 * ("Disable user" -> "User disabled"), or report a failure that has no better
 * place on the page. Call toast() from anywhere; <Toaster /> (mounted once by
 * the app) shows them in a polite live region, bottom right (bottom on phones).
 */

export type ToastTone = 'success' | 'error' | 'info';

export interface ToastItem {
  id: number;
  title: string;
  description?: ReactNode;
  tone: ToastTone;
  /** Milliseconds before it hides; errors stay longer by default. */
  duration: number;
}

export interface ToastOptions {
  description?: ReactNode;
  tone?: ToastTone;
  duration?: number;
}

let items: ToastItem[] = [];
let nextId = 1;
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

/** Shows a toast. Returns its id, for dismissToast(). */
export function toast(title: string, options: ToastOptions = {}): number {
  const tone = options.tone ?? 'success';
  const id = nextId++;
  const item: ToastItem = {
    id,
    title,
    description: options.description,
    tone,
    duration: options.duration ?? (tone === 'error' ? 8000 : 4000),
  };
  // At most three at a time; the oldest goes first.
  items = [...items, item].slice(-3);
  emit();
  return id;
}

toast.success = (title: string, options: Omit<ToastOptions, 'tone'> = {}) =>
  toast(title, { ...options, tone: 'success' });
toast.error = (title: string, options: Omit<ToastOptions, 'tone'> = {}) =>
  toast(title, { ...options, tone: 'error' });
toast.info = (title: string, options: Omit<ToastOptions, 'tone'> = {}) =>
  toast(title, { ...options, tone: 'info' });

export function dismissToast(id: number) {
  items = items.filter((item) => item.id !== id);
  emit();
}

/** Removes every toast (tests, sign-out). */
export function clearToasts() {
  items = [];
  emit();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const getItems = () => items;

const tones: Record<ToastTone, { Icon: typeof Info; tone: string }> = {
  success: { Icon: CircleCheck, tone: 'tone-ok' },
  error: { Icon: CircleAlert, tone: 'tone-fail' },
  info: { Icon: Info, tone: 'tone-brand' },
};

function ToastView({ item }: { item: ToastItem }) {
  const { Icon, tone } = tones[item.tone];
  return (
    <RadixToast.Root
      duration={item.duration}
      type={item.tone === 'error' ? 'foreground' : 'background'}
      onOpenChange={(open) => {
        if (!open) window.setTimeout(() => dismissToast(item.id), 150);
      }}
      className={`${tone} overlay anim-toast pointer-events-auto relative flex w-full items-start gap-3 overflow-hidden rounded-inner py-3 pr-10 pl-4`}
    >
      <span aria-hidden="true" className="tone-bg absolute inset-y-0 left-0 w-[3px]" />
      <Icon size={18} strokeWidth={1.75} aria-hidden="true" className="tone-fg mt-px shrink-0" />
      <div className="min-w-0">
        <RadixToast.Title className="text-body-s font-medium text-text">
          {item.title}
        </RadixToast.Title>
        {item.description ? (
          <RadixToast.Description className="mt-0.5 text-body-s text-text-muted">
            {item.description}
          </RadixToast.Description>
        ) : null}
      </div>
      <RadixToast.Close
        aria-label="Dismiss"
        className="absolute top-2 right-2 inline-flex size-7 items-center justify-center rounded-[7px] text-text-muted hover:bg-wash hover:text-text"
      >
        <X size={14} strokeWidth={1.75} aria-hidden="true" />
      </RadixToast.Close>
    </RadixToast.Root>
  );
}

/** The toast region. Mount once near the root of the app. */
export function Toaster() {
  const current = useSyncExternalStore(subscribe, getItems, getItems);
  return (
    <RadixToast.Provider swipeDirection="right" label="Notifications">
      {current.map((item) => (
        <ToastView key={item.id} item={item} />
      ))}
      <RadixToast.Viewport className="pointer-events-none fixed inset-x-0 bottom-0 z-[60] m-0 flex list-none flex-col gap-2 p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] outline-none sm:inset-x-auto sm:right-0 sm:w-[24rem] sm:p-6" />
    </RadixToast.Provider>
  );
}
