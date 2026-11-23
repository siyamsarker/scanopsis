// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen } from '@testing-library/react';
import {
  EmptyState,
  ErrorState,
  Skeleton,
  SkeletonChart,
  SkeletonStat,
  SkeletonTable,
  SkeletonText,
} from './states';

describe('EmptyState', () => {
  it('shows the aperture motif, a title, a body and an action', () => {
    const { container } = render(
      <EmptyState
        title="No services yet"
        body="Services appear after the first scan."
        action={<button type="button">Add a service</button>}
      />,
    );
    expect(screen.getByRole('heading', { name: 'No services yet' })).toBeInTheDocument();
    expect(screen.getByText('Services appear after the first scan.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add a service' })).toBeInTheDocument();
    expect(container.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
  });

  it('lists numbered steps in the connect variant', () => {
    render(
      <EmptyState
        variant="connect"
        title="Connect your first pipeline"
        steps={[{ title: 'Create a token', body: 'In settings.' }, { title: 'Add the job' }]}
      />,
    );
    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent('Step 1: Create a token');
    expect(items[0]).toHaveTextContent('In settings.');
    expect(items[1]).toHaveTextContent('Step 2: Add the job');
  });

  it('ignores steps outside the connect variant', () => {
    render(<EmptyState title="Empty" steps={[{ title: 'x' }]} />);
    expect(screen.queryByRole('list')).toBeNull();
  });
});

describe('ErrorState', () => {
  it('announces what happened and retries', () => {
    const retry = vi.fn();
    render(
      <ErrorState message="The server did not answer." detail="request abc123" onRetry={retry} />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('The server did not answer.');
    expect(screen.getByText('request abc123')).toHaveClass('font-mono');
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it('disables the button while retrying and has no button without a handler', () => {
    const { rerender } = render(<ErrorState message="Failed." onRetry={() => {}} retrying />);
    expect(screen.getByRole('button', { name: 'Trying again' })).toBeDisabled();
    rerender(<ErrorState message="Failed." />);
    expect(screen.queryByRole('button')).toBeNull();
  });
});

describe('Skeleton', () => {
  it('hides decorative blocks from assistive technology', () => {
    const { container } = render(
      <>
        <Skeleton className="h-4 w-20" />
        <SkeletonText lines={3} />
      </>,
    );
    expect(container.querySelectorAll('[aria-hidden="true"].skeleton')).toHaveLength(4);
  });

  it('announces loading once for composite skeletons', () => {
    render(
      <>
        <SkeletonStat />
        <SkeletonTable rows={3} columns={2} />
        <SkeletonChart />
      </>,
    );
    expect(screen.getAllByRole('status', { name: 'Loading' })).toHaveLength(2);
    expect(screen.getByRole('status', { name: 'Loading chart' })).toBeInTheDocument();
  });

  it('matches table density row heights', () => {
    const { container, rerender } = render(<SkeletonTable rows={2} density="compact" />);
    expect(container.querySelector('.h-8')).not.toBeNull();
    rerender(<SkeletonTable rows={2} />);
    expect(container.querySelector('.h-11')).not.toBeNull();
  });
});
