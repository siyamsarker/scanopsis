// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { artifactLabel } from '../format';
import { Mono } from './Mono';

/**
 * A scanned artifact's name. Image names are identifiers and stay in mono; the
 * "Source tree" label of a secret scan is plain text with the raw name in the tooltip.
 */
export function ArtifactName({
  name,
  kind,
  truncate = false,
  className = '',
}: {
  name: string;
  kind: string;
  truncate?: boolean;
  className?: string;
}) {
  const label = artifactLabel(name, kind);
  if (label === name) return <Mono value={name} truncate={truncate} className={className} />;
  return (
    <span title={name || undefined} className={className}>
      {label}
    </span>
  );
}
