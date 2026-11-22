// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import {
  ArrowRight,
  Ban,
  Box,
  Eraser,
  FileDown,
  KeyRound,
  LogIn,
  LogOut,
  MonitorX,
  PackagePlus,
  PackageX,
  Rocket,
  Settings2,
  ShieldAlert,
  ShieldCheck,
  ShieldX,
  UserCheck,
  UserCog,
  UserMinus,
  UserPlus,
  UserX,
  Webhook,
  type LucideIcon,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { formatExactTime } from '../../format';
import { auditActions, rejectionLabels } from './labels';
import { settingLabel } from './settingLabels';

/*
 * Audit events in plain words. Each event gets an icon, a tone, a sentence
 * after the actor's name ("changed 2 settings") and, where the detail allows,
 * a small before and after view. Detail keys the sentence does not use are
 * listed quietly underneath. Values render as text only (React escapes them).
 */

export type EventTone = 'neutral' | 'warn' | 'fail';

interface Described {
  icon: LucideIcon;
  tone: EventTone;
  sentence: ReactNode;
  /** Before and after values, one row each. */
  changes?: Change[];
  /** Detail keys already said in the sentence or the changes. */
  used: string[];
}

export interface Change {
  label: string;
  from: unknown;
  to: unknown;
  /** Secret values are never logged: only that they changed. */
  secret?: boolean;
  reset?: boolean;
  mono?: boolean;
}

type Detail = Record<string, unknown>;

const str = (v: unknown) => (typeof v === 'string' ? v : undefined);
const num = (v: unknown) => (typeof v === 'number' ? v : undefined);

function Strong({ children }: { children: ReactNode }) {
  return <span className="font-medium text-text">{children}</span>;
}

function Code({ children }: { children: ReactNode }) {
  return <span className="font-mono text-[0.93em] text-text">{children}</span>;
}

const methodWords: Record<string, string> = {
  gitlab: 'with GitLab',
  local: 'with a password',
  totp: 'with an authenticator code',
  recovery: 'with a recovery code',
};

const roleWord = (v: unknown) => (v === 'admin' ? 'Admin' : v === 'viewer' ? 'Viewer' : v);

function plural(n: number, one: string, other = `${one}s`) {
  return `${n} ${n === 1 ? one : other}`;
}

/** The icon, tone, sentence and changes of one event. */
export function describeEvent(action: string, d: Detail): Described {
  const username = str(d.username);
  const path = str(d.gitlab_project_path);
  const method = str(d.method);
  const how = method ? methodWords[method] : undefined;
  switch (action) {
    case 'login.succeeded':
      return {
        icon: LogIn,
        tone: 'neutral',
        sentence: <>signed in {how}</>,
        used: ['method', 'username'],
      };
    case 'login.failed':
      return {
        icon: ShieldX,
        tone: 'warn',
        sentence: (
          <>
            could not sign in {how}
            {str(d.reason) ? (
              <>
                : <Code>{str(d.reason)}</Code>
              </>
            ) : null}
          </>
        ),
        used: ['method', 'reason', 'username'],
      };
    case 'logout':
      return { icon: LogOut, tone: 'neutral', sentence: 'signed out', used: [] };
    case 'session.revoked':
      return {
        icon: MonitorX,
        tone: 'neutral',
        sentence:
          d.scope === 'others'
            ? `ended ${plural(num(d.count) ?? 0, 'other session')}`
            : 'ended one of their sessions',
        used: ['scope', 'count', 'session_id'],
      };
    case 'user.created':
      return {
        icon: UserPlus,
        tone: 'neutral',
        sentence: (
          <>
            created <Strong>{username}</Strong>
          </>
        ),
        used: ['username'],
      };
    case 'user.role_changed':
      return {
        icon: UserCog,
        tone: 'neutral',
        sentence: (
          <>
            changed the role of <Strong>{username}</Strong>
          </>
        ),
        changes: [{ label: 'Role', from: roleWord(d.from), to: roleWord(d.to) }],
        used: ['username', 'user_id', 'from', 'to'],
      };
    case 'user.password_changed':
      return {
        icon: KeyRound,
        tone: 'neutral',
        sentence: 'changed their password',
        used: ['username'],
      };
    case 'user.password_reset':
      return {
        icon: KeyRound,
        tone: 'warn',
        sentence: (
          <>
            reset the password of <Strong>{username ?? 'a user'}</Strong> from the command line
          </>
        ),
        used: ['username'],
      };
    case 'user.disabled':
      return {
        icon: UserX,
        tone: 'fail',
        sentence: (
          <>
            disabled <Strong>{username}</Strong>
          </>
        ),
        used: ['username', 'user_id', 'source'],
      };
    case 'user.enabled':
      return {
        icon: UserCheck,
        tone: 'neutral',
        sentence: (
          <>
            enabled <Strong>{username}</Strong>
          </>
        ),
        used: ['username', 'user_id', 'source'],
      };
    case 'user.deleted':
      return {
        icon: UserMinus,
        tone: 'fail',
        sentence: (
          <>
            deleted <Strong>{username}</Strong>
          </>
        ),
        used: ['username', 'user_id', 'source'],
      };
    case 'mfa.enrolled':
      return {
        icon: ShieldCheck,
        tone: 'neutral',
        sentence: 'set up two-factor sign-in',
        used: ['method'],
      };
    case 'mfa.verified':
      return {
        icon: ShieldCheck,
        tone: 'neutral',
        sentence: <>passed two-factor {how}</>,
        used: ['method'],
      };
    case 'mfa.failed':
      return {
        icon: ShieldAlert,
        tone: 'warn',
        sentence: <>entered a wrong two-factor code {how}</>,
        used: ['method'],
      };
    case 'mfa.locked':
      return {
        icon: ShieldX,
        tone: 'fail',
        sentence: 'was locked out of two-factor after too many wrong codes',
        used: ['method'],
      };
    case 'mfa.reset':
      return {
        icon: ShieldAlert,
        tone: 'warn',
        sentence: (
          <>
            reset two-factor for <Strong>{username}</Strong>
          </>
        ),
        used: ['username', 'user_id'],
      };
    case 'setup.completed':
      return {
        icon: Rocket,
        tone: 'neutral',
        sentence: (
          <>
            completed setup as <Strong>{username}</Strong>
          </>
        ),
        used: ['username'],
      };
    case 'settings.changed': {
      const list = Array.isArray(d.changes) ? (d.changes as Detail[]) : [];
      const keys = Array.isArray(d.keys) ? d.keys.length : list.length;
      return {
        icon: Settings2,
        tone: 'neutral',
        sentence: `changed ${plural(keys, 'setting')}`,
        changes: list.map((c) => ({
          label: settingLabel(String(c.key)),
          from: c.from,
          to: c.to,
          secret: c.changed === true,
          reset: c.reset === true,
          mono: true,
        })),
        used: ['keys', 'changes'],
      };
    }
    case 'service.created':
      return {
        icon: PackagePlus,
        tone: 'neutral',
        sentence: (
          <>
            added <Code>{path}</Code>
            {str(d.reason) ? <span className="text-text-muted">, {str(d.reason)}</span> : null}
          </>
        ),
        used: ['id', 'gitlab_project_path', 'reason', 'gitlab_project_id'],
      };
    case 'service.updated': {
      const changes: Change[] = [];
      const ref = d.default_ref as Detail | undefined;
      if (ref && typeof ref === 'object') {
        changes.push({
          label: 'Dashboard branch',
          from: ref.from ?? 'Latest scanned',
          to: ref.to ?? 'Latest scanned',
          mono: true,
        });
      }
      if (str(d.old_gitlab_project_path)) {
        changes.push({
          label: 'Project path',
          from: d.old_gitlab_project_path,
          to: path,
          mono: true,
        });
      }
      const fields = Array.isArray(d.fields)
        ? (d.fields as string[]).filter((f) => f !== 'default_ref')
        : [];
      const fieldWords: Record<string, string> = {
        name: 'name',
        sonar_project_key: 'SonarQube key',
        archived: 'archive state',
      };
      return {
        icon: Box,
        tone: 'neutral',
        sentence: (
          <>
            updated <Code>{path}</Code>
            {fields.length > 0 ? (
              <span className="text-text-muted">
                : {fields.map((f) => fieldWords[f] ?? f).join(', ')}
              </span>
            ) : null}
            {str(d.reason) ? <span className="text-text-muted">, {str(d.reason)}</span> : null}
          </>
        ),
        changes,
        used: [
          'id',
          'gitlab_project_path',
          'fields',
          'default_ref',
          'reason',
          'old_gitlab_project_path',
        ],
      };
    }
    case 'service.deleted':
      return {
        icon: PackageX,
        tone: 'fail',
        sentence: (
          <>
            deleted <Code>{path}</Code>
          </>
        ),
        used: ['id', 'gitlab_project_path'],
      };
    case 'ingest.rejected': {
      const reason = str(d.reason) ?? '';
      return {
        icon: Ban,
        tone: 'warn',
        sentence: (
          <>
            refused: <Strong>{(rejectionLabels[reason] ?? reason).toLowerCase()}</Strong>
            {str(d.project_path) ? (
              <>
                , from <Code>{str(d.project_path)}</Code>
              </>
            ) : null}
          </>
        ),
        used: ['reason', 'project_path'],
      };
    }
    case 'webhook.rejected':
      return {
        icon: Webhook,
        tone: 'warn',
        sentence: (
          <>
            sent a webhook that was refused: <Code>{str(d.reason)}</Code>
          </>
        ),
        used: ['reason'],
      };
    case 'report.exported':
      return { icon: FileDown, tone: 'neutral', sentence: 'exported a scan report', used: [] };
    case 'retention.deleted': {
      const parts = [
        ['scans', 'scan'],
        ['sonar_analyses', 'analysis'],
        ['audit_events', 'audit event'],
      ]
        .map(([key, one]) => [num(d[key]) ?? 0, one] as const)
        .filter(([n]) => n > 0)
        .map(([n, one]) => plural(n, one, one === 'analysis' ? 'analyses' : `${one}s`));
      return {
        icon: Eraser,
        tone: 'neutral',
        sentence: parts.length
          ? `deleted old data: ${parts.join(', ')}`
          : 'found no old data to delete',
        used: ['scans', 'sonar_analyses', 'audit_events', 'retention_days'],
      };
    }
    default:
      return { icon: Box, tone: 'neutral', sentence: auditActions[action] ?? action, used: [] };
  }
}

function show(value: unknown): string {
  if (value === null || value === undefined || value === '') return 'empty';
  if (typeof value === 'string') return value;
  if (typeof value === 'boolean') return value ? 'On' : 'Off';
  if (Array.isArray(value)) return value.length ? value.join(', ') : 'none';
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
}

const MAX = 80;

/** Before and after, one row per change: label, old value struck through, new value. */
export function ChangeList({ changes }: { changes: Change[] }) {
  if (changes.length === 0) return null;
  return (
    <dl className="mt-2 grid max-w-2xl gap-px overflow-hidden rounded-inner border border-line bg-line text-small">
      {changes.map((c, i) => (
        <div
          key={`${c.label}-${i}`}
          className="grid grid-cols-1 gap-x-3 gap-y-0.5 bg-sunken px-3 py-2 sm:grid-cols-[minmax(8rem,12rem)_minmax(0,1fr)]"
        >
          <dt className="text-text-muted">{c.label}</dt>
          <dd className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
            {c.secret ? (
              <span className="text-text">Changed; secret values are never recorded</span>
            ) : (
              <>
                <span
                  className={`min-w-0 break-all text-text-muted line-through decoration-[color-mix(in_srgb,var(--text-muted)_60%,transparent)] ${c.mono ? 'font-mono' : ''}`}
                >
                  <span className="sr-only">from </span>
                  {clip(show(c.from))}
                </span>{' '}
                <ArrowRight
                  size={12}
                  strokeWidth={2}
                  aria-hidden="true"
                  className="shrink-0 text-text-faint"
                />
                <span className={`min-w-0 break-all text-text ${c.mono ? 'font-mono' : ''}`}>
                  <span className="sr-only">to </span>
                  {clip(show(c.to))}
                </span>
              </>
            )}
            {c.reset ? <span className="text-caption text-text-faint">(reset)</span> : null}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function clip(text: string) {
  return text.length > MAX ? `${text.slice(0, MAX)}…` : text;
}

/** An RFC 3339 time as the server writes it: "2026-08-24T15:58:51Z". */
const TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;

/** Detail keys the sentence did not use, as quiet "key value" pairs. */
export function AuditDetails({ detail, used = [] }: { detail: Detail; used?: string[] }) {
  const entries = Object.entries(detail).filter(([key]) => !used.includes(key));
  if (entries.length === 0) return null;
  return (
    <dl className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-small">
      {entries.map(([key, value]) => {
        const term = key === 'ip' ? 'Address' : key.replace(/_/g, ' ');
        if (typeof value === 'string' && TIMESTAMP.test(value)) {
          return (
            <div key={key} className="flex min-w-0 gap-1.5">
              <dt className="text-text-faint">{term}</dt>
              <dd className="m-0 min-w-0 text-text-muted tabular-nums">
                <time dateTime={value} title={value}>
                  {formatExactTime(value)}
                </time>
              </dd>
            </div>
          );
        }
        const full = show(value);
        return (
          <div key={key} className="flex min-w-0 gap-1.5">
            <dt className="text-text-faint">{term}</dt>
            <dd
              className="m-0 min-w-0 font-mono break-all text-text-muted"
              title={full.length > MAX ? full : undefined}
            >
              {clip(full)}
            </dd>
          </div>
        );
      })}
    </dl>
  );
}
