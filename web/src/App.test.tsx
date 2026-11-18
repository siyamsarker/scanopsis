// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/react';
import { App } from './App';

describe('App', () => {
  it('renders the wordmark as the page heading', () => {
    render(<App />);
    expect(screen.getByRole('heading', { level: 1, name: 'scanopsis' })).toBeInTheDocument();
  });

  it('renders the description and status line', () => {
    render(<App />);
    expect(
      screen.getByText('SonarQube and Trivy results from your pipelines, in one view.'),
    ).toBeInTheDocument();
    expect(screen.getByText('The dashboard is being set up.')).toBeInTheDocument();
  });
});
