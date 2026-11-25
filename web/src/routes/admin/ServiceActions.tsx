// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Archive, ArchiveRestore, GitBranch, Pencil, Trash2 } from 'lucide-react';
import { useId, useRef, useState, type FormEvent } from 'react';
import {
  useDeleteService,
  useUpdateService,
  type Service,
  type ServiceUpdate,
} from '../../api/admin';
import { describeError } from '../../api/errors';
import { ConfirmDialog, Spinner } from '../../components/ConfirmDialog';
import { Dialog } from '../../components/Dialog';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from '../../components/Menu';
import { toast } from '../../components/Toast';
import { buttonClass, iconButtonClass, inputClass, monoInputClass } from '../../ui';
import { MoreHorizontal } from 'lucide-react';
import { Problem } from './kit';

type Mode = 'edit' | 'archive' | 'delete' | null;

const iconProps = { size: 16, strokeWidth: 1.75, 'aria-hidden': true } as const;

/**
 * The row menu of one service: edit, choose the dashboard branch, archive or
 * unarchive, and delete. Each action confirms with a toast in the button's words.
 */
export function ServiceActions({
  service,
  onChooseBranch,
}: {
  service: Service;
  /** Opens the dashboard branch editor of the row. */
  onChooseBranch: () => void;
}) {
  const update = useUpdateService();
  const [mode, setMode] = useState<Mode>(null);
  const [problem, setProblem] = useState('');
  const trigger = useRef<HTMLButtonElement>(null);
  // Set while the menu closes towards the branch editor, so focus goes there.
  const toBranch = useRef(false);

  function close() {
    setMode(null);
    setProblem('');
  }

  /**
   * Sends the change and confirms it with title and description, or with the
   * notice `instead` returns for the answer when it differs from the request.
   */
  function patch(
    body: ServiceUpdate,
    title: string,
    description: string,
    instead?: (updated: Service) => string | null,
  ) {
    update.mutate(
      { id: service.id, body },
      {
        onSuccess: (updated) => {
          close();
          const notice = instead?.(updated);
          if (notice) toast.info(notice);
          else toast.success(title, { description });
        },
        onError: (error) => {
          if (mode) setProblem(describeError(error));
          else
            toast.error(`${service.name} could not be changed`, {
              description: describeError(error),
            });
        },
      },
    );
  }

  function open(next: Mode) {
    update.reset();
    setProblem('');
    setMode(next);
  }

  return (
    <>
      <Menu>
        <MenuTrigger
          ref={trigger}
          className={iconButtonClass('ghost', 'sm')}
          aria-label={`Actions for ${service.name}`}
        >
          <MoreHorizontal size={18} strokeWidth={1.75} aria-hidden="true" />
        </MenuTrigger>
        <MenuContent
          onCloseAutoFocus={(event) => {
            if (toBranch.current) {
              event.preventDefault();
              toBranch.current = false;
            }
          }}
        >
          <MenuItem icon={<Pencil {...iconProps} />} onSelect={() => open('edit')}>
            Edit
          </MenuItem>
          <MenuItem
            icon={<GitBranch {...iconProps} />}
            onSelect={() => {
              toBranch.current = true;
              onChooseBranch();
            }}
          >
            Choose dashboard branch
          </MenuItem>
          <MenuSeparator />
          {service.archived ? (
            <MenuItem
              icon={<ArchiveRestore {...iconProps} />}
              onSelect={() =>
                patch(
                  { archived: false },
                  'Service unarchived',
                  `${service.name} accepts uploads again and is back on the portfolio.`,
                )
              }
            >
              Unarchive
            </MenuItem>
          ) : (
            <MenuItem icon={<Archive {...iconProps} />} onSelect={() => open('archive')}>
              Archive
            </MenuItem>
          )}
          <MenuItem tone="danger" icon={<Trash2 {...iconProps} />} onSelect={() => open('delete')}>
            Delete
          </MenuItem>
        </MenuContent>
      </Menu>

      {mode === 'edit' ? (
        <EditDialog
          service={service}
          pending={update.isPending}
          problem={problem}
          onCancel={close}
          onSave={(body) =>
            patch(body, 'Changes saved', `${body.name ?? service.name} is up to date.`)
          }
          onCancelWaiting={() =>
            // Never unlinks: when the service was linked first, the answer
            // shows the key and nothing was cancelled. It is the waiting key
            // when SonarQube confirmed it, or another one an administrator set.
            patch(
              { sonar_key_declared: null },
              'Waiting key cancelled',
              `${service.name} no longer waits for ${service.sonar_key_declared ?? 'a SonarQube key'}.`,
              ({ name, sonar_project_key: key }) => {
                if (!key) return null;
                return key === service.sonar_key_declared
                  ? `SonarQube already confirmed ${key}.`
                  : `${name} is already linked to ${key}.`;
              },
            )
          }
        />
      ) : null}

      {mode === 'archive' ? (
        <ConfirmDialog
          title={`Archive ${service.name}?`}
          confirmLabel="Archive service"
          tone="danger"
          pending={update.isPending}
          onConfirm={() =>
            patch(
              { archived: true },
              'Service archived',
              `${service.name} is off the portfolio and refuses new uploads.`,
            )
          }
          onCancel={close}
          returnFocusTo={trigger}
        >
          <p>
            {service.name} leaves the portfolio and its pipeline uploads are refused from now on.
            Its scans and analyses are kept, and you can unarchive it at any time.
          </p>
          {problem ? <Problem>{problem}</Problem> : null}
        </ConfirmDialog>
      ) : null}

      {mode === 'delete' ? (
        <DeleteDialog service={service} onDone={close} returnFocusTo={trigger} />
      ) : null}
    </>
  );
}

function EditDialog({
  service,
  pending,
  problem,
  onCancel,
  onSave,
  onCancelWaiting,
}: {
  service: Service;
  pending: boolean;
  problem: string;
  onCancel: () => void;
  onSave: (body: ServiceUpdate) => void;
  onCancelWaiting: () => void;
}) {
  const ids = useId();
  const nameInput = useRef<HTMLInputElement>(null);
  const [name, setName] = useState(service.name);
  const [sonar, setSonar] = useState(service.sonar_project_key ?? '');
  const [nameError, setNameError] = useState('');

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!name.trim()) {
      setNameError('Enter a name for the service.');
      return;
    }
    setNameError('');
    const body: ServiceUpdate = { name: name.trim() };
    // The key is sent only when it changed: setting or clearing it also
    // cancels a key the pipeline declared that waits for SonarQube. An empty
    // key unlinks the SonarQube project.
    if (sonar.trim() !== (service.sonar_project_key ?? '')) {
      body.sonar_project_key = sonar.trim() === '' ? null : sonar.trim();
    }
    onSave(body);
  }
  const waiting = !service.sonar_project_key ? service.sonar_key_declared : null;

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) onCancel();
      }}
      title={`Edit ${service.name}`}
      description={
        <span className="font-mono text-small break-all">{service.gitlab_project_path}</span>
      }
      modalStrict
      onOpenAutoFocus={(event) => {
        event.preventDefault();
        nameInput.current?.focus();
      }}
      footer={
        <>
          <button
            type="button"
            className={buttonClass('secondary')}
            onClick={onCancel}
            disabled={pending}
          >
            Cancel
          </button>
          <button
            type="submit"
            form={`${ids}-form`}
            className={buttonClass('primary')}
            disabled={pending}
            aria-busy={pending || undefined}
          >
            {pending ? <Spinner /> : null}
            Save changes
          </button>
        </>
      }
    >
      <form id={`${ids}-form`} onSubmit={submit} noValidate className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${ids}-name`} className="text-body-s font-medium text-text">
            Name
          </label>
          <input
            id={`${ids}-name`}
            ref={nameInput}
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoComplete="off"
            aria-invalid={nameError ? true : undefined}
            aria-describedby={nameError ? `${ids}-name-error` : undefined}
          />
          {nameError ? <Problem id={`${ids}-name-error`}>{nameError}</Problem> : null}
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${ids}-sonar`} className="text-body-s font-medium text-text">
            SonarQube project key
          </label>
          <input
            id={`${ids}-sonar`}
            className={monoInputClass}
            value={sonar}
            onChange={(e) => setSonar(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            placeholder="group:project"
            aria-describedby={`${ids}-sonar-hint`}
          />
          <p id={`${ids}-sonar-hint`} className="text-caption text-text-muted">
            The key of the project in SonarQube. Leave it empty to unlink.
            {waiting ? (
              <>
                {' '}
                Waiting for SonarQube project <span className="font-mono">{waiting}</span>, declared
                by the pipeline. Setting a key here, or choosing Cancel waiting key, stops waiting
                for <span className="font-mono">{waiting}</span>.
              </>
            ) : null}
          </p>
          {waiting ? (
            <div>
              <button
                type="button"
                className={buttonClass('secondary', 'sm')}
                onClick={onCancelWaiting}
                disabled={pending}
              >
                Cancel waiting key
              </button>
            </div>
          ) : null}
        </div>
        {problem ? <Problem>{problem}</Problem> : null}
      </form>
    </Dialog>
  );
}

function DeleteDialog({
  service,
  onDone,
  returnFocusTo,
}: {
  service: Service;
  onDone: () => void;
  returnFocusTo: React.RefObject<HTMLElement | null>;
}) {
  const remove = useDeleteService();
  const ids = useId();
  const [typed, setTyped] = useState('');
  const [problem, setProblem] = useState('');
  const matches = typed.trim() === service.gitlab_project_path;

  function confirm() {
    if (!matches) {
      setProblem('Type the project path exactly as shown to confirm.');
      return;
    }
    remove.mutate(service.id, {
      onSuccess: () => {
        onDone();
        toast.success('Service deleted', {
          description: `${service.name} and all its scans and analyses are gone.`,
        });
      },
      onError: (error) => setProblem(describeError(error)),
    });
  }

  return (
    <ConfirmDialog
      title={`Delete ${service.name}?`}
      confirmLabel="Delete service"
      tone="danger"
      pending={remove.isPending}
      onConfirm={confirm}
      onCancel={onDone}
      returnFocusTo={returnFocusTo}
    >
      <p>
        This deletes every scan and analysis of {service.name} and cannot be undone. Archive the
        service instead to keep its history.
      </p>
      <div className="mt-4 flex flex-col gap-1.5">
        <label htmlFor={`${ids}-confirm`} className="text-body-s text-text-muted">
          Type <span className="font-mono text-text">{service.gitlab_project_path}</span> to confirm
        </label>
        <input
          id={`${ids}-confirm`}
          className={monoInputClass}
          value={typed}
          onChange={(e) => {
            setTyped(e.target.value);
            setProblem('');
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              confirm();
            }
          }}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={problem ? true : undefined}
        />
      </div>
      {problem ? (
        <div className="mt-3">
          <Problem>{problem}</Problem>
        </div>
      ) : null}
    </ConfirmDialog>
  );
}
