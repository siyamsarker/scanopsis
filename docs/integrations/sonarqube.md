# Connect SonarQube

Show the quality gate, ratings, coverage and duplication from SonarQube next to
the Trivy results of each service. This guide connects the two servers and links
your SonarQube projects to services.

**Audience:** Operators · **Back to:** [README](../../README.md) · [All guides](../../README.md#documentation)

---

## How it works

After every analysis, SonarQube sends a signed **webhook** to Scanopsis.
Scanopsis then reads the results through the SonarQube **Web API** with a
read-only token. Every sync interval (15 minutes by default) it also reads the
newest analysis of every linked project, so a lost webhook only delays a result.

Scanopsis shows exactly what SonarQube reports. A metric that SonarQube did not
report is shown as "Not available", never as 0.

This guide was written for SonarQube Community Build 26.9. Names such as
`sonarqube.example.com`, `platform/payments-api` and `platform:payments-api` are
examples. Commands marked "example" need your SonarQube or GitLab, so adapt them
before you run them.

## Before you start

You need:

- A running Scanopsis, and an administrator account. See the [getting started
  guide](../getting-started.md).
- A SonarQube administrator, who can create a user and a webhook.
- A network path in both directions: from SonarQube to Scanopsis for the
  webhook, and from Scanopsis to SonarQube for the Web API.

## Step 1. Create a read-only user and token

Scanopsis needs a SonarQube user that can only browse projects.

1. In SonarQube, open **Administration > Security > Users** and create a user,
   for example `scanopsis-reader`.
2. Give the user the **Browse** permission on the projects, directly or through
   a group. A permission template makes this automatic for new projects.
3. Create a **user token** for it: sign in as that user and open **My Account >
   Security**, or generate the token from the Users page as an administrator.

Do not give the user Execute Analysis or administrator rights. Scanopsis calls
only these endpoints, which Browse allows:

- `api/measures/component`
- `api/qualitygates/project_status`
- `api/project_analyses/search`
- `api/server/version`

Check the token (example):

```sh
curl -s -H "Authorization: Bearer $SONAR_TOKEN" https://sonarqube.example.com/api/server/version
```

It prints the SonarQube version, a number that starts with `26.9` on Community
Build 26.9.

## Step 2. Enter SonarQube in Scanopsis

Create a long random webhook secret:

```sh
openssl rand -hex 32
```

As an administrator, open **Settings** and fill in the group **SonarQube**. You
can also seed these values at the first start with the environment variables:

| Setting | Environment variable | Value |
| --- | --- | --- |
| `sonar.url` | `SCANOPSIS_SONAR_URL` | The base URL, for example `https://sonarqube.example.com`. |
| `sonar.token` | `SCANOPSIS_SONAR_TOKEN` | The user token from step 1. |
| `sonar.webhook_secret` | `SCANOPSIS_SONAR_WEBHOOK_SECRET` | The random secret you just created. |
| `sonar.sync_interval` | `SCANOPSIS_SONAR_SYNC_INTERVAL` | How often to read every linked project. Default `15m`, at least `1m`. |

Select **Save changes**. The page shows "Settings saved. Changes apply now." No
restart is needed.

The token and the secret are stored encrypted and never shown or logged. The URL
and the token need each other: saving one without the other fails with "Required
when other SonarQube settings are set." The webhook is refused with
`sonar_not_configured` until all three are set.

### Use https

Scanopsis sends the token with every Web API call, so use an `https://` URL. A
plain `http://` URL works, because internal servers often have no TLS, but then
the token and the results cross the network in clear text. In that case
Scanopsis logs `the SonarQube token is sent without TLS; use an https://
SonarQube URL` the first time it calls SonarQube.

Two more rules keep the token where it belongs:

- When you change the SonarQube URL, enter the token again in the same save.
  Otherwise saving fails with "Enter this value again when you change the URL,
  so the stored one is never sent to a new address."
- Scanopsis does not follow a redirect to another scheme or host. If your
  SonarQube URL redirects, for example from `http` to `https`, enter the final
  address.

## Step 3. Create the webhook in SonarQube

In SonarQube, open **Administration > Configuration > Webhooks** for all
projects, or **Project Settings > Webhooks** for one project. Create a webhook
with:

- **URL**: your Scanopsis address plus `/api/v1/hooks/sonarqube`, for example
  `https://scanopsis.example.com/api/v1/hooks/sonarqube`.
- **Secret**: the webhook secret from step 2.

SonarQube signs each delivery: the header `X-Sonar-Webhook-HMAC-SHA256` holds
the HMAC-SHA256 of the body, keyed with the secret. Scanopsis accepts a delivery
only when the signature matches.

SonarQube waits only 10 seconds for an answer and does not retry. So Scanopsis
answers `202` at once and does the work in the background.

### If SonarQube refuses a private address

SonarQube refuses to send webhooks to private or loopback addresses unless an
administrator sets `sonar.validateWebhooks=false`. Where you set it depends on
how SonarQube runs: in `sonar.properties` or in the server settings. That
setting turns off the protection for every webhook on the server, so prefer one
of these:

- Give Scanopsis an address that SonarQube accepts, such as a public name behind
  your reverse proxy.
- Or accept the trade-off and limit who can create webhooks.

Without webhooks Scanopsis still works. The sync reads the newest analysis of
every linked project every sync interval.

## Step 4. Link projects to services

A SonarQube analysis is shown on the service its project is linked to. There are
three ways to link: by key, from the upload, and from the pipeline's analysis.

### Link by key

As an administrator, open **Services**, select **Edit** on the
service and enter the **SonarQube project key**, for example
`platform:payments-api`. This is the safest way, and the only way the sync uses.

### Link from the upload

The Trivy upload of the pipeline can declare the project key with the
parameter `sonar_key`. The scripts of the [GitLab CI
guide](gitlab-ci.md#link-the-sonarqube-project) send the variable
`SONAR_PROJECT_KEY` this way. The analysis must also name the GitLab project:

```sh
sonar-scanner \
  -Dsonar.projectKey="$SONAR_PROJECT_KEY" \
  -Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID" \
  -Dsonar.analysis.pipelineId="$CI_PIPELINE_ID"
```

The CI token of the upload proves the GitLab project, but anyone can write any
key into an upload. So the analysis confirms the key: Scanopsis sets the
declared key `K` on the service of GitLab project `P` only when a webhook
delivery of an analysis of `K` names `P` in `sonar.analysis.gitlabProjectId`.
Only a SonarQube user who may run analyses of `K` (the Execute Analysis
permission) can cause such a delivery.

1. **The analysis came first** (it arrived in the last 7 days): the upload sets
   the key at once, links the analyses of `K` that name `P`, and Scanopsis
   reads the newest one from SonarQube. The answer has
   `"sonar_key_ignored":null`.
2. **The upload came first**, or the analysis does not name `P`: the key waits
   on the service, and the answer has `"sonar_key_ignored":"sonar_key_pending"`.
   The next delivery of an analysis of `K` that names `P` sets it, as long as
   the service still has no key and no other service took `K`.

While a declared key waits, only that key can link the service: an analysis of
any other project that names `P` is not linked, and its delivery says why. A
newer upload that declares another key replaces the waiting one.

- **See it.** The Services page shows a waiting key next to the SonarQube
  field as "Waiting for SonarQube project `K`", and the API gives it as
  `sonar_key_declared` and `sonar_key_declared_at` of the service. When an
  analysis is not linked to a service, check this first.
- **Override it.** An administrator who sets or clears the SonarQube key of
  the service cancels the waiting key. On the Services page, open **Edit**
  and enter a key, or select **Cancel waiting key**; saving only a new name
  keeps the waiting key. The audit event then has
  `cleared_sonar_key_declared`.
- **A taken key.** If another service gets `K` while it waits, the next upload
  of the project that declares `K` drops the waiting key and answers
  `key_linked_to_other_service`. Until that upload, the waiting key still
  keeps other projects' analyses from linking the service.

The upload never changes a key that is set, and never takes a key from another
service. It answers `service_has_other_key` when the service has another key,
and `key_linked_to_other_service` when another service, maybe an archived one,
has `K`. The scan is stored either way.

Every step is in the audit log:

| Event | Actor | When |
| --- | --- | --- |
| `service.updated` with the reason "declared by the pipeline's upload and confirmed by a SonarQube analysis of the same project" | `ci:<project path>` with the pipeline and job, or `sonarqube` when the webhook confirms a waiting key | The key is set. |
| `service.updated` with `sonar_key_declared` and the reason "declared by the pipeline's upload; set when a SonarQube analysis of the same project confirms it" | `ci:<project path>` | The key starts to wait. A repeated upload of the same waiting key is not recorded again. |
| `ingest.sonar_key_ignored` (**SonarQube key ignored**) with the reason, the declared key, for `service_has_other_key` the current one, and `cleared_sonar_key_declared` when it dropped a waiting key | `ci:<project path>` | The upload's key was not used. Not recorded again while the service is unchanged: a new reason or key, or any change of the service, is recorded again. The server log has a warning for every such upload. |
| `service.updated` with `cleared_sonar_key_declared` | The administrator | An administrator set or cleared the key while a declared key waited. |

### Link from the analysis

Add two scanner properties to the analysis step of
the GitLab pipeline (example):

```sh
sonar-scanner \
  -Dsonar.projectKey=platform:payments-api \
  -Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID" \
  -Dsonar.analysis.pipelineId="$CI_PIPELINE_ID"
```

SonarQube copies properties that start with `sonar.analysis.` into the webhook.
Scanopsis then links this way:

1. It looks for a service with the project key first.
2. Only if none has it, it looks for the service of the GitLab project in
   `sonar.analysis.gitlabProjectId`. If that service has no SonarQube key yet,
   the analysis links it and Scanopsis saves the key on the service. From then
   on the link is fixed.
   If the analysis runs before the first Trivy scan of the project, there is
   no service yet. The analysis waits for up to 7 days. When the first scan
   of the same pipeline (`sonar.analysis.pipelineId`) creates the service,
   that analysis links it under the same rules and its result is shown.
   Analyses that name the project from another pipeline are never linked
   this way, and if analyses of two SonarQube projects come from the same
   pipeline, none is linked: set the key on the service.
3. A pipeline can never move a service to another SonarQube project, or take
   over a service that is linked to a different one.
4. If an upload of the service declared a key, only an analysis of that key
   links it, see [link from the upload](#link-from-the-upload). An upload with
   `sonar_key` never links the service this way alone.

Each automatic link shows up in the audit log as `service.updated` by the actor
`sonarqube`, with the service, the key and the task ID. An administrator can
change or remove the key on the Services page. `sonar.analysis.pipelineId` is
shown next to the analysis.

Archived services are never linked and never synced. After unarchiving a
service, set its SonarQube key if it has none.

Community Build analyses one branch per project, the main branch, so each
service has one SonarQube result.

## Step 5. Check that it works

1. Run an analysis in SonarQube.
2. In Scanopsis, open **Integrations**. The **SonarQube** card shows **Ready**,
   the server version, the last webhook, the last sync and the recent
   deliveries. The server version appears after the first sync, about 10 seconds
   after Scanopsis starts.
3. Find your analysis under **Recent deliveries** with the outcome **Linked**,
   then open the service. The analysis appears within a few seconds.

In SonarQube, the delivery log of the webhook shows the HTTP status that
Scanopsis answered.

You can also send a signed test delivery by hand (example; set `SECRET` to your
webhook secret):

```sh
BODY='{"taskId":"test-1","status":"SUCCESS","analysedAt":"2026-11-19T10:00:00+0000","project":{"key":"platform:payments-api"}}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $NF}')
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://scanopsis.example.com/api/v1/hooks/sonarqube \
  -H "Content-Type: application/json" -H "X-Sonar-Webhook-HMAC-SHA256: $SIG" \
  --data "$BODY"
```

It prints `202`. The delivery then appears on the Integrations page as
**Unlinked** unless a service has that project key. A real service with that key
would make Scanopsis read the analysis from SonarQube, which fails for a made-up
task, so use a key that no service has.

## How Scanopsis reads analyses

### MQR and Standard mode

SonarQube reports ratings in one of two modes. Scanopsis detects the mode from
the measures and supports both. There is nothing to configure.

| | MQR mode | Standard mode |
| --- | --- | --- |
| Reliability | `software_quality_reliability_rating` | `reliability_rating` |
| Security | `software_quality_security_rating` | `security_rating` |
| Maintainability | `software_quality_maintainability_rating` | `sqale_rating` |

In both modes Scanopsis also reads `security_review_rating`, `coverage`,
`duplicated_lines_density`, `ncloc`, `alert_status` and the hotspot measures. It
keeps every measure as reported, shows ratings `1.0` to `5.0` as A to E, and
shows the mode with each analysis.

Scanopsis asks for the keys of both modes in one request. If SonarQube rejects
keys it does not know (HTTP 400 or 404), Scanopsis asks again with only the MQR
keys, then with only the Standard keys. It remembers the set that worked until
the SonarQube URL or token changes.

### Deliveries and the sync

- Four workers process deliveries. Up to 500 more can wait. A delivery that
  finds the queue full is not lost: it stays **Pending**, and the next sync
  reads the analysis if the project is linked by key.
- Deliveries and the sync run apart. A slow SonarQube delays neither the `202`
  answers nor the sync.
- A sync stops early when reading the SonarQube version fails, after 3 calls in
  a row get no answer, or when SonarQube rejects the token (401). It shows the
  cause as the last sync error and tries again at the next interval. So an
  unreachable SonarQube costs about one timeout of 10 seconds, not one per
  project.
- A sync with failures writes one warning, `SonarQube sync finished with
  failures`, with the number of failed projects and the first error. The lines
  per project are at `debug` level.
- A delivery that stays **Pending** for more than 10 minutes is marked
  **Failed** as interrupted. A delivery that waits because the queue was full
  gets two more sync intervals first.
- Only one server syncs at a time. The sync holds a PostgreSQL advisory lock on
  a connection of its own, and no pooled connection while it calls SonarQube.

### Coverage of 0% after a failing test step

If a test step fails but still writes an empty coverage report, SonarQube
reports 0% coverage. Scanopsis shows what SonarQube reports, so the coverage
looks bad, and so does the quality gate if it has a coverage condition. Fix the
pipeline so that a failed test step does not publish an empty report, or skips
the analysis.

## Troubleshooting

### Answers of the webhook endpoint

| Status and code | Cause | Fix |
| --- | --- | --- |
| `202` | Accepted. A repeated delivery with the same `taskId` is accepted too, and processed once. | Nothing to do. |
| `503` `sonar_not_configured` | The URL, the token or the webhook secret is not set. | Set all three in Settings. |
| `401` `invalid_signature` | The header is missing, the secret differs from the one in SonarQube, or something on the way changed the body, such as a proxy that rewrites it. | Use the same secret in both places. Send the webhook straight to Scanopsis, or through a proxy that passes the body unchanged. |
| `400` `invalid_payload` | The body is not JSON, or has no `taskId` or `project.key`. | Make sure the webhook comes from SonarQube. |
| `413` `payload_too_large` | The body is larger than 1 MiB. | Pass fewer `sonar.analysis.*` properties. |
| `500` `internal_error` | The database could not be reached. | Read the Scanopsis log. |

### Rejected webhooks in the audit log

A refused webhook (missing or wrong signature, body too large, signed body that
is not a SonarQube webhook) writes one audit event, **Webhook rejected**
(`webhook.rejected`, actor `sonarqube:unknown`), with the reason and the client
address. The server log gets a `webhook rejected` warning.

Anyone who can reach the endpoint can cause a refusal. To keep them from filling
the audit log, Scanopsis records at most 10 per client address at once, then one
per minute. An IPv6 client counts by its /64 network. The rest appear only in
the server log at `debug` level. The answer is the same either way.

Many of these events mean a wrong webhook secret in SonarQube, or someone else
calling the endpoint.

### Outcomes on the Integrations page

| Outcome and detail | Cause | Fix |
| --- | --- | --- |
| Pending | The delivery waits for a worker. | Nothing to do. It changes within seconds. |
| Pending: `The processing queue was full; the next sync will read the analysis.` | More than 500 deliveries were waiting. | Nothing to do if the project is linked by key: the next sync reads the analysis and marks it Linked. Otherwise link it by key, or redeliver the webhook later. |
| Linked: `The processing queue was full; the sync read the analysis.` | The delivery waited for the sync, which stored the analysis. | Nothing to do. |
| Linked: `The analysis was already stored by a sync.` | The sync was faster than the webhook. | Nothing to do. |
| Linked: `A newer analysis (...) already exists in SonarQube; the measures are from it. The next sync will add it.` | Another analysis finished before this one was read. | Nothing to do. |
| Unlinked: `No service is linked to SonarQube project "...". Pass -Dsonar.analysis.gitlabProjectId=... and -Dsonar.analysis.pipelineId=... ...` | No service has this project key, and the pipeline passed no usable `sonar.analysis.gitlabProjectId`. | Set the key on the service, or add both properties. See [step 4](#step-4-link-projects-to-services). |
| Unlinked: `No service is linked to SonarQube project "..." yet. The analysis is linked when the scan of the same pipeline (...) reaches Scanopsis ...` | The analysis ran before the first Trivy scan of the project, so there is no service yet. | Nothing to do: the first scan of that pipeline links it, and the delivery then shows Linked. If that upload declares another key with `sonar_key`, only an analysis of the declared key is linked, and this delivery then says the service waits for it (see below). If the upload's `sonar_key` is ignored, the delivery stays as it is: set the key on the service. |
| Unlinked: `No service is linked to SonarQube project "...", and no service has GitLab project ... yet. Pass -Dsonar.analysis.pipelineId=... ...` | As above, but without `sonar.analysis.pipelineId`, so the analysis cannot be tied to the first scan. | Add `-Dsonar.analysis.pipelineId="$CI_PIPELINE_ID"`, or set the key on the service. |
| Unlinked: `The service of GitLab project ... is archived, so SonarQube project "..." was not linked. ...` | The service of the project is archived. | An administrator unarchives the service and sets its SonarQube key. |
| Unlinked: `More than one SonarQube project names GitLab project ... in pipeline ..., so none was linked automatically. ...` | Analyses of two SonarQube projects came from the pipeline of the first scan. | An administrator sets the right key on the service. |
| Unlinked: `Not linked: the service of GitLab project ... was linked to SonarQube project "..." by the analysis of the pipeline of its first scan. ...` | Another analysis, of the first scan's pipeline, was linked. This one came from another pipeline or named another project. | Nothing to do if the link is right; otherwise change the key on the service. |
| Linked: `Linked when the first scan of the same pipeline created the service of the GitLab project; ...` | The analysis waited for the first scan of its pipeline. | Nothing to do. |
| Linked: `Linked when an upload of the service's pipeline declared this SonarQube project key; ...` | The analysis arrived first, and a later upload declared its key (`sonar_key`). | Nothing to do. |
| Linked: `Linked when a service was given this SonarQube project key while the delivery was processed; ...` | The service got the key, by an upload or an administrator, while the delivery was processed. | Nothing to do. |
| Unlinked: `The service of GitLab project ... waits for SonarQube project "...", the key its pipeline declared in an upload, so ...` | An upload declared another key, which waits for its own analysis. | Nothing to do if the declared key is right. Otherwise correct `SONAR_PROJECT_KEY`, or set the key on the service. |
| Unlinked: `Not linked: the service of GitLab project ... is linked to SonarQube project "...", the key its pipeline declared in its upload. ...` | The declared key was set, and this analysis is of another project. | Nothing to do if the link is right; otherwise change the key on the service. |
| Unlinked: `The service named by sonar.analysis.gitlabProjectId is linked to a different SonarQube project, ...` | The service already has another key, for example because the project was renamed. | Change the SonarQube key on the service. |
| Unlinked: `The service named by sonar.analysis.gitlabProjectId cannot be linked to SonarQube project "..." because the service or the key is already linked, or the service's pipeline declared another key in an upload.` | Another service, maybe an archived one, has this key, or the service waits for the key its pipeline declared. | Look at the service on the Services page: a waiting key shows there. Free the key on the other service, correct `SONAR_PROJECT_KEY`, or set the key on the service. |
| Failed: `The analysis task ended with status "..."; nothing was stored.` | The analysis failed in SonarQube. | Fix the analysis. The next successful one is stored. |
| Failed: `Reading measures failed: SonarQube answered 401 on api/measures/component. ...` | The token is wrong or revoked. | Create a new token and save it in Settings. |
| Failed: `... SonarQube answered 403 on ...` or `... answered 404 on ...` | The user lacks Browse on the project, or the project key does not exist. Newer versions answer 404 for a missing permission. | Give the user Browse on the project. |
| Failed: `... SonarQube answered 5xx on ...` | SonarQube had an error. | Check SonarQube. The next sync retries. |
| Failed: `... the request failed or the answer was unreadable; see the server log. ...` | SonarQube could not be reached: DNS, firewall, TLS certificate, the 10-second timeout, or a refused redirect. | Read the Scanopsis log, which has the full error. |
| Failed: `The SonarQube URL and token are not configured, so the analysis cannot be read.` | The settings were cleared after the delivery arrived. | Set them again. |
| Failed: `The webhook has no readable analysedAt.` | The payload has no valid analysis time. | Make sure the webhook comes from SonarQube. |
| Failed: `Storing the analysis failed; see the server log.` | A database error. | Read the Scanopsis log. |
| Failed: `Processing was interrupted. Redeliver the webhook from SonarQube to process it again; ...` | Scanopsis stopped while it processed the delivery, so it stayed pending for more than 10 minutes. | In SonarQube, open the webhook settings, show the recent deliveries of the Scanopsis webhook and redeliver this one. A project linked by key needs nothing: the next sync reads it. |

### Other problems

| Problem | Cause | Fix |
| --- | --- | --- |
| SonarQube shows the delivery as failed, with a timeout or a connection error. | SonarQube cannot reach Scanopsis, or blocks private addresses. | See [If SonarQube refuses a private address](#if-sonarqube-refuses-a-private-address). |
| A service shows no SonarQube data. | It has no SonarQube key, and no analysis arrived with its GitLab project ID. Or its pipeline declared a key that waits: the Services page shows "Waiting for SonarQube project ...". | Link it, see [step 4](#step-4-link-projects-to-services). For a waiting key, run an analysis of that key with `-Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID"`, or set the key on the service. |
| The Integrations page shows "The last sync failed". | The last sync could not read a project. The text names the project and the SonarQube status. | Fix the cause. The next good sync clears it. |
| A rating or the coverage says "Not available". | SonarQube did not report that metric for the analysis. | Check the project in SonarQube. |

---

[Back to the README](../../README.md) · [All guides](../../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
