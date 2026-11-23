// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { configure, fireEvent, render, screen, within } from '@testing-library/react';
import type { RestoreStatus } from '../../../api/backups';
import { RestoreOverlay } from '../../../components/RestoreOverlay';
import { json, mockFetch } from '../../../test-utils';

configure({ asyncUtilTimeout: 5000 });

function serve(...answers: (RestoreStatus | Error)[]) {
  let i = 0;
  return mockFetch((call) => {
    if (call.path !== '/api/v1/restore/status') return undefined;
    const answer = answers[Math.min(i, answers.length - 1)];
    i += 1;
    if (answer instanceof Error) throw answer;
    return json(200, answer);
  });
}

const running: RestoreStatus = {
  state: 'restoring',
  percent: 40,
  message: 'Restoring a backup.',
  finished_at: null,
};

describe('RestoreOverlay', () => {
  it('follows the restore and offers sign-in when it is done', async () => {
    serve(running, running, running, {
      state: 'done',
      percent: 100,
      message: 'Restore finished. Sign in with an account from the backup.',
      finished_at: '2026-11-23T12:00:00Z',
    });
    const onSignIn = vi.fn();
    render(<RestoreOverlay onSignIn={onSignIn} onBack={vi.fn()} pollMs={20} />);
    const overlay = screen.getByRole('alertdialog', { name: 'Restoring a backup' });
    expect(
      await within(overlay).findByText(
        'Keep this page open. It shows the result when the restore ends.',
      ),
    ).toBeVisible();
    // The server's sentence is not repeated under the same heading.
    expect(within(overlay).queryByText('Restoring a backup.')).toBeNull();
    expect(within(overlay).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
    const button = await screen.findByRole('button', { name: 'Go to sign-in' });
    expect(screen.getByRole('alertdialog', { name: 'Restore finished' })).toHaveTextContent(
      'Sign in with an account from the backup.',
    );
    expect(button).toHaveFocus();
    fireEvent.click(button);
    expect(onSignIn).toHaveBeenCalled();
  });

  it('says nothing was changed when the restore failed and goes back', async () => {
    serve({
      state: 'failed',
      percent: 30,
      message: 'The restore failed and nothing was changed. The server log has the reason.',
      finished_at: '2026-11-23T12:00:00Z',
    });
    const onBack = vi.fn();
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={onBack} pollMs={20} />);
    const overlay = await screen.findByRole('alertdialog', { name: 'The restore did not finish' });
    expect(overlay).toHaveTextContent('Nothing was changed.');
    expect(overlay).toHaveTextContent(
      'The restore failed and nothing was changed. The server log has the reason.',
    );
    fireEvent.click(within(overlay).getByRole('button', { name: 'Back to Backups' }));
    expect(onBack).toHaveBeenCalled();
  });

  it('shows the restart instruction as the server words it, with no way back', async () => {
    const message =
      'The backup was restored, but Scanopsis must restart to finish. Restart it, for example with docker compose restart scanopsis.';
    serve({ state: 'failed', percent: 100, message, finished_at: '2026-11-23T12:00:00Z' });
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={vi.fn()} pollMs={20} />);
    const overlay = await screen.findByRole('alertdialog', {
      name: 'Restart Scanopsis to finish the restore',
    });
    expect(overlay).toHaveTextContent(message);
    expect(within(overlay).queryByRole('button', { name: 'Back to Backups' })).toBeNull();
    expect(overlay).not.toHaveTextContent('Nothing was changed.');
  });

  it('keeps waiting while the server does not answer', async () => {
    serve(new TypeError('Failed to fetch'), running);
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={vi.fn()} pollMs={20} />);
    expect(await screen.findByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
  });
});
