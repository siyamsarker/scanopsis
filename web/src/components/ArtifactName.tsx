// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { artifactLabel, scannedName, scannedTag, type ArtifactRef } from '../format';
import { Mono } from './Mono';

/**
 * A scanned artifact, named by its key: an image without its tag, or the
 * scanned folder of the repository. Image names and paths are identifiers and
 * stay in mono; the repository itself reads "Source tree". The name the scan
 * reported, with its tag or the runner's folder, is in the tooltip.
 */
export function ArtifactName({
  artifact,
  truncate = false,
  className = '',
}: {
  artifact: ArtifactRef;
  truncate?: boolean;
  className?: string;
}) {
  const key = artifact.artifact_key;
  const label = artifactLabel(key, artifact.kind);
  const reported = artifact.artifact_name !== key ? artifact.artifact_name : undefined;
  if (label === key)
    return <Mono value={key} title={reported} truncate={truncate} className={className} />;
  return (
    <span title={reported ?? (key || undefined)} className={className}>
      {label}
    </span>
  );
}

/**
 * The tag or digest an image was scanned with, as a muted line under its
 * name: "Latest: web-frontend-stage:4". A long name is cut in the middle, so
 * the tag stays in view. Nothing when the name is the key.
 */
export function ArtifactDetail({
  artifact,
  label,
  className = '',
}: {
  artifact: ArtifactRef;
  /** What the name is to the reader, "Latest"; without it the name stands alone. */
  label?: string;
  className?: string;
}) {
  const name = scannedName(artifact);
  const tag = scannedTag(artifact);
  if (name === null || tag === null) return null;
  const head = name.slice(0, name.length - tag.length);
  return (
    <span
      title={name}
      className={`flex min-w-0 max-w-full whitespace-nowrap text-caption text-text-muted ${className}`}
    >
      {label ? `${label}:\u00a0` : null}
      <span className="flex min-w-0 font-mono">
        {head ? <span className="min-w-[2ch] truncate">{head}</span> : null}
        {/* A tag stays whole; a long digest may be cut too. */}
        <span className={tag.length > 16 ? 'min-w-[4ch] truncate' : 'shrink-0'}>{tag}</span>
      </span>
    </span>
  );
}
