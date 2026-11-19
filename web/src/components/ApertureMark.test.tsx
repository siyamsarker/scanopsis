// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/react';
import { ApertureMark, Wordmark } from './ApertureMark';

describe('ApertureMark', () => {
  it('draws two arcs in currentColor and is hidden from assistive technology', () => {
    const { container } = render(<ApertureMark size={16} />);
    const svg = container.querySelector('svg');
    expect(svg).toHaveAttribute('aria-hidden', 'true');
    expect(svg).toHaveAttribute('width', '16');
    expect(svg).toHaveAttribute('stroke', 'currentColor');
    expect(svg).toHaveAttribute('fill', 'none');
    expect(container.querySelectorAll('path')).toHaveLength(2);
    expect(container.querySelector('linearGradient, radialGradient')).toBeNull();
  });

  it('pairs the mark with the wordmark text', () => {
    render(<Wordmark />);
    expect(screen.getByText('scanopsis')).toBeInTheDocument();
  });
});
