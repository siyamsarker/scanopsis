// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleAlert } from 'lucide-react';
import { useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import {
  useDeleteService,
  useUpdateService,
  type Service,
  type ServiceUpdate,
} from '../../api/admin';
import { describeError } from '../../api/errors';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { inputClass, monoInputClass, secondaryButtonClass } from '../../ui';

type Mode = 'edit' | 'archive' | 'delete' | null;

const small = `${secondaryButtonClass} min-h-8 px-3 text-sm`;

function Problem({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className="mt-3 flex items-start gap-2 text-text">
      <CircleAlert
        size={16}
        strokeWidth={1.5}
        aria-hidden="true"
        className="mt-1 shrink-0 text-fail"
      />
      <span>{children}</span>
    </p>
  );
}

/** Edit, archive or unarchive, and delete for one service. */
export function ServiceActions({
  service,
  onNotice,
}: {
  service: Service;
  onNotice: (message: string) => void;
}) {
  const update = useUpdateService();
  const remove = useDeleteService();
  const [mode, setMode] = useState<Mode>(null);
  const [name, setName] = useState(service.name);
  const [sonar, setSonar] = useState(service.sonar_project_key ?? '');
  const [typed, setTyped] = useState('');
  const [problem, setProblem] = useState('');
  const trigger = useRef<HTMLButtonElement>(null);
  const ids = useId();

  function open(next: Mode, button: HTMLButtonElement) {
    trigger.current = button;
    setName(service.name);
    setSonar(service.sonar_project_key ?? '');
    setTyped('');
    setProblem('');
    update.reset();
    remove.reset();
    setMode(next);
  }

  function close() {
    setMode(null);
    setProblem('');
  }

  function patch(body: ServiceUpdate, done: string) {
    update.mutate(
      { id: service.id, body },
      {
        onSuccess: () => {
          close();
          onNotice(done);
        },
        onError: (error) => {
          setProblem(describeError(error));
          onNotice(describeError(error));
        },
      },
    );
  }

  function save() {
    if (!name.trim()) {
      setProblem('Enter a name for the service.');
      return;
    }
    // An empty key unlinks the SonarQube project.
    patch(
      { name: name.trim(), sonar_project_key: sonar.trim() === '' ? null : sonar.trim() },
      `Saved ${name.trim()}.`,
    );
  }

  function confirmDelete() {
    if (typed !== service.gitlab_project_path) {
      setProblem('Type the project path exactly as shown to confirm.');
      return;
    }
    remove.mutate(service.id, {
      onSuccess: () => {
        close();
        onNotice(`Deleted ${service.name} and all its scans and analyses.`);
      },
      onError: (error) => {
        setProblem(describeError(error));
        onNotice(describeError(error));
      },
    });
  }

  function onEnter(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      save();
    }
  }

  return (
    <div className="flex flex-wrap gap-2">
      <button
        type="button"
        aria-label={`Edit ${service.name}`}
        className={small}
        onClick={(e) => open('edit', e.currentTarget)}
      >
        Edit
      </button>
      {service.archived ? (
        <button
          type="button"
          aria-label={`Unarchive ${service.name}`}
          className={small}
          disabled={update.isPending}
          onClick={() => patch({ archived: false }, `Unarchived ${service.name}.`)}
        >
          Unarchive
        </button>
      ) : (
        <button
          type="button"
          aria-label={`Archive ${service.name}`}
          className={small}
          onClick={(e) => open('archive', e.currentTarget)}
        >
          Archive
        </button>
      )}
      <button
        type="button"
        aria-label={`Delete ${service.name}`}
        className={small}
        onClick={(e) => open('delete', e.currentTarget)}
      >
        Delete
      </button>

      {mode === 'edit' && (
        <ConfirmDialog
          title={`Edit ${service.name}`}
          confirmLabel="Save"
          pending={update.isPending}
          onConfirm={save}
          onCancel={close}
          returnFocusTo={trigger}
        >
          <div className="flex flex-col gap-4" onKeyDown={onEnter}>
            <div className="flex flex-col gap-1">
              <label htmlFor={`${ids}-name`} className="font-medium text-text">
                Name
              </label>
              <input
                id={`${ids}-name`}
                className={inputClass}
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="off"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor={`${ids}-sonar`} className="font-medium text-text">
                SonarQube project key
              </label>
              <input
                id={`${ids}-sonar`}
                className={monoInputClass}
                value={sonar}
                onChange={(e) => setSonar(e.target.value)}
                autoComplete="off"
                spellCheck={false}
                aria-describedby={`${ids}-sonar-hint`}
              />
              <p id={`${ids}-sonar-hint`} className="text-sm">
                Leave empty to unlink the SonarQube project.
              </p>
            </div>
          </div>
          {problem && <Problem>{problem}</Problem>}
        </ConfirmDialog>
      )}

      {mode === 'archive' && (
        <ConfirmDialog
          title={`Archive ${service.name}?`}
          confirmLabel="Archive"
          pending={update.isPending}
          onConfirm={() => patch({ archived: true }, `Archived ${service.name}.`)}
          onCancel={close}
          returnFocusTo={trigger}
        >
          <p>
            Archived services are hidden from the portfolio and receive no new uploads. Their data
            is kept, and you can unarchive the service at any time.
          </p>
          {problem && <Problem>{problem}</Problem>}
        </ConfirmDialog>
      )}

      {mode === 'delete' && (
        <ConfirmDialog
          title={`Delete ${service.name}?`}
          confirmLabel="Delete service"
          tone="danger"
          pending={remove.isPending}
          onConfirm={confirmDelete}
          onCancel={close}
          returnFocusTo={trigger}
        >
          <p>
            This deletes all scans and analyses of this service and cannot be undone. To confirm,
            type the project path{' '}
            <span className="font-mono text-text">{service.gitlab_project_path}</span>.
          </p>
          <div className="mt-4 flex flex-col gap-1">
            <label htmlFor={`${ids}-confirm`} className="font-medium text-text">
              Project path
            </label>
            <input
              id={`${ids}-confirm`}
              className={monoInputClass}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete="off"
              spellCheck={false}
            />
          </div>
          {problem && <Problem>{problem}</Problem>}
        </ConfirmDialog>
      )}
    </div>
  );
}
