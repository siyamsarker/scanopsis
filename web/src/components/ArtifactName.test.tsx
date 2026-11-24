// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/react';
import { artifactLabel, scannedName } from '../format';
import { ArtifactDetail, ArtifactName } from './ArtifactName';

describe('artifactLabel', () => {
  it('names a source tree secret scan', () => {
    expect(artifactLabel('.', 'secret-fs')).toBe('Source tree');
    expect(artifactLabel('', 'secret-fs')).toBe('Source tree');
    expect(artifactLabel(null, 'secret-fs')).toBe('Source tree');
  });

  it('keeps every other name as reported', () => {
    expect(artifactLabel('registry/app:1.2', 'image')).toBe('registry/app:1.2');
    expect(artifactLabel('.', 'image')).toBe('.');
    expect(artifactLabel('src/app', 'secret-fs')).toBe('src/app');
  });
});

describe('scannedName', () => {
  const image = { artifact_key: 'web-frontend-stage', kind: 'image' };
  it('is the reported name of an image when it adds a tag or digest', () => {
    expect(scannedName({ ...image, artifact_name: 'web-frontend-stage:4' })).toBe(
      'web-frontend-stage:4',
    );
    expect(scannedName({ ...image, artifact_name: 'web-frontend-stage' })).toBeNull();
  });

  it('is nothing for a secret scan, whose name is a folder on the runner', () => {
    expect(
      scannedName({
        artifact_key: '.',
        artifact_name: '/builds/x/0/demo/web-frontend',
        kind: 'secret-fs',
      }),
    ).toBeNull();
  });
});

describe('ArtifactName', () => {
  it('shows a tagged image by its key in mono, the scanned name in the title', () => {
    render(
      <ArtifactName
        artifact={{
          artifact_key: 'web-frontend-stage',
          artifact_name: 'web-frontend-stage:4',
          kind: 'image',
        }}
      />,
    );
    const name = screen.getByText('web-frontend-stage');
    expect(name).toHaveClass('font-mono');
    expect(name).toHaveAttribute('title', 'web-frontend-stage:4');
    expect(screen.queryByText(/:4/)).not.toBeInTheDocument();
  });

  it('shows Source tree for the repository, the scanned folder in the title', () => {
    render(
      <ArtifactName
        artifact={{
          artifact_key: '.',
          artifact_name: '/builds/x/0/demo/web-frontend',
          kind: 'secret-fs',
        }}
      />,
    );
    expect(screen.getByText('Source tree')).toHaveAttribute(
      'title',
      '/builds/x/0/demo/web-frontend',
    );
  });

  it('shows a folder of the repository by its path', () => {
    render(
      <ArtifactName
        artifact={{
          artifact_key: 'services/api',
          artifact_name: '/builds/x/0/demo/web-frontend/services/api',
          kind: 'secret-fs',
        }}
      />,
    );
    expect(screen.getByText('services/api')).toHaveClass('font-mono');
  });
});

describe('ArtifactDetail', () => {
  it('names the latest tag of an image', () => {
    render(
      <ArtifactDetail
        label="Latest"
        artifact={{
          artifact_key: 'web-frontend-stage',
          artifact_name: 'web-frontend-stage:4',
          kind: 'image',
        }}
      />,
    );
    const detail = screen.getByText(/^Latest:/);
    expect(detail).toHaveTextContent('Latest: web-frontend-stage:4');
    expect(detail).toHaveAttribute('title', 'web-frontend-stage:4');
    // A long name is cut before the tag, never the tag first.
    expect(screen.getByText(':4')).not.toHaveClass('min-w-[2ch]');
    expect(screen.getByText('web-frontend-stage')).toHaveClass('truncate');
  });

  it('shows nothing when the name is the key', () => {
    const { container } = render(
      <ArtifactDetail
        label="Latest"
        artifact={{ artifact_key: 'app', artifact_name: 'app', kind: 'image' }}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
