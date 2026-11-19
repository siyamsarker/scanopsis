// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Shared Tailwind class strings. Focus rings come from index.css.

const control =
  'min-h-10 rounded-control border border-border-strong bg-bg px-3 py-2 text-text transition-colors duration-150 ease-out hover:border-text-muted';

export const inputClass = `${control} w-full`;
export const selectClass = `${control} disabled:opacity-60`;

const button =
  'inline-flex min-h-10 items-center justify-center gap-2 rounded-control px-4 py-2 font-medium transition-[background-color,opacity,transform] duration-150 ease-out active:translate-y-px';

export const primaryButtonClass = `${button} bg-brand text-bg shadow-sm hover:opacity-90 disabled:opacity-60`;
export const secondaryButtonClass = `${button} border border-border-strong bg-surface text-text hover:bg-surface-raised disabled:opacity-60`;

export const cardClass =
  'rounded-card border border-border bg-surface shadow-[0_1px_2px_rgb(0_0_0/0.06),0_8px_24px_-12px_rgb(0_0_0/0.3)]';
export const headingClass = 'text-2xl font-semibold tracking-tight text-text';
export const linkClass = 'text-brand underline underline-offset-2';
