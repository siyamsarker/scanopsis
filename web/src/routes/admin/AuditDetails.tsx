// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

const MAX = 80;

function show(value: unknown): string {
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
}

/**
 * Event details as a compact key: value list. Values render as text only (React
 * escapes them), long ones are cut with the full text in the tooltip.
 */
export function AuditDetails({ detail }: { detail: Record<string, unknown> }) {
  const entries = Object.entries(detail);
  if (entries.length === 0) return <span className="text-text-muted">None</span>;
  return (
    <dl className="m-0 space-y-0.5 text-sm">
      {entries.map(([key, value]) => {
        const full = show(value);
        const text = full.length > MAX ? `${full.slice(0, MAX)}…` : full;
        return (
          <div key={key} className="flex gap-2">
            <dt className="shrink-0 font-mono text-text-muted">{key}:</dt>
            <dd
              className="m-0 min-w-0 font-mono break-all"
              title={full.length > MAX ? full : undefined}
            >
              {text}
            </dd>
          </div>
        );
      })}
    </dl>
  );
}
