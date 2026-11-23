// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

/** SVG path helpers for bars with a rounded data end and a square baseline end. */

/** A bar from y (top) to y + h whose top corners are rounded by r. */
export function roundedTop(x: number, y: number, w: number, h: number, r: number): string {
  const k = Math.max(0, Math.min(r, w / 2, h));
  if (k < 0.5) return `M${x},${y}h${w}v${h}h${-w}Z`;
  return (
    `M${x},${y + h}V${y + k}Q${x},${y} ${x + k},${y}` +
    `H${x + w - k}Q${x + w},${y} ${x + w},${y + k}V${y + h}Z`
  );
}

/** A bar from y (top, the baseline) to y + h whose bottom corners are rounded by r. */
export function roundedBottom(x: number, y: number, w: number, h: number, r: number): string {
  const k = Math.max(0, Math.min(r, w / 2, h));
  if (k < 0.5) return `M${x},${y}h${w}v${h}h${-w}Z`;
  return (
    `M${x},${y}H${x + w}V${y + h - k}Q${x + w},${y + h} ${x + w - k},${y + h}` +
    `H${x + k}Q${x},${y + h} ${x},${y + h - k}Z`
  );
}

/** Round to 0.01 px so path strings stay short. */
export function px(n: number): number {
  return Math.round(n * 100) / 100;
}
