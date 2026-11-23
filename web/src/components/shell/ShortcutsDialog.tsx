// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Dialog } from '../Dialog';
import { Kbd } from '../inputs';
import { shortcutList } from './shortcuts';

/** The keyboard shortcuts, opened with "?" or from the account menu. */
export function ShortcutsDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Keyboard shortcuts" size="sm">
      <dl className="divide-y divide-line">
        {shortcutList().map((shortcut) => (
          <div
            key={shortcut.description}
            className="flex items-center justify-between gap-4 py-2.5"
          >
            <dt className="text-body-s text-text">{shortcut.description}</dt>
            <dd className="flex shrink-0 gap-1">
              {shortcut.keys.map((key) => (
                <Kbd key={key}>{key}</Kbd>
              ))}
            </dd>
          </div>
        ))}
      </dl>
    </Dialog>
  );
}
