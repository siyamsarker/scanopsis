// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { encode } from 'uqr';

const quiet = 2;

/**
 * Draws a QR code as inline SVG. Colours are fixed (dark on white, with a quiet
 * zone) so every authenticator app can read it in both themes.
 */
export default function QrCode({ value, label }: { value: string; label: string }) {
  const { data, size } = encode(value, { ecc: 'M', border: 0 });
  let path = '';
  data.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) path += `M${x + quiet} ${y + quiet}h1v1h-1z`;
    }),
  );
  const total = size + quiet * 2;
  return (
    <svg
      role="img"
      aria-label={label}
      viewBox={`0 0 ${total} ${total}`}
      shapeRendering="crispEdges"
      className="aspect-square w-full max-w-52 rounded-control border border-border"
    >
      <rect width={total} height={total} fill="#ffffff" />
      <path d={path} fill="#000000" />
    </svg>
  );
}
