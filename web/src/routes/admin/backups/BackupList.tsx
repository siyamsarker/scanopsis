// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Download, History, MoreHorizontal, Trash2 } from 'lucide-react';
import { useRef, useState } from 'react';
import { backupDownloadUrl, useDeleteBackup, type BackupInfo } from '../../../api/backups';
import { describeError } from '../../../api/errors';
import { StatusPill, Tag } from '../../../components/badges';
import { ConfirmDialog } from '../../../components/ConfirmDialog';
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuLinkItem,
  MenuSeparator,
  MenuTrigger,
} from '../../../components/Menu';
import { toast } from '../../../components/Toast';
import { formatBytes, NO_VALUE, relativeTime } from '../../../format';
import { iconButtonClass } from '../../../ui';
import { FlatTable, td, th } from '../kit';
import { backupDate, blockedHints, blockedLabels, blockedReason, kindLabel, madeBy } from './model';
import { RestoreDialog } from './RestoreDialog';

const iconProps = { size: 16, strokeWidth: 1.75, 'aria-hidden': true } as const;

function BackupRow({ backup, busy }: { backup: BackupInfo; busy: boolean }) {
  const remove = useDeleteBackup();
  const [confirm, setConfirm] = useState<'delete' | 'restore' | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const date = backupDate(backup);
  const blocked = blockedReason(backup);

  function deleteBackup() {
    remove.mutate(backup.name, {
      onSuccess: () => {
        setConfirm(null);
        toast.success('Backup deleted', { description: `The backup from ${date} was removed.` });
      },
      onError: (error) => {
        setConfirm(null);
        toast.error('The backup could not be deleted', { description: describeError(error) });
      },
    });
  }

  return (
    <tr className="hover-tint">
      <td className={`${td} max-w-0 sm:w-[30%] sm:min-w-56`}>
        <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <time
            dateTime={backup.created_at}
            title={date}
            className="font-medium whitespace-nowrap text-text"
          >
            {date}
          </time>
          <span className="text-small whitespace-nowrap text-text-muted">
            {relativeTime(backup.created_at)}
          </span>
        </p>
        {backup.original_name ? (
          <p
            className="mt-0.5 truncate font-mono text-small text-text-muted"
            title={backup.original_name}
          >
            {backup.original_name}
          </p>
        ) : null}
        {blocked ? (
          <StatusPill status={blocked === 'unreadable' ? 'fail' : 'warn'} className="mt-1.5">
            {blockedLabels[blocked]}
          </StatusPill>
        ) : null}
        {blocked === 'unreadable' && backup.problem ? (
          <p className="mt-1 text-small break-words whitespace-normal text-text-muted">
            {backup.problem}
          </p>
        ) : null}
        <p className="mt-1 text-small text-text-muted sm:hidden">
          {kindLabel(backup.kind)}, {formatBytes(backup.size)}
        </p>
      </td>
      <td className={`${td} hidden whitespace-nowrap sm:table-cell`}>
        <Tag>{kindLabel(backup.kind)}</Tag>
      </td>
      <td className={`${td} hidden text-right whitespace-nowrap tabular-nums sm:table-cell`}>
        {formatBytes(backup.size)}
      </td>
      <td className={`${td} hidden whitespace-nowrap md:table-cell`}>
        <span className="font-mono text-small">
          {backup.readable ? backup.app_version : NO_VALUE}
        </span>
      </td>
      <td className={`${td} hidden max-w-40 truncate lg:table-cell`} title={backup.created_by}>
        {backup.readable ? madeBy(backup.created_by) : NO_VALUE}
      </td>
      <td className={`${td} w-12 pr-2 text-right`}>
        <Menu>
          <MenuTrigger
            ref={trigger}
            className={iconButtonClass('ghost', 'sm')}
            aria-label={`Actions for the backup from ${date}`}
          >
            <MoreHorizontal size={18} strokeWidth={1.75} aria-hidden="true" />
          </MenuTrigger>
          <MenuContent className="min-w-52">
            <MenuLinkItem
              href={backupDownloadUrl(backup.name)}
              download
              icon={<Download {...iconProps} />}
            >
              Download
            </MenuLinkItem>
            <MenuItem
              icon={<History {...iconProps} />}
              disabled={Boolean(blocked) || busy}
              onSelect={() => setConfirm('restore')}
            >
              Restore
            </MenuItem>
            {blocked ? (
              <MenuLabel>{blockedHints[blocked]}</MenuLabel>
            ) : busy ? (
              <MenuLabel>A backup or restore is running. Restore once it has finished.</MenuLabel>
            ) : null}
            <MenuSeparator />
            <MenuItem
              icon={<Trash2 {...iconProps} />}
              tone="danger"
              onSelect={() => setConfirm('delete')}
            >
              Delete
            </MenuItem>
          </MenuContent>
        </Menu>

        {confirm === 'delete' ? (
          <ConfirmDialog
            title={`Delete the backup from ${date}?`}
            confirmLabel="Delete backup"
            tone="danger"
            pending={remove.isPending}
            onConfirm={deleteBackup}
            onCancel={() => setConfirm(null)}
            returnFocusTo={trigger}
          >
            <p>
              This removes the file from the backup folder. It cannot be restored afterwards, unless
              you keep a downloaded copy.
            </p>
            <p className="font-mono text-small break-all">{backup.name}</p>
          </ConfirmDialog>
        ) : null}
        {confirm === 'restore' ? (
          <RestoreDialog backup={backup} onClose={() => setConfirm(null)} returnFocusTo={trigger} />
        ) : null}
      </td>
    </tr>
  );
}

/** The backups, newest first, in a flat table; the less important columns give way on phones. */
export function BackupList({ backups, busy }: { backups: BackupInfo[]; busy: boolean }) {
  return (
    <FlatTable
      caption="Backups"
      head={
        <>
          <th scope="col" className={th}>
            Created
          </th>
          <th scope="col" className={`${th} hidden sm:table-cell`}>
            Kind
          </th>
          <th scope="col" className={`${th} hidden text-right sm:table-cell`}>
            Size
          </th>
          <th scope="col" className={`${th} hidden md:table-cell`}>
            Version
          </th>
          <th scope="col" className={`${th} hidden lg:table-cell`}>
            Made by
          </th>
          <th scope="col" className={`${th} text-right`}>
            <span className="sr-only">Actions</span>
          </th>
        </>
      }
    >
      {backups.map((backup) => (
        <BackupRow key={backup.name} backup={backup} busy={busy} />
      ))}
    </FlatTable>
  );
}
