// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/** Section of the settings page each setting belongs to, in page order. */
export const settingGroups = [
  {
    id: 'gitlab',
    title: 'GitLab',
    lead: 'The GitLab instance people sign in with and CI tokens come from.',
  },
  { id: 'sign-in', title: 'Sign-in', lead: 'How people may sign in.' },
  { id: 'sessions', title: 'Sessions', lead: 'How long a sign-in lasts.' },
  { id: 'sonarqube', title: 'SonarQube', lead: 'The server analyses are read from.' },
  { id: 'uploads', title: 'Scan uploads', lead: 'Which pipelines may send Trivy reports.' },
  { id: 'retention', title: 'Retention', lead: 'How long scan history is kept.' },
] as const;

export type SettingGroup = (typeof settingGroups)[number]['id'];

export function groupOf(key: string): SettingGroup {
  if (key.startsWith('gitlab.')) return 'gitlab';
  if (key.startsWith('session')) return 'sessions';
  if (key.startsWith('ingest.')) return 'uploads';
  if (key.startsWith('sonar.')) return 'sonarqube';
  if (key.startsWith('retention.')) return 'retention';
  return 'sign-in';
}

/** A short label per setting; the server's description becomes the help text. */
export const settingLabels: Record<string, string> = {
  'gitlab.url': 'GitLab URL',
  'gitlab.client_id': 'Application ID',
  'gitlab.client_secret': 'Application secret',
  'gitlab.allowed_groups': 'Groups that may sign in',
  'auth.local_login_enabled': 'Local accounts',
  'session.idle_timeout': 'Idle timeout',
  'session.absolute_timeout': 'Longest session',
  'ingest.allowed_namespaces': 'Namespaces that may upload',
  'ingest.protected_refs_only': 'Protected branches and tags only',
  'sonar.url': 'SonarQube URL',
  'sonar.token': 'Access token',
  'sonar.webhook_secret': 'Webhook secret',
  'sonar.sync_interval': 'Sync interval',
  'retention.days': 'Keep scan history for',
};

/** The short label of a setting, or its key when it has none. */
export function settingLabel(key: string): string {
  return settingLabels[key] ?? key;
}
