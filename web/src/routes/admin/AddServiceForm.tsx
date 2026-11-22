// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useId, useRef, useState, type FormEvent } from 'react';
import { useCreateService, type Service, type ServiceCreate } from '../../api/admin';
import { ApiError } from '../../api/client';
import { describeError } from '../../api/errors';
import { Spinner } from '../../components/ConfirmDialog';
import { Dialog } from '../../components/Dialog';
import { buttonClass, inputClass, monoInputClass } from '../../ui';
import { Problem } from './kit';

type FieldName = 'path' | 'name' | 'sonar';

/** The API reports one message per 400; attach it to the field it talks about. */
function fieldOf(message: string): FieldName {
  const text = message.toLowerCase();
  if (text.includes('sonar')) return 'sonar';
  if (text.includes('name') && !text.includes('path')) return 'name';
  return 'path';
}

/** The name a service gets when none is given: the last part of its path. */
export function defaultName(path: string): string {
  const parts = path.trim().replace(/\/+$/, '').split('/');
  return parts[parts.length - 1] ?? '';
}

/**
 * Adds a service in a dialog. Only the GitLab project path is needed; the
 * first upload from the project's pipeline fills in the rest.
 */
export function AddServiceDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (service: Service) => void;
}) {
  const create = useCreateService();
  const ids = useId();
  const pathInput = useRef<HTMLInputElement>(null);
  const [path, setPath] = useState('');
  const [name, setName] = useState('');
  const [sonar, setSonar] = useState('');
  const [errors, setErrors] = useState<Partial<Record<FieldName, string>>>({});
  const [failure, setFailure] = useState('');

  function reset() {
    setPath('');
    setName('');
    setSonar('');
    setErrors({});
    setFailure('');
    create.reset();
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    setFailure('');
    if (!path.trim()) {
      setErrors({ path: 'Enter the GitLab project path, for example group/project.' });
      return;
    }
    setErrors({});
    const body: ServiceCreate = { gitlab_project_path: path.trim() };
    if (name.trim()) body.name = name.trim();
    if (sonar.trim()) body.sonar_project_key = sonar.trim();
    create.mutate(body, {
      onSuccess: (service) => {
        reset();
        onCreated(service);
      },
      onError: (error) => {
        if (error instanceof ApiError && error.status === 400) {
          setErrors({ [fieldOf(error.message)]: error.message });
        } else if (error instanceof ApiError && error.status === 409) {
          setErrors({ path: error.message });
        } else {
          setFailure(describeError(error));
        }
      },
    });
  }

  const field = (id: FieldName) => ({
    id: `${ids}-${id}`,
    'aria-invalid': errors[id] ? true : undefined,
    'aria-describedby': [`${ids}-${id}-hint`, errors[id] ? `${ids}-${id}-error` : '']
      .filter(Boolean)
      .join(' '),
  });
  const suggested = defaultName(path);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (create.isPending) return;
        if (!next) reset();
        onOpenChange(next);
      }}
      title="Add a service"
      description="Only the GitLab project path is needed. The first upload from its pipeline fills in the rest."
      modalStrict
      onOpenAutoFocus={(event) => {
        event.preventDefault();
        pathInput.current?.focus();
      }}
      footer={
        <>
          <button
            type="button"
            className={buttonClass('secondary')}
            disabled={create.isPending}
            onClick={() => {
              reset();
              onOpenChange(false);
            }}
          >
            Cancel
          </button>
          <button
            type="submit"
            form={`${ids}-form`}
            className={buttonClass('primary')}
            disabled={create.isPending}
            aria-busy={create.isPending || undefined}
          >
            {create.isPending ? <Spinner /> : null}
            Add service
          </button>
        </>
      }
    >
      <form id={`${ids}-form`} onSubmit={submit} noValidate className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${ids}-path`} className="text-body-s font-medium text-text">
            GitLab project path
          </label>
          <input
            {...field('path')}
            ref={pathInput}
            className={monoInputClass}
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder="group/project"
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="off"
          />
          <p id={`${ids}-path-hint`} className="text-caption text-text-muted">
            As in the project&apos;s GitLab address, after the host.
          </p>
          {errors.path ? <Problem id={`${ids}-path-error`}>{errors.path}</Problem> : null}
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-1.5">
            <label htmlFor={`${ids}-name`} className="text-body-s font-medium text-text">
              Name <span className="font-normal text-text-faint">optional</span>
            </label>
            <input
              {...field('name')}
              className={inputClass}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={suggested || 'payments-api'}
              autoComplete="off"
            />
            <p id={`${ids}-name-hint`} className="text-caption text-text-muted">
              {suggested ? (
                <>
                  Empty means <span className="text-text">{suggested}</span>.
                </>
              ) : (
                'Empty means the last part of the path.'
              )}
            </p>
            {errors.name ? <Problem id={`${ids}-name-error`}>{errors.name}</Problem> : null}
          </div>
          <div className="flex flex-col gap-1.5">
            <label htmlFor={`${ids}-sonar`} className="text-body-s font-medium text-text">
              SonarQube key <span className="font-normal text-text-faint">optional</span>
            </label>
            <input
              {...field('sonar')}
              className={monoInputClass}
              value={sonar}
              onChange={(e) => setSonar(e.target.value)}
              placeholder="group:project"
              autoComplete="off"
              spellCheck={false}
            />
            <p id={`${ids}-sonar-hint`} className="text-caption text-text-muted">
              Links its quality gate and ratings.
            </p>
            {errors.sonar ? <Problem id={`${ids}-sonar-error`}>{errors.sonar}</Problem> : null}
          </div>
        </div>
        {failure ? <Problem>{failure}</Problem> : null}
      </form>
    </Dialog>
  );
}
