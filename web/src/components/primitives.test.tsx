// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useRef, useState } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { chooseOption } from '../test-utils';
import { Chip, GateBadge, StatusPill, Tag } from './badges';
import { ConfirmDialog } from './ConfirmDialog';
import { Dialog, Sheet } from './Dialog';
import { Select } from './inputs';
import { Menu, MenuContent, MenuItem, MenuTrigger, RowActions } from './Menu';
import { Popover, PopoverContent, PopoverTrigger } from './Popover';
import { SeverityMark, StatusMark } from './SeverityMark';
import { StatTile } from './StatTile';
import { EmptyState, ErrorState, ForbiddenState, NotFoundState, PageLoader } from './states';
import { clearToasts, toast, Toaster } from './Toast';
import { Tooltip } from './Tooltip';

describe('Select', () => {
  const options = [
    { value: '', label: 'All actions' },
    { value: 'login', label: 'Sign-in' },
    { value: 'logout', label: 'Sign-out', disabled: true },
  ];

  it('shows the chosen option and accepts an empty value', async () => {
    const onChange = vi.fn();
    render(<Select label="Action" value="" onChange={onChange} options={options} />);
    const trigger = screen.getByRole('combobox', { name: 'Action' });
    expect(trigger).toHaveTextContent('All actions');
    await chooseOption('Action', 'Sign-in');
    expect(onChange).toHaveBeenCalledWith('login');
  });

  it('reports the empty value as an empty string', async () => {
    const onChange = vi.fn();
    render(<Select label="Action" value="login" onChange={onChange} options={options} />);
    await chooseOption('Action', 'All actions');
    expect(onChange).toHaveBeenCalledWith('');
  });

  it('opens a listbox in a portal, marks the selected option and closes with Escape', async () => {
    const { container } = render(
      <Select label="Action" value="login" onChange={() => {}} options={options} />,
    );
    fireEvent.keyDown(screen.getByRole('combobox', { name: 'Action' }), { key: 'ArrowDown' });
    const listbox = await screen.findByRole('listbox');
    expect(container.contains(listbox)).toBe(false);
    expect(within(listbox).getByRole('option', { name: 'Sign-in' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    expect(within(listbox).getByRole('option', { name: 'Sign-out' })).toHaveAttribute(
      'data-disabled',
    );
    fireEvent.keyDown(listbox, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
  });

  it('shows the placeholder when the value matches no option', () => {
    render(
      <Select
        label="Ref"
        value="gone"
        placeholder="Pick a ref"
        onChange={() => {}}
        options={options}
      />,
    );
    expect(screen.getByRole('combobox', { name: 'Ref' })).toHaveTextContent('Pick a ref');
  });

  it('is disabled with the disabled prop', () => {
    render(<Select label="Action" value="" disabled onChange={() => {}} options={options} />);
    expect(screen.getByRole('combobox', { name: 'Action' })).toBeDisabled();
  });
});

describe('Dialog', () => {
  function Harness({ onClose = () => {} }: { onClose?: () => void }) {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Open
        </button>
        <Dialog
          open={open}
          onOpenChange={(next) => {
            setOpen(next);
            if (!next) onClose();
          }}
          title="Edit service"
          description="Change the name."
          footer={<button type="button">Save</button>}
        >
          <input aria-label="Name" />
        </Dialog>
      </>
    );
  }

  it('renders in a portal with a name, a description and a close button', async () => {
    const { container } = render(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));
    const dialog = await screen.findByRole('dialog', { name: 'Edit service' });
    expect(container.contains(dialog)).toBe(false);
    expect(dialog).toHaveAccessibleDescription('Change the name.');
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeInTheDocument();
  });

  it('closes with Escape and returns focus to the opener', async () => {
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);
    const opener = screen.getByRole('button', { name: 'Open' });
    opener.focus();
    fireEvent.click(opener);
    const dialog = await screen.findByRole('dialog');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    await waitFor(() => expect(opener).toHaveFocus());
  });
});

describe('ConfirmDialog', () => {
  function Harness({ pending = false }: { pending?: boolean }) {
    const [open, setOpen] = useState(true);
    const opener = useRef<HTMLButtonElement>(null);
    return (
      <>
        <button ref={opener} type="button" onClick={() => setOpen(true)}>
          Delete alice
        </button>
        {open ? (
          <ConfirmDialog
            title="Delete alice?"
            confirmLabel="Delete user"
            tone="danger"
            pending={pending}
            onConfirm={() => setOpen(false)}
            onCancel={() => setOpen(false)}
            returnFocusTo={opener}
          >
            <p>This removes alice with their sessions.</p>
          </ConfirmDialog>
        ) : null}
      </>
    );
  }

  it('is an alertdialog that starts on Cancel and uses the danger button', async () => {
    render(<Harness />);
    const dialog = await screen.findByRole('alertdialog', { name: 'Delete alice?' });
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus(),
    );
    expect(within(dialog).getByRole('button', { name: 'Delete user' })).toHaveClass('bg-fail');
    expect(within(dialog).queryByRole('button', { name: 'Close' })).toBeNull();
  });

  it('does not close on Escape while pending', async () => {
    render(<Harness pending />);
    const dialog = await screen.findByRole('alertdialog');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Delete user' })).toBeDisabled();
  });

  it('returns focus to the given control after cancelling', async () => {
    render(<Harness />);
    const dialog = await screen.findByRole('alertdialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete alice' })).toHaveFocus());
  });
});

describe('Sheet', () => {
  it('slides in from the chosen side with a named close button', async () => {
    const onOpenChange = vi.fn();
    render(
      <Sheet
        open
        onOpenChange={onOpenChange}
        title="CVE-2026-1"
        side="left"
        closeLabel="Close details"
      >
        <p>Details</p>
      </Sheet>,
    );
    const sheet = await screen.findByRole('dialog', { name: 'CVE-2026-1' });
    expect(sheet).toHaveClass('anim-sheet-left');
    fireEvent.click(within(sheet).getByRole('button', { name: 'Close details' }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe('Menu', () => {
  it('opens from the keyboard, runs an item and closes', async () => {
    const onSelect = vi.fn();
    render(
      <Menu>
        <MenuTrigger>Actions</MenuTrigger>
        <MenuContent>
          <MenuItem onSelect={onSelect}>Archive</MenuItem>
          <MenuItem tone="danger" onSelect={() => {}}>
            Delete
          </MenuItem>
        </MenuContent>
      </Menu>,
    );
    fireEvent.keyDown(screen.getByRole('button', { name: 'Actions' }), { key: 'Enter' });
    const menu = await screen.findByRole('menu');
    expect(within(menu).getByRole('menuitem', { name: 'Delete' })).toHaveClass('text-fail');
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Archive' }));
    expect(onSelect).toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument());
  });

  it('names the row actions trigger', () => {
    render(
      <RowActions label="Actions for payments-api">
        <MenuItem>Edit</MenuItem>
      </RowActions>,
    );
    expect(screen.getByRole('button', { name: 'Actions for payments-api' })).toHaveAttribute(
      'aria-haspopup',
      'menu',
    );
  });
});

describe('Popover and Tooltip', () => {
  it('opens a popover in a portal and closes it with Escape', async () => {
    render(
      <Popover>
        <PopoverTrigger>Filters</PopoverTrigger>
        <PopoverContent aria-label="Filters">
          <button type="button">Apply</button>
        </PopoverContent>
      </Popover>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Filters' }));
    const dialog = await screen.findByRole('dialog', { name: 'Filters' });
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('shows a tooltip on keyboard focus', async () => {
    render(
      <Tooltip content="Collapse sidebar">
        <button type="button" aria-label="Collapse">
          x
        </button>
      </Tooltip>,
    );
    fireEvent.focus(screen.getByRole('button', { name: 'Collapse' }));
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Collapse sidebar');
  });
});

describe('toast', () => {
  afterEach(() => clearToasts());

  it('shows a toast with its title and lets the person dismiss it', async () => {
    render(<Toaster />);
    act(() => {
      toast('User disabled', { description: 'alice was signed out.' });
    });
    const region = screen.getByRole('region', { name: /Notifications/ });
    expect(within(region).getByText('User disabled')).toBeInTheDocument();
    expect(within(region).getByText('alice was signed out.')).toBeInTheDocument();
    fireEvent.click(within(region).getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(within(region).queryByText('User disabled')).toBeNull());
  });

  it('keeps at most three and marks errors', () => {
    render(<Toaster />);
    act(() => {
      toast('One');
      toast('Two');
      toast('Three');
      toast.error('Saving failed');
    });
    const region = screen.getByRole('region', { name: /Notifications/ });
    expect(within(region).queryByText('One')).toBeNull();
    expect(within(region).getByText('Saving failed').closest('.tone-fail')).not.toBeNull();
  });
});

describe('marks and pills', () => {
  it('draws a different shape for every severity and status', () => {
    const shapes = (['critical', 'high', 'medium', 'low', 'unknown'] as const).map((s) =>
      renderToStaticMarkup(<SeverityMark severity={s} />),
    );
    expect(new Set(shapes).size).toBe(5);
    const statuses = (['ok', 'fail', 'warn', 'none'] as const).map((s) =>
      renderToStaticMarkup(<StatusMark status={s} />),
    );
    expect(new Set(statuses).size).toBe(4);
  });

  it('renders pills and tags with their tone and text', () => {
    render(
      <>
        <Chip tone="brand">Default branch</Chip>
        <StatusPill status="fail">Disabled</StatusPill>
        <Tag>GitLab</Tag>
        <GateBadge status="ERROR" />
      </>,
    );
    expect(screen.getByText('Default branch')).toHaveClass('chip', 'tone-brand');
    expect(screen.getByText('Disabled')).toHaveClass('tone-fail');
    expect(screen.getByText('GitLab')).toHaveClass('tag');
    expect(screen.getByText('Failed').querySelector('[data-status="fail"]')).not.toBeNull();
  });
});

describe('StatTile', () => {
  it('shows a trend next to the value', () => {
    render(
      <StatTile
        label="Open findings"
        value={42}
        trend={<svg data-testid="spark" />}
        delta={{ value: -2 }}
      />,
    );
    expect(screen.getByTestId('spark')).toBeInTheDocument();
    expect(screen.getByText('42')).toHaveClass('tabular-nums');
  });
});

describe('states', () => {
  it('picks a purpose-made illustration per kind', () => {
    const arts = (['lens', 'services', 'search', 'clean', 'pipeline', 'timeline'] as const).map(
      (art) => renderToStaticMarkup(<EmptyState title="x" art={art} />),
    );
    expect(new Set(arts).size).toBe(arts.length);
    const { container } = render(<EmptyState title="Connect" variant="connect" />);
    expect(container.querySelector('[data-art="pipeline"]')).not.toBeNull();
  });

  it('offers the action of an empty state', () => {
    render(
      <EmptyState
        title="No services match"
        art="search"
        action={<button type="button">Clear search and filter</button>}
      />,
    );
    expect(screen.getByRole('heading', { name: 'No services match' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear search and filter' })).toBeInTheDocument();
  });

  it('explains an error and retries', () => {
    const onRetry = vi.fn();
    render(<ErrorState message="The server did not answer." onRetry={onRetry} />);
    expect(screen.getByRole('alert')).toHaveTextContent('The server did not answer.');
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(onRetry).toHaveBeenCalled();
  });

  it('announces loading', () => {
    render(<PageLoader label="Loading your workspace" />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading your workspace…');
  });

  it('has not-found and forbidden pages with a way back', () => {
    render(
      <MemoryRouter>
        <NotFoundState />
        <ForbiddenState />
      </MemoryRouter>,
    );
    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: 'Go to the portfolio' })).toHaveLength(2);
    expect(screen.getByText('You need the admin role for this page')).toBeInTheDocument();
  });
});
