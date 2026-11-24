// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Check, ChevronDown, Lock } from 'lucide-react';
import { useRef, useState, type KeyboardEvent } from 'react';
import { useEarlierScans, type Scan, type ScanSummary } from '../../api/scan';
import { Popover, PopoverContent, PopoverTrigger } from '../../components/Popover';
import { Skeleton } from '../../components/states';
import {
  formatExactTime,
  formatInt,
  formatShortDate,
  relativeTime,
  scannedTag,
} from '../../format';
import { buttonClass } from '../../ui';
import { DistributionMini, riskPoints } from '../../viz';

/** "Pipeline #1000 on main, 4 days ago": what a scan is to the reader. */
export function describeScan(s: ScanSummary) {
  return `pipeline #${s.gitlab_pipeline_id} on ${s.ref}, ${relativeTime(s.scanned_at)}`;
}

function Option({
  scan,
  selected,
  previous,
  sameRef,
  onPick,
}: {
  scan: ScanSummary;
  selected: boolean;
  previous: boolean;
  sameRef: boolean;
  onPick: () => void;
}) {
  const tag = scannedTag(scan);
  return (
    <li>
      <button
        type="button"
        role="option"
        aria-selected={selected}
        data-roving
        onClick={onPick}
        className={`grid w-full grid-cols-[1rem_minmax(0,1fr)_auto] items-center gap-x-3 rounded-inner px-3 py-2.5 text-left transition-colors duration-micro hover:bg-wash ${selected ? 'bg-wash' : ''}`}
      >
        <span aria-hidden="true">
          {selected ? <Check size={14} strokeWidth={2} className="text-brand" /> : null}
        </span>
        <span className="min-w-0">
          <span className="flex min-w-0 items-center gap-2 text-body-s text-text">
            <span className="shrink-0 font-medium tabular-nums">
              Pipeline #{scan.gitlab_pipeline_id}
            </span>
            {tag ? (
              <span
                className="min-w-0 truncate font-mono text-small text-text-muted"
                title={scan.artifact_name}
              >
                {tag}
              </span>
            ) : null}
            {previous ? <span className="tag shrink-0">Previous</span> : null}
          </span>
          <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-small text-text-muted">
            <span className={`truncate font-mono ${sameRef ? '' : 'text-text'}`} title={scan.ref}>
              {scan.ref}
            </span>
            {scan.ref_protected ? (
              <Lock size={11} strokeWidth={2} aria-label="protected" className="shrink-0" />
            ) : null}
            <time
              dateTime={scan.scanned_at}
              title={formatExactTime(scan.scanned_at)}
              className="ml-1.5 shrink-0"
            >
              {formatShortDate(scan.scanned_at)}
            </time>
          </span>
        </span>
        <span className="flex flex-col items-end gap-1">
          <DistributionMini
            counts={scan.counts}
            label={`Findings of pipeline ${scan.gitlab_pipeline_id}`}
          />
          <span className="text-caption text-text-muted tabular-nums">
            {formatInt(riskPoints(scan.counts))} pts
          </span>
        </span>
      </button>
    </li>
  );
}

/**
 * Chooses the scan to compare with: any earlier scan of the same artifact
 * and kind, on this ref or another, newest first. The previous scan of the
 * same ref is the default.
 */
export function ScanPicker({
  scan,
  base,
  onChange,
}: {
  scan: Scan;
  /** The scan compared with now, or null when there is none. */
  base: ScanSummary | null;
  onChange: (id: number | null) => void;
}) {
  const [open, setOpen] = useState(false);
  const [sameRefOnly, setSameRefOnly] = useState(false);
  const earlier = useEarlierScans(scan.id, open);
  const list = useRef<HTMLUListElement>(null);
  const loaded = earlier.data?.pages.flatMap((p) => p.items) ?? [];
  // The default comparison: the newest earlier scan of the same ref.
  const previousId = loaded.find((s) => s.ref === scan.ref)?.id ?? null;
  const items = loaded.filter((s) => !sameRefOnly || s.ref === scan.ref);

  function onKeyDown(event: KeyboardEvent) {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    const buttons = [
      ...(list.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? []),
    ];
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next =
      event.key === 'ArrowDown' ? Math.min(buttons.length - 1, at + 1) : Math.max(0, at - 1);
    buttons[next]?.focus();
    event.preventDefault();
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={`${buttonClass('secondary')} max-w-full justify-between`}
          aria-label={
            base ? `Compare with ${describeScan(base)}. Change` : 'Choose a scan to compare with'
          }
        >
          <span className="min-w-0 truncate">
            {base ? (
              <>
                <span className="text-text-muted">Compared with </span>
                pipeline #{base.gitlab_pipeline_id}
                {base.ref !== scan.ref ? <span className="font-mono"> ({base.ref})</span> : null}
              </>
            ) : (
              'Choose a scan'
            )}
          </span>
          <ChevronDown size={16} strokeWidth={1.75} aria-hidden="true" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[26rem] p-2">
        <div className="flex items-center justify-between gap-3 px-3 pt-2 pb-2">
          <p className="text-body-s font-semibold text-text">Compare with an earlier scan</p>
          <label className="inline-flex items-center gap-2 text-small text-text-muted">
            <input
              type="checkbox"
              checked={sameRefOnly}
              onChange={(e) => setSameRefOnly(e.target.checked)}
              className="size-3.5 accent-[var(--brand)]"
            />
            Only <span className="font-mono">{scan.ref}</span>
          </label>
        </div>
        {earlier.isPending ? (
          <div className="space-y-2 p-2">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : earlier.isError ? (
          <p className="px-3 py-4 text-body-s text-text-muted">
            The earlier scans could not be loaded.{' '}
            <button type="button" className="underline" onClick={() => void earlier.refetch()}>
              Try again
            </button>
          </p>
        ) : items.length === 0 ? (
          <p className="px-3 py-4 text-body-s text-text-muted">
            No earlier scan of this artifact{sameRefOnly ? ' on this ref' : ''}.
          </p>
        ) : (
          <ul
            ref={list}
            role="listbox"
            aria-label="Earlier scans"
            onKeyDown={onKeyDown}
            className="max-h-80 space-y-0.5 overflow-y-auto"
          >
            {items.map((s) => (
              <Option
                key={s.id}
                scan={s}
                selected={s.id === base?.id}
                previous={s.id === previousId}
                sameRef={s.ref === scan.ref}
                onPick={() => {
                  onChange(s.id === previousId ? null : s.id);
                  setOpen(false);
                }}
              />
            ))}
          </ul>
        )}
        {earlier.hasNextPage ? (
          <div className="px-2 pt-2 pb-1">
            <button
              type="button"
              className={`${buttonClass('ghost', 'sm')} w-full`}
              onClick={() => void earlier.fetchNextPage()}
              disabled={earlier.isFetchingNextPage}
            >
              {earlier.isFetchingNextPage ? 'Loading…' : 'Show older scans'}
            </button>
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}
