// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/react';
import { artifactLabel } from '../format';
import { ArtifactName } from './ArtifactName';

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

describe('ArtifactName', () => {
  it('shows Source tree with the raw name in the title', () => {
    render(<ArtifactName name="." kind="secret-fs" />);
    expect(screen.getByText('Source tree')).toHaveAttribute('title', '.');
  });

  it('shows an image name in mono', () => {
    render(<ArtifactName name="registry/app:1.2" kind="image" />);
    expect(screen.getByText('registry/app:1.2')).toHaveClass('font-mono');
  });
});
