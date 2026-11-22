# Example API reference

Look up any endpoint of the HTTP API: what it takes, what it returns and how
it is authenticated.

**Audience:** Developers · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

<!-- Generated from api/openapi.yaml by `make generate`. Do not edit by hand. -->

This page is generated from the OpenAPI document `api/openapi.yaml`, the
contract of the API. To change it, change that file and run `make generate`.
Version of the OpenAPI document: 1.2.3.

A small API used to test the documentation generator.

A second paragraph stays a paragraph of its own.

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

### `GET /api/v1/admin/log`

The log.

- Operation ID: `log`
- Authentication: Session, admin

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | The log. | none |

### `POST /api/v1/hook`

Webhook target.

Verified with the X-Example-HMAC-SHA256 header (hex HMAC-SHA256 of the body).

- Operation ID: `hook`
- Authentication: HMAC signature (`X-Example-HMAC-SHA256` header)

Responses:

| Status | Description | Body |
|---|---|---|
| 202 | Accepted. | none |

### `GET /api/v1/items`

List items.

- Operation ID: `listItems`
- Authentication: Session

Parameters:

| Name | In | Required | Type | Description |
|---|---|---|---|---|
| `after` | query | no | integer (int64) |  |
| `limit` | query | no | integer (minimum 1, maximum 200, default 50) | Page size. |

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | One page. | `application/json` [ItemPage](#itempage) |
| 401 | No valid session. | `application/json` [Error](#error) |

### `POST /api/v1/items`

Add an item (admin only).

- Operation ID: `createItem`
- Authentication: Session, admin

Request body (required):

- `application/json`: [Item](#item)

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | Created. | none |
| 403 | Not an admin. | `application/json` [Error](#error) |

### `POST /api/v1/upload`

Upload a report.

Authenticated with the job token.

- Operation ID: `upload`
- Authentication: CI ID token

Request body (optional):

- `application/json`: object

Responses:

| Status | Description | Body |
|---|---|---|
| 201 | Stored. | none |

### `GET /healthz`

Liveness probe.

- Operation ID: `healthz`
- Authentication: None

Responses:

| Status | Description | Body |
|---|---|---|
| 200 | Running. | `application/json` [Status](#status) |

## Schemas

### Error

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `error` | object | yes |  |

Properties of `error`:

| Property | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes | Stable code. |

### Item

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `name` | string (maxLength 20) | yes |  |
| `owner` | string or null | no |  |
| `parent` | [Status](#status) or null | no |  |
| `tags` | array of string | no |  |

### ItemPage

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `extra` | [Status](#status) | no |  |
| `items` | array of [Item](#item) | no |  |

### Status

Type: object

| Property | Type | Required | Description |
|---|---|---|---|
| `status` | string (one of `ok`, `ready`) | no |  |

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
