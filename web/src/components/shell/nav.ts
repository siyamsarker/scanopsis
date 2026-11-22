// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import {
  Boxes,
  LayoutGrid,
  Plug,
  ScrollText,
  Search,
  Settings,
  Users,
  type LucideIcon,
} from 'lucide-react';

export interface NavItem {
  to: string;
  label: string;
  Icon: LucideIcon;
  /** Match the path exactly (the portfolio at "/"). */
  end?: boolean;
  /** Words the command palette also matches. */
  keywords?: string[];
}

export interface NavGroup {
  label: string;
  items: NavItem[];
  /** Shown to administrators only. */
  admin?: boolean;
}

/** The application's pages, in the order the sidebar and the command palette list them. */
export const NAV: NavGroup[] = [
  {
    label: 'Overview',
    items: [
      { to: '/', label: 'Portfolio', Icon: LayoutGrid, end: true, keywords: ['home', 'services'] },
      { to: '/search', label: 'Search', Icon: Search, keywords: ['vulnerability', 'cve', 'ghsa'] },
    ],
  },
  {
    label: 'Admin',
    admin: true,
    items: [
      { to: '/admin/services', label: 'Services', Icon: Boxes, keywords: ['projects', 'archive'] },
      { to: '/admin/users', label: 'Users', Icon: Users, keywords: ['roles', 'people'] },
      {
        to: '/admin/integrations',
        label: 'Integrations',
        Icon: Plug,
        keywords: ['gitlab', 'sonarqube', 'uploads', 'tokens'],
      },
      { to: '/admin/audit', label: 'Audit log', Icon: ScrollText, keywords: ['events', 'history'] },
      { to: '/settings', label: 'Settings', Icon: Settings, keywords: ['retention', 'config'] },
    ],
  },
];

/** The groups a person with this role sees. */
export function navFor(isAdmin: boolean): NavGroup[] {
  return NAV.filter((group) => isAdmin || !group.admin);
}

/** CVE-2024-3094, GHSA-xxxx-xxxx-xxxx: a vulnerability identifier the search page understands. */
const VULN_ID = /^(CVE-\d{4}-\d{4,}|GHSA(?:-[23456789cfghjmpqrvwx]{4}){3})$/i;

/** The identifier in upper case when the text is one, otherwise null. */
export function vulnerabilityId(text: string): string | null {
  const trimmed = text.trim();
  if (!VULN_ID.test(trimmed)) return null;
  return trimmed.toUpperCase().startsWith('GHSA')
    ? `GHSA${trimmed.slice(4).toLowerCase()}`
    : trimmed.toUpperCase();
}
