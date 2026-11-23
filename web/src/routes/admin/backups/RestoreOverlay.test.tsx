// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { QueryClientProvider } from '@tanstack/react-query';
import { configure, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import type { RestoreStatus } from '../../../api/backups';
import { meKey, setupKey } from '../../../api/hooks';
import { createQueryClient } from '../../../api/queryClient';
import { RestoreOverlay, RestoreOverlayHost } from '../../../components/RestoreOverlay';
import { apiError, json, makeUser, mockFetch } from '../../../test-utils';

configure({ asyncUtilTimeout: 5000 });

function serve(...answers: (Partial<RestoreStatus> | Error | Response)[]) {
  let i = 0;
  return mockFetch((call) => {
    if (call.path !== '/api/v1/restore/status') return undefined;
    const answer = answers[Math.min(i, answers.length - 1)];
    i += 1;
    if (answer instanceof Error) throw answer;
    if (answer instanceof Response) return answer.clone();
    return json(200, { restart_required: false, finished_at: null, ...answer });
  });
}

const running: RestoreStatus = {
  state: 'restoring',
  percent: 40,
  message: 'Restoring a backup.',
  finished_at: null,
  restart_required: false,
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

  it('shows the server sentence once when the restore failed, and goes back', async () => {
    serve({
      state: 'failed',
      percent: 30,
      message:
        'The restore failed and nothing was changed. Admins see the reason on the Backups page.',
      finished_at: '2026-11-23T12:00:00Z',
    });
    const onBack = vi.fn();
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={onBack} pollMs={20} />);
    const overlay = await screen.findByRole('alertdialog', { name: 'The restore did not finish' });
    expect(overlay).toHaveTextContent(
      'The restore failed and nothing was changed. Admins see the reason on the Backups page.',
    );
    // Said once, in the server's words.
    expect(within(overlay).queryByText('Nothing was changed.')).toBeNull();
    fireEvent.click(within(overlay).getByRole('button', { name: 'Back to Backups' }));
    expect(onBack).toHaveBeenCalled();
  });

  it('shows the restart instruction as the server words it, with no way back', async () => {
    const message =
      'The backup was restored, but Scanopsis must restart to finish. Restart it, for example with docker compose restart scanopsis.';
    serve({ state: 'failed', percent: 100, message, restart_required: true });
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={vi.fn()} pollMs={20} />);
    const overlay = await screen.findByRole('alertdialog', {
      name: 'Restart Scanopsis to finish the restore',
    });
    expect(overlay).toHaveTextContent(message);
    expect(within(overlay).queryByRole('button', { name: 'Back to Backups' })).toBeNull();
  });

  it('goes by the restart flag, whatever the sentence', async () => {
    serve({ state: 'failed', message: 'Something else went wrong.', restart_required: true });
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={vi.fn()} pollMs={20} />);
    const overlay = await screen.findByRole('alertdialog', {
      name: 'Restart Scanopsis to finish the restore',
    });
    expect(within(overlay).queryByRole('button')).toBeNull();
  });

  it('keeps waiting while the server does not answer', async () => {
    serve(new TypeError('Failed to fetch'), new TypeError('Failed to fetch'), running);
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={vi.fn()} pollMs={20} />);
    expect(
      await screen.findByText('Scanopsis is not answering right now. Trying again.'),
    ).toBeVisible();
    expect(await screen.findByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
    expect(screen.queryByText(/Trying again/)).toBeNull();
  });

  it('says the server answered with an error, not that it is down', async () => {
    serve(apiError(500, 'internal', 'Internal error.'));
    render(<RestoreOverlay onSignIn={vi.fn()} onBack={vi.fn()} pollMs={20} />);
    expect(
      await screen.findByText('Scanopsis answered with an error. Trying again.'),
    ).toBeVisible();
    expect(screen.queryByText(/not answering/)).toBeNull();
  });

  it.each([
    ['admin', 'Back to Backups', '/admin/backups'],
    ['viewer', 'Back to the portfolio', '/'],
    ['setup', 'Back to setup', '/setup'],
  ] as const)('leads a failed restore back by role (%s)', async (role, label, path) => {
    serve({
      state: 'failed',
      message:
        'The restore failed and nothing was changed. Admins see the reason on the Backups page.',
    });
    const client = createQueryClient();
    if (role === 'setup') client.setQueryData(setupKey, { required: true });
    else client.setQueryData(meKey, makeUser({ role }));
    function Probe() {
      const location = useLocation();
      return <output data-testid="location">{location.pathname}</output>;
    }
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={['/services/7']}>
          <RestoreOverlayHost />
          <Probe />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: label }));
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe(path));
  });
});
