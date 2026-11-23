// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

interface ApertureMarkProps {
  /** Rendered width and height in pixels. */
  size?: number;
  className?: string;
}

/**
 * The Scanopsis mark: two overlapping arcs that form a lens. Single weight, no
 * fill, drawn in currentColor so it follows the surrounding text or brand colour.
 * It is decorative; the wordmark or heading beside it carries the name.
 */
export function ApertureMark({ size = 24, className }: ApertureMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      aria-hidden="true"
      focusable="false"
      className={className}
    >
      <path d="M6.78 5.89A6.5 6.5 0 1 1 2.6 10.87" />
      <path d="M17.22 18.11A6.5 6.5 0 1 1 21.4 13.13" />
    </svg>
  );
}

/** The mark followed by the lowercase wordmark. */
export function Wordmark({ size = 24, className = '' }: ApertureMarkProps) {
  return (
    <span className={`inline-flex items-center gap-2 ${className}`}>
      <ApertureMark size={size} className="shrink-0 text-brand" />
      <span className="font-semibold tracking-tight text-text">scanopsis</span>
    </span>
  );
}
