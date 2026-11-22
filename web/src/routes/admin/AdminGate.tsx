// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';
import { useMe } from '../../api/hooks';
import { PageHeader } from '../../components/layout';
import { ForbiddenState, Skeleton } from '../../components/states';

/**
 * Shows the page for admins only. Everyone else gets a calm message and the
 * children never mount, so no admin request is made.
 */
export function AdminGate({
  title,
  subtitle,
  actions,
  children,
}: {
  title: string;
  subtitle: string;
  /** Header actions, shown to admins only. */
  actions?: ReactNode;
  children: ReactNode;
}) {
  const me = useMe();
  const isAdmin = me.data?.role === 'admin';
  return (
    <div>
      <PageHeader title={title} subtitle={subtitle} actions={isAdmin ? actions : undefined} />
      {isAdmin ? (
        children
      ) : me.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : (
        <ForbiddenState title="You need the admin role to see this page." />
      )}
    </div>
  );
}
