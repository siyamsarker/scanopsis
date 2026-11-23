// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Plus, X } from 'lucide-react';
import { useId, useMemo, useState, type FormEvent, type KeyboardEvent } from 'react';
import type { BackupList } from '../../../api/backups';
import { describeError } from '../../../api/errors';
import { SettingsValidationError, useSettings, useUpdateSettings } from '../../../api/hooks';
import { Spinner } from '../../../components/ConfirmDialog';
import { SearchInput, Select } from '../../../components/inputs';
import { ErrorState, Skeleton } from '../../../components/states';
import { Switch } from '../../../components/Switch';
import { toast } from '../../../components/Toast';
import { buttonClass, monoInputClass } from '../../../ui';
import { formatExactTime, relativeTime } from '../../../format';
import { Problem } from '../kit';
import {
  browserTimeZone,
  currentZoneName,
  KEEP_DAYS_MAX,
  KEEP_DAYS_MIN,
  newTimeProblem,
  scheduleProblems,
  timeZoneOptions,
  zoneMatches,
} from './model';

const KEYS = {
  enabled: 'backup.schedule_enabled',
  times: 'backup.schedule_times',
  zone: 'backup.time_zone',
  keepDays: 'backup.keep_days',
} as const;

interface Schedule {
  enabled: boolean;
  times: string[];
  zone: string;
  keepDays: string;
}

type Field = 'enabled' | 'times' | 'zone' | 'keepDays';

const labelClass = 'text-body-s font-medium text-text';

function ScheduleFields({
  saved,
  lastRun,
}: {
  saved: Schedule;
  lastRun: BackupList['schedule'] | undefined;
}) {
  const update = useUpdateSettings();
  const id = useId();
  const [draft, setDraft] = useState<Schedule>(saved);
  const [newTime, setNewTime] = useState('');
  const [timeProblem, setTimeProblem] = useState<string>();
  const [problems, setProblems] = useState<Partial<Record<Field, string>>>({});
  const browser = useMemo(() => browserTimeZone(), []);
  const [zoneFilter, setZoneFilter] = useState('');
  const zones = useMemo(
    () => timeZoneOptions(draft.zone, browser, zoneFilter),
    [draft.zone, browser, zoneFilter],
  );
  const zoneMatchList = zoneFilter.trim()
    ? zones.filter((zone) => zoneMatches(zone.value, zoneFilter))
    : null;

  /** Enter in the filter chooses the first zone that matches what was typed. */
  function chooseFirstZone(typed: string) {
    if (!typed.trim()) return;
    const first = timeZoneOptions(draft.zone, browser, typed).find((zone) =>
      zoneMatches(zone.value, typed),
    );
    if (first) change({ zone: first.value });
  }

  const dirty =
    draft.enabled !== saved.enabled ||
    draft.zone !== saved.zone ||
    draft.keepDays !== saved.keepDays ||
    draft.times.join() !== saved.times.join() ||
    newTime.trim() !== '';

  const serverProblems: Partial<Record<Field, string>> = {};
  if (update.error instanceof SettingsValidationError) {
    for (const [field, key] of Object.entries(KEYS) as [Field, string][]) {
      if (update.error.fields[key]) serverProblems[field] = update.error.fields[key];
    }
  }
  const shown = { ...serverProblems, ...problems };

  function change(next: Partial<Schedule>) {
    setDraft((prev) => ({ ...prev, ...next }));
    setProblems((prev) => {
      const out = { ...prev };
      for (const key of Object.keys(next) as Field[]) delete out[key];
      return out;
    });
    if (update.isError) update.reset();
  }

  function addTime() {
    const time = newTime.trim();
    const problem = newTimeProblem(time, draft.times);
    setTimeProblem(problem);
    if (problem) return;
    change({ times: [...draft.times, time].sort() });
    setNewTime('');
  }

  function onTimeKey(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter') {
      event.preventDefault();
      addTime();
    }
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    // A time typed but not added yet is meant to be saved too.
    let times = draft.times;
    const typed = newTime.trim();
    if (typed) {
      const problem = newTimeProblem(typed, times);
      if (problem) {
        setTimeProblem(problem);
        return;
      }
      times = [...times, typed].sort();
      setDraft((prev) => ({ ...prev, times }));
      setNewTime('');
    }
    const found = scheduleProblems({ ...draft, times });
    if (Object.keys(found).length > 0) {
      setProblems(found);
      return;
    }
    update.mutate(
      {
        [KEYS.enabled]: draft.enabled,
        [KEYS.times]: times,
        [KEYS.zone]: draft.zone,
        [KEYS.keepDays]: Number(draft.keepDays.trim()),
      },
      {
        onSuccess: () =>
          toast.success('Schedule saved', {
            description: draft.enabled
              ? `Backups run at ${times.join(', ')} (${draft.zone}).`
              : 'Scheduled backups are off.',
          }),
      },
    );
  }

  const timesHint = `${id}-times-hint`;
  const keepHint = `${id}-keep-hint`;
  const keepError = `${id}-keep-error`;

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-6">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <label htmlFor={`${id}-enabled`} className="sr-only">
            Scheduled backups
          </label>
          {/* Wraps before the switch on narrow panels. */}
          <p id={`${id}-enabled-hint`} className="text-body-s text-text-muted">
            {lastRun?.last_run_at ? (
              <>
                {lastRun.ok ? 'Last scheduled backup ' : 'Last scheduled backup failed '}
                <time
                  dateTime={lastRun.last_run_at}
                  title={formatExactTime(lastRun.last_run_at)}
                  className="whitespace-nowrap"
                >
                  {relativeTime(lastRun.last_run_at)}
                </time>
              </>
            ) : (
              'No scheduled backup has run yet.'
            )}
          </p>
        </div>
        <Switch
          id={`${id}-enabled`}
          checked={draft.enabled}
          onChange={(enabled) => change({ enabled })}
          describedBy={`${id}-enabled-hint`}
        />
      </div>

      <fieldset className="flex min-w-0 flex-col gap-2">
        <legend className={`${labelClass} mb-2`}>Times of day</legend>
        {draft.times.length > 0 ? (
          <ul aria-label="Backup times" className="flex flex-wrap gap-2">
            {draft.times.map((time) => (
              <li
                key={time}
                className="inline-flex h-8 items-center gap-1 rounded-full border border-line-strong bg-sunken pr-1 pl-3 font-mono text-body-s text-text pointer-coarse:h-10"
              >
                {time}
                <button
                  type="button"
                  aria-label={`Remove ${time}`}
                  onClick={() => change({ times: draft.times.filter((t) => t !== time) })}
                  className="inline-flex size-6 items-center justify-center rounded-full text-text-muted hover:bg-wash hover:text-text pointer-coarse:size-8"
                >
                  <X size={14} strokeWidth={1.75} aria-hidden="true" />
                </button>
              </li>
            ))}
          </ul>
        ) : null}
        {shown.times ? <Problem>{shown.times}</Problem> : null}
        <div className="mt-1 flex flex-wrap items-start gap-2">
          <div className="flex flex-col gap-1">
            <label htmlFor={`${id}-time`} className="sr-only">
              Add a time
            </label>
            <input
              id={`${id}-time`}
              type="text"
              inputMode="numeric"
              autoComplete="off"
              placeholder="HH:MM"
              maxLength={5}
              value={newTime}
              aria-invalid={timeProblem ? true : undefined}
              aria-describedby={timeProblem ? `${id}-time-error` : timesHint}
              onChange={(e) => {
                setNewTime(e.target.value);
                setTimeProblem(undefined);
              }}
              onKeyDown={onTimeKey}
              className={`${monoInputClass} w-28!`}
            />
          </div>
          <button type="button" className={buttonClass('secondary')} onClick={addTime}>
            <Plus size={16} strokeWidth={1.75} aria-hidden="true" />
            Add time
          </button>
        </div>
        {timeProblem ? <Problem id={`${id}-time-error`}>{timeProblem}</Problem> : null}
        <p id={timesHint} className="text-small text-text-muted">
          24-hour times, up to 24 a day.
        </p>
      </fieldset>

      <div className="flex flex-col gap-2">
        {/* The fields below carry their own names for screen readers. */}
        <span aria-hidden="true" className={labelClass}>
          Time zone
        </span>
        {/* Enter in the filter chooses the first match; it does not save the form. */}
        <div
          className="flex flex-col gap-1.5"
          onKeyDown={(event) => {
            if (event.key !== 'Enter') return;
            event.preventDefault();
            if (event.target instanceof HTMLInputElement) chooseFirstZone(event.target.value);
          }}
        >
          <SearchInput
            label="Filter time zones"
            placeholder="Type a city, such as Berlin"
            value={zoneFilter}
            onChange={setZoneFilter}
            delayMs={120}
            shortcutHint={false}
          />
          <p aria-live="polite" className="text-small text-text-muted empty:hidden">
            {zoneMatchList === null
              ? ''
              : zoneMatchList.length === 0
                ? 'No time zone matches.'
                : `${zoneMatchList.length === 1 ? '1 time zone matches' : `${zoneMatchList.length} time zones match`}. Press Enter to choose ${zoneMatchList[0].label}.`}
          </p>
        </div>
        <Select
          label="Time zone"
          hideLabel
          value={draft.zone}
          onChange={(zone) => change({ zone })}
          options={zones}
        />
        {browser !== draft.zone ? (
          <button
            type="button"
            className="self-start text-small text-brand underline underline-offset-[3px]"
            onClick={() => change({ zone: browser })}
          >
            Use my time zone ({browser.replace(/_/g, ' ')})
          </button>
        ) : null}
        {shown.zone ? <Problem>{shown.zone}</Problem> : null}
      </div>

      <div className="flex flex-col gap-2">
        <label htmlFor={`${id}-keep`} className={labelClass}>
          Keep scheduled backups for
        </label>
        <span className="flex items-center gap-2">
          <input
            id={`${id}-keep`}
            type="number"
            inputMode="numeric"
            step={1}
            min={KEEP_DAYS_MIN}
            max={KEEP_DAYS_MAX}
            value={draft.keepDays}
            aria-invalid={shown.keepDays ? true : undefined}
            aria-describedby={shown.keepDays ? `${keepError} ${keepHint}` : keepHint}
            onChange={(e) => change({ keepDays: e.target.value })}
            className={`${monoInputClass} w-24!`}
          />
          <span className="text-body-s text-text-muted">days</span>
        </span>
        {shown.keepDays ? <Problem id={keepError}>{shown.keepDays}</Problem> : null}
        <p id={keepHint} className="text-small text-text-muted">
          The newest scheduled backup is always kept.
        </p>
      </div>

      {shown.enabled ? <Problem>{shown.enabled}</Problem> : null}
      {update.isError && !(update.error instanceof SettingsValidationError) ? (
        <Problem>{`The schedule was not saved. ${describeError(update.error)}`}</Problem>
      ) : null}

      <div className="flex flex-wrap items-center gap-2 border-t border-line pt-5">
        <button
          type="submit"
          className={buttonClass('primary')}
          disabled={!dirty || update.isPending}
          aria-busy={update.isPending || undefined}
        >
          {update.isPending ? <Spinner /> : null}
          Save schedule
        </button>
        {dirty && !update.isPending ? (
          <button
            type="button"
            className={buttonClass('ghost')}
            onClick={() => {
              setDraft(saved);
              setProblems({});
              setTimeProblem(undefined);
              update.reset();
            }}
          >
            Undo changes
          </button>
        ) : null}
      </div>
    </form>
  );
}

function readSchedule(items: { key: string; value: unknown }[]): Schedule {
  const value = (key: string) => items.find((s) => s.key === key)?.value;
  const times = value(KEYS.times);
  const keep = value(KEYS.keepDays);
  const zone = value(KEYS.zone);
  return {
    enabled: value(KEYS.enabled) === true,
    times: Array.isArray(times) ? times.map(String) : ['02:00'],
    // A zone saved by a former name shows, and is saved again, by its current one.
    zone: typeof zone === 'string' && zone ? currentZoneName(zone) : 'UTC',
    keepDays: typeof keep === 'number' ? String(keep) : '7',
  };
}

/**
 * The backup schedule: on or off, the times of day, their time zone and how
 * long scheduled backups are kept. They are settings, saved through the
 * settings API, and a restore keeps this server's values.
 */
export function ScheduleForm({ lastRun }: { lastRun: BackupList['schedule'] | undefined }) {
  const settings = useSettings();
  if (settings.isPending) {
    return (
      <div role="status" aria-label="Loading" className="flex flex-col gap-4">
        <Skeleton className="h-6 w-40" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
      </div>
    );
  }
  if (settings.isError) {
    return (
      <ErrorState
        compact
        title="The schedule could not be loaded"
        message={describeError(settings.error)}
        onRetry={() => void settings.refetch()}
        retrying={settings.isFetching}
      />
    );
  }
  const saved = readSchedule(settings.data);
  // A saved change starts the form again from the server's values.
  return <ScheduleFields key={JSON.stringify(saved)} saved={saved} lastRun={lastRun} />;
}
