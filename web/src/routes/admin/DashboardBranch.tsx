// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Check, GitBranch } from 'lucide-react';
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from 'react';
import { useUpdateService, type Service } from '../../api/admin';
import { describeError } from '../../api/errors';
import { useOverview } from '../../api/service';
import { Spinner } from '../../components/ConfirmDialog';
import { Popover, PopoverAnchor, PopoverContent } from '../../components/Popover';
import { toast } from '../../components/Toast';
import { buttonClass, monoInputClass } from '../../ui';
import { Problem } from './kit';

/** How many scanned branches the list shows at once; typing narrows it. */
const MAX_SHOWN = 8;

/**
 * The branch shown by the dashboards of one service, as a branch tag. A click
 * opens an editor in a popover: a combobox of the service's scanned branches
 * (any other name may be typed, so a branch can be chosen before its first
 * scan), Save, and a way back to the latest scanned branch.
 *
 * `open` and `onOpenChange` let the row's action menu open the same editor.
 */
export function DashboardBranch({
  service,
  latest,
  open,
  onOpenChange,
}: {
  service: Service;
  /** The branch the dashboards show while none is chosen, when known. */
  latest?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const chosen = service.default_ref;
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverAnchor asChild>
        <button
          type="button"
          onClick={() => onOpenChange(!open)}
          aria-label={`Dashboard branch of ${service.name}: ${
            chosen ?? (latest ? `latest scanned, ${latest}` : 'latest scanned')
          }. Change`}
          aria-haspopup="dialog"
          aria-expanded={open}
          className={`group inline-flex max-w-full min-w-0 items-center sm:max-w-56 gap-1.5 rounded-tag border px-2 py-0.5 text-small transition-[border-color,background-color] duration-micro ease-enter hover:border-line-strong hover:bg-wash pointer-coarse:min-h-9 ${
            chosen !== null
              ? 'border-line bg-sunken text-text'
              : 'border-dashed border-line-strong text-text-muted'
          } ${open ? 'border-brand!' : ''}`}
        >
          <GitBranch
            size={13}
            strokeWidth={1.75}
            aria-hidden="true"
            className={chosen !== null ? 'text-brand' : 'text-text-faint'}
          />
          {chosen !== null ? (
            <span className="truncate font-mono">{chosen}</span>
          ) : (
            <span className="flex min-w-0 items-center gap-1">
              <span className="shrink-0">Latest</span>
              {latest ? <span className="truncate font-mono text-text-faint">{latest}</span> : null}
            </span>
          )}
        </button>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        className="w-80"
        aria-label={`Dashboard branch of ${service.name}`}
      >
        {open ? (
          <BranchEditor service={service} latest={latest} onDone={() => onOpenChange(false)} />
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

function BranchEditor({
  service,
  latest,
  onDone,
}: {
  service: Service;
  latest?: string;
  onDone: () => void;
}) {
  const update = useUpdateService();
  const overview = useOverview(service.id, undefined, true);
  const refs = useMemo(() => overview.data?.refs ?? [], [overview.data]);
  const [value, setValue] = useState(service.default_ref ?? '');
  const [active, setActive] = useState(-1);
  const [problem, setProblem] = useState('');
  const ids = useId();
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);

  const query = value.trim().toLowerCase();
  const matches = useMemo(() => {
    const all = query ? refs.filter((ref) => ref.toLowerCase().includes(query)) : refs;
    return all.slice(0, MAX_SHOWN);
  }, [refs, query]);

  function send(default_ref: string | null) {
    update.mutate(
      { id: service.id, body: { default_ref } },
      {
        onSuccess: () => {
          onDone();
          toast.success('Dashboard branch changed', {
            description:
              default_ref === null
                ? `${service.name} shows the latest scanned branch.`
                : `${service.name} shows ${default_ref}.`,
          });
        },
        onError: (error) => setProblem(describeError(error)),
      },
    );
  }

  function save(event?: FormEvent) {
    event?.preventDefault();
    const branch = (active >= 0 && matches[active] ? matches[active] : value).trim();
    if (branch === '') {
      setProblem('Enter a branch name, or show the latest scanned branch instead.');
      return;
    }
    if (branch === service.default_ref) {
      onDone();
      return;
    }
    send(branch);
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setActive((i) => (matches.length === 0 ? -1 : (i + 1) % matches.length));
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      setActive((i) => (matches.length === 0 ? -1 : i <= 0 ? matches.length - 1 : i - 1));
    } else if (event.key === 'Enter' && active >= 0 && matches[active]) {
      event.preventDefault();
      setValue(matches[active]);
      setActive(-1);
    }
  }

  const listId = `${ids}-list`;
  const optionId = (i: number) => `${ids}-opt-${i}`;
  return (
    <form onSubmit={save} noValidate className="flex flex-col gap-3">
      <div>
        <p className="text-body-s font-semibold text-text">Dashboard branch</p>
        <p className="mt-0.5 text-caption text-text-muted">
          The branch every dashboard of {service.name} shows. It need not have scans yet.
        </p>
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`${ids}-ref`} className="sr-only">
          Branch name
        </label>
        <input
          ref={input}
          id={`${ids}-ref`}
          role="combobox"
          aria-expanded={matches.length > 0}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={active >= 0 ? optionId(active) : undefined}
          aria-invalid={problem ? true : undefined}
          className={monoInputClass}
          value={value}
          placeholder={latest ?? 'main'}
          onChange={(e) => {
            setValue(e.target.value);
            setActive(-1);
            setProblem('');
          }}
          onKeyDown={onKeyDown}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          maxLength={255}
        />
        <ul
          id={listId}
          role="listbox"
          aria-label="Scanned branches"
          className="flex max-h-56 flex-col overflow-y-auto"
        >
          {overview.isPending ? (
            <li className="flex items-center gap-2 px-2 py-2 text-caption text-text-muted">
              <Spinner className="size-3.5" />
              Loading scanned branches
            </li>
          ) : null}
          {matches.map((ref, i) => {
            const selected = ref === service.default_ref;
            return (
              <li
                key={ref}
                id={optionId(i)}
                role="option"
                aria-selected={i === active}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  setValue(ref);
                  setActive(-1);
                  input.current?.focus();
                }}
                className={`flex min-h-8 cursor-default items-center gap-2 rounded-[7px] px-2 text-body-s pointer-coarse:min-h-11 ${
                  i === active ? 'bg-wash text-text' : 'text-text hover:bg-wash'
                }`}
              >
                <GitBranch
                  size={13}
                  strokeWidth={1.75}
                  aria-hidden="true"
                  className="shrink-0 text-text-faint"
                />
                <span className="min-w-0 flex-1 truncate font-mono text-small">{ref}</span>
                {ref === refs[0] ? (
                  <span className="shrink-0 text-caption text-text-faint">latest scan</span>
                ) : null}
                {selected ? (
                  <Check
                    size={14}
                    strokeWidth={2}
                    aria-label="chosen"
                    className="shrink-0 text-brand"
                  />
                ) : null}
              </li>
            );
          })}
        </ul>
        {!overview.isPending && refs.length === 0 ? (
          <p className="px-0.5 text-caption text-text-muted">
            No scans yet. Type the branch the first upload will come from.
          </p>
        ) : !overview.isPending && matches.length === 0 && query ? (
          <p className="px-0.5 text-caption text-text-muted">
            No scanned branch matches. Save to choose{' '}
            <span className="font-mono text-text">{value.trim()}</span> anyway.
          </p>
        ) : null}
      </div>
      {problem ? <Problem>{problem}</Problem> : null}
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3">
        <button
          type="button"
          className={buttonClass('ghost', 'sm')}
          disabled={update.isPending || service.default_ref === null}
          onClick={() => send(null)}
        >
          Use latest scanned
        </button>
        <button
          type="submit"
          className={buttonClass('primary', 'sm')}
          disabled={update.isPending}
          aria-busy={update.isPending || undefined}
        >
          {update.isPending ? <Spinner className="size-3.5" /> : null}
          Save branch
        </button>
      </div>
    </form>
  );
}
