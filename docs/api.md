# Scanopsis API reference

<!-- Generated from api/openapi.yaml by `go run ./scripts/apidocs > docs/api.md`. Do not edit by hand. -->

This page lists every endpoint of the HTTP API, generated from the OpenAPI
document `api/openapi.yaml`. API version: 0.1.0.

JSON API of Scanopsis. Every endpoint under /api/v1 except the authentication endpoints requires a session cookie. State-changing requests from browsers must be same-origin (checked with the Sec-Fetch-Site and Origin headers).

## Authentication

Each endpoint names how it is authenticated.

| Label | Meaning |
|---|---|
| None | No credentials are needed. |
| Session | A session cookie from a completed sign-in (password or GitLab, then two-factor code). |
| Session, admin | A session whose user has the admin role. |
| CI ID token | A GitLab CI/CD ID token in `Authorization: Bearer <token>`. |
| HMAC signature | The HMAC-SHA256 signature of the body, in a request header. |

Endpoints that need a session answer `401` without one and `403` while the
session still needs two-factor authentication or a password change.

## Endpoints

### `GET /api/v1/admin/audit`

Audit log, newest first (admin only).

- Operation ID: `listAuditEvents`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `action` | query | no | string (maxLength 100) | Exact action |
| `actor` | query | no | string (maxLength 200) | Substring of the actor |
| `before` | query | no | integer (int64, minimum 1) | Return events with an id lower than this value. |
| `limit` | query | no | integer (minimum 1, maximum 200, default 50) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page of events. | `application/json` [AuditPage](#auditpage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |

### `GET /api/v1/admin/integrations/sonarqube`

Health of the SonarQube integration (admin only).

- Operation ID: `getSonarIntegration`
- Authentication: Session, admin

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Status. | `application/json` [SonarIntegration](#sonarintegration) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |

### `GET /api/v1/admin/integrations/trivy`

Health of scan ingestion (admin only).

- Operation ID: `getTrivyIntegration`
- Authentication: Session, admin

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Status. | `application/json` [TrivyIntegration](#trivyintegration) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |

### `GET /api/v1/auth/gitlab/callback`

GitLab redirect target. Completes sign-in.

On success, sets the session cookie and redirects to return_to. On failure, redirects to /login?error=<code> where code is one of access_denied, group_not_allowed, login_expired, login_failed.

- Operation ID: `gitlabCallback`
- Authentication: None

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `code` | query | no | string |  |
| `error` | query | no | string |  |
| `state` | query | no | string |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 302 | Redirect to the application or to the sign-in page with an error code. | none |

### `GET /api/v1/auth/gitlab/login`

Start GitLab sign-in.

Redirects the browser to GitLab (authorization code flow with PKCE, state and nonce). return_to must be a local path starting with a single "/"; anything else is replaced by "/".

- Operation ID: `gitlabLogin`
- Authentication: None

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `return_to` | query | no | string |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 302 | Redirect to the GitLab authorization endpoint. | none |
| 404 | GitLab sign-in is not configured. | `application/json` [Error](#error) |
| 503 | GitLab could not be reached. | `application/json` [Error](#error) |

### `POST /api/v1/auth/local/login`

Sign in as the local administrator.

- Operation ID: `localLogin`
- Authentication: None

Request body (required):

- `application/json`: [LocalLoginRequest](#localloginrequest)

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Signed in. The session cookie is set. | none |
| 400 | Malformed request. | `application/json` [Error](#error) |
| 401 | Wrong username or password. | `application/json` [Error](#error) |
| 404 | Local sign-in is disabled. | `application/json` [Error](#error) |
| 429 | Too many attempts from this network or for this username. Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is busy hashing other passwords and the request ended while waiting. Try again. | `application/json` [Error](#error) |

### `POST /api/v1/auth/logout`

- Operation ID: `logout`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Signed out. The session is deleted and the cookie cleared. | none |

### `GET /api/v1/auth/methods`

Sign-in methods enabled on this server.

- Operation ID: `getAuthMethods`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Enabled methods. | `application/json` [AuthMethods](#authmethods) |

### `POST /api/v1/auth/mfa/enrollment`

Start TOTP enrolment and get the secret to add to an authenticator app.

Allowed for a session whose user has not enrolled (first sign-in or after a reset). Calling it again replaces the pending secret.

- Operation ID: `startMFAEnrollment`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Pending enrolment. | `application/json` [MFAEnrollment](#mfaenrollment) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 409 | Already enrolled (code mfa_already_enrolled). | `application/json` [Error](#error) |

### `POST /api/v1/auth/mfa/enrollment/confirm`

Confirm enrolment with a code from the app and receive recovery codes.

- Operation ID: `confirmMFAEnrollment`
- Authentication: Session

Request body (required):

- `application/json`: [MFACode](#mfacode)

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Enrolled. The recovery codes are shown only this once; the session is fully usable. | `application/json` [RecoveryCodes](#recoverycodes) |
| 400 | Malformed request. | `application/json` [Error](#error) |
| 401 | Wrong code (code invalid_code) | `application/json` [Error](#error) |
| 409 | No pending enrolment (code mfa_enrollment_not_started) or already enrolled. | `application/json` [Error](#error) |
| 429 | Too many attempts. | `application/json` [Error](#error) |

### `POST /api/v1/auth/mfa/verify`

Complete sign-in with a TOTP code or a recovery code.

Allowed for a session that has passed the first factor and whose user has enrolled.

- Operation ID: `verifyMFA`
- Authentication: Session

Request body (required):

- `application/json`: [MFACode](#mfacode)

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Second factor accepted; the session is fully usable. | none |
| 400 | Malformed request. | `application/json` [Error](#error) |
| 401 | Wrong or reused code (code invalid_code) | `application/json` [Error](#error) |
| 409 | Two-factor authentication is not enrolled yet (code mfa_enrollment_required). | `application/json` [Error](#error) |
| 429 | Too many attempts. Retry-After gives seconds. | `application/json` [Error](#error) |

### `POST /api/v1/hooks/sonarqube`

SonarQube webhook target.

Verified with the X-Sonar-Webhook-HMAC-SHA256 header (hex HMAC-SHA256 of the raw body with the webhook secret). Deliveries are deduplicated by taskId. The analysis is then read from the SonarQube Web API.

- Operation ID: `sonarWebhook`
- Authentication: HMAC signature (`X-Sonar-Webhook-HMAC-SHA256` header)

Request body (required):

- `application/json`: object

Responses:

| Status | Description | Body |
|---|---|---|
| 202 | Accepted (also for a duplicate delivery). | none |
| 400 | Malformed payload (code invalid_payload). | `application/json` [Error](#error) |
| 401 | Missing or wrong signature (code invalid_signature). | `application/json` [Error](#error) |
| 413 | Payload too large. | `application/json` [Error](#error) |
| 503 | SonarQube integration not configured (code sonar_not_configured). | `application/json` [Error](#error) |

### `POST /api/v1/ingest/trivy`

Upload one Trivy JSON report from a GitLab CI job.

Authenticated with the job's GitLab CI/CD ID token (`Authorization: Bearer <token>`), whose audience must be the Scanopsis public URL. The project, pipeline, job, ref and commit come from the verified token, never from the report. The body is the raw report (`Content-Type: application/json`), optionally gzip-compressed (`Content-Encoding: gzip`). A repeated upload for the same job, artifact and kind replaces the earlier one. Every rejection is written to the audit log.

- Operation ID: `ingestTrivy`
- Authentication: CI ID token

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `kind` | query | no | string (one of `image`, `secret-fs`, default image) | image (default) for `trivy image`, secret-fs for `trivy fs --scanners secret`. |

Request body (required):

- `application/json`: object

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | Stored. | `application/json` [IngestResult](#ingestresult) |
| 400 | Malformed body or gzip stream (code invalid_report) | `application/json` [Error](#error) |
| 401 | Missing or invalid ID token (code invalid_token; the message names the failed check). | `application/json` [Error](#error) |
| 403 | Project namespace not allowed (code namespace_not_allowed) or ref not protected (code ref_not_protected). | `application/json` [Error](#error) |
| 409 | The project's service is archived (code service_archived) | `application/json` [Error](#error) |
| 413 | Report larger than the limit or with too many findings (code report_too_large). | `application/json` [Error](#error) |
| 415 | Unsupported content type or encoding (code unsupported_media_type). | `application/json` [Error](#error) |
| 422 | Not a Trivy JSON report | `application/json` [Error](#error) |
| 429 | Too many uploads from this address. Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The GitLab URL is not configured (code ingest_not_configured) | `application/json` [Error](#error) |

### `GET /api/v1/me`

The signed-in user and the state of the session.

Also answers for a session that still has to complete two-factor authentication or a password change, so the web interface can guide the user. Every other endpoint that needs a session answers 403 with code mfa_required, mfa_enrollment_required or password_change_required until the session is complete.

- Operation ID: `getMe`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Current user. | `application/json` [Me](#me) |
| 401 | No valid session. | `application/json` [Error](#error) |

### `POST /api/v1/me/password`

Change the password of the signed-in local user.

Required after a password reset from the command line. Allowed for a session whose second factor is verified. Other sessions of the user are signed out.

- Operation ID: `changePassword`
- Authentication: Session

Request body (required):

- `application/json`: [PasswordChange](#passwordchange)

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Password changed. | none |
| 400 | Invalid request or weak password (code weak_password). | `application/json` [Error](#error) |
| 401 | Wrong current password (code invalid_credentials) | `application/json` [Error](#error) |
| 403 | Not a local account (code not_local) or second factor not verified. | `application/json` [Error](#error) |

### `GET /api/v1/portfolio`

Current posture of every non-archived service.

- Operation ID: `getPortfolio`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Portfolio. | `application/json` [Portfolio](#portfolio) |
| 401 | No valid session. | `application/json` [Error](#error) |

### `GET /api/v1/scans/{id}`

- Operation ID: `getScan`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Scan. | `application/json` [Scan](#scan) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/scans/{id}/diff`

Findings new and fixed since the previous scan of the same service, ref, artifact and kind.

- Operation ID: `getScanDiff`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Diff. | `application/json` [ScanDiff](#scandiff) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/search/vulnerabilities`

Every service and artifact whose current state contains a vulnerability ID.

- Operation ID: `searchVulnerabilities`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | query | yes | string (minLength 3, maxLength 100) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Matches. | `application/json` [VulnerabilitySearch](#vulnerabilitysearch) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |

### `GET /api/v1/services`

- Operation ID: `listServices`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `after` | query | no | integer (int64, minimum 0, default 0) |  |
| `archived` | query | no | boolean (default false) |  |
| `limit` | query | no | integer (minimum 1, maximum 500, default 100) |  |
| `q` | query | no | string (maxLength 200) | Substring of name or project path. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page of services ordered by id. | `application/json` [ServicePage](#servicepage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |

### `POST /api/v1/services`

Add a service by GitLab project path (admin only).

- Operation ID: `createService`
- Authentication: Session, admin

Request body (required):

- `application/json`: [ServiceCreate](#servicecreate)

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | Created. | `application/json` [Service](#service) |
| 400 | Invalid request. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 409 | The project path or SonarQube key is already used (code conflict). | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}`

- Operation ID: `getService`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Service. | `application/json` [Service](#service) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `PATCH /api/v1/services/{id}`

Rename, link a SonarQube project key, or archive (admin only).

- Operation ID: `updateService`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Request body (required):

- `application/json`: [ServiceUpdate](#serviceupdate)

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Updated. | `application/json` [Service](#service) |
| 400 | Invalid request. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 409 | The SonarQube key is already linked to another service (code conflict). | `application/json` [Error](#error) |

### `DELETE /api/v1/services/{id}`

Delete a service and all its data (admin only).

- Operation ID: `deleteService`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Deleted. | none |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/overview`

Current Trivy posture of a service for one ref.

- Operation ID: `getServiceOverview`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `ref` | query | no | string | Git ref; defaults to the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Overview. | `application/json` [Overview](#overview) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/scans`

Scans of a service, newest first.

- Operation ID: `listServiceScans`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `before` | query | no | integer (int64) | Return the scans that come after the scan with this id in newest-first order (by scan time |
| `limit` | query | no | integer (minimum 1, maximum 200, default 50) |  |
| `ref` | query | no | string | Git ref; defaults to the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page of scans. | `application/json` [ScanPage](#scanpage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/secrets`

Secret findings in the current state of a ref. Matched values are never stored or returned.

- Operation ID: `listServiceSecrets`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `ref` | query | no | string | Git ref; defaults to the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Secret findings. | `application/json` [SecretFindingList](#secretfindinglist) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/sonar`

Latest SonarQube analysis of a service and its history.

- Operation ID: `getServiceSonar`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `limit` | query | no | integer (minimum 1, maximum 200, default 30) | Number of history entries. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | SonarQube data; latest is null when nothing was analysed yet. | `application/json` [ServiceSonar](#servicesonar) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/trends`

Severity counts per scan over time for one ref.

- Operation ID: `getServiceTrends`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `days` | query | no | integer (minimum 1, maximum 365, default 90) |  |
| `ref` | query | no | string | Git ref; defaults to the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Trend points | `application/json` [Trends](#trends) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/vulnerabilities`

Vulnerabilities in the current state (latest scan per artifact) of a ref.

- Operation ID: `listServiceVulnerabilities`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `after` | query | no | integer (int64, minimum 0, default 0) |  |
| `fixable` | query | no | boolean |  |
| `limit` | query | no | integer (minimum 1, maximum 500, default 100) |  |
| `q` | query | no | string (maxLength 200) | Substring of package name |
| `ref` | query | no | string | Git ref; defaults to the ref with the most recent scan. |
| `severity` | query | no | string | Comma-separated list. |
| `suppressed` | query | no | boolean (default false) | Include suppressed findings. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page ordered by id. | `application/json` [VulnerabilityPage](#vulnerabilitypage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |

### `GET /api/v1/settings`

List all runtime settings (admin only). Secret values are never returned.

- Operation ID: `listSettings`
- Authentication: Session, admin

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Settings. | `application/json` [SettingList](#settinglist) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |

### `PATCH /api/v1/settings`

Change runtime settings (admin only). Changes apply immediately.

The body maps setting keys to new values. A null value resets the key to its environment or default value. All changes are validated together and applied atomically.

- Operation ID: `updateSettings`
- Authentication: Session, admin

Request body (required):

- `application/json`: [SettingUpdate](#settingupdate)

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Settings after the change. | `application/json` [SettingList](#settinglist) |
| 400 | Validation failed (code invalid_settings); details name each key. | `application/json` [SettingsError](#settingserror) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |

### `GET /api/v1/setup`

Whether the first-run setup is still required (no administrator exists).

- Operation ID: `getSetupStatus`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Setup status. | `application/json` [SetupStatus](#setupstatus) |

### `POST /api/v1/setup`

Create the first administrator (local account) with the one-time setup token.

The token is printed to the server log at start while no administrator exists. On success the session cookie is set; the new administrator must enrol two-factor authentication before the session is fully usable.

- Operation ID: `completeSetup`
- Authentication: None

Request body (required):

- `application/json`: [SetupRequest](#setuprequest)

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Administrator created and signed in (two-factor enrolment pending). | none |
| 400 | Invalid request or password does not meet the rules (code invalid_request or weak_password). | `application/json` [Error](#error) |
| 403 | Wrong setup token (code invalid_setup_token). | `application/json` [Error](#error) |
| 409 | Setup is already complete (code setup_complete). | `application/json` [Error](#error) |
| 429 | Too many attempts. Retry-After gives seconds. | `application/json` [Error](#error) |

### `GET /api/v1/users`

List users (admin only), ordered by id.

- Operation ID: `listUsers`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `after` | query | no | integer (int64, minimum 0, default 0) | Return users with an id greater than this value. |
| `limit` | query | no | integer (minimum 1, maximum 200, default 50) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page of users. | `application/json` [UserPage](#userpage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |

### `PATCH /api/v1/users/{id}`

Change a user's role (admin only).

- Operation ID: `updateUser`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Request body (required):

- `application/json`: [UserUpdate](#userupdate)

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Updated user. | `application/json` [User](#user) |
| 400 | Invalid request body. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No such user. | `application/json` [Error](#error) |
| 409 | The change would leave no administrator (code last_admin). | `application/json` [Error](#error) |

### `DELETE /api/v1/users/{id}/mfa`

Reset a user's two-factor authentication (admin only).

Removes the TOTP secret and recovery codes and signs the user out everywhere; the user enrols again at the next sign-in.

- Operation ID: `resetUserMFA`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Reset. | none |
| 400 | Invalid id. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No such user. | `application/json` [Error](#error) |

### `GET /api/v1/version`

- Operation ID: `getVersion`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Build information. | `application/json` [Version](#version) |

### `GET /healthz`

Liveness probe. Does not check dependencies.

- Operation ID: `healthz`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The process is running. | `application/json` [Status](#status) |

### `GET /readyz`

Readiness probe. Checks the database connection.

- Operation ID: `readyz`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Ready to serve requests. | `application/json` [Status](#status) |
| 503 | Not ready. The reason names the failing dependency without secrets. | `application/json` [Status](#status) |

## Schemas

### AuditEvent

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `action` | string | yes |  |
| `actor` | string | yes |  |
| `at` | string (date-time) | yes |  |
| `detail` | object | yes | Event details as recorded; never contains secrets. |
| `id` | integer (int64) | yes |  |

### AuditPage

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [AuditEvent](#auditevent) | yes |  |
| `next_before` | integer or null (int64) | yes |  |

### AuthMethods

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `gitlab` | boolean | yes |  |
| `local` | boolean | yes |  |

### Error

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `error` | object | yes |  |

Properties of `error`:

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes | Stable machine-readable code. |
| `message` | string | yes | Human-readable explanation and next step. |

### IngestResult

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `counts` | [SeverityCounts](#severitycounts) | yes |  |
| `fixable` | integer | yes |  |
| `replaced` | boolean | yes | An earlier upload for the same job |
| `scan_id` | integer (int64) | yes |  |
| `secrets` | integer | yes |  |
| `service_created` | boolean | yes |  |
| `service_id` | integer (int64) | yes |  |
| `suppressed` | integer | yes |  |

### LocalLoginRequest

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `password` | string (minLength 1, maxLength 1024) | yes |  |
| `username` | string (minLength 1, maxLength 200) | yes |  |

### MFACode

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | string (maxLength 64) | yes | A 6-digit TOTP code or a recovery code (xxxxx-xxxxx). |

### MFAEnrollment

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `otpauth_uri` | string | yes | otpauth://totp/... URI for a QR code. |
| `secret` | string | yes | Base32 secret for manual entry. |

### Me

Type: [User](#user) + object

| Property | Type | Required | Description |
|---|---|---|---|
| `created_at` | string (date-time) | yes |  |
| `email` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `last_login_at` | string or null (date-time) | yes |  |
| `mfa_enrolled` | boolean | yes | The user has a confirmed TOTP enrolment. |
| `mfa_verified` | boolean | yes | This session has passed the second factor. |
| `must_change_password` | boolean | yes |  |
| `name` | string | yes |  |
| `role` | [Role](#role) | yes |  |
| `source` | string (one of `gitlab`, `local`) | yes |  |
| `username` | string | yes |  |

### Overview

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `artifacts` | array of [ScanSummary](#scansummary) | yes | Latest scan per artifact and kind for the ref. |
| `by_class` | map of [SeverityCounts](#severitycounts) | yes | Counts per Trivy result class (os-pkgs, lang-pkgs). |
| `counts` | [SeverityCounts](#severitycounts) | yes |  |
| `data_at` | string or null (date-time) | yes | Scan time of the newest scan in this overview. |
| `fixable` | integer | yes |  |
| `fixed_since_previous` | integer | yes |  |
| `new_since_previous` | integer | yes |  |
| `ref` | string or null | yes | null when the service has no scans yet. |
| `refs` | array of string | yes | Refs with scans |
| `secrets` | integer | yes |  |
| `service` | [Service](#service) | yes |  |
| `suppressed` | integer | yes |  |
| `top_packages` | array of [PackageImpact](#packageimpact) | yes |  |

### PackageImpact

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `by_severity` | [SeverityCounts](#severitycounts) | yes |  |
| `findings` | integer | yes |  |
| `fix_version` | string | yes | Highest fixed version among the findings; upgrading to it clears them. Empty when none is fixable. |
| `installed_version` | string | yes |  |
| `pkg_name` | string | yes |  |

### PasswordChange

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `current_password` | string (minLength 1, maxLength 1024) | yes |  |
| `new_password` | string (minLength 12, maxLength 1024) | yes |  |

### Portfolio

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `generated_at` | string (date-time) | yes |  |
| `items` | array of [PortfolioItem](#portfolioitem) | yes |  |
| `totals` | object | yes |  |

Properties of `totals`:

| Property | Type | Required | Description |
|---|---|---|---|
| `failing_quality_gates` | integer | yes |  |
| `fixable` | integer | yes |  |
| `services` | integer | yes |  |
| `services_with_critical` | integer | yes |  |

### PortfolioItem

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `counts` | [SeverityCounts](#severitycounts) | yes |  |
| `coverage` | number or null | yes |  |
| `fixable` | integer | yes |  |
| `last_analysis_at` | string or null (date-time) | yes |  |
| `last_scan_at` | string or null (date-time) | yes |  |
| `maintainability_rating` | [Rating](#rating) | yes |  |
| `quality_gate_status` | string or null | yes |  |
| `ref` | string or null | yes | Ref with the most recent scan. |
| `reliability_rating` | [Rating](#rating) | yes |  |
| `secrets` | integer | yes |  |
| `security_rating` | [Rating](#rating) | yes |  |
| `service` | [Service](#service) | yes |  |

### Rating

null when SonarQube did not report the metric.

Type: string or null (one of `A`, `B`, `C`, `D`, `E`, ``)

### RecoveryCodes

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `recovery_codes` | array of string | yes |  |

### Role

Type: string (one of `admin`, `viewer`)

### Scan

Type: [ScanSummary](#scansummary) + object

| Property | Type | Required | Description |
|---|---|---|---|
| `artifact_name` | string | yes |  |
| `commit_sha` | string | yes |  |
| `commit_url` | string or null | yes |  |
| `counts` | [SeverityCounts](#severitycounts) | yes |  |
| `fixable` | integer | yes |  |
| `gitlab_job_id` | integer (int64) | yes |  |
| `gitlab_pipeline_id` | integer (int64) | yes |  |
| `gitlab_project_path` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `job_url` | string or null | yes |  |
| `kind` | string (one of `image`, `secret-fs`) | yes |  |
| `pipeline_url` | string or null | yes | Link into GitLab; null when the GitLab URL is not configured. |
| `received_at` | string (date-time) | yes |  |
| `ref` | string | yes |  |
| `ref_protected` | boolean | yes |  |
| `scanned_at` | string (date-time) | yes |  |
| `secrets` | integer | yes |  |
| `service_id` | integer (int64) | yes |  |
| `suppressed` | integer | yes |  |
| `trivy_version` | string | yes |  |

### ScanDiff

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `fixed` | array of [Vulnerability](#vulnerability) | yes |  |
| `new` | array of [Vulnerability](#vulnerability) | yes |  |
| `previous_scan_id` | integer or null (int64) | yes |  |
| `scan_id` | integer (int64) | yes |  |
| `unchanged` | integer | yes |  |

### ScanPage

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [ScanSummary](#scansummary) | yes |  |
| `next_before` | integer or null (int64) | yes |  |

### ScanSummary

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `artifact_name` | string | yes |  |
| `commit_sha` | string | yes |  |
| `counts` | [SeverityCounts](#severitycounts) | yes |  |
| `fixable` | integer | yes |  |
| `gitlab_job_id` | integer (int64) | yes |  |
| `gitlab_pipeline_id` | integer (int64) | yes |  |
| `id` | integer (int64) | yes |  |
| `kind` | string (one of `image`, `secret-fs`) | yes |  |
| `received_at` | string (date-time) | yes |  |
| `ref` | string | yes |  |
| `ref_protected` | boolean | yes |  |
| `scanned_at` | string (date-time) | yes |  |
| `secrets` | integer | yes |  |
| `service_id` | integer (int64) | yes |  |
| `suppressed` | integer | yes |  |
| `trivy_version` | string | yes |  |

### SecretFinding

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `artifact_name` | string | yes |  |
| `category` | string | yes |  |
| `end_line` | integer | yes |  |
| `id` | integer (int64) | yes |  |
| `rule_id` | string | yes |  |
| `scan_id` | integer (int64) | yes |  |
| `severity` | [Severity](#severity) | yes |  |
| `start_line` | integer | yes |  |
| `suppressed` | boolean | yes |  |
| `target` | string | yes |  |
| `title` | string | yes |  |

### SecretFindingList

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [SecretFinding](#secretfinding) | yes |  |

### Service

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `archived` | boolean | yes |  |
| `created_at` | string (date-time) | yes |  |
| `created_via` | string (one of `ingest`, `manual`) | yes |  |
| `gitlab_project_id` | integer or null (int64) | yes |  |
| `gitlab_project_path` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `sonar_project_key` | string or null | yes |  |

### ServiceCreate

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `gitlab_project_path` | string (minLength 1, maxLength 500) | yes |  |
| `name` | string (maxLength 200) | no | Defaults to the last path segment. |
| `sonar_project_key` | string (maxLength 400) | no |  |

### ServicePage

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [Service](#service) | yes |  |
| `next_after` | integer or null (int64) | yes |  |

### ServiceSonar

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `history` | array of [SonarAnalysis](#sonaranalysis) | yes | Newest first |
| `latest` | any | yes |  |
| `sonar_project_key` | string or null | yes |  |

### ServiceUpdate

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `archived` | boolean | no |  |
| `name` | string (minLength 1, maxLength 200) | no |  |
| `sonar_project_key` | string or null (maxLength 400) | no | null unlinks. |

### Setting

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `description` | string | yes |  |
| `env_var` | string | yes | Environment variable that seeds this setting. |
| `is_set` | boolean | yes | For secrets |
| `key` | string | yes |  |
| `secret` | boolean | yes |  |
| `source` | string (one of `default`, `env`, `ui`) | yes |  |
| `type` | string (one of `string`, `url`, `boolean`, `duration`, `string_list`, `integer`) | yes |  |
| `updated_at` | string or null (date-time) | no |  |
| `updated_by` | string or null | no |  |
| `value` | any | yes | Current value; always null for secrets. |

### SettingList

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [Setting](#setting) | yes |  |

### SettingUpdate

Type: map of any

### SettingsError

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `error` | object | yes |  |

Properties of `error`:

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes |  |
| `fields` | map of string | yes | Problem per setting key. |
| `message` | string | yes |  |

### SetupRequest

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `password` | string (minLength 12, maxLength 1024) | yes |  |
| `token` | string (maxLength 200) | yes |  |
| `username` | string (minLength 1, maxLength 200) | yes |  |

### SetupStatus

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `required` | boolean | yes |  |

### Severity

Type: string (one of `CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, `UNKNOWN`)

### SeverityCounts

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `critical` | integer | yes |  |
| `high` | integer | yes |  |
| `low` | integer | yes |  |
| `medium` | integer | yes |  |
| `unknown` | integer | yes |  |

### SonarAnalysis

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `analysed_at` | string (date-time) | yes |  |
| `conditions` | array of object | yes |  |
| `coverage` | number or null | yes |  |
| `duplicated_lines_density` | number or null | yes |  |
| `gitlab_pipeline_id` | integer or null (int64) | yes |  |
| `id` | integer (int64) | yes |  |
| `maintainability_rating` | [Rating](#rating) | yes |  |
| `measures` | map of string | yes | Every measure as reported by SonarQube, by metric key. |
| `mode` | string (one of `mqr`, `standard`, ``) | yes |  |
| `project_url` | string or null | yes | Link to the project in SonarQube. |
| `quality_gate_status` | string (one of `OK`, `WARN`, `ERROR`, `NONE`, ``) | yes |  |
| `reliability_rating` | [Rating](#rating) | yes |  |
| `revision` | string | yes |  |
| `security_rating` | [Rating](#rating) | yes |  |
| `security_review_rating` | [Rating](#rating) | yes |  |

### SonarIntegration

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `configured` | boolean | yes |  |
| `last_sync_at` | string or null (date-time) | yes |  |
| `last_sync_error` | string or null | yes |  |
| `last_webhook_at` | string or null (date-time) | yes |  |
| `recent_deliveries` | array of object | yes |  |
| `server_version` | string or null | yes |  |

### Status

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `reason` | string | no |  |
| `status` | string (one of `ok`, `ready`, `not_ready`) | yes |  |

### TrendPoint

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `artifact_name` | string | yes |  |
| `commit_sha` | string | yes |  |
| `counts` | [SeverityCounts](#severitycounts) | yes |  |
| `fixable` | integer | yes |  |
| `gitlab_pipeline_id` | integer (int64) | yes |  |
| `kind` | string (one of `image`, `secret-fs`) | yes |  |
| `scan_id` | integer (int64) | yes |  |
| `scanned_at` | string (date-time) | yes |  |

### Trends

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `points` | array of [TrendPoint](#trendpoint) | yes |  |
| `ref` | string or null | yes |  |

### TrivyIntegration

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `configured` | boolean | yes | The GitLab URL is set |
| `last_upload_at` | string or null (date-time) | yes |  |
| `recent_rejections` | array of object | yes |  |
| `rejections_24h` | integer | yes |  |
| `uploads_24h` | integer | yes |  |

### User

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `created_at` | string (date-time) | yes |  |
| `email` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `last_login_at` | string or null (date-time) | yes |  |
| `name` | string | yes |  |
| `role` | [Role](#role) | yes |  |
| `source` | string (one of `gitlab`, `local`) | yes |  |
| `username` | string | yes |  |

### UserPage

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [User](#user) | yes |  |
| `next_after` | integer or null (int64) | yes | Pass as `after` to get the next page; null on the last page. |

### UserUpdate

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `role` | [Role](#role) | yes |  |

### Version

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `commit` | string | yes |  |
| `go` | string | yes |  |
| `version` | string | yes |  |

### Vulnerability

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `artifact_name` | string | yes |  |
| `class` | string | yes |  |
| `first_seen_at` | string (date-time) | yes | Scan time of the earliest scan of the same ref and artifact that contains this finding (vuln_id |
| `fixed_version` | string | yes | Empty when no fix is available. |
| `id` | integer (int64) | yes |  |
| `installed_version` | string | yes |  |
| `pkg_name` | string | yes |  |
| `pkg_path` | string | yes |  |
| `pkg_type` | string | yes |  |
| `primary_url` | string | yes |  |
| `scan_id` | integer (int64) | yes |  |
| `severity` | [Severity](#severity) | yes |  |
| `status` | string | yes |  |
| `suppressed` | boolean | yes |  |
| `suppression` | string | yes | Reason recorded by Trivy for a suppressed finding. |
| `target` | string | yes |  |
| `title` | string | yes |  |
| `vuln_id` | string | yes |  |

### VulnerabilityPage

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [Vulnerability](#vulnerability) | yes |  |
| `next_after` | integer or null (int64) | yes |  |

### VulnerabilitySearch

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of object | yes |  |
| `vuln_id` | string | yes |  |

