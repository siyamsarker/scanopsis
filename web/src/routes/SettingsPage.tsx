// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleAlert, RotateCcw, TriangleAlert, Undo2 } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react';
import { describeError } from '../api/errors';
import {
  SettingsValidationError,
  useMe,
  useSettings,
  useUpdateSettings,
  type Setting,
  type SettingValue,
} from '../api/hooks';
import { Spinner } from '../components/ConfirmDialog';
import { Tag } from '../components/badges';
import { PageHeader } from '../components/layout';
import { ErrorState, ForbiddenState, Skeleton } from '../components/states';
import { toast } from '../components/Toast';
import { Tooltip } from '../components/Tooltip';
import { formatExactTime } from '../format';
import { buttonClass, inputClass, monoInputClass } from '../ui';
import { Problem, When } from './admin/kit';
import { groupOf, settingGroups, settingLabel, type SettingGroup } from './admin/settingLabels';

type Draft = string | boolean;

/** Limits of integer settings; the server enforces the same ones. */
const integerBounds: Record<string, { min: number; max: number; unit: string }> = {
  'retention.days': { min: 7, max: 3650, unit: 'days' },
};

/** The value as the form shows it: lists are one entry per line. */
function shown(setting: Setting): Draft {
  const { value } = setting;
  if (setting.type === 'boolean') return value === true;
  if (Array.isArray(value)) return value.join('\n');
  if (typeof value === 'number') return String(value);
  return typeof value === 'string' ? value : '';
}

function toValue(setting: Setting, draft: Draft): SettingValue {
  if (setting.type === 'string_list') {
    return String(draft)
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean);
  }
  if (typeof draft === 'boolean') return draft;
  const text = draft.trim();
  if (setting.type === 'integer') {
    // A value that is not a number goes to the server as text, which names the problem.
    const n = Number(text);
    return text !== '' && Number.isFinite(n) ? n : text;
  }
  return text;
}

/** A problem the browser can find before saving, in the server's words where it has them. */
function localProblem(setting: Setting, draft: Draft): string | undefined {
  const bounds = integerBounds[setting.key];
  if (bounds && typeof draft === 'string') {
    const n = Number(draft.trim());
    if (!Number.isInteger(n) || n < bounds.min || n > bounds.max) {
      return `Use a whole number from ${bounds.min} to ${bounds.max}.`;
    }
  }
  return undefined;
}

/** On and off as a switch: a button with role switch, named by the row's label. */
function Switch({
  id,
  checked,
  onChange,
  describedBy,
}: {
  id: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  describedBy: string;
}) {
  return (
    <span className="inline-flex min-h-10 items-center gap-3">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        aria-describedby={describedBy}
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition-colors duration-micro ease-enter pointer-coarse:h-7 pointer-coarse:w-12 ${
          checked ? 'border-brand bg-brand' : 'border-line-strong bg-sunken'
        }`}
      >
        <span
          aria-hidden="true"
          className={`inline-block size-4.5 rounded-full shadow-1 transition-transform duration-micro ease-enter pointer-coarse:size-5.5 ${
            checked
              ? 'translate-x-[1.375rem] bg-brand-ink pointer-coarse:translate-x-[1.5rem]'
              : 'translate-x-0.5 bg-text-muted'
          }`}
        />
      </button>
      <span aria-hidden="true" className="text-body-s text-text">
        {checked ? 'On' : 'Off'}
      </span>
    </span>
  );
}

function Control({
  setting,
  value,
  id,
  describedBy,
  invalid,
  onChange,
}: {
  setting: Setting;
  value: Draft;
  id: string;
  describedBy: string;
  invalid: boolean;
  onChange: (value: Draft) => void;
}) {
  const common = {
    id,
    'aria-describedby': describedBy,
    'aria-invalid': invalid || undefined,
  };
  if (setting.type === 'boolean') {
    return (
      <Switch id={id} checked={value === true} onChange={onChange} describedBy={describedBy} />
    );
  }
  if (setting.type === 'string_list') {
    return (
      <textarea
        {...common}
        rows={3}
        spellCheck={false}
        value={String(value)}
        placeholder="One per line"
        onChange={(e) => onChange(e.target.value)}
        className={`${monoInputClass} min-h-24 resize-y`}
      />
    );
  }
  if (setting.secret) {
    return (
      <input
        {...common}
        type="password"
        autoComplete="new-password"
        placeholder={setting.is_set ? '••••••••  saved; type to replace' : 'Not set'}
        value={String(value)}
        onChange={(e) => onChange(e.target.value)}
        className={inputClass}
      />
    );
  }
  if (setting.type === 'integer') {
    const bounds = integerBounds[setting.key];
    return (
      <span className="flex items-center gap-2">
        <input
          {...common}
          type="number"
          inputMode="numeric"
          step={1}
          min={bounds?.min}
          max={bounds?.max}
          value={String(value)}
          onChange={(e) => onChange(e.target.value)}
          className={`${monoInputClass} w-32!`}
        />
        {bounds ? <span className="text-body-s text-text-muted">{bounds.unit}</span> : null}
      </span>
    );
  }
  return (
    <input
      {...common}
      type={setting.type === 'url' ? 'url' : 'text'}
      autoComplete="off"
      spellCheck={false}
      value={String(value)}
      placeholder={setting.type === 'url' ? 'https://' : setting.type === 'duration' ? '30m' : ''}
      onChange={(e) => onChange(e.target.value)}
      className={setting.type === 'duration' ? `${monoInputClass} w-32!` : monoInputClass}
    />
  );
}

const sourceText = { default: 'Default', env: 'From environment', ui: 'Set here' } as const;

function SourceTag({ setting }: { setting: Setting }) {
  const tip =
    setting.source === 'env'
      ? `Seeded by ${setting.env_var}. A value saved here overrides it.`
      : setting.source === 'ui'
        ? `Saved here${setting.updated_by ? ` by ${setting.updated_by}` : ''}${
            setting.updated_at ? `, ${formatExactTime(setting.updated_at)}` : ''
          }. It overrides ${setting.env_var}.`
        : `Nothing set. ${setting.env_var} would seed it.`;
  return (
    <Tooltip content={tip}>
      <button
        type="button"
        className="rounded-tag"
        aria-label={`${sourceText[setting.source]}. ${tip}`}
      >
        <Tag>{sourceText[setting.source]}</Tag>
      </button>
    </Tooltip>
  );
}

interface RowProps {
  setting: Setting;
  draft: Draft | undefined;
  willReset: boolean;
  error: string | undefined;
  onDraft: (key: string, draft: Draft | undefined) => void;
  onReset: (key: string, reset: boolean) => void;
}

function SettingRow({ setting, draft, willReset, error, onDraft, onReset }: RowProps) {
  const id = useId();
  const original = shown(setting);
  const current = draft ?? original;
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hintId, error ? errorId : ''].filter(Boolean).join(' ');
  const changed = draft !== undefined || willReset;
  const disablingLocal =
    setting.key === 'auth.local_login_enabled' && original === true && draft === false;

  function change(next: Draft) {
    if (setting.secret) onDraft(setting.key, next === '' ? undefined : next);
    else onDraft(setting.key, next === original ? undefined : next);
    onReset(setting.key, false);
  }

  return (
    <div
      data-setting={setting.key}
      className={`relative grid gap-x-8 gap-y-3 px-5 py-5 sm:px-6 md:grid-cols-[minmax(0,5fr)_minmax(0,6fr)] ${
        changed ? 'bg-[color-mix(in_srgb,var(--brand)_5%,transparent)]' : ''
      }`}
    >
      {changed ? (
        <span
          aria-hidden="true"
          className="absolute inset-y-3 left-0 w-[3px] rounded-r-full bg-brand"
        />
      ) : null}
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <label htmlFor={id} className="text-body-s font-semibold text-text">
            {settingLabel(setting.key)}
          </label>
          {changed ? (
            <span className="text-caption font-medium text-brand">
              {willReset ? 'Resets on save' : 'Unsaved'}
            </span>
          ) : null}
        </div>
        <p id={hintId} className="mt-1 text-small text-text-muted">
          {setting.description}
          {setting.type === 'string_list' ? ' One per line.' : null}
          {setting.secret ? ' The saved value is never shown.' : null}
        </p>
        <p className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-text-faint">
          <SourceTag setting={setting} />
          {setting.secret ? (
            <span>{setting.is_set ? 'A value is saved' : 'No value saved'}</span>
          ) : null}
          {setting.source === 'ui' && setting.updated_at ? (
            <When at={setting.updated_at} prefix="Changed" />
          ) : null}
          <span className="font-mono">{setting.key}</span>
        </p>
      </div>
      <div className="flex min-w-0 flex-col items-start gap-2">
        <div className="w-full">
          <Control
            setting={setting}
            value={current}
            id={id}
            describedBy={describedBy}
            invalid={Boolean(error)}
            onChange={change}
          />
        </div>
        {disablingLocal ? (
          <p className="flex items-start gap-2 text-small text-text">
            <TriangleAlert
              size={16}
              strokeWidth={1.75}
              aria-hidden="true"
              className="mt-0.5 shrink-0 text-warn"
            />
            <span>
              Make sure GitLab sign-in works before you turn local accounts off, or nobody can sign
              in.
            </span>
          </p>
        ) : null}
        {error ? <Problem id={errorId}>{error}</Problem> : null}
        {setting.source === 'ui' || changed ? (
          <div className="flex flex-wrap items-center gap-2">
            {changed ? (
              <button
                type="button"
                className={buttonClass('ghost', 'sm')}
                onClick={() => {
                  onDraft(setting.key, undefined);
                  onReset(setting.key, false);
                }}
              >
                <Undo2 size={14} strokeWidth={1.75} aria-hidden="true" />
                Undo <span className="sr-only">{settingLabel(setting.key)}</span>
              </button>
            ) : (
              <button
                type="button"
                className={buttonClass('ghost', 'sm')}
                onClick={() => {
                  onDraft(setting.key, undefined);
                  onReset(setting.key, true);
                }}
              >
                <RotateCcw size={14} strokeWidth={1.75} aria-hidden="true" />
                Reset to {setting.env_var ? 'environment or default' : 'default'}{' '}
                <span className="sr-only">for {settingLabel(setting.key)}</span>
              </button>
            )}
          </div>
        ) : null}
      </div>
    </div>
  );
}

/** The section whose heading is nearest the top of the viewport. */
function useCurrentSection(ids: readonly string[]) {
  const [current, setCurrent] = useState(ids[0]);
  useEffect(() => {
    if (typeof IntersectionObserver === 'undefined') return;
    const seen = new Map<string, boolean>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) seen.set(entry.target.id, entry.isIntersecting);
        const first = ids.find((id) => seen.get(`section-${id}`));
        if (first) setCurrent(first);
      },
      { rootMargin: '-15% 0px -60% 0px' },
    );
    for (const id of ids) {
      const el = document.getElementById(`section-${id}`);
      if (el) observer.observe(el);
    }
    return () => observer.disconnect();
  }, [ids]);
  return [current, setCurrent] as const;
}

function SettingsForm({ settings }: { settings: Setting[] }) {
  const update = useUpdateSettings();
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [resets, setResets] = useState<Set<string>>(new Set());
  const [localErrors, setLocalErrors] = useState<Record<string, string>>({});
  const form = useRef<HTMLFormElement>(null);

  const groups = useMemo(
    () =>
      settingGroups
        .map((g) => ({ ...g, items: settings.filter((s) => groupOf(s.key) === g.id) }))
        .filter((g) => g.items.length > 0),
    [settings],
  );
  const ids = useMemo(() => groups.map((g) => g.id), [groups]);
  const [current, setCurrent] = useCurrentSection(ids);

  const dirtyKeys = new Set([...Object.keys(drafts), ...resets]);
  const dirty = dirtyKeys.size;
  const serverErrors = update.error instanceof SettingsValidationError ? update.error.fields : {};
  const fieldErrors = { ...serverErrors, ...localErrors };
  const dirtyIn = (group: SettingGroup) =>
    [...dirtyKeys].filter((key) => groupOf(key) === group).length;
  const errorsIn = (group: SettingGroup) =>
    Object.keys(fieldErrors).filter((key) => groupOf(key) === group).length;

  // Leaving the page with unsaved changes asks first.
  useEffect(() => {
    if (dirty === 0) return;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  function setDraft(key: string, draft: Draft | undefined) {
    setLocalErrors((prev) => {
      if (!(key in prev)) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
    setDrafts((prev) => {
      const next = { ...prev };
      if (draft === undefined) delete next[key];
      else next[key] = draft;
      return next;
    });
  }

  function setReset(key: string, reset: boolean) {
    setResets((prev) => {
      if (prev.has(key) === reset) return prev;
      const next = new Set(prev);
      if (reset) next.add(key);
      else next.delete(key);
      return next;
    });
  }

  function discard() {
    setDrafts({});
    setResets(new Set());
    setLocalErrors({});
    update.reset();
  }

  function focusFirstError(keys: string[]) {
    window.requestAnimationFrame(() => {
      const row = keys
        .map((key) =>
          form.current?.querySelector<HTMLElement>(`[data-setting="${CSS.escape(key)}"]`),
        )
        .find(Boolean);
      row?.scrollIntoView({ block: 'center', behavior: 'smooth' });
      row
        ?.querySelector<HTMLElement>('input, textarea, button[role="switch"]')
        ?.focus({ preventScroll: true });
    });
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    const changes: Record<string, SettingValue> = {};
    const problems: Record<string, string> = {};
    for (const setting of settings) {
      if (resets.has(setting.key)) changes[setting.key] = null;
      else if (setting.key in drafts) {
        const problem = localProblem(setting, drafts[setting.key]);
        if (problem) problems[setting.key] = problem;
        changes[setting.key] = toValue(setting, drafts[setting.key]);
      }
    }
    if (Object.keys(problems).length > 0) {
      setLocalErrors(problems);
      focusFirstError(Object.keys(problems));
      return;
    }
    const count = Object.keys(changes).length;
    update.mutate(changes, {
      onSuccess: () => {
        setDrafts({});
        setResets(new Set());
        toast.success('Settings saved', {
          description: `${count === 1 ? 'The change applies' : `${count} changes apply`} now.`,
        });
      },
      // The save bar says what went wrong; the fields say where.
      onError: (error) => {
        if (error instanceof SettingsValidationError) focusFirstError(Object.keys(error.fields));
      },
    });
  }

  return (
    <div className="grid gap-8 lg:grid-cols-[12rem_minmax(0,1fr)]">
      <nav aria-label="Settings sections" className="min-w-0 lg:sticky lg:top-8 lg:self-start">
        <ul className="-mx-1 flex gap-1 overflow-x-auto px-1 pb-1 [scrollbar-width:none] lg:flex-col lg:overflow-visible">
          {groups.map((g) => {
            const pending = dirtyIn(g.id);
            const errors = errorsIn(g.id);
            const active = current === g.id;
            return (
              <li key={g.id} className="shrink-0">
                <a
                  href={`#section-${g.id}`}
                  aria-current={active ? 'true' : undefined}
                  onClick={() => setCurrent(g.id)}
                  className={`relative flex min-h-9 items-center justify-between gap-3 rounded-inner px-3 text-body-s whitespace-nowrap transition-colors duration-micro pointer-coarse:min-h-11 ${
                    active
                      ? 'bg-surface-raised font-medium text-text shadow-[inset_0_0_0_1px_var(--line)]'
                      : 'text-text-muted hover:bg-wash hover:text-text'
                  }`}
                >
                  {active ? (
                    <span
                      aria-hidden="true"
                      className="absolute inset-y-2 left-0 hidden w-0.5 rounded-full bg-brand lg:block"
                    />
                  ) : null}
                  {g.title}{' '}
                  {errors > 0 ? (
                    <span className="tone-fail tone-fg text-caption font-semibold tabular-nums">
                      {errors}
                      <span className="sr-only"> to fix</span>
                    </span>
                  ) : pending > 0 ? (
                    <span className="rounded-full bg-[color-mix(in_srgb,var(--brand)_18%,transparent)] px-1.5 text-caption font-medium text-text tabular-nums">
                      {pending}
                      <span className="sr-only"> unsaved</span>
                    </span>
                  ) : null}
                </a>
              </li>
            );
          })}
        </ul>
      </nav>
      <form ref={form} onSubmit={submit} noValidate className="flex min-w-0 flex-col gap-10">
        {groups.map((g) => (
          <section
            key={g.id}
            id={`section-${g.id}`}
            aria-labelledby={`heading-${g.id}`}
            className="scroll-mt-20 lg:scroll-mt-8"
          >
            <h2 id={`heading-${g.id}`} className="text-h3 font-semibold tracking-tight text-text">
              {g.title}
            </h2>
            <p className="mt-0.5 mb-4 text-body-s text-text-muted">{g.lead}</p>
            <div className="panel divide-y divide-line overflow-hidden">
              {g.items.map((setting) => (
                <SettingRow
                  key={setting.key}
                  setting={setting}
                  draft={drafts[setting.key]}
                  willReset={resets.has(setting.key)}
                  error={fieldErrors[setting.key]}
                  onDraft={setDraft}
                  onReset={setReset}
                />
              ))}
            </div>
          </section>
        ))}
        <div
          className={`sticky bottom-4 z-20 transition-[opacity,translate] duration-enter ease-enter ${
            dirty > 0 ? 'translate-y-0 opacity-100' : 'pointer-events-none translate-y-3 opacity-0'
          }`}
          aria-hidden={dirty === 0 || undefined}
        >
          <div
            role="region"
            aria-label="Unsaved changes"
            className="overlay flex flex-wrap items-center justify-between gap-3 rounded-panel px-4 py-3 sm:px-5"
          >
            <div className="min-w-0" aria-live="polite">
              <p className="flex items-center gap-2.5 text-body-s text-text">
                <span aria-hidden="true" className="size-2 rounded-full bg-brand" />
                <span className="tabular-nums">
                  {dirty} unsaved {dirty === 1 ? 'change' : 'changes'}
                </span>
              </p>
              {update.isError ? (
                <p className="mt-1 flex items-start gap-2 text-small text-text">
                  <CircleAlert
                    size={14}
                    strokeWidth={1.75}
                    aria-hidden="true"
                    className="mt-0.5 shrink-0 text-fail"
                  />
                  <span>
                    <span className="font-medium">Nothing was saved.</span>{' '}
                    <span>
                      {update.error instanceof SettingsValidationError
                        ? 'Fix the settings marked in red, then save again.'
                        : describeError(update.error)}
                    </span>
                  </span>
                </p>
              ) : null}
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                className={buttonClass('ghost')}
                onClick={discard}
                disabled={update.isPending}
                tabIndex={dirty === 0 ? -1 : undefined}
              >
                Discard
              </button>
              <button
                type="submit"
                className={buttonClass('primary')}
                disabled={dirty === 0 || update.isPending}
                aria-busy={update.isPending || undefined}
                tabIndex={dirty === 0 ? -1 : undefined}
              >
                {update.isPending ? <Spinner /> : null}
                Save changes
              </button>
            </div>
          </div>
        </div>
      </form>
    </div>
  );
}

export function SettingsPage() {
  const me = useMe();
  const isAdmin = me.data?.role === 'admin';
  const settings = useSettings(isAdmin);

  let body;
  if (me.isPending) {
    body = <Skeleton className="h-96 w-full" />;
  } else if (!isAdmin) {
    body = <ForbiddenState title="You need the admin role to see this page." />;
  } else if (settings.isPending) {
    body = (
      <div
        className="grid gap-8 lg:grid-cols-[12rem_minmax(0,1fr)]"
        role="status"
        aria-label="Loading"
      >
        <Skeleton className="hidden h-56 lg:block" />
        <div className="flex flex-col gap-4">
          <Skeleton className="h-7 w-40" />
          <Skeleton className="h-64" />
        </div>
      </div>
    );
  } else if (settings.isError) {
    body = (
      <ErrorState
        title="Settings could not be loaded"
        message={describeError(settings.error)}
        onRetry={() => void settings.refetch()}
        retrying={settings.isFetching}
      />
    );
  } else {
    body = <SettingsForm settings={settings.data} />;
  }

  return (
    <>
      <PageHeader
        title="Settings"
        subtitle="Changes apply as soon as you save them, without a restart. A value saved here overrides the environment."
      />
      {body}
    </>
  );
}
