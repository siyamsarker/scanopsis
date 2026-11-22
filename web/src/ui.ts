// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Shared Tailwind class strings. Focus rings come from index.css.

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
export type ButtonSize = 'sm' | 'md' | 'lg';

const buttonBase =
  'relative inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-inner font-medium select-none ' +
  'transition-[background-color,border-color,color,box-shadow,translate] duration-micro ease-enter ' +
  'disabled:cursor-not-allowed disabled:opacity-55 aria-disabled:cursor-not-allowed aria-disabled:opacity-55 ' +
  '[&_svg]:shrink-0';

// 32 / 40 / 44 px on a mouse; every size grows to 44 px on a touch screen.
const buttonSizes: Record<ButtonSize, string> = {
  sm: 'min-h-8 px-3 text-body-s pointer-coarse:min-h-11',
  md: 'min-h-10 px-4 text-body-s pointer-coarse:min-h-11',
  lg: 'min-h-11 px-5 text-body',
};

const buttonVariants: Record<ButtonVariant, string> = {
  primary:
    'bg-brand text-brand-ink shadow-1 hover:bg-[color-mix(in_srgb,var(--brand)_86%,var(--text))] active:bg-brand',
  secondary:
    'border border-line-strong bg-surface-raised text-text shadow-1 hover:border-text-faint hover:bg-surface-overlay',
  ghost: 'text-text-muted hover:bg-wash hover:text-text',
  danger:
    'bg-fail text-fail-ink shadow-1 hover:bg-[color-mix(in_srgb,var(--fail)_86%,var(--text))] active:bg-fail',
};

/** Class string for a button or a link styled as one. */
export function buttonClass(variant: ButtonVariant = 'secondary', size: ButtonSize = 'md'): string {
  return `${buttonBase} ${buttonSizes[size]} ${buttonVariants[variant]}`;
}

const iconSizes: Record<ButtonSize, string> = {
  sm: 'size-8 pointer-coarse:size-11',
  md: 'size-10 pointer-coarse:size-11',
  lg: 'size-11',
};

/** A square button holding only an icon. Give it an aria-label. */
export function iconButtonClass(variant: ButtonVariant = 'ghost', size: ButtonSize = 'md'): string {
  return `${buttonBase} ${iconSizes[size]} ${buttonVariants[variant]}`;
}

export const primaryButtonClass = buttonClass('primary');
export const secondaryButtonClass = buttonClass('secondary');
export const ghostButtonClass = buttonClass('ghost');
export const dangerButtonClass = buttonClass('danger');

const control =
  'min-h-10 rounded-inner border border-line-strong bg-sunken px-3 py-2 text-body-s text-text ' +
  'transition-[border-color,box-shadow] duration-micro ease-enter placeholder:text-text-faint ' +
  'hover:border-text-faint disabled:cursor-not-allowed disabled:opacity-60 ' +
  'aria-invalid:border-fail pointer-coarse:min-h-11';

export const inputClass = `${control} w-full`;
export const monoInputClass = `${control} w-full font-mono`;
/** @deprecated Use the Select component; kept for any native select that remains. */
export const selectClass = control;

export const cardClass = 'panel';
export const headingClass = 'text-h2 font-semibold tracking-tight text-text';
export const linkClass =
  'text-brand underline decoration-[color-mix(in_srgb,var(--brand)_45%,transparent)] underline-offset-[3px] transition-colors duration-micro hover:decoration-brand';
