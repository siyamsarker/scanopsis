// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ApiError } from '../../api/client';
import { Card } from '../../components/layout';
import { ErrorState } from '../../components/states';

export const SCAN_KIND_LABEL: Record<string, string> = {
  image: 'Image',
  'secret-fs': 'Secret scan',
};

/** A section that failed on its own: plain words, the request code and a retry. */
export function SectionError({
  what,
  error,
  onRetry,
  retrying,
  bare = false,
}: {
  what: string;
  error: unknown;
  onRetry: () => void;
  retrying?: boolean;
  /** Without the surrounding card, for use inside one. */
  bare?: boolean;
}) {
  const state = (
    <ErrorState
      compact
      title={`${what} could not be loaded`}
      message="The rest of the page is not affected. Try again in a moment."
      detail={error instanceof ApiError ? `${error.code} (${error.status})` : undefined}
      onRetry={onRetry}
      retrying={retrying}
    />
  );
  return bare ? state : <Card padded={false}>{state}</Card>;
}
