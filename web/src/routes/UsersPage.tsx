// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleAlert } from 'lucide-react';
import type { Role, User } from '../api/client';
import { useMe, useUpdateUserRole, useUsers } from '../api/hooks';
import { cardClass, headingClass, secondaryButtonClass, selectClass } from '../ui';

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
];

/** Formats an ISO timestamp as a relative time such as "3 days ago". */
export function formatRelative(iso: string, now: number = Date.now()): string {
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000);
  const formatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return formatter.format(Math.round(seconds / size), unit);
  }
  return Math.abs(seconds) < 10 ? 'just now' : formatter.format(seconds, 'second');
}

const cell = 'px-4 py-3 text-left align-middle';

function UserRow({ user }: { user: User }) {
  const update = useUpdateUserRole();
  // While a change is in flight, show the requested role; on failure the
  // server data is unchanged, so the control falls back to the saved role.
  const shown = update.isPending ? update.variables.role : user.role;

  let status = '';
  if (update.isPending) status = 'Saving…';
  else if (update.isSuccess)
    status = `Role changed to ${update.data.role === 'admin' ? 'Admin' : 'Viewer'}.`;

  return (
    <tr className="border-t border-border">
      <td className={`${cell} font-mono`}>{user.username}</td>
      <td className={cell}>{user.name}</td>
      <td className={cell}>{user.email}</td>
      <td className={cell}>{user.source === 'gitlab' ? 'GitLab' : 'Local'}</td>
      <td className={cell}>
        <select
          aria-label={`Role for ${user.username}`}
          className={selectClass}
          value={shown}
          disabled={update.isPending}
          onChange={(e) => update.mutate({ id: user.id, role: e.target.value as Role })}
        >
          <option value="admin">Admin</option>
          <option value="viewer">Viewer</option>
        </select>
        <div aria-live="polite" className="mt-1 min-h-5 text-text-muted">
          {update.isError ? (
            <span className="flex items-start gap-2 text-text">
              <CircleAlert
                size={16}
                strokeWidth={1.5}
                aria-hidden="true"
                className="mt-0.5 shrink-0 text-fail"
              />
              <span>{update.error.message}</span>
            </span>
          ) : (
            status
          )}
        </div>
      </td>
      <td className={cell}>
        {user.last_login_at ? (
          <time dateTime={user.last_login_at} title={new Date(user.last_login_at).toLocaleString()}>
            {formatRelative(user.last_login_at)}
          </time>
        ) : (
          'Never'
        )}
      </td>
    </tr>
  );
}

function UsersTable() {
  const users = useUsers();

  if (users.isPending) {
    return (
      <p role="status" className="mt-4 text-text-muted">
        Loading users…
      </p>
    );
  }
  if (users.isError) {
    return (
      <p role="alert" className="mt-4 text-text">
        {users.error.message} Reload the page to try again.
      </p>
    );
  }

  const items = users.data.pages.flatMap((page) => page.items);
  return (
    <>
      <div
        role="region"
        aria-label="Users table"
        tabIndex={0}
        className={`${cardClass} mt-6 overflow-x-auto`}
      >
        <table className="w-full border-collapse">
          <thead>
            <tr className="text-text-muted">
              <th scope="col" className={`${cell} font-medium`}>
                Username
              </th>
              <th scope="col" className={`${cell} font-medium`}>
                Name
              </th>
              <th scope="col" className={`${cell} font-medium`}>
                E-mail
              </th>
              <th scope="col" className={`${cell} font-medium`}>
                Sign-in
              </th>
              <th scope="col" className={`${cell} font-medium`}>
                Role
              </th>
              <th scope="col" className={`${cell} font-medium`}>
                Last sign-in
              </th>
            </tr>
          </thead>
          <tbody>
            {items.map((user) => (
              <UserRow key={user.id} user={user} />
            ))}
          </tbody>
        </table>
      </div>
      {users.hasNextPage && (
        <button
          type="button"
          className={`${secondaryButtonClass} mt-4`}
          disabled={users.isFetchingNextPage}
          onClick={() => void users.fetchNextPage()}
        >
          Load more
        </button>
      )}
    </>
  );
}

export function UsersPage() {
  const me = useMe();
  return (
    <>
      <h1 className={headingClass}>Users</h1>
      {me.data?.role === 'admin' ? (
        <UsersTable />
      ) : (
        <p className="mt-4 text-text-muted">You need the admin role to see this page.</p>
      )}
    </>
  );
}
