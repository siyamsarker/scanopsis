// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Check, TriangleAlert } from 'lucide-react';
import { useId, useState, type FormEvent } from 'react';
import {
  SettingsValidationError,
  useMe,
  useSettings,
  useUpdateSettings,
  type Setting,
  type SettingValue,
} from '../api/hooks';
import { ErrorNote } from '../components/ErrorNote';
import {
  cardClass,
  headingClass,
  inputClass,
  monoInputClass,
  primaryButtonClass,
  secondaryButtonClass,
} from '../ui';
import { formatRelative } from './UsersPage';

type Draft = string | boolean;

const groups = ['GitLab', 'Sign-in', 'Sessions', 'Scan uploads', 'SonarQube'] as const;

function groupOf(key: string): (typeof groups)[number] {
  if (key.startsWith('gitlab.')) return 'GitLab';
  if (key.startsWith('session')) return 'Sessions';
  if (key.startsWith('ingest.')) return 'Scan uploads';
  if (key.startsWith('sonar.')) return 'SonarQube';
  return 'Sign-in';
}

const sourceLabels = { default: 'Default', env: 'Environment', ui: 'Set here' } as const;

/** The value as the form shows it: lists are one entry per line. */
function shown(setting: Setting): Draft {
  const { value } = setting;
  if (setting.type === 'boolean') return value === true;
  if (Array.isArray(value)) return value.join('\n');
  return typeof value === 'string' ? value : '';
}

function toValue(setting: Setting, draft: Draft): SettingValue {
  if (setting.type === 'string_list') {
    return String(draft)
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean);
  }
  return typeof draft === 'boolean' ? draft : draft.trim();
}

function SourceBadge({ source }: { source: Setting['source'] }) {
  return (
    <span className="rounded-control border border-border px-1.5 py-0.5 text-xs text-text-muted">
      {sourceLabels[source]}
    </span>
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
      <label className="inline-flex min-h-10 items-center gap-3 text-text">
        <input
          {...common}
          type="checkbox"
          role="switch"
          checked={value === true}
          onChange={(e) => onChange(e.target.checked)}
          className="size-5 accent-brand"
        />
        {value === true ? 'On' : 'Off'}
      </label>
    );
  }
  if (setting.type === 'string_list') {
    return (
      <textarea
        {...common}
        rows={4}
        spellCheck={false}
        value={String(value)}
        onChange={(e) => onChange(e.target.value)}
        className={`${monoInputClass} resize-y`}
      />
    );
  }
  if (setting.secret) {
    return (
      <input
        {...common}
        type="password"
        autoComplete="new-password"
        placeholder={setting.is_set ? 'Enter a new value to replace it' : 'Enter a value'}
        value={String(value)}
        onChange={(e) => onChange(e.target.value)}
        className={inputClass}
      />
    );
  }
  return (
    <input
      {...common}
      type={setting.type === 'url' ? 'url' : 'text'}
      autoComplete="off"
      spellCheck={false}
      value={String(value)}
      onChange={(e) => onChange(e.target.value)}
      className={setting.type === 'duration' ? monoInputClass : inputClass}
    />
  );
}

function SettingRow({ setting, draft, willReset, error, onDraft, onReset }: RowProps) {
  const id = useId();
  const original = shown(setting);
  const current = draft ?? original;
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hintId, error ? errorId : ''].filter(Boolean).join(' ');
  const disablingLocal =
    setting.type === 'boolean' && /local/.test(setting.key) && original === true && draft === false;

  function change(next: Draft) {
    if (setting.secret) {
      onDraft(setting.key, next === '' ? undefined : next);
    } else {
      onDraft(setting.key, next === original ? undefined : next);
    }
    onReset(setting.key, false);
  }

  return (
    <div className="grid gap-3 px-4 py-5 sm:px-6 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)] md:gap-8">
      <div className="min-w-0">
        <label htmlFor={id} className="font-medium text-text">
          {setting.description}
        </label>
        <p className="mt-1 break-all font-mono text-[13px] text-text-muted">{setting.key}</p>
        <p className="mt-2 flex flex-wrap items-center gap-2 text-text-muted">
          <SourceBadge source={setting.source} />
          {setting.secret && <span>{setting.is_set ? 'Set' : 'Not set'}</span>}
        </p>
        <p id={hintId} className="mt-2 text-text-muted">
          {setting.source === 'ui' && setting.updated_by && setting.updated_at ? (
            <>
              Changed by {setting.updated_by}{' '}
              <time dateTime={setting.updated_at}>{formatRelative(setting.updated_at)}</time>.
            </>
          ) : (
            <>
              Seeded by <span className="font-mono text-text">{setting.env_var}</span>.
            </>
          )}
          {setting.type === 'duration' && ' Use a duration such as 8h or 30m.'}
          {setting.type === 'string_list' && ' One entry per line.'}
          {setting.secret && ' The saved value is never shown.'}
        </p>
      </div>
      <div className="flex min-w-0 flex-col gap-3">
        <Control
          setting={setting}
          value={current}
          id={id}
          describedBy={describedBy}
          invalid={Boolean(error)}
          onChange={change}
        />
        {disablingLocal && (
          <p className="flex items-start gap-2 text-text">
            <TriangleAlert
              size={16}
              strokeWidth={1.5}
              aria-hidden="true"
              className="mt-0.5 shrink-0 text-warn"
            />
            <span>
              Before you turn local sign-in off, make sure GitLab sign-in works. Otherwise nobody
              can sign in.
            </span>
          </p>
        )}
        {error && (
          <p id={errorId} className="text-text">
            {error}
          </p>
        )}
        {setting.source === 'ui' && (
          <div className="flex flex-wrap items-center gap-3">
            {willReset ? (
              <>
                <span className="text-text-muted">Will reset when you save.</span>
                <button
                  type="button"
                  className={secondaryButtonClass}
                  onClick={() => onReset(setting.key, false)}
                >
                  Undo
                </button>
              </>
            ) : (
              <button
                type="button"
                className={secondaryButtonClass}
                onClick={() => {
                  onDraft(setting.key, undefined);
                  onReset(setting.key, true);
                }}
              >
                Reset to environment or default <span className="sr-only">for {setting.key}</span>
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function SettingsForm({ settings }: { settings: Setting[] }) {
  const update = useUpdateSettings();
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [resets, setResets] = useState<Set<string>>(new Set());

  const dirty = Object.keys(drafts).length + resets.size;
  const fieldErrors = update.error instanceof SettingsValidationError ? update.error.fields : {};

  function setDraft(key: string, draft: Draft | undefined) {
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

  function submit(event: FormEvent) {
    event.preventDefault();
    const changes: Record<string, SettingValue> = {};
    for (const setting of settings) {
      if (resets.has(setting.key)) changes[setting.key] = null;
      else if (setting.key in drafts) changes[setting.key] = toValue(setting, drafts[setting.key]);
    }
    update.mutate(changes, {
      onSuccess: () => {
        setDrafts({});
        setResets(new Set());
      },
    });
  }

  return (
    <form onSubmit={submit} className="mt-6 flex flex-col gap-8">
      {groups.map((group) => {
        const items = settings.filter((s) => groupOf(s.key) === group);
        if (items.length === 0) return null;
        return (
          <section key={group} aria-labelledby={`group-${group}`}>
            <h2 id={`group-${group}`} className="mb-3 text-lg font-semibold text-text">
              {group}
            </h2>
            <div className={`${cardClass} divide-y divide-border`}>
              {items.map((setting) => (
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
        );
      })}
      <div className="flex flex-col gap-3">
        {update.isError && (
          <ErrorNote>
            {update.error instanceof SettingsValidationError
              ? 'Some settings need attention. Nothing was saved.'
              : update.error.message}
          </ErrorNote>
        )}
        <div className="flex flex-wrap items-center gap-4">
          <button
            type="submit"
            disabled={dirty === 0 || update.isPending}
            className={primaryButtonClass}
          >
            Save changes
          </button>
          <span role="status" className="flex items-center gap-2 text-text-muted">
            {update.isSuccess && dirty === 0 && (
              <>
                <Check size={16} strokeWidth={1.5} aria-hidden="true" className="text-pass" />
                Settings saved. Changes apply now.
              </>
            )}
          </span>
        </div>
      </div>
    </form>
  );
}

export function SettingsPage() {
  const me = useMe();
  const isAdmin = me.data?.role === 'admin';
  const settings = useSettings(isAdmin);

  let body;
  if (!isAdmin) {
    body = <p className="mt-4 text-text-muted">You need the admin role to see this page.</p>;
  } else if (settings.isPending) {
    body = (
      <p role="status" className="mt-4 text-text-muted">
        Loading settings…
      </p>
    );
  } else if (settings.isError) {
    body = (
      <p role="alert" className="mt-4 text-text">
        {settings.error.message} Reload the page to try again.
      </p>
    );
  } else {
    body = <SettingsForm settings={settings.data} />;
  }

  return (
    <>
      <h1 className={headingClass}>Settings</h1>
      <p className="mt-1 text-text-muted">
        Runtime settings. Each one shows where its value comes from.
      </p>
      {body}
    </>
  );
}
