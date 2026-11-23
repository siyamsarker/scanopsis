// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

/** On and off as a switch: a button with role switch, named by the row's label. */
export function Switch({
  id,
  checked,
  onChange,
  describedBy,
}: {
  id: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  describedBy?: string;
}) {
  return (
    <span className="inline-flex min-h-10 items-center gap-3">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        aria-describedby={describedBy}
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition-colors duration-micro ease-enter pointer-coarse:h-7 pointer-coarse:w-12 ${
          checked ? 'border-brand bg-brand' : 'border-line-strong bg-sunken'
        }`}
      >
        <span
          aria-hidden="true"
          className={`inline-block size-4.5 rounded-full shadow-1 transition-transform duration-micro ease-enter pointer-coarse:size-5.5 ${
            checked
              ? 'translate-x-[1.375rem] bg-brand-ink pointer-coarse:translate-x-[1.5rem]'
              : 'translate-x-0.5 bg-text-muted'
          }`}
        />
      </button>
      <span aria-hidden="true" className="text-body-s text-text">
        {checked ? 'On' : 'Off'}
      </span>
    </span>
  );
}
