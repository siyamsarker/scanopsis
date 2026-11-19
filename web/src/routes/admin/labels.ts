// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/** Plain-language explanation for each reason code a scan upload can be rejected with. */
export const rejectionReasons: Record<string, string> = {
  invalid_token: 'The GitLab ID token was missing, expired or could not be verified.',
  namespace_not_allowed: 'The project is outside the GitLab namespaces allowed to upload.',
  ref_not_protected: 'The upload came from a branch or tag that is not protected.',
  report_too_large: 'The report is larger than the upload size limit.',
  not_a_trivy_report: 'The file is JSON but not a Trivy report.',
  invalid_report: 'The Trivy report is malformed or incomplete.',
  unsupported_media_type: 'The upload was not sent as JSON.',
  too_many_requests: 'The project uploaded too often and was rate limited.',
  service_archived: 'The service is archived and receives no new uploads.',
  service_conflict: 'The project path conflicts with another service.',
  gitlab_unavailable: 'Scanopsis could not reach GitLab to verify the project.',
};

/** Plain label for each audit action. */
export const auditActions: Record<string, string> = {
  'login.succeeded': 'Signed in',
  'login.failed': 'Sign-in failed',
  logout: 'Signed out',
  'user.created': 'User created',
  'user.role_changed': 'Role changed',
  'user.password_changed': 'Password changed',
  'user.password_reset': 'Password reset',
  'mfa.enrolled': 'Two-factor set up',
  'mfa.verified': 'Two-factor verified',
  'mfa.failed': 'Two-factor failed',
  'mfa.locked': 'Two-factor locked',
  'mfa.reset': 'Two-factor reset',
  'setup.completed': 'Setup completed',
  'settings.changed': 'Settings changed',
  'service.created': 'Service created',
  'service.updated': 'Service updated',
  'service.deleted': 'Service deleted',
  'ingest.rejected': 'Upload rejected',
};
