// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/react';
import Showcase from './Showcase';

describe('Showcase', () => {
  it('renders every section with sample data, in both themes', () => {
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(400);
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(800);
    render(<Showcase />);
    for (const name of [
      'Atoms in both themes',
      'Overlays and feedback',
      'Controls',
      'States',
      'Charts',
      'Tables',
    ]) {
      expect(screen.getByRole('region', { name })).toBeInTheDocument();
    }
    expect(document.querySelectorAll('[data-theme-scope]')).toHaveLength(2);
    expect(screen.getByRole('table', { name: /^Sample services/ })).toBeInTheDocument();
    expect(screen.getByRole('table', { name: 'Services' })).toBeInTheDocument();
    expect(screen.getByRole('table', { name: 'Findings' })).toBeInTheDocument();
    vi.restoreAllMocks();
  });
});
