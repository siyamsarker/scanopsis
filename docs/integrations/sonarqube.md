# SonarQube integration

Scanopsis shows the quality gate, ratings, coverage and duplication that
SonarQube reports for each service. It receives a webhook after every
analysis, reads the results through the SonarQube Web API, and checks every
linked project periodically in case a webhook was missed.

It shows exactly what SonarQube reports. A metric that SonarQube did not
report is shown as "not available", never as 0.

This page was written for SonarQube Community Build 26.9. Names such as
`sonarqube.example.com`, `platform/payments-api` and `platform:payments-api`
are examples; replace them with your own.

Commands marked "example" have not been run against your servers: adapt them.

## 1. Create a read-only user and token

Scanopsis needs a user that can only browse the projects.

1. In SonarQube, go to **Administration > Security > Users** and create a user
   such as `scanopsis-reader`.
2. Give the user the **Browse** permission on the projects, directly or through
   a group (for example a group that has Browse on every project or on a
   permission template).
3. Sign in as that user, or generate a token for it from the users page, under
   **My Account > Security > Generate Tokens**. Choose a user token.

Do not give the user Execute Analysis or administrator rights. Scanopsis only
calls these endpoints, which Browse allows:

- `api/measures/component`
- `api/qualitygates/project_status`
- `api/project_analyses/search`
- `api/server/version`

Check the token (example, replace the URL and token):

```sh
curl -s -H "Authorization: Bearer $SONAR_TOKEN" https://sonarqube.example.com/api/server/version
```

It prints the version, for example `26.9.0.12345`.

## 2. Configure Scanopsis

Set these on the Settings page (an administrator), or as environment variables
before the first start:

| Setting | Environment variable | Value |
| --- | --- | --- |
| SonarQube URL | `SCANOPSIS_SONAR_URL` | `https://sonarqube.example.com` |
| SonarQube token | `SCANOPSIS_SONAR_TOKEN` | The user token from step 1. |
| Webhook secret | `SCANOPSIS_SONAR_WEBHOOK_SECRET` | A long random string. |
| Sync interval | `SCANOPSIS_SONAR_SYNC_INTERVAL` | Default `15m`, at least `1m`. |

Generate a secret (example):

```sh
openssl rand -hex 32
```

The webhook is refused with `sonar_not_configured` until the URL, the token and
the webhook secret are all set. The token and the secret are stored encrypted
and never logged.

### Use https

The token is sent to SonarQube as a bearer token on every Web API call. An
`https://` URL protects it. A plain `http://` URL is allowed, because internal
servers often have no TLS, but then the token and the results cross the network
in clear text. Scanopsis logs a warning ("the SonarQube token is sent without
TLS") when it starts with, or is changed to, a plain `http://` URL. Use `https://`
unless the network between the two servers is fully trusted.

Scanopsis does not follow a redirect from SonarQube to another scheme or host,
so the token cannot be sent elsewhere. If your SonarQube URL redirects (for
example from `http` to `https`, or to another host name), configure the final
address.

## 3. Create the webhook in SonarQube

Go to **Administration > Configuration > Webhooks** for all projects, or the
project's **Project Settings > Webhooks** for one, and create a webhook:

- URL: `<Scanopsis URL>/api/v1/hooks/sonarqube`, for example
  `https://scanopsis.example.com/api/v1/hooks/sonarqube`
- Secret: the same string as the webhook secret in Scanopsis.

SonarQube signs every delivery with the header `X-Sonar-Webhook-HMAC-SHA256`,
the lowercase hex HMAC-SHA256 of the request body with the secret. Scanopsis
accepts the delivery only if the signature matches. Webhooks time out after 10
seconds and SonarQube does not retry them; Scanopsis answers `202` at once and
processes the delivery in the background. The periodic sync covers lost
deliveries.

### Private addresses

SonarQube refuses to send webhooks to private or loopback addresses unless an
administrator sets `sonar.validateWebhooks=false` (in `sonar.properties` or the
server settings, depending on how SonarQube is run). That setting turns off the
protection against webhooks that reach internal services, for every webhook on
the server. Either give Scanopsis an address SonarQube accepts (a public name
that resolves to a public address, behind your reverse proxy), or accept the
trade-off and restrict who can create webhooks.

Without webhooks Scanopsis still works: the sync reads the newest analysis of
every linked project every sync interval (15 minutes by default).

## 4. Link projects to services

A SonarQube project is shown on the service it is linked to. There are two ways.

**Link by key.** On the Services page, enter the SonarQube project key on the
service (for example `platform:payments-api`). This is the safest way and the
only way the periodic sync uses.

**Link from the pipeline.** Add scanner properties to the analysis step of the
GitLab pipeline (example):

```sh
sonar-scanner \
  -Dsonar.projectKey=platform:payments-api \
  -Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID" \
  -Dsonar.analysis.pipelineId="$CI_PIPELINE_ID"
```

SonarQube copies properties that start with `sonar.analysis.` into the
webhook. Scanopsis links by the project key first. It uses
`sonar.analysis.gitlabProjectId` only for a service that has no SonarQube key
yet: the first analysis that carries the property links the project, and
Scanopsis saves the project key on that service, so the link is fixed from then
on. The link is shown on the Services page, where an administrator can change
or remove it. Administrators see each automatic link in the audit log, as a
`service.updated` event by the actor `sonarqube` with the service, the key and
the task ID. A pipeline cannot move a service to another SonarQube project, or
attach itself to a service that is already linked to a different project.
`sonar.analysis.pipelineId` is shown next to the analysis.

Archived services are never linked.

Community Build analyses one branch per project (the main branch), so there is
one result per project.

## 5. Modes: MQR and Standard

SonarQube reports ratings in one of two modes. Scanopsis detects the mode from
the measures it gets, and supports both.

| | MQR mode | Standard mode |
| --- | --- | --- |
| Reliability | `software_quality_reliability_rating` | `reliability_rating` |
| Security | `software_quality_security_rating` | `security_rating` |
| Maintainability | `software_quality_maintainability_rating` | `sqale_rating` |

In both modes Scanopsis also reads `security_review_rating`, `coverage`,
`duplicated_lines_density`, `ncloc`, `alert_status` and the hotspot measures.
Every measure SonarQube returns is kept as reported. Ratings `1.0` to `5.0`
are shown as A to E. The mode is shown with each analysis.

Scanopsis adapts to the server's mode automatically; there is nothing to
configure. It asks for the keys of both modes in one request. If a server
rejects metric keys it does not know (HTTP 400 or 404), Scanopsis asks again
with only the MQR keys, then with only the Standard keys, and remembers which
set worked until the SonarQube URL or token is changed.

## 6. Coverage of 0% after a failing test step

If a test step fails but still writes an empty coverage report, SonarQube
analyses the project with no covered lines and reports 0% coverage. Scanopsis
shows what SonarQube reports, so the coverage and, if the quality gate has a
coverage condition, the gate will look bad. Fix the pipeline so that a failed
test step does not publish an empty report, or does not run the analysis.

## 7. Check that it works

Open the integration page (an administrator; the data comes from
`GET /api/v1/admin/integrations/sonarqube`). It shows:

- `configured`: the URL and the token are set.
- `server_version`: read from SonarQube by the last sync. It is empty until the
  first sync has run (the first one starts shortly after the server starts).
- `last_webhook_at`: when the last delivery arrived.
- `last_sync_at` and `last_sync_error`: the last completed sync and its error.
- `recent_deliveries`: the 50 newest deliveries and what happened to them.

In SonarQube, the webhook's delivery log (Webhooks page) shows the HTTP status
Scanopsis answered. Run an analysis and open the service: the analysis appears
within a few seconds.

You can also test the signature by hand (example, with your own secret and
payload; the payload needs `taskId` and `project.key`):

```sh
BODY='{"taskId":"test-1","status":"SUCCESS","analysedAt":"2026-11-19T10:00:00+0000","project":{"key":"platform:payments-api"}}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $NF}')
curl -i -X POST https://scanopsis.example.com/api/v1/hooks/sonarqube \
  -H "Content-Type: application/json" -H "X-Sonar-Webhook-HMAC-SHA256: $SIG" \
  --data "$BODY"
```

`HTTP/2 202` means the delivery was accepted. It then appears on the
integration page as `unlinked` unless a service uses that project key.

## Troubleshooting

### Responses of the webhook endpoint

| Status and code | Cause | What to do |
| --- | --- | --- |
| 503 `sonar_not_configured` | The URL, the token or the webhook secret is not set. | Set all three in Settings. |
| 401 `invalid_signature` | The header is missing, or the secret differs from the one in SonarQube, or something changed the body on the way (a proxy that rewrites it). | Set the same secret in both places; send the webhook straight to Scanopsis or through a proxy that passes the body unchanged. |
| 400 `invalid_payload` | The body is not JSON, or has no `taskId` or `project.key`. | Make sure the webhook comes from SonarQube. |
| 413 `payload_too_large` | The body is larger than 1 MiB. | Reduce the number of `sonar.analysis.*` properties. |
| 500 `internal_error` | The database could not be reached. | See the server log. |
| 202 | Accepted, also for a delivery seen before (same `taskId`). | None. |

### Outcomes on the integration page

| Outcome and detail | Cause | What to do |
| --- | --- | --- |
| `unlinked`: No service is linked to SonarQube project "...". | No service has this project key, and the pipeline did not pass a usable `sonar.analysis.gitlabProjectId` of a service without a key. | Set the key on the service, or add `-Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID"`. |
| `unlinked`: The service named by `sonar.analysis.gitlabProjectId` is linked to a different SonarQube project... | The service already has another key (for example the project was renamed). | Change the SonarQube key on the service. |
| `unlinked`: ...cannot be linked to SonarQube project "..." because the service or the key is already linked. | The key belongs to another (possibly archived) service. | Free the key on the other service, or link by key. |
| `failed`: The analysis task ended with status "...". | The analysis failed in SonarQube; nothing was stored. | Fix the analysis; the next successful one is stored. |
| `failed`: Reading measures failed: SonarQube answered 401 on api/measures/component. | The token is wrong or revoked. | Create a new token and update the setting. |
| `failed`: ... answered 403 or 404 on ... | The user lacks Browse on the project, or the project key does not exist. (Newer versions answer 404 for a missing permission.) | Grant Browse to the user. |
| `failed`: ... answered 5xx on ... | SonarQube had an error. | Check SonarQube; the next sync retries. |
| `failed`: ...the request failed or the answer was unreadable; see the server log. | SonarQube could not be reached (DNS, firewall, TLS certificate, timeout of 10 seconds, or a refused redirect). | Read the server log, which has the full error. |
| `failed`: The SonarQube URL and token are not configured... | The settings were cleared after the delivery arrived. | Set them again. |
| `failed`: The webhook has no readable analysedAt. | The payload has no valid analysis time. | Make sure the webhook comes from SonarQube. |
| `failed`: The processing queue was full... | More than 100 deliveries were waiting. | None; the next sync reads the analysis. |
| `failed`: Storing the analysis failed; see the server log. | A database error. | Read the server log. |
| `linked` with a note: A newer analysis (...) already exists | Another analysis finished before this one was read. The measures are from the newer one. | None; the next sync adds the newer analysis. |
| `linked`: The analysis was already stored by a sync. | The sync was faster than the webhook. | None. |
| `pending` | The delivery is waiting to be processed. | None, it changes within seconds. |
| `failed`: Processing was interrupted. Redeliver the webhook from SonarQube to process it again; analyses of projects linked by key are also picked up by the next sync. | The server stopped while processing it; a delivery pending for more than 10 minutes is marked this way by the next sync. | In SonarQube, open the project's (or the global) webhook settings, show the recent deliveries of the Scanopsis webhook and redeliver this one. A project already linked by key needs nothing: the next sync reads the analysis. |

### Other problems

| Problem | Cause | What to do |
| --- | --- | --- |
| SonarQube shows the webhook delivery as failed with a timeout or connection error | SonarQube cannot reach Scanopsis, or blocks private addresses. | See "Private addresses". |
| Nothing is shown for a service | It has no SonarQube key and no analysis arrived with its project ID. | Link it (step 4). |
| `last_sync_error` is set | The last sync could not read a project; the text names the project and the SonarQube status. | Fix the cause; the next sync clears it. |
| A rating or coverage is "not available" | SonarQube did not report that metric for the analysis. | Check the project in SonarQube. |
