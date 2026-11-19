// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Shared Tailwind class strings. Focus rings come from index.css.

const control =
  'rounded-control border border-border bg-bg px-3 py-2 text-text transition-colors duration-150 ease-out';

export const inputClass = `${control} w-full`;
export const selectClass = `${control} disabled:opacity-60`;

const button =
  'inline-flex items-center justify-center gap-2 rounded-control px-4 py-2 font-medium transition-colors duration-150 ease-out';

export const primaryButtonClass = `${button} bg-brand text-bg hover:opacity-90 disabled:opacity-60`;
export const secondaryButtonClass = `${button} border border-border bg-surface text-text hover:bg-surface-raised disabled:opacity-60`;

export const cardClass = 'rounded-card border border-border bg-surface';
export const headingClass = 'text-2xl font-semibold tracking-tight text-text';
export const linkClass = 'text-brand underline underline-offset-2';
