# Scanopsis API reference

Look up any endpoint of the HTTP API: what it takes, what it returns and how
it is authenticated.

**Audience:** Developers · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

<!-- Generated from api/openapi.yaml by `make generate`. Do not edit by hand. -->

This page is generated from the OpenAPI document `api/openapi.yaml`, the
contract of the API. To change it, change that file and run `make generate`.
Version of the OpenAPI document: 0.4.0.

JSON API of Scanopsis. Most endpoints require a signed-in session (a session cookie); each endpoint states how it is authenticated: a session, an administrator session, a GitLab CI/CD ID token (the Trivy upload), a signed webhook (SonarQube) or none (setup, version, health). State-changing requests from browsers must be same-origin (checked with the Sec-Fetch-Site and Origin headers).

The server serves a limited number of API requests at a time, as many as it has database connections. When all are busy a request waits a few seconds and is then answered `503` with the code `busy` and a `Retry-After` header (seconds). Nothing was done: retry the request. One client address may hold at most a quarter of the slots, so one client cannot use them all. The probes, the web interface, the Trivy upload, the backup download and the parts of backup uploads are not counted.

While a backup is being restored, every API request except GET /api/v1/version and GET /api/v1/restore/status is answered `503` with the code `restoring` and `Retry-After: 30`. The probes and the web interface keep working.

## Authentication

Each endpoint says how it is authenticated:

| Label | What you send |
|---|---|
| None | Nothing. |
| Session | The session cookie of a finished sign-in: password or GitLab, then the two-factor code. |
| Session, admin | The same, for a user with the admin role. |
| CI ID token | A GitLab CI/CD ID token, in `Authorization: Bearer <token>`. |
| HMAC signature | The HMAC-SHA256 signature of the body, in a request header. |

An endpoint that needs a session answers `401` without one. It answers `403`
while the session still has to pass two-factor authentication or change its
password, and when an admin endpoint is called without the admin role.

## Endpoints

### `GET /api/v1/admin/audit`

Audit log, newest first (admin only).

- Operation ID: `listAuditEvents`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `action` | query | no | string (maxLength 100) | Exact action, for example login.failed. |
| `actor` | query | no | string (maxLength 200) | Substring of the actor, case-insensitive. |
| `before` | query | no | integer (int64, minimum 1) | Return events with an id lower than this value. |
| `limit` | query | no | integer (minimum 1, maximum 200, default 50) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page of events. | `application/json` [AuditPage](#auditpage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `POST /api/v1/admin/backup-uploads`

Start uploading a backup file (admin only).

Send the file in parts with PUT, then complete the upload. At most two uploads may be open at once; one without a part for an hour is dropped. The file must fit the free space of the backup folder less 512 MiB and less what other open uploads still need.

- Operation ID: `startBackupUpload`
- Authentication: Session, admin

Request body (required):

- `application/json`: [UploadStart](#uploadstart)

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | The upload is open. | `application/json` [UploadStatus](#uploadstatus) |
| 400 | Invalid file name or size (code invalid_request). | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 409 | Two uploads are open already (code too_many_uploads). | `application/json` [Error](#error) |
| 503 | The backup folder cannot be used (code folder_unavailable), the server is busy (code busy) or a backup is being restored (code restoring). | `application/json` [Error](#error) |
| 507 | Not enough free space (code no_space; the message gives the numbers). | `application/json` [Error](#error) |

### `GET /api/v1/admin/backup-uploads/{id}`

How much of an upload the server has (admin only).

After a dropped connection, ask this and send the next part from `received`.

- Operation ID: `getBackupUpload`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | string | Id of an open upload. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The upload. | `application/json` [UploadStatus](#uploadstatus) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `PUT /api/v1/admin/backup-uploads/{id}`

Send one part of an upload (admin only).

The body is the raw bytes of the file from `offset`, at most `chunk_size` (8 MiB) of them. `offset` must equal `received`. A part may take up to 5 minutes to arrive. A part that fails (a dropped connection, a timeout) is not kept: send it again from the same offset. Not counted by the concurrency limit.

- Operation ID: `putBackupUploadChunk`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | string | Id of an open upload. |
| `offset` | query | yes | integer (int64, minimum 0) | Where the part starts in the file; must equal `received`. |

Request body (required):

- `application/octet-stream`: string (binary)

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The part was stored. | `application/json` [UploadStatus](#uploadstatus) |
| 400 | Invalid offset, a part past the size given at the start, or a part that was cut off (code invalid_request). | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 408 | The part did not arrive within 5 minutes (code request_timeout). | `application/json` [Error](#error) |
| 409 | offset is not where the received data ends (code offset_mismatch; `received` gives it), or another part is still being received (code upload_busy). | `application/json` [UploadError](#uploaderror) |
| 413 | The part is larger than chunk_size (code payload_too_large). | `application/json` [Error](#error) |
| 503 | A backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |
| 507 | The backup folder is full (code no_space). | `application/json` [Error](#error) |

### `DELETE /api/v1/admin/backup-uploads/{id}`

Cancel an upload and delete what was received (admin only).

- Operation ID: `cancelBackupUpload`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | string | Id of an open upload. |

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Cancelled. | none |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `POST /api/v1/admin/backup-uploads/{id}/complete`

Check a fully received upload and add it to the backups (admin only).

Reads the whole file and checks its manifest, checksums and signature. A file that passes joins the list as a backup of kind uploaded, with its original name; audited as backup.uploaded. A file that fails is deleted and the message says why. The body may be empty.

- Operation ID: `completeBackupUpload`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | string | Id of an open upload. |

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | The backup was added. | `application/json` [BackupInfo](#backupinfo) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 409 | Not all of the file was received (code upload_incomplete; `received` gives how much), or a part is still being received (code upload_busy). | `application/json` [UploadError](#uploaderror) |
| 422 | The file is damaged, not a Scanopsis backup or not signed by this installation's key (code invalid_backup), was made with another secret key (code key_mismatch) or by a newer version (code newer_schema). The message says why and how to fix it. | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/admin/backups`

List the backups, the current or last job, the backup schedule's last run and the folder (admin only).

The files in the backup folder are the source of truth: the list is read from the folder, newest first. A file whose manifest cannot be read is listed with `readable` false so it can be deleted. When the folder cannot be used, `folder.ok` is false, `folder.error` says why and how to fix it, and the list is empty.

- Operation ID: `listBackups`
- Authentication: Session, admin

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Backups and status. | `application/json` [BackupList](#backuplist) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `POST /api/v1/admin/backups`

Start a manual backup (admin only).

The backup runs in the background; follow it with `job` in the list. Audited as backup.created when it is written, or backup.failed.

- Operation ID: `createBackup`
- Authentication: Session, admin

Responses:

| Status | Description | Body |
|---|---|---|
| 202 | The backup started. | `application/json` [JobStatus](#jobstatus) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 409 | A backup or restore is already running (code busy). | `application/json` [Error](#error) |
| 503 | The backup folder cannot be used (code folder_unavailable; the message names the folder and the fix), the server is busy (code busy) or a backup is being restored (code restoring). | `application/json` [Error](#error) |
| 507 | The backup folder has too little free space for a backup of the database and 512 MiB to spare (code no_space; the message gives both numbers). | `application/json` [Error](#error) |

### `DELETE /api/v1/admin/backups/{name}`

Delete a backup (admin only).

Audited as backup.deleted with the name and size.

- Operation ID: `deleteBackup`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `name` | path | yes | string | File name of a backup, as listed. |

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Deleted. | none |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No backup with this name (code not_found). | `application/json` [Error](#error) |
| 409 | The backup is being restored (code in_use). | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/admin/backups/{name}/download`

Download a backup file (admin only).

Sends the file as `application/gzip` with `Content-Disposition: attachment`, `Content-Length` and range support: a `Range` request resumes an interrupted download (206). The download may take as long as the client keeps reading; it is cut off only after 60 seconds without progress. Not counted by the concurrency limit. Every GET that sends content (200 or 206) is audited as backup.downloaded with the name, the size and the first and last byte sent (for several ranges, the span and their number); HEAD, 304 and 416 are not.

- Operation ID: `downloadBackup`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `Range` | header | no | string | A byte range, for example bytes=1048576- to resume. |
| `name` | path | yes | string | File name of a backup, as listed. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The whole file. | `application/gzip` string (binary) |
| 206 | The requested range. | `application/gzip` string (binary) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No backup with this name (code not_found). | `application/json` [Error](#error) |
| 416 | The range is outside the file. | none |
| 503 | A backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/admin/backups/{name}/preview`

Check a backup in full and tell what restoring it would do (admin only).

Reads the whole file and checks its checksums and signature, as a restore does before it changes anything. A backup made by a newer version or with another secret key is described from its manifest with `schema_ok` or `key_matches` false. `scans_removed_by_retention` is how many of the backup's scans the next data clean-up deletes under the retention period the restored installation will have.

- Operation ID: `previewRestore`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `name` | path | yes | string | File name of a backup, as listed. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The preview. | `application/json` [RestorePreview](#restorepreview) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No backup with this name (code not_found). | `application/json` [Error](#error) |
| 422 | The file is damaged, not a Scanopsis backup, of an unknown format or not signed by this installation's key (code invalid_backup; the message says why). | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `POST /api/v1/admin/backups/{name}/restore`

Replace all data with a backup (admin only).

Needs the phrase RESTORE and a current two-factor code of the administrator. Unless `skip_safety_backup` is true, a backup of the current data (kind before-restore) is taken first. The restore then runs in the background in one database transaction; while it replaces the data, the API answers 503 with the code restoring. Every session ends, this one too. The backup schedule and keep-days of this server are kept. Follow it with GET /api/v1/restore/status.

- Operation ID: `restoreBackup`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `name` | path | yes | string | File name of a backup, as listed. |

Request body (required):

- `application/json`: [RestoreRequest](#restorerequest)

Responses:

| Status | Description | Body |
|---|---|---|
| 202 | The restore started. | `application/json` [JobStatus](#jobstatus) |
| 400 | Malformed body (code invalid_request). | `application/json` [Error](#error) |
| 401 | No valid session, or too many wrong two-factor codes ended it (code unauthenticated); sign in again. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No backup with this name (code not_found). | `application/json` [Error](#error) |
| 409 | A job is running (code busy), or the backup was made with another secret key (code key_mismatch) or by a newer version (code newer_schema); the message says how to fix it. | `application/json` [Error](#error) |
| 422 | confirm is not RESTORE (code confirmation_required), the two-factor code is missing, wrong or used (code invalid_code), or the file cannot be read as a backup (code invalid_backup). | `application/json` [Error](#error) |
| 429 | Too many code attempts (code too_many_attempts). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/auth/gitlab/callback`

GitLab redirect target. Completes sign-in.

On success, sets the session cookie and redirects to return_to. On failure, redirects to /login?error=<code> where code is one of access_denied, account_disabled, group_not_allowed, login_expired, login_failed.

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/auth/gitlab/login`

Start GitLab sign-in.

Redirects the browser to GitLab (authorization code flow with PKCE, state and nonce). return_to must be a local path. It is cleaned (dot segments and repeated slashes resolved) and then checked: a backslash or a control character anywhere, raw or percent-encoded, or a result that does not start with exactly one "/" is replaced by "/". Only the path and the query are kept.

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
| 503 | GitLab could not be reached, or the server is busy (code busy; Retry-After gives seconds). | `application/json` [Error](#error) |

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
| 403 | The password is right, but an administrator disabled the account (code account_disabled). | `application/json` [Error](#error) |
| 404 | Local sign-in is disabled. | `application/json` [Error](#error) |
| 429 | Too many attempts from this network or for this username. Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is busy: no slot became free within a few seconds (code busy; Retry-After gives seconds), or it is busy hashing other passwords and the request ended while waiting. Try again. | `application/json` [Error](#error) |

### `POST /api/v1/auth/logout`

- Operation ID: `logout`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Signed out. The session is deleted and the cookie cleared. | none |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/auth/methods`

Sign-in methods enabled on this server.

- Operation ID: `getAuthMethods`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Enabled methods. | `application/json` [AuthMethods](#authmethods) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 401 | Wrong code (code invalid_code), or no session. | `application/json` [Error](#error) |
| 409 | No pending enrolment (code mfa_enrollment_not_started) or already enrolled. | `application/json` [Error](#error) |
| 429 | Too many attempts. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 401 | Wrong or reused code (code invalid_code), or no session. | `application/json` [Error](#error) |
| 409 | Two-factor authentication is not enrolled yet (code mfa_enrollment_required). | `application/json` [Error](#error) |
| 423 | Ten wrong codes in a row locked two-factor sign-in for this user (code mfa_locked). Every code is refused until an administrator resets two-factor authentication or the password; the user's sessions were ended. | `application/json` [Error](#error) |
| 429 | Too many attempts. Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | SonarQube integration not configured (code sonar_not_configured), or the server is busy (code busy; Retry-After gives seconds). | `application/json` [Error](#error) |

### `POST /api/v1/ingest/trivy`

Upload one Trivy JSON report from a GitLab CI job.

Authenticated with the job's GitLab CI/CD ID token (`Authorization: Bearer <token>`), whose audience must be the Scanopsis public URL. The project, pipeline, job, ref and commit come from the verified token, never from the report. The body is the raw report (`Content-Type: application/json`), optionally gzip-compressed (`Content-Encoding: gzip`). A repeated upload for the same job, artifact and kind replaces the earlier one. Rejections are written to the audit log, except requests limited per client address (429, code too_many_requests), and at most a bounded number per client address for rejections before the namespace and protected-ref checks and for the per-project limits. A job stores at most 50 different artifacts (artifact name and kind); a project may upload 100 reports at once and then one every 10 seconds. The body must arrive within 5 minutes of the upload being accepted.

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
| 400 | Malformed body or gzip stream (code invalid_report), or an unknown kind (code invalid_parameter). | `application/json` [Error](#error) |
| 401 | Missing or invalid ID token (code invalid_token; the message names the failed check). | `application/json` [Error](#error) |
| 403 | Project namespace not allowed (code namespace_not_allowed) or ref not protected (code ref_not_protected). | `application/json` [Error](#error) |
| 408 | The body did not arrive within the read deadline of 5 minutes (code request_timeout). The connection is closed. Retry the upload. | `application/json` [Error](#error) |
| 409 | The project's service is archived (code service_archived), or its path belongs to another service (code service_conflict). | `application/json` [Error](#error) |
| 413 | Report larger than the limit or with too many findings (code report_too_large). | `application/json` [Error](#error) |
| 415 | Unsupported content type or encoding (code unsupported_media_type). | `application/json` [Error](#error) |
| 422 | Not a Trivy JSON report, or an unsupported schema version (code not_a_trivy_report). | `application/json` [Error](#error) |
| 429 | Too many uploads from this address (code too_many_requests), too many from the project (code project_rate_limited), or the job already stored 50 different artifacts (code job_artifact_limit; its Retry-After is an hour, because waiting does not help that job). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The GitLab URL is not configured (code ingest_not_configured), GitLab's signing keys could not be fetched (code gitlab_unavailable), no memory was free for the upload within 30 seconds or the request ended while waiting (code busy; Retry-After gives seconds), or the database could not be reached (code unavailable; the report was not stored; Retry-After gives seconds). | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 401 | Wrong current password (code invalid_credentials), or no session. | `application/json` [Error](#error) |
| 403 | Not a local account (code not_local) or second factor not verified. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/me/sessions`

The signed-in user's sessions that are still valid, most recently used first.

Lists at most 200 sessions. The client address and the browser are as recorded when the session started; sessions started before they were recorded show empty strings.

- Operation ID: `listMySessions`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Sessions of the signed-in user. | `application/json` [SessionList](#sessionlist) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `DELETE /api/v1/me/sessions`

End every other session of the signed-in user.

The session making the request stays signed in. Recorded in the audit log as session.revoked when at least one session ended.

- Operation ID: `endOtherSessions`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | How many sessions ended. | `application/json` [SessionsEnded](#sessionsended) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `DELETE /api/v1/me/sessions/{id}`

End one other session of the signed-in user.

Recorded in the audit log as session.revoked. The current session ends with sign-out instead.

- Operation ID: `endMySession`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | The session ended. | none |
| 400 | Invalid id. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | The user has no other session with this id. | `application/json` [Error](#error) |
| 409 | This is the current session (code current_session). | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/portfolio`

Current posture of every non-archived service.

- Operation ID: `getPortfolio`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Portfolio. | `application/json` [Portfolio](#portfolio) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/portfolio/history`

Recent pipelines of every service and the portfolio over the last week.

For sparklines and deltas, loaded after the portfolio. Each service lists the state of its dashboard ref after each of its latest 20 pipelines; days gives the state of the whole portfolio 7, 6, ... and 1 day ago. A state is the sum over every artifact and kind of its latest scan at that moment, the rule the portfolio counts follow, so the last point of a service equals its portfolio counts. The answer is computed at most once every 30 seconds and may be that far behind the latest upload; when nothing changed it is reused for up to 2 minutes. generated_at says when it was computed.

- Operation ID: `getPortfolioHistory`
- Authentication: Session

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Portfolio history. | `application/json` [PortfolioHistory](#portfoliohistory) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/restore/status`

State of the current or last restore. Answers during a restore.

Public, so the web interface can show progress while every other API request is answered 503 restoring. It names no backup, account or folder. The state is idle when the last job was not a restore. After a restore that needs a restart, the state is failed, `restart_required` is true and the message says to restart Scanopsis.

- Operation ID: `getRestoreStatus`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The state. | `application/json` [RestoreStatus](#restorestatus) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/scans/{id}/diff`

Findings new and fixed since the previous scan of the same service, ref, artifact and kind, or since an earlier scan chosen with base.

- Operation ID: `getScanDiff`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `base` | query | no | integer (int64, minimum 1) | Compare with this scan instead of the previous one: any earlier scan of the same service, artifact and kind, on any ref (see listEarlierScans). |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Diff. | `application/json` [ScanDiff](#scandiff) |
| 400 | The base is not an earlier scan of the same service, artifact and kind (code invalid_parameter). | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/scans/{id}/earlier`

Earlier scans of the same service, artifact and kind, newest first; the scans a diff can use as its base.

- Operation ID: `listEarlierScans`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `before` | query | no | integer (int64) | Return the scans that come after the scan with this id in newest-first order (by scan time, then id); use next_before from the previous page. |
| `limit` | query | no | integer (minimum 1, maximum 200, default 50) |  |
| `ref` | query | no | string | Only scans of this ref. Every ref when absent. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page of scans. | `application/json` [ScanPage](#scanpage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/search/vulnerabilities`

Every service and artifact whose current state contains a vulnerability ID, by default on the dashboard branch of each service and on protected refs.

- Operation ID: `searchVulnerabilities`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | query | yes | string (minLength 3, maxLength 100) |  |
| `scope` | query | no | string (one of `dashboard`, `all`, default dashboard) | Which refs are searched. dashboard (the default): for each service the ref its dashboards show (the branch an administrator chose, else the ref with the most recent scan) and every protected ref, that is every ref whose latest scan of an artifact was protected. all: every ref of every service, which reads far more data and is slower. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Matches. | `application/json` [VulnerabilitySearch](#vulnerabilitysearch) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `PATCH /api/v1/services/{id}`

Rename, link a SonarQube project key, choose the dashboard branch, or archive (admin only).

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/overview`

Current Trivy posture of a service for one ref.

- Operation ID: `getServiceOverview`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `ref` | query | no | string | Git ref. Defaults to the dashboard branch an administrator chose for the service, else the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Overview. | `application/json` [Overview](#overview) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/scans`

Scans of a service, newest first.

- Operation ID: `listServiceScans`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `before` | query | no | integer (int64) | Return the scans that come after the scan with this id in newest-first order (by scan time, then id); use next_before from the previous page. |
| `limit` | query | no | integer (minimum 1, maximum 200, default 50) |  |
| `ref` | query | no | string | Git ref. Defaults to the dashboard branch an administrator chose for the service, else the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page of scans. | `application/json` [ScanPage](#scanpage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/secrets`

Secret findings in the current state of a ref. Matched values are never stored or returned.

- Operation ID: `listServiceSecrets`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `ref` | query | no | string | Git ref. Defaults to the dashboard branch an administrator chose for the service, else the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Secret findings. | `application/json` [SecretFindingList](#secretfindinglist) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/services/{id}/trends`

Severity counts per scan over time, with what each scan changed.

- Operation ID: `getServiceTrends`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |
| `all_refs` | query | no | boolean (default false) | Return the scans of every ref in the window, not only those of ref. ref and ref_source in the response still name the ref the dashboards show. The 300 scans listed at most are counted over every ref together. |
| `days` | query | no | integer (minimum 1, maximum 365, default 90) |  |
| `ref` | query | no | string | Git ref. Defaults to the dashboard branch an administrator chose for the service, else the ref with the most recent scan. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Trend points, oldest first: the newest 300 scans of the window at most. | `application/json` [Trends](#trends) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| `q` | query | no | string (maxLength 200) | Substring of package name, vulnerability ID or title. |
| `ref` | query | no | string | Git ref. Defaults to the dashboard branch an administrator chose for the service, else the ref with the most recent scan. |
| `severity` | query | no | string | Comma-separated list. |
| `suppressed` | query | no | boolean (default false) | Include suppressed findings. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page ordered by id. | `application/json` [VulnerabilityPage](#vulnerabilitypage) |
| 400 | Invalid parameter. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 404 | Not found. | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/setup`

Whether the first-run setup is still required (no administrator exists).

- Operation ID: `getSetupStatus`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Setup status. | `application/json` [SetupStatus](#setupstatus) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `POST /api/v1/setup/restore`

Restore an uploaded backup into a new installation.

Only while no administrator exists, with the setup token and the phrase RESTORE. No backup of the empty database is taken first, and the backup's own schedule settings are kept. The restore runs in the background; follow it with GET /api/v1/restore/status. Afterwards the administrators of the backup sign in, and the setup endpoints answer 409 setup_done.

- Operation ID: `restoreSetupBackup`
- Authentication: None

Request body (required):

- `application/json`: [SetupRestoreRequest](#setuprestorerequest)

Responses:

| Status | Description | Body |
|---|---|---|
| 202 | The restore started. | `application/json` [RestoreStatus](#restorestatus) |
| 400 | Malformed body (code invalid_request). | `application/json` [Error](#error) |
| 403 | Wrong setup token (code invalid_setup_token). | `application/json` [Error](#error) |
| 404 | No completed upload with this id (code not_found). | `application/json` [Error](#error) |
| 409 | Setup is complete (code setup_done), a job is running (code busy), or the backup was made with another secret key (code key_mismatch) or by a newer version (code newer_schema). | `application/json` [Error](#error) |
| 422 | confirm is not RESTORE (code confirmation_required). | `application/json` [Error](#error) |
| 429 | Too many wrong setup tokens from this address (code too_many_attempts). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `POST /api/v1/setup/restore/uploads`

Start uploading a backup on the setup screen of a new installation.

Only while no administrator exists, with the setup token; otherwise 409 setup_done. Works as POST /api/v1/admin/backup-uploads; the uploads of the setup screen are separate from those of the admin panel.

- Operation ID: `startSetupRestoreUpload`
- Authentication: None

Request body (required):

- `application/json`: [SetupUploadStart](#setupuploadstart)

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | The upload is open. | `application/json` [UploadStatus](#uploadstatus) |
| 400 | Invalid request (code invalid_request). | `application/json` [Error](#error) |
| 403 | Wrong setup token (code invalid_setup_token). | `application/json` [Error](#error) |
| 409 | Setup is complete (code setup_done), or two uploads are open already (code too_many_uploads). | `application/json` [Error](#error) |
| 429 | Too many wrong setup tokens from this address (code too_many_attempts). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The backup folder cannot be used (code folder_unavailable), the server is busy (code busy) or a backup is being restored (code restoring). | `application/json` [Error](#error) |
| 507 | Not enough free space (code no_space). | `application/json` [Error](#error) |

### `GET /api/v1/setup/restore/uploads/{id}`

How much of a setup upload the server has.

The setup token goes in the X-Setup-Token header.

- Operation ID: `getSetupRestoreUpload`
- Authentication: None

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `X-Setup-Token` | header | yes | string | The one-time setup token from the server log. |
| `id` | path | yes | string | Id of an open upload. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The upload. | `application/json` [UploadStatus](#uploadstatus) |
| 403 | Wrong setup token (code invalid_setup_token). | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 409 | Setup is complete (code setup_done). | `application/json` [Error](#error) |
| 429 | Too many wrong setup tokens from this address (code too_many_attempts). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `PUT /api/v1/setup/restore/uploads/{id}`

Send one part of a setup upload.

Works as PUT /api/v1/admin/backup-uploads/{id}; the setup token goes in the X-Setup-Token header.

- Operation ID: `putSetupRestoreUploadChunk`
- Authentication: None

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `X-Setup-Token` | header | yes | string | The one-time setup token from the server log. |
| `id` | path | yes | string | Id of an open upload. |
| `offset` | query | yes | integer (int64, minimum 0) | Where the part starts in the file; must equal `received`. |

Request body (required):

- `application/octet-stream`: string (binary)

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The part was stored. | `application/json` [UploadStatus](#uploadstatus) |
| 400 | Invalid offset, a part past the size given at the start, or a part that was cut off (code invalid_request). | `application/json` [Error](#error) |
| 403 | Wrong setup token (code invalid_setup_token). | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 408 | The part did not arrive within 5 minutes (code request_timeout). | `application/json` [Error](#error) |
| 409 | Setup is complete (code setup_done), offset is not where the received data ends (code offset_mismatch; `received` gives it), or another part is still being received (code upload_busy). | `application/json` [UploadError](#uploaderror) |
| 413 | The part is larger than chunk_size (code payload_too_large). | `application/json` [Error](#error) |
| 429 | Too many wrong setup tokens from this address (code too_many_attempts). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | A backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |
| 507 | The backup folder is full (code no_space). | `application/json` [Error](#error) |

### `DELETE /api/v1/setup/restore/uploads/{id}`

Cancel a setup upload and delete what was received.

The setup token goes in the X-Setup-Token header.

- Operation ID: `cancelSetupRestoreUpload`
- Authentication: None

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `X-Setup-Token` | header | yes | string | The one-time setup token from the server log. |
| `id` | path | yes | string | Id of an open upload. |

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Cancelled. | none |
| 403 | Wrong setup token (code invalid_setup_token). | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 409 | Setup is complete (code setup_done). | `application/json` [Error](#error) |
| 429 | Too many wrong setup tokens from this address (code too_many_attempts). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `POST /api/v1/setup/restore/uploads/{id}/complete`

Check a fully received setup upload and tell what restoring it would do.

Checks the file in full, as POST /api/v1/admin/backup-uploads/{id}/complete does, adds it to the backup folder as kind uploaded and returns the preview of the restore. Use the upload id to restore it. The setup token goes in the body or in the X-Setup-Token header; with the header the body may be empty.

- Operation ID: `completeSetupRestoreUpload`
- Authentication: None

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `X-Setup-Token` | header | no | string | The one-time setup token, when the body does not carry it. |
| `id` | path | yes | string | Id of an open upload. |

Request body (optional):

- `application/json`: [SetupTokenBody](#setuptokenbody)

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | The backup was added. | `application/json` [RestorePreview](#restorepreview) |
| 400 | Malformed body (code invalid_request). | `application/json` [Error](#error) |
| 403 | Wrong setup token (code invalid_setup_token). | `application/json` [Error](#error) |
| 404 | No upload with this id is open (code not_found). | `application/json` [Error](#error) |
| 409 | Setup is complete (code setup_done), not all of the file was received (code upload_incomplete; `received` gives how much), or a part is still being received (code upload_busy). | `application/json` [UploadError](#uploaderror) |
| 422 | The file is damaged, not a Scanopsis backup or not signed by this installation's key (code invalid_backup), was made with another secret key (code key_mismatch) or by a newer version (code newer_schema). | `application/json` [Error](#error) |
| 429 | Too many wrong setup tokens from this address (code too_many_attempts). Retry-After gives seconds. | `application/json` [Error](#error) |
| 503 | The server is busy (code busy) or a backup is being restored (code restoring). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `PATCH /api/v1/users/{id}`

Change a user's role, or disable or enable the user (admin only).

Disabling refuses every sign-in of the user (local and GitLab) and ends all of the user's sessions; enabling restores access. Recorded in the audit log as user.role_changed, user.disabled or user.enabled. Administrators cannot disable their own account, and the last enabled administrator can be neither disabled nor demoted.

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
| 409 | The change would leave no enabled administrator (code last_admin), or disables the caller's own account (code own_account). | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `DELETE /api/v1/users/{id}`

Delete a user (admin only).

Removes the user with their sessions and recovery codes. Audit events keep the name they were recorded with. Recorded as user.deleted. A GitLab user who signs in again afterwards comes back as a new viewer; disable the user instead to keep them out. Administrators cannot delete their own account or the last enabled administrator.

- Operation ID: `deleteUser`
- Authentication: Session, admin

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `id` | path | yes | integer (int64) |  |

Responses:

| Status | Description | Body |
|---|---|---|
| 204 | Deleted. | none |
| 400 | Invalid id. | `application/json` [Error](#error) |
| 401 | No valid session. | `application/json` [Error](#error) |
| 403 | The signed-in user lacks the required role. | `application/json` [Error](#error) |
| 404 | No such user. | `application/json` [Error](#error) |
| 409 | The user is the last enabled administrator (code last_admin), or the caller (code own_account). | `application/json` [Error](#error) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

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
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /api/v1/version`

- Operation ID: `getVersion`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Build information. | `application/json` [Version](#version) |
| 503 | The server is serving as many requests as it can and none became free within a few seconds (code busy). Nothing was done; retry after the number of seconds in Retry-After. | `application/json` [Error](#error) |

### `GET /healthz`

Liveness probe. Does not check dependencies.

- Operation ID: `healthz`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The process is running. | `application/json` [Status](#status) |

### `GET /readyz`

Readiness probe. Checks the database over a connection of its own, so it answers while requests are waiting for the pool.

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

### BackupInfo

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `app_version` | string | yes | Version of Scanopsis that made the backup. |
| `created_at` | string (date-time) | yes | When the data was backed up. |
| `created_by` | string | yes | Who started the backup: an account (source:username), scheduler or setup. |
| `key_matches` | boolean | yes | The backup was made with the secret key this server runs with. Only such a backup can be restored. |
| `kind` | [BackupKind](#backupkind) | yes |  |
| `name` | string | yes |  |
| `original_name` | string | yes | The file name an uploaded backup had; empty for other kinds. |
| `readable` | boolean | yes | False when the manifest cannot be read: the backup can be deleted but not restored, and only name, kind, size and created_at are known. |
| `schema` | integer (int64) | yes | Database schema version of the backup. |
| `size` | integer (int64) | yes | Bytes. |
| `uploaded_at` | string or null (date-time) | yes | When an uploaded backup arrived. |

### BackupKind

Type: string (one of `manual`, `scheduled`, `uploaded`, `before-restore`)

### BackupList

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `backups` | array of [BackupInfo](#backupinfo) | yes | Newest first. |
| `disk` | object | yes |  |
| `folder` | object | yes |  |
| `job` | [JobStatus](#jobstatus) | yes |  |
| `schedule` | [ScheduleStatus](#schedulestatus) | yes |  |

Properties of `disk`:

| Property | Type | Required | Description |
|---|---|---|---|
| `free_bytes` | integer (int64) | yes | Free space in the backup folder. |
| `used_bytes` | integer (int64) | yes | Total size of the backups. |

Properties of `folder`:

| Property | Type | Required | Description |
|---|---|---|---|
| `error` | string | no | Why the folder cannot be used and how to fix it. |
| `ok` | boolean | yes |  |
| `path` | string | yes |  |

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
| `replaced` | boolean | yes | An earlier upload for the same job, artifact and kind was replaced. |
| `scan_id` | integer (int64) | yes |  |
| `secrets` | integer | yes |  |
| `service_created` | boolean | yes |  |
| `service_id` | integer (int64) | yes |  |
| `suppressed` | integer | yes |  |

### JobProgress

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `bytes` | integer (int64) | yes | Table data copied so far. |
| `table` | string | yes | Table being copied. |
| `tables_done` | integer | yes |  |
| `tables_total` | integer | yes |  |

### JobStatus

The current or last backup or restore job. A finished job stays done or failed until the next one starts.

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `finished_at` | string or null (date-time) | yes |  |
| `kind` | [BackupKind](#backupkind) | no |  |
| `message` | string | no | What happens or went wrong, in a sentence. |
| `name` | string | no | For a backup, the new file once written; for a restore, the backup being restored. |
| `percent` | integer (minimum 0, maximum 100) | yes |  |
| `progress` | [JobProgress](#jobprogress) | yes |  |
| `started_at` | string or null (date-time) | yes |  |
| `state` | string (one of `idle`, `backing_up`, `restoring`, `done`, `failed`) | yes |  |
| `type` | string (one of `backup`, `restore`) | no | Absent while idle. |

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
| `code` | string (maxLength 64) | yes | A 6-digit TOTP code or a recovery code (xxxxx-xxxxx-xxxxx-xxxxx; codes of the older form xxxxx-xxxxx are still accepted until used). Case, spaces and hyphens do not matter. |

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
| `disabled_at` | string or null (date-time) | yes | When an administrator disabled the user; null while the user may sign in. |
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
| `ref` | string or null | yes | The ref shown. null when no ref was requested or chosen and the service has no scans yet. |
| `ref_source` | [RefSource](#refsource) | yes |  |
| `refs` | array of string | yes | Refs with scans, most recent first. |
| `secrets` | integer | yes |  |
| `service` | [Service](#service) | yes |  |
| `suppressed` | integer | yes |  |
| `top_packages` | array of [PackageImpact](#packageimpact) | yes | Packages whose upgrade clears the most risk points first; then the most risk, the most findings and the most severe. Packages without a fix come last. |

### PackageImpact

One installed package and what upgrading it clears. Risk points weigh each finding critical 10, high 5, medium 3, low 1, unknown 5 (the OWASP Dependency-Track weights).

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `by_severity` | [SeverityCounts](#severitycounts) | yes |  |
| `findings` | integer | yes |  |
| `fix_version` | string | yes | Highest fixed version among the findings; upgrading to it clears the fixable ones. Empty when none is fixable. |
| `fixable` | integer | yes | Findings that report a fixed version. |
| `installed_version` | string | yes |  |
| `pkg_name` | string | yes |  |
| `risk` | integer | yes | Risk points of all the findings. |
| `risk_cleared` | integer | yes | Risk points of the fixable findings, which the upgrade to fix_version clears. |

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

### PortfolioHistory

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `days` | array of object | yes | The state of the listed services together at 7, 6, ... and 1 times 24 hours before generated_at, oldest first. |
| `generated_at` | string (date-time) | yes |  |
| `services` | array of object | yes | Every non-archived service with at least one scan on its dashboard ref, by id. |

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
| `ref` | string or null | yes | Ref the numbers come from: the dashboard branch when chosen, else the ref with the most recent scan. |
| `ref_source` | string (one of `default`, `latest`) | yes | default: the administrator's chosen dashboard branch; latest: the ref with the most recent scan. |
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

### RefSource

How the ref was chosen: requested (the ref parameter), default (the dashboard branch an administrator chose, even without scans yet) or latest (the ref with the most recent scan).

Type: string (one of `requested`, `default`, `latest`)

### RestorePreview

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `info` | [BackupInfo](#backupinfo) | yes |  |
| `key_matches` | boolean | yes | False when another secret key made the backup. |
| `oldest_scan_at` | string or null (date-time) | yes |  |
| `retention_days` | integer | yes | The retention period the restored installation will have. |
| `scans_removed_by_retention` | integer (int64) | yes | Scans of the backup that the next data clean-up deletes. |
| `schema_ok` | boolean | yes | False when a newer version of Scanopsis made the backup. |

### RestoreRequest

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes | A current TOTP code of the administrator. Recovery codes are not accepted. |
| `confirm` | string | yes |  |
| `skip_safety_backup` | boolean (default false) | no | Do not back up the current data first. |

### RestoreStatus

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `finished_at` | string or null (date-time) | yes |  |
| `message` | string | yes | What happens, in a sentence; names no backup or account. |
| `percent` | integer (minimum 0, maximum 100) | yes |  |
| `restart_required` | boolean | no | Always sent. True only when the restore replaced the data but Scanopsis must restart to finish it (state failed). The API stays in restore mode until the restart. |
| `state` | string (one of `idle`, `backing_up`, `restoring`, `done`, `failed`) | yes |  |

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
| `base` | [ScanSummary](#scansummary) or null | yes | The scan compared with, as in previous_scan_id. |
| `fixed` | array of [Vulnerability](#vulnerability) | yes | At most 500 findings, the most severe first. |
| `fixed_counts` | [SeverityCounts](#severitycounts) | yes | Every fixed finding by its severity in the base scan, listed or not. |
| `fixed_total` | integer | yes | Number of fixed findings, including any not listed in fixed. |
| `new` | array of [Vulnerability](#vulnerability) | yes | At most 500 findings, the most severe first. |
| `new_counts` | [SeverityCounts](#severitycounts) | yes | Every new finding by severity, listed or not. |
| `new_total` | integer | yes | Number of new findings, including any not listed in new. |
| `previous_scan_id` | integer or null (int64) | yes | The scan compared with: the base asked for, else the previous scan of the same ref. null when there is none. |
| `scan_id` | integer (int64) | yes |  |
| `truncated` | boolean | yes | True when new, fixed or unchanged_items lists fewer findings than new_total, fixed_total or unchanged. |
| `unchanged` | integer | yes |  |
| `unchanged_counts` | [SeverityCounts](#severitycounts) | yes | Every unchanged finding by its severity in this scan. |
| `unchanged_items` | array of [Vulnerability](#vulnerability) | yes | At most 100 findings present in both scans, the most severe first, as reported in this scan. |

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

### ScheduleStatus

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `error` | string | yes | Why the last scheduled backup failed; empty after a success. |
| `last_run_at` | string or null (date-time) | yes | When the last scheduled backup ran; null before the first. |
| `ok` | boolean | yes |  |

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
| `default_ref` | string or null | yes | The branch (ref) an administrator chose for the dashboards of this service. null when none was chosen; the dashboards then show the ref with the most recent scan. The ref need not have scans yet. |
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
| `history` | array of [SonarAnalysis](#sonaranalysis) | yes | Newest first, including latest. |
| `latest` | [SonarAnalysis](#sonaranalysis) or null | yes |  |
| `sonar_project_key` | string or null | yes |  |

### ServiceUpdate

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `archived` | boolean | no |  |
| `default_ref` | string or null (minLength 1, maxLength 255) | no | Chooses the dashboard branch; null clears the choice. Trimmed; no control characters. It need not have scans yet. |
| `name` | string (minLength 1, maxLength 200) | no |  |
| `sonar_project_key` | string or null (maxLength 400) | no | null unlinks. |

### Session

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `client_ip` | string | yes | Client address when the session started; empty if not recorded. |
| `created_at` | string (date-time) | yes |  |
| `current` | boolean | yes | This is the session making the request. |
| `expires_at` | string (date-time) | yes | Absolute expiry; the session ends earlier after the idle timeout. |
| `id` | integer (int64) | yes | Stable number of the session. |
| `last_seen_at` | string (date-time) | yes | Last request, to the minute. |
| `mfa_verified` | boolean | yes | The session has passed the second factor. |
| `user_agent` | string | yes | First 200 bytes of the User-Agent header when the session started; empty if not recorded. |

### SessionList

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [Session](#session) | yes |  |

### SessionsEnded

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `ended` | integer (int64) | yes | Number of sessions that ended. |

### Setting

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `description` | string | yes |  |
| `env_var` | string | yes | Environment variable that seeds this setting. |
| `is_set` | boolean | yes | For secrets, whether a value exists. |
| `key` | string | yes |  |
| `secret` | boolean | yes |  |
| `source` | string (one of `default`, `env`, `ui`) | yes |  |
| `type` | string (one of `string`, `url`, `boolean`, `duration`, `string_list`, `integer`) | yes |  |
| `updated_at` | string or null (date-time) | no |  |
| `updated_by` | string or null | no |  |
| `value` | null or string or boolean or integer or array of string | yes | Current value; always null for secrets. |

### SettingList

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `items` | array of [Setting](#setting) | yes |  |

### SettingUpdate

Type: map of null or string or boolean or integer or array of string

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

### SetupRestoreRequest

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `confirm` | string | yes |  |
| `token` | string | yes | The one-time setup token from the server log. |
| `upload_id` | string | yes | Id of a completed setup upload. |

### SetupStatus

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `required` | boolean | yes |  |

### SetupTokenBody

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `token` | string | yes | The one-time setup token from the server log. |

### SetupUploadStart

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `file_name` | string (minLength 1, maxLength 255) | yes |  |
| `size` | integer (int64, minimum 1) | yes |  |
| `token` | string | yes | The one-time setup token from the server log. |

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
| `fixed` | integer | yes | Findings of the previous scan that this scan no longer has, as fixed_total of the scan diff counts them. |
| `gitlab_pipeline_id` | integer (int64) | yes |  |
| `kind` | string (one of `image`, `secret-fs`) | yes |  |
| `new` | [SeverityCounts](#severitycounts) | yes | Findings of this scan that the previous scan did not have, by severity, as new_total of the scan diff counts them. A first scan has every finding new; a secret scan has none. |
| `previous_scan_id` | integer or null (int64) | yes | The previous scan of the same ref, artifact and kind, which may be older than the window. null for a first scan. |
| `ref` | string | yes |  |
| `scan_id` | integer (int64) | yes |  |
| `scanned_at` | string (date-time) | yes |  |

### Trends

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `points` | array of [TrendPoint](#trendpoint) | yes | The newest 300 scans of the window at most, by scan time over every ref listed, oldest first. |
| `ref` | string or null | yes |  |
| `ref_source` | [RefSource](#refsource) | yes |  |
| `total` | integer | yes | Number of scans in the window, including any left out of points. |
| `truncated` | boolean | yes | True when the oldest scans of the window were left out because more than 300 fall in it. |

### TrivyIntegration

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `configured` | boolean | yes | The GitLab URL is set, so ID tokens can be verified. |
| `daily` | array of object | yes | Uploads and recorded rejections per UTC day over the last 30 days, oldest first, today last; days without either have zeros. |
| `last_upload_at` | string or null (date-time) | yes |  |
| `recent_rejections` | array of object | yes |  |
| `rejections_24h` | integer | yes | Recorded rejections in the last 24 hours. Rate-limited requests are not recorded and the number of recorded rejections per client address is bounded for rejections before the namespace and protected-ref checks. |
| `uploads_24h` | integer | yes |  |

### UploadError

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `error` | object | yes |  |

Properties of `error`:

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes |  |
| `message` | string | yes |  |
| `received` | integer (int64) | no | For offset_mismatch and upload_incomplete, the bytes the server has. |

### UploadStart

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `file_name` | string (minLength 1, maxLength 255) | yes | The name of the file being uploaded; kept as original_name. |
| `size` | integer (int64, minimum 1) | yes | Size of the whole file in bytes. |

### UploadStatus

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `chunk_size` | integer | yes | The largest part the server takes (8 MiB). |
| `file_name` | string | yes |  |
| `id` | string | yes | 128-bit random id in hex. |
| `received` | integer (int64) | yes | Bytes stored so far; the next part starts here. |
| `size` | integer (int64) | yes |  |

### User

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `created_at` | string (date-time) | yes |  |
| `disabled_at` | string or null (date-time) | yes | When an administrator disabled the user; null while the user may sign in. |
| `email` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `last_login_at` | string or null (date-time) | yes |  |
| `mfa_enrolled` | boolean | yes | The user has a confirmed TOTP enrolment. |
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
| `disabled` | boolean | no | true disables the user and ends their sessions; false enables the user. |
| `role` | [Role](#role) | no |  |

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
| `first_seen_at` | string (date-time) | yes | Scan time of the earliest scan of the same ref and artifact that contains this finding (vuln_id, pkg_name, target). |
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
| `counts` | [SeverityCounts](#severitycounts) | yes | Every matching finding by the severity reported for it, listed or not. |
| `fixed_versions` | array of string | yes | Distinct fixed versions reported over every match, at most 20. |
| `items` | array of object | yes |  |
| `primary_url` | string | yes | An advisory URL reported for the vulnerability, empty when none was. |
| `scope` | string (one of `dashboard`, `all`) | yes | The scope that was searched, as asked for. The totals below count within it. |
| `title` | string | yes | A title reported for the vulnerability, empty when none was. Over every match. |
| `total_artifacts` | integer (int64) | yes | Number of artifacts (per service) with at least one such finding in the scope. |
| `total_findings` | integer (int64) | yes | Number of unsuppressed findings of this ID in the current scans of the scope, including any not listed in items. |
| `total_services` | integer (int64) | yes | Number of non-archived services with at least one such finding in the scope. |
| `truncated` | boolean | yes | True when total_findings exceeds the 1000 findings listed. The listed ones are the most severe, then by service name. |
| `vuln_id` | string | yes |  |

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
