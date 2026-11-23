// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Clock3, Terminal, Upload, Webhook, Workflow, X, type LucideIcon } from 'lucide-react';
import { useMemo } from 'react';
import { useSearchParams } from 'react-router';
import { useAuditLog, type AuditEvent } from '../api/admin';
import { describeError } from '../api/errors';
import { Pagination } from '../components/Pagination';
import { SearchInput, Select } from '../components/inputs';
import { EmptyState, ErrorState, Skeleton } from '../components/states';
import { formatExactTime } from '../format';
import { buttonClass } from '../ui';
import { AdminGate } from './admin/AdminGate';
import { AuditDetails, ChangeList, describeEvent, type EventTone } from './admin/AuditDetails';
import { InitialsAvatar } from './admin/kit';
import { auditActions } from './admin/labels';

const actionOptions = [
  { value: '', label: 'All actions' },
  ...Object.entries(auditActions)
    .map(([value, label]) => ({ value, label }))
    .sort((a, b) => a.label.localeCompare(b.label)),
];

/**
 * Who acted: "local:root" reads "root", a local account. People get their
 * initials; automated actors (CI, SonarQube, the clean-up, the upload
 * endpoint) get an icon instead.
 */
export function describeActor(actor: string): { name: string; kind: string; icon?: LucideIcon } {
  const at = actor.indexOf(':');
  const prefix = at >= 0 ? actor.slice(0, at) : actor;
  const rest = at >= 0 ? actor.slice(at + 1) : '';
  switch (prefix) {
    case 'local':
      return { name: rest, kind: 'Local account' };
    case 'gitlab':
      return { name: rest, kind: 'GitLab account' };
    case 'ci':
      return { name: rest, kind: 'GitLab CI pipeline', icon: Workflow };
    case 'sonarqube':
      return {
        name: 'SonarQube',
        kind: rest === 'unknown' ? 'Unverified webhook sender' : 'SonarQube webhook',
        icon: Webhook,
      };
    case 'retention':
      return { name: 'Retention', kind: 'Scheduled clean-up', icon: Clock3 };
    case 'cli':
      return { name: 'Command line', kind: 'Server command line', icon: Terminal };
    case 'ingest':
      return { name: 'Scan upload', kind: 'Upload endpoint', icon: Upload };
    default:
      return { name: actor, kind: '' };
  }
}

const dayKey = (iso: string) => {
  const d = new Date(iso);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
};

function dayTitle(iso: string, now = new Date()): string {
  const d = new Date(iso);
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((start(now) - start(d)) / 86_400_000);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  return new Intl.DateTimeFormat('en-GB', {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    ...(d.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}),
  }).format(d);
}

const clock = (iso: string) =>
  new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(
    new Date(iso),
  );

const nodeTone: Record<EventTone, string> = {
  neutral: 'border-line-strong bg-surface text-text-muted',
  warn: 'tone-warn border-[color-mix(in_srgb,var(--tone)_55%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_12%,var(--surface))] tone-fg',
  fail: 'tone-fail border-[color-mix(in_srgb,var(--tone)_55%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_12%,var(--surface))] tone-fg',
};

function EventRow({ event }: { event: AuditEvent }) {
  const described = describeEvent(event.action, event.detail);
  const actor = describeActor(event.actor);
  const Icon = described.icon;
  return (
    <li className="relative grid grid-cols-[3rem_2rem_minmax(0,1fr)] gap-x-3 py-3 sm:grid-cols-[3.5rem_2rem_minmax(0,1fr)]">
      <time
        dateTime={event.at}
        title={formatExactTime(event.at)}
        className="pt-1.5 text-right text-small text-text-muted tabular-nums"
      >
        {clock(event.at)}
      </time>
      <span
        className={`relative z-[1] mt-0.5 flex size-8 items-center justify-center rounded-full border ${nodeTone[described.tone]}`}
      >
        <Icon size={15} strokeWidth={1.75} aria-hidden="true" />
        <span className="sr-only">{auditActions[event.action] ?? event.action}</span>
      </span>
      <div className="min-w-0 pt-1">
        <p className="text-body-s leading-7 text-text-muted">
          <span className="mr-1.5 inline-flex items-center gap-1.5 align-middle">
            {actor.icon ? (
              <span
                aria-hidden="true"
                className="inline-flex size-7 shrink-0 items-center justify-center rounded-[8px] border border-line bg-sunken text-text-muted"
              >
                <actor.icon size={14} strokeWidth={1.75} />
              </span>
            ) : (
              <InitialsAvatar name={actor.name} size="sm" />
            )}
            <span
              className="font-medium text-text"
              title={actor.kind ? `${actor.kind}: ${event.actor}` : event.actor}
            >
              {actor.name}
            </span>
            {actor.kind ? <span className="sr-only"> ({actor.kind}) </span> : null}
          </span>
          <span>{described.sentence}</span>
        </p>
        {described.changes ? <ChangeList changes={described.changes} /> : null}
        <AuditDetails detail={event.detail} used={described.used} />
      </div>
    </li>
  );
}

function Timeline({ events }: { events: AuditEvent[] }) {
  const days = useMemo(() => {
    const groups: { key: string; title: string; events: AuditEvent[] }[] = [];
    // Newest first by time; pages arrive newest first by id, which can differ slightly.
    const sorted = [...events].sort((a, b) => Date.parse(b.at) - Date.parse(a.at) || b.id - a.id);
    for (const event of sorted) {
      const key = dayKey(event.at);
      let group = groups[groups.length - 1];
      if (!group || group.key !== key) {
        group = { key, title: dayTitle(event.at), events: [] };
        groups.push(group);
      }
      group.events.push(event);
    }
    return groups;
  }, [events]);

  return (
    <div className="flex flex-col gap-2">
      {days.map((day) => (
        <section key={day.key} aria-labelledby={`day-${day.key}`}>
          <h2
            id={`day-${day.key}`}
            className="sticky top-14 z-10 -mx-1 flex items-baseline justify-between gap-3 border-b border-line bg-[color-mix(in_srgb,var(--bg)_92%,transparent)] px-1 py-2.5 backdrop-blur-sm md:top-0"
          >
            <span className="text-body font-semibold tracking-tight text-text">{day.title}</span>{' '}
            <span className="text-small text-text-muted tabular-nums">
              {day.events.length} {day.events.length === 1 ? 'event' : 'events'}
            </span>
          </h2>
          <ol className="relative before:absolute before:top-0 before:bottom-0 before:left-[calc(3rem+0.75rem+1rem-0.5px)] before:w-px before:bg-line sm:before:left-[calc(3.5rem+0.75rem+1rem-0.5px)]">
            {day.events.map((event) => (
              <EventRow key={event.id} event={event} />
            ))}
          </ol>
        </section>
      ))}
    </div>
  );
}

function AuditLog() {
  const [params, setParams] = useSearchParams();
  const action = params.get('action') ?? '';
  const actor = params.get('actor') ?? '';
  const audit = useAuditLog({ action, actor }, true);
  const rows = audit.data?.pages.flatMap((page) => page.items) ?? [];
  const filtered = Boolean(action || actor);

  function set(name: string, value: string) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(name, value);
        else next.delete(name);
        return next;
      },
      { replace: true },
    );
  }

  return (
    <>
      <div className="mb-6 flex flex-wrap items-end gap-3">
        <Select
          label="Action"
          value={action}
          onChange={(v) => set('action', v)}
          options={actionOptions}
          className="w-full sm:w-60"
        />
        <SearchInput
          label="Actor"
          hideLabel={false}
          placeholder="Name, username or project"
          value={actor}
          onChange={(v) => set('actor', v)}
          primary
          className="w-full sm:w-72"
        />
        {filtered ? (
          <button
            type="button"
            className={buttonClass('ghost', 'md')}
            onClick={() => setParams(new URLSearchParams(), { replace: true })}
          >
            <X size={16} strokeWidth={1.75} aria-hidden="true" />
            Clear filters
          </button>
        ) : null}
      </div>
      {audit.isError ? (
        <ErrorState
          title="The audit log could not be loaded"
          message={describeError(audit.error)}
          onRetry={() => void audit.refetch()}
          retrying={audit.isFetching}
        />
      ) : audit.isPending ? (
        <div role="status" aria-label="Loading" className="flex flex-col gap-4">
          {[0, 1, 2, 3, 4].map((i) => (
            <div key={i} className="flex items-center gap-4">
              <Skeleton className="h-4 w-10" />
              <Skeleton className="size-8 rounded-full" />
              <Skeleton className="h-4 w-80 max-w-full" />
            </div>
          ))}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          art={filtered ? 'search' : 'timeline'}
          title={filtered ? 'No events match' : 'No events yet'}
          body={
            filtered
              ? 'Try another action, or a part of the actor’s name.'
              : 'Sign-ins, changes and refused uploads are recorded here as they happen.'
          }
        />
      ) : (
        <>
          <Timeline events={rows} />
          <Pagination
            className="mt-6"
            shown={rows.length}
            hasMore={audit.hasNextPage}
            loading={audit.isFetchingNextPage}
            onLoadMore={() => void audit.fetchNextPage()}
            noun="event"
          />
        </>
      )}
    </>
  );
}

export function AuditPage() {
  return (
    <AdminGate
      title="Audit log"
      subtitle="Who did what and when: sign-ins, changes to users, services and settings, and refused uploads."
    >
      <AuditLog />
    </AdminGate>
  );
}
