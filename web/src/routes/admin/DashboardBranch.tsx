// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleAlert } from 'lucide-react';
import { useId, useState, type FormEvent, type KeyboardEvent } from 'react';
import { useUpdateService, type Service } from '../../api/admin';
import { describeError } from '../../api/errors';
import { useOverview } from '../../api/service';
import { Mono } from '../../components/Mono';
import { monoInputClass, primaryButtonClass, secondaryButtonClass } from '../../ui';

const small = 'min-h-8 px-3 text-sm';

/**
 * The branch (ref) the dashboards of one service show. Unset, the dashboards show
 * the latest scanned branch, which is `latest` when the portfolio knows it.
 * Editing offers the service's scanned refs, but any branch name is accepted so a
 * branch can be chosen before its first upload.
 */
export function DashboardBranch({
  service,
  latest,
  onNotice,
}: {
  service: Service;
  /** The branch the dashboards show while none is chosen, when known. */
  latest?: string;
  onNotice: (message: string) => void;
}) {
  const update = useUpdateService();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState('');
  const [problem, setProblem] = useState('');
  const ids = useId();
  // The scanned refs are only needed while editing.
  const overview = useOverview(service.id, undefined, editing);
  const refs = overview.data?.refs ?? [];

  function open() {
    setValue(service.default_ref ?? '');
    setProblem('');
    update.reset();
    setEditing(true);
  }

  function close() {
    setEditing(false);
    setProblem('');
  }

  function send(default_ref: string | null, done: string) {
    update.mutate(
      { id: service.id, body: { default_ref } },
      {
        onSuccess: () => {
          close();
          onNotice(done);
        },
        onError: (error) => setProblem(describeError(error)),
      },
    );
  }

  function save(event?: FormEvent) {
    event?.preventDefault();
    const branch = value.trim();
    if (branch === '') {
      setProblem('Enter a branch name, or use Clear to show the latest scanned branch.');
      return;
    }
    send(branch, `The dashboards of ${service.name} now show ${branch}.`);
  }

  function onKeyDown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      close();
    }
  }

  const shown = service.default_ref;
  return (
    <div className="flex min-w-60 flex-col items-start gap-2">
      {shown !== null ? (
        <p className="min-w-0 break-words">
          <Mono value={shown} />
        </p>
      ) : (
        <p className="text-text-muted">
          Not chosen
          {latest ? (
            <>
              {' '}
              — showing the latest scanned branch (<Mono value={latest} />)
            </>
          ) : (
            ' — showing the latest scanned branch'
          )}
        </p>
      )}

      {!editing ? (
        <button
          type="button"
          className={`${secondaryButtonClass} ${small}`}
          aria-label={`${shown === null ? 'Choose' : 'Change'} dashboard branch of ${service.name}`}
          onClick={open}
        >
          {shown === null ? 'Choose' : 'Change'}
        </button>
      ) : (
        <form className="flex w-full flex-col gap-2" onSubmit={save} onKeyDown={onKeyDown}>
          <label htmlFor={`${ids}-ref`} className="sr-only">
            {`Dashboard branch of ${service.name}`}
          </label>
          <input
            id={`${ids}-ref`}
            className={monoInputClass}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            list={`${ids}-refs`}
            placeholder="main"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            maxLength={255}
            autoFocus
            aria-invalid={problem ? true : undefined}
            aria-describedby={`${ids}-hint${problem ? ` ${ids}-problem` : ''}`}
          />
          <datalist id={`${ids}-refs`}>
            {refs.map((ref) => (
              <option key={ref} value={ref} />
            ))}
          </datalist>
          <p id={`${ids}-hint`} className="text-sm text-text-muted">
            {refs.length > 0
              ? 'Pick a scanned branch or type another one.'
              : 'Type a branch name; it can be chosen before its first scan.'}
          </p>
          {problem && (
            <p id={`${ids}-problem`} role="alert" className="flex items-start gap-2 text-text">
              <CircleAlert
                size={16}
                strokeWidth={1.5}
                aria-hidden="true"
                className="mt-1 shrink-0 text-fail"
              />
              <span>{problem}</span>
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <button
              type="submit"
              className={`${primaryButtonClass} ${small}`}
              aria-label={`Save dashboard branch of ${service.name}`}
              disabled={update.isPending}
            >
              Save
            </button>
            <button
              type="button"
              className={`${secondaryButtonClass} ${small}`}
              aria-label={`Clear dashboard branch of ${service.name}`}
              disabled={update.isPending || shown === null}
              onClick={() =>
                send(
                  null,
                  `Cleared the dashboard branch of ${service.name}; it shows the latest scanned branch.`,
                )
              }
            >
              Clear
            </button>
            <button
              type="button"
              className={`${secondaryButtonClass} ${small}`}
              aria-label={`Cancel editing the dashboard branch of ${service.name}`}
              disabled={update.isPending}
              onClick={close}
            >
              Cancel
            </button>
          </div>
        </form>
      )}
    </div>
  );
}
