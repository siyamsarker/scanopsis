// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { countOf, formatInt } from '../format';
import { secondaryButtonClass } from '../ui';

/**
 * Paging for the API's cursor lists: a count of what is shown and a "Load more"
 * button while more exist. The count sits in a polite live region so loading a
 * page is announced.
 */
export function Pagination({
  shown,
  total,
  hasMore,
  loading = false,
  onLoadMore,
  noun = 'result',
  nounPlural,
  className = '',
}: {
  /** How many rows are loaded so far. */
  shown: number;
  /** The full count when the API knows it. */
  total?: number;
  hasMore: boolean;
  loading?: boolean;
  onLoadMore: () => void;
  /** What the rows are, singular: "finding". */
  noun?: string;
  nounPlural?: string;
  className?: string;
}) {
  const status = hasMore
    ? total !== undefined
      ? `Showing ${formatInt(shown)} of ${formatInt(total)}`
      : `Showing ${formatInt(shown)}`
    : `All ${countOf(shown, noun, nounPlural)} shown`;
  return (
    <div className={`flex flex-wrap items-center justify-between gap-3 ${className}`}>
      <p role="status" className="text-sm text-text-muted tabular-nums">
        {status}
      </p>
      {hasMore ? (
        <button
          type="button"
          className={secondaryButtonClass}
          onClick={onLoadMore}
          disabled={loading}
        >
          {loading ? 'Loading' : 'Load more'}
        </button>
      ) : null}
    </div>
  );
}
