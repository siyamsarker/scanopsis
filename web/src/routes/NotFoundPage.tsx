// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useLocation } from 'react-router';
import { NotFoundState } from '../components/states';

export function NotFoundPage() {
  const { pathname } = useLocation();
  return (
    <NotFoundState
      body={
        <>
          Nothing lives at <span className="font-mono break-all text-text">{pathname}</span>. Check
          the address, or press <kbd className="font-sans font-medium text-text">Ctrl K</kbd> to
          jump to a service or page.
        </>
      }
    />
  );
}
