// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import {
  KeyRound,
  MoreHorizontal,
  ShieldCheck,
  ShieldMinus,
  Trash2,
  UserCheck,
  UserX,
} from 'lucide-react';
import { useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import type { Role, User } from '../api/client';
import { describeError } from '../api/errors';
import {
  useDeleteUser,
  useMe,
  useResetUserMfa,
  useSetUserDisabled,
  useUpdateUserRole,
  useUsers,
} from '../api/hooks';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { FilterChips } from '../components/FilterChips';
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
} from '../components/Menu';
import { Pagination } from '../components/Pagination';
import { StatusPill, Tag } from '../components/badges';
import { Select } from '../components/inputs';
import { PageHeader } from '../components/layout';
import { EmptyState, ErrorState, ForbiddenState, SkeletonTable } from '../components/states';
import { toast } from '../components/Toast';
import { countOf } from '../format';
import { iconButtonClass } from '../ui';
import { FlatTable, InitialsAvatar, When, td, th } from './admin/kit';

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

const roleOptions = [
  { value: 'admin', label: 'Admin' },
  { value: 'viewer', label: 'Viewer' },
];

const nameOf = (user: User) => user.name || user.username;

type Confirm = 'disable' | 'delete' | 'mfa' | null;

const iconProps = { size: 16, strokeWidth: 1.75, 'aria-hidden': true } as const;

function UserRow({ user, isSelf }: { user: User; isSelf: boolean }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const role = useUpdateUserRole();
  const setDisabled = useSetUserDisabled();
  const remove = useDeleteUser();
  const resetMfa = useResetUserMfa();
  const [confirm, setConfirm] = useState<Confirm>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const disabled = user.disabled_at !== null;
  const name = nameOf(user);
  // While a change is in flight, show the requested role; on failure the
  // server data is unchanged, so the control falls back to the saved role.
  const shownRole = role.isPending ? role.variables.role : user.role;

  function changeRole(next: Role) {
    if (next === user.role) return;
    role.mutate(
      { id: user.id, role: next },
      {
        onSuccess: (updated) =>
          toast.success('Role changed', {
            description: `${name} is now ${updated.role === 'admin' ? 'an admin' : 'a viewer'}.`,
          }),
        onError: (error) =>
          toast.error('The role could not be changed', { description: describeError(error) }),
      },
    );
  }

  function enable() {
    setDisabled.mutate(
      { id: user.id, disabled: false },
      {
        onSuccess: () =>
          toast.success('User enabled', { description: `${name} can sign in again.` }),
        onError: (error) =>
          toast.error(`${name} could not be enabled`, { description: describeError(error) }),
      },
    );
  }

  function disable() {
    setDisabled.mutate(
      { id: user.id, disabled: true },
      {
        onSuccess: () => {
          setConfirm(null);
          toast.success('User disabled', {
            description: `${name} was signed out everywhere and cannot sign in.`,
          });
        },
        onError: (error) => {
          setConfirm(null);
          toast.error(`${name} could not be disabled`, { description: describeError(error) });
        },
      },
    );
  }

  function deleteUser() {
    remove.mutate(user.id, {
      onSuccess: () => {
        setConfirm(null);
        toast.success('User deleted', { description: `${name} was removed.` });
      },
      onError: (error) => {
        setConfirm(null);
        toast.error(`${name} could not be deleted`, { description: describeError(error) });
      },
    });
  }

  function reset() {
    resetMfa.mutate(user.id, {
      onSuccess: () => {
        setConfirm(null);
        if (isSelf) {
          // The server ended every session of this user, this one included.
          queryClient.clear();
          void navigate('/login', { replace: true });
          return;
        }
        toast.success('Two-factor reset', {
          description: `${name} sets it up again at the next sign-in.`,
        });
        void queryClient.invalidateQueries({ queryKey: ['users'] });
      },
      onError: (error) => {
        setConfirm(null);
        toast.error('Two-factor could not be reset', { description: describeError(error) });
      },
    });
  }

  return (
    <tr
      className={`hover-tint ${disabled ? '[&>td]:bg-[color-mix(in_srgb,var(--sunken)_55%,transparent)]' : ''}`}
    >
      <td className={`${td} max-w-0 sm:w-[36%] sm:min-w-56`}>
        <div className="flex min-w-0 items-center gap-3">
          <InitialsAvatar name={name} ring={user.role === 'admin'} muted={disabled} />
          <div className="min-w-0">
            <p className="flex min-w-0 items-center gap-2">
              <span
                className={`truncate font-medium ${disabled ? 'text-text-muted' : 'text-text'}`}
              >
                {name}
              </span>
              {isSelf ? <span className="shrink-0 text-caption text-text-faint">you</span> : null}
              <span className="shrink-0 text-caption text-text-muted sm:hidden">
                {user.role === 'admin' ? 'Admin' : 'Viewer'}
              </span>
              {disabled ? (
                <StatusPill
                  status="fail"
                  className="shrink-0"
                  title={`Disabled ${new Date(user.disabled_at!).toLocaleString()}`}
                >
                  Disabled
                </StatusPill>
              ) : null}
            </p>
            <p className="truncate text-small text-text-muted">
              {user.name ? <span className="font-mono">{user.username}</span> : null}
              {user.name && user.email ? ' ' : null}
              {user.email ? <span>{user.email}</span> : null}
              {!user.name && !user.email ? 'No e-mail on record' : null}
            </p>
          </div>
        </div>
      </td>
      <td className={`${td} hidden md:table-cell`}>
        <Tag>{user.source === 'gitlab' ? 'GitLab' : 'Local'}</Tag>
      </td>
      <td className={`${td} hidden w-36 sm:table-cell`}>
        <Select
          label={`Role for ${user.username}`}
          hideLabel
          size="sm"
          className="w-28"
          triggerClassName="min-h-8! pointer-coarse:min-h-11!"
          value={shownRole}
          disabled={role.isPending}
          onChange={(next) => changeRole(next as Role)}
          options={roleOptions}
        />
      </td>
      <td className={`${td} hidden whitespace-nowrap md:table-cell`}>
        {user.mfa_enrolled ? (
          <StatusPill status="ok">On</StatusPill>
        ) : (
          <StatusPill status="warn" title="Set up at the next sign-in">
            Not set up
          </StatusPill>
        )}
      </td>
      <td className={`${td} hidden whitespace-nowrap text-text-muted lg:table-cell`}>
        {user.last_login_at ? <When at={user.last_login_at} /> : 'Never'}
      </td>
      <td className={`${td} w-12 pr-2 text-right`}>
        <Menu>
          <MenuTrigger
            ref={trigger}
            className={iconButtonClass('ghost', 'sm')}
            aria-label={`Actions for ${user.username}`}
          >
            <MoreHorizontal size={18} strokeWidth={1.75} aria-hidden="true" />
          </MenuTrigger>
          <MenuContent className="min-w-56">
            {user.role === 'admin' ? (
              <MenuItem icon={<ShieldMinus {...iconProps} />} onSelect={() => changeRole('viewer')}>
                Make viewer
              </MenuItem>
            ) : (
              <MenuItem icon={<ShieldCheck {...iconProps} />} onSelect={() => changeRole('admin')}>
                Make admin
              </MenuItem>
            )}
            <MenuItem icon={<KeyRound {...iconProps} />} onSelect={() => setConfirm('mfa')}>
              Reset two-factor
            </MenuItem>
            <MenuSeparator />
            {isSelf ? <MenuLabel>You cannot disable or delete your own account.</MenuLabel> : null}
            {disabled ? (
              <MenuItem icon={<UserCheck {...iconProps} />} disabled={isSelf} onSelect={enable}>
                Enable user
              </MenuItem>
            ) : (
              <MenuItem
                icon={<UserX {...iconProps} />}
                tone="danger"
                disabled={isSelf}
                onSelect={() => setConfirm('disable')}
              >
                Disable user
              </MenuItem>
            )}
            <MenuItem
              icon={<Trash2 {...iconProps} />}
              tone="danger"
              disabled={isSelf}
              onSelect={() => setConfirm('delete')}
            >
              Delete user
            </MenuItem>
          </MenuContent>
        </Menu>

        {confirm === 'mfa' ? (
          <ConfirmDialog
            title={`Reset two-factor for ${name}?`}
            confirmLabel="Reset two-factor"
            tone="danger"
            pending={resetMfa.isPending}
            onConfirm={reset}
            onCancel={() => setConfirm(null)}
            returnFocusTo={trigger}
          >
            <p>
              This removes the authenticator and recovery codes of {name} and signs them out
              everywhere. They set up two-factor again at the next sign-in.
            </p>
            {isSelf ? <p>This is your own account, so you will be signed out now.</p> : null}
          </ConfirmDialog>
        ) : null}
        {confirm === 'disable' ? (
          <ConfirmDialog
            title={`Disable ${name}?`}
            confirmLabel="Disable user"
            tone="danger"
            pending={setDisabled.isPending}
            onConfirm={disable}
            onCancel={() => setConfirm(null)}
            returnFocusTo={trigger}
          >
            <p>
              {name} is signed out everywhere and cannot sign in, with a password or with GitLab,
              until an administrator enables the account again.
            </p>
          </ConfirmDialog>
        ) : null}
        {confirm === 'delete' ? (
          <ConfirmDialog
            title={`Delete ${name}?`}
            confirmLabel="Delete user"
            tone="danger"
            pending={remove.isPending}
            onConfirm={deleteUser}
            onCancel={() => setConfirm(null)}
            returnFocusTo={trigger}
          >
            <p>
              This removes {name} with their sessions and two-factor setup. The audit log keeps
              their entries.
            </p>
            {user.source === 'gitlab' ? (
              <p>
                A GitLab user who signs in again comes back as a new viewer. Disable the account
                instead to keep them out.
              </p>
            ) : null}
          </ConfirmDialog>
        ) : null}
      </td>
    </tr>
  );
}

type Facet = 'admin' | 'no-mfa' | 'disabled';

const facetTest: Record<Facet, (user: User) => boolean> = {
  admin: (u) => u.role === 'admin',
  'no-mfa': (u) => !u.mfa_enrolled,
  disabled: (u) => u.disabled_at !== null,
};

function UsersTable() {
  const users = useUsers();
  const me = useMe();
  const [facets, setFacets] = useState<string[]>([]);
  const items = useMemo(() => users.data?.pages.flatMap((page) => page.items) ?? [], [users.data]);

  if (users.isPending) {
    return (
      <div className="panel overflow-hidden">
        <SkeletonTable rows={4} columns={5} />
      </div>
    );
  }
  if (users.isError) {
    return (
      <ErrorState
        title="Users could not be loaded"
        message={describeError(users.error)}
        onRetry={() => void users.refetch()}
        retrying={users.isFetching}
      />
    );
  }

  const count = (facet: Facet) => items.filter(facetTest[facet]).length;
  const shown = items.filter((u) => facets.every((f) => facetTest[f as Facet](u)));

  return (
    <div className="panel overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-5">
        <FilterChips
          label="Filter users"
          selected={facets}
          onChange={setFacets}
          options={[
            { value: 'admin', label: 'Admins', count: count('admin') },
            { value: 'no-mfa', label: 'Without two-factor', count: count('no-mfa') },
            { value: 'disabled', label: 'Disabled', count: count('disabled') },
          ]}
        />
        <p className="text-small text-text-muted tabular-nums">
          {countOf(items.length, 'person', 'people')}
        </p>
      </div>
      <div className="border-t border-line">
        {shown.length === 0 ? (
          <EmptyState
            compact
            art="search"
            title="Nobody matches these filters"
            body="Clear a filter to see more people."
            action={
              <button
                type="button"
                className="text-body-s text-brand underline underline-offset-[3px]"
                onClick={() => setFacets([])}
              >
                Clear filters
              </button>
            }
          />
        ) : (
          <FlatTable
            caption="Users"
            head={
              <>
                <th scope="col" className={th}>
                  Person
                </th>
                <th scope="col" className={`${th} hidden md:table-cell`}>
                  Sign-in
                </th>
                <th scope="col" className={`${th} hidden sm:table-cell`}>
                  Role
                </th>
                <th scope="col" className={`${th} hidden md:table-cell`}>
                  Two-factor
                </th>
                <th scope="col" className={`${th} hidden lg:table-cell`}>
                  Last sign-in
                </th>
                <th scope="col" className={`${th} text-right`}>
                  <span className="sr-only">Actions</span>
                </th>
              </>
            }
          >
            {shown.map((user) => (
              <UserRow key={user.id} user={user} isSelf={me.data?.id === user.id} />
            ))}
          </FlatTable>
        )}
      </div>
      {users.hasNextPage ? (
        <Pagination
          className="border-t border-line px-4 py-3"
          shown={items.length}
          hasMore={users.hasNextPage}
          loading={users.isFetchingNextPage}
          onLoadMore={() => void users.fetchNextPage()}
          noun="person"
          nounPlural="people"
        />
      ) : null}
    </div>
  );
}

export function UsersPage() {
  const me = useMe();
  return (
    <>
      <PageHeader
        title="Users"
        subtitle="Everyone who has signed in, the role they hold, and whether they may sign in."
      />
      {me.data?.role === 'admin' ? (
        <UsersTable />
      ) : (
        <ForbiddenState title="You need the admin role to see this page." />
      )}
    </>
  );
}
