# Upload Trivy reports from GitLab CI

This guide makes your GitLab CI jobs send their Trivy reports to Scanopsis, so
that the dashboard shows every finding of every image and repository, traced to
the pipeline, job and commit that produced it.

A job uploads its report with an HTTP request that carries a GitLab CI/CD ID
token. GitLab creates the token for the job and signs it. Scanopsis checks the
signature against the public keys of your GitLab instance. There is no shared
secret to create, store, rotate or leak, and the token proves which project,
pipeline, job, branch and commit the report belongs to. A token is only valid
for the timeout of the job that received it.

The upload never changes the result of the job. A pipeline that passed keeps
passing when Scanopsis is down, and the dashboard shows exactly what Trivy
reported.

Command output in this guide was captured from a test instance of Scanopsis
and a test GitLab. The host names were replaced by `scanopsis.example.com` and
`gitlab.example.com`, and long tables are shortened where marked.

## Before you start

You need:

- A running Scanopsis. See the [getting started guide](../getting-started.md).
- GitLab 15.7 or later. Earlier versions do not have the `id_tokens` keyword.
- Trivy 0.50 or later on the runner. Older versions have no `--show-suppressed`.
- `curl`, `jq` and `gzip` on the runner.
- Network access from the runner to Scanopsis, and from Scanopsis to your
  GitLab instance (Scanopsis downloads the signing keys from it).

Check the tools on the runner:

```console
$ trivy --version | head -1
Version: 0.76.2
$ jq --version
jq-1.7.1-apple
$ curl --version | head -1
curl 8.7.1 (x86_64-apple-darwin26.0) libcurl/8.7.1 (SecureTransport) LibreSSL/3.3.6 zlib/1.2.12 nghttp2/1.69.0
$ gzip --version | head -1
Apple gzip 487.40.2
```

## Step 1: Tell Scanopsis where your GitLab is

Scanopsis needs the base URL of your GitLab instance to verify tokens. Set it
in **Settings** in Scanopsis, or start Scanopsis with the environment variable:

```text
SCANOPSIS_GITLAB_URL=https://gitlab.example.com
```

Without it every upload is answered with `503 ingest_not_configured`.

The tokens must also carry the public URL of Scanopsis as their audience
(`aud`). This is `SCANOPSIS_PUBLIC_URL`, for example
`https://scanopsis.example.com`. Step 3 sets it.

## Step 2: Add the scan script to your repository

Save this as `ci/scan.sh`. It scans each image once, with every severity and
with unfixed findings included, uploads that full report, and then decides
whether the job fails from the same report.

```sh
#!/bin/sh
# ci/scan.sh: scan images and the repository, upload the full Trivy reports
# to Scanopsis, and fail the job only on fixed HIGH and CRITICAL findings.
#
# Needs: trivy, curl, jq, gzip.
# Variables: SCANOPSIS_URL, SCANOPSIS_ID_TOKEN (from id_tokens), IMAGES (space separated).
set -u
status=0
out=scanopsis-out
mkdir -p "$out"

# upload <report.json> <image|secret-fs>
# Prints what happened and always returns 0: a problem with the upload never
# changes the result of the job. curl retries for about four minutes (8
# retries, waiting 1 s, 2 s, 4 s and so on, or the Retry-After of the answer)
# when Scanopsis answers 408, 429, 500, 502, 503 or 504, when a transfer times
# out, and when the connection is refused (Scanopsis is restarting). It does
# not retry other answers: a 4xx such as 401, 409 or 413 stays the same.
upload() {
  gzip -c "$1" >"$1.gz"
  code=$(curl -sS --max-time 120 --retry 8 --retry-connrefused --retry-max-time 300 -X POST \
    -H "Authorization: Bearer ${SCANOPSIS_ID_TOKEN:-}" \
    -H 'Content-Type: application/json' \
    -H 'Content-Encoding: gzip' \
    --data-binary @"$1.gz" \
    -o "$out/response.json" -w '%{http_code}' \
    "$SCANOPSIS_URL/api/v1/ingest/trivy?kind=$2") || code=000
  echo "scanopsis: upload of $1 returned HTTP $code"
  [ -s "$out/response.json" ] && cat "$out/response.json"
  return 0
}

for image in $IMAGES; do
  name=$(printf '%s' "$image" | tr '/:@' '___')

  # One full scan: all severities, unfixed findings included, ignore file applied.
  if ! trivy image --quiet --scanners vuln --format json --show-suppressed \
      --output "$out/$name.json" "$image"; then
    echo "trivy could not scan $image"
    status=1
    continue
  fi

  upload "$out/$name.json" image

  # The gate, built from the same JSON: drop findings without a fix (this is
  # what --ignore-unfixed does), then fail on HIGH and CRITICAL.
  jq 'if .Results then .Results |= map(if .Vulnerabilities then .Vulnerabilities |= map(select(.Status == "fixed")) else . end) else . end' \
    "$out/$name.json" >"$out/$name.gate.json"
  trivy convert --format table --severity HIGH,CRITICAL --exit-code 1 "$out/$name.gate.json" || status=1
done

# Secrets are a separate scan without a severity filter.
trivy fs --quiet --scanners secret --skip-dirs "$out" --format json --output "$out/secrets.json" . || status=1
upload "$out/secrets.json" secret-fs
trivy convert --format table --exit-code 1 "$out/secrets.json" || status=1

exit "$status"
```

What the script does, in order:

1. `trivy image ... --format json --show-suppressed` writes one complete report
   per image. It lists all severities, fixed and unfixed findings, and also the
   findings your ignore file (`.trivyignore`) or VEX documents suppressed, so
   that Scanopsis can show them as suppressed.
2. `upload` sends the report, gzip-compressed, and prints the HTTP status and
   the answer. It retries a temporary failure (see
   [Retries and limits](#retries-and-limits)). It always returns 0, so a failed
   upload cannot fail the job.
3. `jq` removes the vulnerabilities whose `Status` is not `fixed`, and
   `trivy convert` prints the table of HIGH and CRITICAL findings from what is
   left and returns exit code 1 if there are any. This is the gate you had
   before, built from the report that was uploaded.
4. The secret scan is a separate run, because secrets have no fix status and
   must not be filtered by severity. It uploads with `kind=secret-fs`.
5. `exit "$status"` is the last line, so the job result is decided by the
   gates and the scans only.

`trivy convert` prints `No enabled scanners found. Summary table will not be
displayed.` on stderr. This is expected; the findings table follows it.

### Why the gate gives the same result as `--ignore-unfixed`

Trivy implements `--ignore-unfixed` as "hide every vulnerability whose status
is not `fixed`". The `jq` step does exactly that on the JSON. This was checked
by running the old gate and the new pipeline on real images:

```text
old: trivy image --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 IMAGE
new: trivy image --format json --show-suppressed IMAGE, jq (Status == "fixed"),
     trivy convert --format table --severity HIGH,CRITICAL --exit-code 1
```

| Image | Exit code old / new | HIGH / CRITICAL old | HIGH / CRITICAL new |
| --- | --- | --- | --- |
| `python:3.9-slim-buster` | 1 / 1 | 18 / 0 | 18 / 0 |
| `python:3.9-slim-buster` with an ignore file of 3 CVEs | 1 / 1 | 17 / 0 | 17 / 0 |
| `alpine:3.17.0` | 1 / 1 | 26 / 0 | 26 / 0 |
| `node:16-alpine` | 1 / 1 | 40 / 1 | 40 / 1 |
| `alpine:3.20` | 1 / 1 | 2 / 0 | 2 / 0 |
| `busybox:1.36` (clean: Trivy writes no `Results`) | 0 / 0 | 0 / 0 | 0 / 0 |
| `gcr.io/distroless/static-debian12` | 0 / 0 | 0 / 0 | 0 / 0 |

Check it on one of your own images before you replace your gate.

## Step 3: Call the script from `.gitlab-ci.yml`

```yaml
trivy-scan:
  stage: test
  image: registry.example.com/ci/scanner:latest   # any image with trivy, curl, jq and gzip
  variables:
    SCANOPSIS_URL: https://scanopsis.example.com
    IMAGES: registry.example.com/team/app:$CI_COMMIT_SHORT_SHA
  id_tokens:
    SCANOPSIS_ID_TOKEN:
      aud: "$SCANOPSIS_URL"
  script:
    - sh ci/scan.sh
```

- `id_tokens` asks GitLab for a token in the variable `SCANOPSIS_ID_TOKEN`.
  `aud` must be the public URL of Scanopsis, written exactly as Scanopsis has it
  in `SCANOPSIS_PUBLIC_URL`.
- `IMAGES` is a space-separated list. Each image gets its own report, and each
  image name is one artifact in the dashboard.
- The scan job must run after the image is built and pushed, and must be allowed
  to pull it.

## Step 4: Run a pipeline and read the log

The first successful upload creates the service for your project in Scanopsis
(named after the last part of the project path) and records it in the audit log
as `service.created`, with the actor `ci:<project path>`. The log of the job
shows:

```console
$ sh ci/scan.sh
scanopsis: upload of scanopsis-out/alpine_3.17.0.json returned HTTP 201
{"scan_id":1,"service_id":1,"service_created":true,"replaced":false,"counts":{"critical":0,"high":26,"medium":40,"low":26,"unknown":0},"fixable":92,"secrets":0,"suppressed":0}
2026-11-19T12:22:49+06:00	WARN	[report] No enabled scanners found. Summary table will not be displayed.

alpine:3.17.0 (alpine 3.17.0)
=============================
Total: 26 (HIGH: 26, CRITICAL: 0)

┌────────────┬────────────────┬──────────┬────────┬───────────────────┬───────────────┬──────────────────────────────────────────────────────────────┐
│  Library   │ Vulnerability  │ Severity │ Status │ Installed Version │ Fixed Version │                            Title                             │
├────────────┼────────────────┼──────────┼────────┼───────────────────┼───────────────┼──────────────────────────────────────────────────────────────┤
│ libcrypto3 │ CVE-2022-3996  │ HIGH     │ fixed  │ 3.0.7-r0          │ 3.0.7-r2      │ openssl: double locking leads to denial of service           │
...  (table shortened)
scanopsis: upload of scanopsis-out/secrets.json returned HTTP 201
{"scan_id":2,"service_id":1,"service_created":false,"replaced":false,"counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"fixable":0,"secrets":2,"suppressed":0}
2026-11-19T12:22:49+06:00	WARN	[report] No enabled scanners found. Summary table will not be displayed.

config.env (secrets)
====================
Total: 2 (UNKNOWN: 0, LOW: 0, MEDIUM: 0, HIGH: 0, CRITICAL: 2)

CRITICAL: AWS (aws-access-key-id)
...  (shortened)
job exit code: 1
```

(`job exit code: 1` is printed by the test harness. In GitLab the job simply
fails because of the gate. This example image does have fixed HIGH findings.)

The `201` answer says how many findings of each severity were stored.
`fixable` counts findings that have a fixed version, `suppressed` the findings
your ignore file hid, and `replaced: true` means an earlier upload of the same
job, image and kind was replaced. Retrying a job therefore never duplicates
data.

The match text of a secret is never stored. Scanopsis keeps the rule and the
file and line only. (The raw report is kept compressed for audit, exactly as
Trivy wrote it, and Trivy masks the secret in it.)

## Retries and limits

A problem on the Scanopsis side is usually temporary: a restart, a database that
is not reachable yet, a burst of uploads. The script retries for about four
minutes, so a short outage does not lose a scan. curl decides what to retry:

| Answer | Cause | Retried |
| --- | --- | --- |
| `408` `request_timeout` | The body did not arrive within 5 minutes. | yes |
| `429` | Too many uploads from the address (`too_many_requests`) or from the project (`project_rate_limited`), or the job is over its artifact limit (`job_artifact_limit`). | yes, after the `Retry-After` seconds; not for a job over its limit, whose `Retry-After` is an hour, so curl gives up |
| `500`, `502`, `504` | An error in Scanopsis, or a proxy that could not reach it. | yes |
| `503` | Scanopsis is busy (`busy`), cannot reach its database (`unavailable`) or cannot reach GitLab (`gitlab_unavailable`); each carries `Retry-After` except the GitLab one. | yes |
| Connection refused, timeout | Scanopsis is down or restarting. | yes (`--retry-connrefused`) |
| `400`, `401`, `403`, `409`, `413`, `415`, `422` | The request itself is wrong. The same request gets the same answer. | no |

The script uses `--retry` and not `--retry-all-errors`, which would also repeat
the last row and delay the job. Retrying is safe: a repeated upload of the same
job, image and kind replaces the earlier one. curl 7.66 or later honours
`Retry-After`; 8 retries with curl's doubling wait (1 s, 2 s, 4 s, up to 128 s)
and `--retry-max-time 300` give the four minutes.

Two limits protect the database from one project. They apply to projects
that passed the namespace and protected-branch checks, and both are answered `429` with `Retry-After` and
recorded in the audit log like other rejections (at most 10 per client address at
once, then one more per minute):

- **Uploads per project.** A project may upload 100 reports at once and then
  one more every 10 seconds (360 an hour). The limit is per GitLab project, not
  per address, so it does not matter how many runners the project has. It is
  sized for 3,000 pipelines a day over 500 services, which is six pipelines a
  day for the average service: a monorepo that scans 30 images uploads 31 reports
  per pipeline, so three such pipelines can finish together, and 11 an hour can
  go on all day. A project above that gets `project_rate_limited`, and the
  retries of the script spread the rest out. Other projects are not affected.
- **Artifacts per job.** One job may store at most 50 different artifacts
  (the artifact name and the kind, on the job's branch or tag). Uploading again
  an artifact the job already has replaces it and does not count. The 51st gets
  `job_artifact_limit`. Scans of different artifacts are never replaced by
  later ones, so without this limit one job could create them without end. Split
  a job that scans more than 50 images into several jobs.

Scanopsis reads an upload for at most 5 minutes after it accepted it. The limit
is its own, longer than the 30 seconds of the rest of the server, so a large
report can cross a slow link. A body that is not complete by then is answered with
`408`. Uploads are admitted by the memory they need, which follows their size,
not by a fixed number: many small reports are processed at once, and a few
large ones. An upload that fits the free memory is admitted at once, even when a
larger one is waiting, until the oldest waiting upload has waited 5 seconds;
newer uploads then queue behind it, so large uploads are not starved. An upload
that finds no free memory waits up to 30 seconds and is then answered with `503`
`busy`. At most a quarter of the database connections store uploads at the same
time; an upload that cannot get a place within 30 seconds is answered `busy` too. See [sizing](../deployment.md#sizing).

## Restrict who may upload

> **Set the allowed namespaces on a shared or public GitLab instance.** With the
> list empty, every project of the GitLab instance can upload: a token is valid
> for Scanopsis whenever the instance that signed it is the configured one. On
> a shared or public instance (gitlab.com, for example) that means any project
> of any user on it. Scanopsis logs a warning ("ingest accepts uploads from
> every project of the GitLab instance; set ingest.allowed_namespaces to
> restrict it") at start-up, and whenever the settings change into that state.

Anyone who can run a job on your GitLab instance can get a token, and the token
is valid for Scanopsis, because it is signed by your GitLab. Two settings limit
which jobs are accepted. Set them in **Settings**, or with environment variables.

| Setting | Variable | Effect |
| --- | --- | --- |
| Allowed namespaces | `SCANOPSIS_INGEST_ALLOWED_NAMESPACES` | Comma-separated full group paths, for example `platform,security/appsec`. Only projects in these groups or below them may upload. Empty accepts every project. |
| Protected refs only | `SCANOPSIS_INGEST_PROTECTED_REFS_ONLY` | `true` accepts only pipelines on protected branches and tags, so a feature branch cannot add scans or replace results. |

A rejected upload is answered with 403 and recorded in the audit log as
`ingest.rejected`, with the reason and, when the token was valid, the project
path. Anyone who can get an ID token from your GitLab can cause a rejected upload
before the allowed namespaces and protected refs are checked (an invalid token,
GitLab unreachable, a namespace not allowed, a ref not protected). The audit log
records at most 10 of those rejections per client address at once and then one
more per minute; the others are answered the same way but appear only in the
server log at debug level. When allowed namespaces are set, every later
rejection (media type, kind, report too large or invalid, archived service,
service conflict, busy, request timeout) comes from a project you chose to accept
and is always recorded. Without allowed namespaces any project can get that far,
so these rejections are bounded per client address in the same way. The
rejections for the project limits (`project_rate_limited`, `job_artifact_limit`)
are always bounded per client address, because a project that keeps uploading
would otherwise fill the audit log. A rate-limited
request (`429`) is never recorded. The "Recorded rejections" number on the
Integrations page counts what was recorded.

## What the dashboard shows

The dashboard shows exactly what Trivy reported. Nothing is added, merged or
re-rated: the counts per severity are the unsuppressed findings of the report,
a suppressed finding is shown as suppressed with the reason Trivy gave, and
duplicates are kept as Trivy listed them (Trivy can report one CVE for several
targets). Every report is traceable to the pipeline, job, branch and commit in
the token, and the compressed original is kept.

Scanopsis keeps the scans of every branch that uploads. A dashboard shows one
branch, and its numbers are the latest scan of each artifact on that branch.
An administrator chooses the branch for each service on the Services page, see
[choose the dashboard branch](../operations.md#choose-the-dashboard-branch-of-a-service).
Until one is chosen, a dashboard shows the branch with the most recent scan,
so a scan of a feature branch changes what is shown. Choose the branch right
after the first upload, usually the default branch of the project.

## Troubleshooting

The log line `scanopsis: upload of ... returned HTTP <code>` is followed by the
answer of Scanopsis. HTTP `000` means curl got no answer at all (see the
error line above it: wrong URL, DNS, TLS or firewall).

| Log shows | Cause | Fix |
| --- | --- | --- |
| `503` `ingest_not_configured`: `Uploads are not enabled: the GitLab URL is not configured in Scanopsis.` | `SCANOPSIS_GITLAB_URL` is not set. | Set the GitLab URL in Settings (step 1). |
| `401` `invalid_token`: `The Authorization header is missing. Send Authorization: Bearer <ID token>.` | The request had no `Authorization` header. | Send `Authorization: Bearer $SCANOPSIS_ID_TOKEN`. |
| `401` `invalid_token`: `The Authorization header must be: Bearer <ID token>.` | The token variable is empty: the job has no `id_tokens` entry with this name. | Add `id_tokens: SCANOPSIS_ID_TOKEN:` to the job (step 3). |
| `401` `invalid_token`: `The token is malformed: it is not a signed JWT (RS256).` | The variable holds something other than the ID token. | Use the variable named under `id_tokens`, not a personal or job token. |
| `401` `invalid_token`: `The token audience (aud) does not contain https://scanopsis.example.com. ...` | `aud` is not the Scanopsis URL, or differs from `SCANOPSIS_PUBLIC_URL` in a character (`http` instead of `https`, a trailing slash). | Set `aud: "$SCANOPSIS_URL"` with the exact public URL. |
| `401` `invalid_token`: `The token issuer (iss) is not the GitLab URL configured in Scanopsis.` | The job runs on another GitLab than the one in Scanopsis settings. | Correct the GitLab URL in Settings, or upload from the right GitLab. |
| `401` `invalid_token`: `The token signature is not valid for the signing keys of the GitLab configured in Scanopsis.` | The token was changed, or comes from a different GitLab with the same URL setting. Scanopsis downloads the signing keys of GitLab with the first upload, and again at most every 10 seconds, and only for a token that names a key it does not have yet. After GitLab starts to sign with a new key, the first upload within 10 seconds of an earlier download of the keys can be refused for this reason. | Use the unmodified token of the current job. Run the job again if GitLab just rotated its keys. |
| `503` `gitlab_unavailable`: `The signature could not be checked: Scanopsis could not fetch the signing keys from GitLab. Try again later.` | Scanopsis cannot reach GitLab, or does not trust its certificate, or GitLab did not answer within 10 seconds. Uploads that need the keys at the same time share one request and are all answered when it ends, and a failed request is not repeated for 10 seconds. The response never names the GitLab address; the server log has it, with the cause, in the `upload rejected` warning (`gitlab_url`, `cause`). | Allow the network path, and set `SCANOPSIS_CA_FILE` for an internal CA. See [outgoing TLS connections](../configuration.md#outgoing-tls-connections). |
| `401` `invalid_token`: `The token has expired. ...` | The upload ran after the job timeout, or the runner clock is far off. | Upload from within the job; fix the clock. |
| `401` `invalid_token`: `The token is not valid yet (nbf is in the future). ...` | The clock of the Scanopsis host is more than 10 seconds behind GitLab. | Synchronise the clocks (NTP). |
| `401` `invalid_token`: `The token was issued in the future (iat is ahead of the clock). ...` | Same cause: the token's issue time is more than 10 seconds ahead of the clock of Scanopsis. | Synchronise the clocks (NTP). |
| `401` `invalid_token`: `The token has no job_id claim.` (or another claim) | The token is not a GitLab CI/CD ID token. | Use a token from `id_tokens`. |
| `403` `namespace_not_allowed`: `The project namespace platform is not in the allowed namespaces of this Scanopsis.` | The project is outside the allowed groups. | Add the group to the allowed namespaces, or leave the list empty. |
| `403` `ref_not_protected`: `Only protected branches and tags may upload, and feature/login is not protected.` | Protected refs only is on and the pipeline runs on an unprotected ref. | Protect the branch or tag, or turn the setting off. |
| `415` `unsupported_media_type`: `Content-Type must be application/json.` | The `Content-Type` header is missing or different. | Send `-H 'Content-Type: application/json'`. |
| `415` `unsupported_media_type`: `Content-Encoding must be gzip or identity.` | Another compression was used. | Use gzip, or send the plain JSON without `Content-Encoding`. |
| `400` `invalid_report`: `The body is not valid JSON: ...` | The file is cut off or is not JSON, for example Trivy failed and wrote nothing. | Check the Trivy step. The script stops on a failed scan. |
| `400` `invalid_report`: `The body is not a complete, valid gzip stream.` | `Content-Encoding: gzip` was sent with data that is not gzip, or the file was cut off. | Compress with `gzip -c`, or drop the header. |
| `400` `invalid_parameter`: `kind must be image or secret-fs.` | The `kind` parameter has another value. | Use `kind=image` for `trivy image` and `kind=secret-fs` for the secret scan. |
| `422` `not_a_trivy_report`: `... SchemaVersion is missing` or `SchemaVersion 1 is not supported, only 2 is` | The file is not a Trivy JSON report, or has an unsupported schema version. | Create it with `--format json` from Trivy 0.50 or later. |
| `413` `report_too_large`: `The report is larger than the limit of ... bytes (decompressed).` | The decompressed report is above the limit (64 MiB by default). | Scan fewer targets per report, or ask the administrator to raise the limit. |
| `413` `report_too_large`: `The report has too many findings: ...` | The report holds more than 200000 vulnerabilities and secrets. | Split the scan, for example one report per image. |
| `503` `busy`: `The server is processing other uploads. Retry later.` and a `Retry-After` header | Uploads are admitted by the memory they need, and none was free within 30 seconds (or the request ended before). | The script retries. Otherwise run the upload again after the number of seconds in `Retry-After`, or spread the uploads of many jobs over time. |
| `503` `unavailable`: `The database is not available, so the report was not stored. Retry later.` and a `Retry-After` header | Scanopsis cannot reach its PostgreSQL database, or the database cannot take the work now. The report was not stored. | The script retries. If it keeps happening, check the database and the log of Scanopsis (`cannot store scan: the database is not available`). |
| `408` `request_timeout`: `The body did not arrive in time. Upload it faster, or retry.` | The whole body did not arrive within 5 minutes. | The script retries. Check the link between the runner and Scanopsis, or the size of the report. |
| `409` `service_archived`: `The service for this project is archived. ...` | An administrator archived the service of this project. Archived services receive no data. | Ask an administrator to unarchive it. |
| `409` `service_conflict`: `A different service already uses the project path ...` | Another service has this project path, for example from a deleted project with the same path. | Archive or delete the old service in Scanopsis. |
| `429` `too_many_requests`: `Too many uploads from this address. Retry later.` and a `Retry-After` header | More than the allowed number of uploads per second from one address. | Upload fewer reports at once, or wait the number of seconds in `Retry-After`. |
| `429` `project_rate_limited`: `This project uploaded too often. Retry later.` and a `Retry-After` header | The project used its burst of 100 uploads and uploads faster than one every 10 seconds. | The script retries. Upload fewer reports, or wait the number of seconds in `Retry-After`. |
| `429` `job_artifact_limit`: `This job already uploaded 50 different artifacts, ...` and `Retry-After: 3600` | One job tried to store more than 50 different artifacts (artifact name and kind). | Scan fewer images in one job, or split them over several jobs. Uploading an artifact again replaces it and does not count. |

If the answer is `500` (`internal_error`), the upload was not stored and the
cause is in the log of Scanopsis. The script retries it; if the retries fail too,
run the job again.
