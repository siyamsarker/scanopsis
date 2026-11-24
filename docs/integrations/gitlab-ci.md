# Upload Trivy reports from GitLab CI

Make your GitLab CI jobs send their Trivy reports to Scanopsis. The dashboard
then shows every finding of every image and repository, traced to the pipeline,
job and commit that found it.

**Audience:** Operators · **Back to:** [README](../../README.md) · [All guides](../../README.md#documentation)

---

## How it works

Each job proves who it is with a GitLab CI/CD **ID token**. GitLab creates this
short-lived token for the job and signs it. Scanopsis checks the signature
against the public keys of your GitLab. So there is no shared upload secret to
create, store or leak, and the token tells Scanopsis the project, pipeline, job,
branch and commit. A token is valid only for the timeout of its job.

The upload never changes the result of a job. If Scanopsis is down, a pipeline
that passed still passes.

The examples below use `scanopsis.example.com` and `gitlab.example.com`. Replace
them with your own addresses.

## Before you start

You need:

- A running Scanopsis. See the [getting started guide](../getting-started.md).
- GitLab 15.7 or later. Older versions do not have the `id_tokens` keyword.
- Trivy 0.50 or later on the runner. Older versions do not have
  `--show-suppressed`.
- `curl`, `jq` and `gzip` on the runner. With curl 7.71 or later the upload
  also survives a Scanopsis that is down, see [Retries and
  limits](#retries-and-limits).
- A network path from the runner to Scanopsis, and from Scanopsis to GitLab.
  Scanopsis downloads the signing keys from GitLab.

To check the tools, run this on the runner:

```sh
trivy --version | head -1
jq --version
curl --version | head -1
gzip --version | head -1
```

Each command prints a version. If one says `command not found`, install that
tool in the job image.

## Step 1. Tell Scanopsis where your GitLab is

Scanopsis needs the base URL of your GitLab to check the tokens. As an
administrator, open **Settings**, find the section **GitLab** and set
**GitLab URL** (`gitlab.url`), for example `https://gitlab.example.com`. You can also seed it at
the first start with the environment variable `SCANOPSIS_GITLAB_URL`.

Until it is set, every upload gets `503 ingest_not_configured`.

The token must also name Scanopsis as its audience (`aud`). The audience is the
public URL of Scanopsis, `SCANOPSIS_PUBLIC_URL`, for example
`https://scanopsis.example.com`. You set it in the job in step 3.

## Step 2. Add the scan script to your repository

Save this file as `ci/scan.sh` in your repository. It scans each image once,
with every severity and with unfixed findings. It uploads that full report, then
decides from the same report whether the job fails.

> **The upload scan must always ask for everything.** The script passes
> `--severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL` and does not ignore unfixed
> findings, on purpose. Many GitLab projects set the job variables
> `TRIVY_SEVERITY` (for example `HIGH,CRITICAL`) or `TRIVY_IGNORE_UNFIXED=true`
> for their gate. Trivy reads them as defaults, so without the flags the report
> would silently lose its MEDIUM and LOW findings, and Scanopsis would show
> fewer findings than exist. A flag on the command line wins over the
> environment variable (checked with Trivy 0.72.0), so the script is safe
> whatever the job sets. The gate keeps its own filter, further down.

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

# curl 7.71 or later retries every failed connection; an older one only a
# refused connection.
retry=--retry-all-errors
curl --retry-all-errors --version >/dev/null 2>&1 || retry=--retry-connrefused

# upload <report.json> <image|secret-fs> [artifact]
# The optional third argument names the artifact, see "Keep two variants
# apart". Use letters, digits, - _ . only, or percent-encode the value.
# Prints what happened and always returns 0: a problem with the upload never
# changes the result of the job. curl retries for about four minutes (8
# retries, waiting 1 s, 2 s, 4 s and so on, or the Retry-After of the answer)
# when Scanopsis answers 408, 429, 500, 502, 503 or 504, and when it gets no
# answer at all (Scanopsis is down or restarting). It does not retry other
# answers: a 4xx such as 401, 409 or 413 stays the same.
upload() {
  gzip -c "$1" >"$1.gz"
  rm -f "$out/response.json"
  url="$SCANOPSIS_URL/api/v1/ingest/trivy?kind=$2"
  [ -n "${3:-}" ] && url="$url&artifact=$3"
  code=$(curl -sS --max-time 120 --retry 8 "$retry" --retry-max-time 300 -X POST \
    -H "Authorization: Bearer ${SCANOPSIS_ID_TOKEN:-}" \
    -H 'Content-Type: application/json' \
    -H 'Content-Encoding: gzip' \
    --data-binary @"$1.gz" \
    -o "$out/response.json" -w '%{http_code}' \
    "$url") || code=000
  echo "scanopsis: upload of $1 returned HTTP $code"
  [ -s "$out/response.json" ] && cat "$out/response.json"
  return 0
}

for image in $IMAGES; do
  name=$(printf '%s' "$image" | tr '/:@' '___')

  # One full scan: all severities, unfixed findings included, ignore file
  # applied, secrets in the image layers included. The flags win over any
  # TRIVY_SEVERITY or TRIVY_IGNORE_UNFIXED set in the job.
  if ! trivy image --quiet --scanners vuln,secret --format json --show-suppressed \
      --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL --ignore-unfixed=false \
      --output "$out/$name.json" "$image"; then
    echo "trivy could not scan $image"
    status=1
    continue
  fi

  upload "$out/$name.json" image

  # The gate, built from the same JSON: drop the secrets (the gate has always
  # looked at vulnerabilities only) and the findings without a fix (this is
  # what --ignore-unfixed does), then fail on HIGH and CRITICAL.
  jq 'if .Results then .Results |= map(del(.Secrets) | if .Vulnerabilities then .Vulnerabilities |= map(select(.Status == "fixed")) else . end) else . end' \
    "$out/$name.json" >"$out/$name.gate.json"
  trivy convert --format table --severity HIGH,CRITICAL --exit-code 1 "$out/$name.gate.json" || status=1
done

# Secrets are a separate scan without a severity filter.
trivy fs --quiet --scanners secret --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
  --skip-dirs "$out" --format json --output "$out/secrets.json" . || status=1
upload "$out/secrets.json" secret-fs
trivy convert --format table --exit-code 1 "$out/secrets.json" || status=1

exit "$status"
```

### What the script does

1. `trivy image ... --format json --show-suppressed` writes one complete report
   per image: all severities (`--severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL`),
   fixed and unfixed findings (`--ignore-unfixed=false`), the secrets that sit
   in the image layers (`--scanners vuln,secret`), and the findings that
   your ignore file (`.trivyignore`) or VEX documents suppressed. Scanopsis
   shows those as suppressed.
2. `upload` compresses the report, sends it, and prints the HTTP status and the
   answer. It retries a temporary failure, see [Retries and
   limits](#retries-and-limits). It always returns 0, so a failed upload never
   fails the job.
3. `jq` removes the secrets and every vulnerability whose `Status` is not
   `fixed`. `trivy convert` then prints the HIGH and CRITICAL findings that are left, and exits
   with 1 if there are any. This is your security gate, built from the report
   you uploaded. The gate looks at vulnerabilities only. To fail the job on
   secrets in the image layers too, add the secret gate of the [central
   script](#a-central-scan-script-with-separate-jobs) after the gate line:

   ```sh
   jq 'del(.Results[]?.Vulnerabilities)' "$out/$name.json" >"$out/$name.secret.json"
   trivy convert --format table --exit-code 1 "$out/$name.secret.json" || status=1
   ```
4. The secret scan of the repository runs on its own, because secrets have no
   fix status. Its report has every severity, whatever the job sets. The gate
   that follows is `trivy convert` without `--severity`, so a `TRIVY_SEVERITY`
   in the job decides which secrets fail it, and without that variable every
   secret does. It uploads with `kind=secret-fs`. Scanning `.` and scanning `$CI_PROJECT_DIR` give the same
   artifact in Scanopsis, see [How Scanopsis tells artifacts
   apart](#how-scanopsis-tells-artifacts-apart).
5. The last line, `exit "$status"`, makes the gates and the scans decide the job
   result, never the upload.

`trivy convert` prints `No enabled scanners found. Summary table will not be
displayed.` before the table. That is expected.

### Check the gate before you replace yours

Trivy's `--ignore-unfixed` hides every vulnerability whose status is not
`fixed`. The `jq` step does the same on the JSON, so the new gate fails exactly
when the old one did:

```text
old: trivy image --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 IMAGE
new: trivy image --format json --show-suppressed IMAGE, then jq (Status == "fixed"),
     then trivy convert --format table --severity HIGH,CRITICAL --exit-code 1
```

Both were run on real images with Trivy 0.76.2:

| Image | Exit code, old / new | HIGH / CRITICAL, old | HIGH / CRITICAL, new |
| --- | --- | --- | --- |
| `python:3.9-slim-buster` | 1 / 1 | 18 / 0 | 18 / 0 |
| `python:3.9-slim-buster` with an ignore file of 3 CVEs | 1 / 1 | 17 / 0 | 17 / 0 |
| `alpine:3.17.0` | 1 / 1 | 26 / 0 | 26 / 0 |
| `node:16-alpine` | 1 / 1 | 40 / 1 | 40 / 1 |
| `alpine:3.20` | 1 / 1 | 2 / 0 | 2 / 0 |
| `busybox:1.36` (clean, so Trivy writes no `Results`) | 0 / 0 | 0 / 0 | 0 / 0 |
| `gcr.io/distroless/static-debian12` | 0 / 0 | 0 / 0 | 0 / 0 |

The numbers change as the vulnerability database grows. Run both gates on one of
your own images before you switch.

## Step 3. Call the script from `.gitlab-ci.yml`

Add a job like this one (example):

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

- `id_tokens` asks GitLab for a token in the variable `SCANOPSIS_ID_TOKEN`. Its
  `aud` must be the public URL of Scanopsis, written exactly as in
  `SCANOPSIS_PUBLIC_URL`: same scheme, no trailing slash.
- `IMAGES` is a space-separated list. Each image gets its own report and becomes
  one artifact on the dashboard. A tag that changes with every run, such as
  `$CI_PIPELINE_IID` or `$CI_COMMIT_SHORT_SHA`, is fine: the artifact is the
  image name without its tag, so the newest scan replaces the older ones.
- A runner that uses the shell executor ignores `image:`. The job then runs
  with the tools installed on the runner itself, so check `trivy`, `jq`, `curl`
  and `gzip` there.
- Run the job after the image is built and pushed, with access to pull it.

## Step 4. Run a pipeline and read the log

The first successful upload creates the service for your project. The service is
named after the last part of the project path, and the audit log records it as
`service.created` by the actor `ci:<project path>`.

The job log looks like this (the table is shortened):

```console
$ sh ci/scan.sh
scanopsis: upload of scanopsis-out/alpine_3.17.0.json returned HTTP 201
{"scan_id":1,"service_id":1,"service_created":true,"replaced":false,"counts":{"critical":0,"high":26,"medium":40,"low":26,"unknown":0},"fixable":92,"secrets":0,"suppressed":0}
2026-11-22T17:14:37+06:00  WARN  [report] No enabled scanners found. Summary table will not be displayed.

alpine:3.17.0 (alpine 3.17.0)
=============================
Total: 26 (HIGH: 26, CRITICAL: 0)
...
scanopsis: upload of scanopsis-out/secrets.json returned HTTP 201
{"scan_id":2,"service_id":1,"service_created":false,"replaced":false,"counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"fixable":0,"secrets":2,"suppressed":0}
2026-11-22T17:14:38+06:00  WARN  [report] No enabled scanners found. Summary table will not be displayed.

config.env (secrets)
====================
Total: 2 (UNKNOWN: 0, LOW: 0, MEDIUM: 0, HIGH: 0, CRITICAL: 2)
...
```

This job fails, because the image has fixed HIGH findings and the repository has
secrets. Both uploads still worked.

How to read the `201` answer:

| Field | Meaning |
| --- | --- |
| `counts` | The stored findings per severity, without the suppressed ones. |
| `fixable` | Findings that have a fixed version. |
| `secrets` | Secret findings. |
| `suppressed` | Findings your ignore file or VEX documents hid. |
| `service_created` | `true` when this upload created the service. |
| `replaced` | `true` when an earlier upload of the same job, reported name and kind was replaced. Retrying a job never duplicates data. |

## How Scanopsis tells artifacts apart

Scanopsis keeps one current scan per artifact. A new upload replaces the older
scan of the same artifact, and the new and fixed findings are counted against
it. The artifact is named by its **key**:

| What you scan | The key | Examples |
| --- | --- | --- |
| An image | The image name without its tag and digest. The registry, the port and the letter case stay as Trivy reports them. | `registry.example.com/team/web-frontend:4` and `registry.example.com/team/web-frontend:5` are one artifact, `registry.example.com/team/web-frontend`. `registry.example.com:5000/team/app:1` becomes `registry.example.com:5000/team/app`. |
| A folder (`kind=secret-fs`) | `.` for the project folder, otherwise the path inside it. A path in the runner's build folder is read relative to your repository, using the project path of the token. | `.`, `/builds/demo/web-frontend` and `$CI_PROJECT_DIR` are all `.`. `/builds/demo/web-frontend/services/api` is `services/api`. Any other name stays as it is. |

A relative folder is a key as written: `./services/api`, `services/api/` and
`services/api` are three different keys. Use the same spelling in every run, or
scan `.`.

So per-run tags such as `$CI_PIPELINE_IID` or `$CI_COMMIT_SHORT_SHA` do not
create a new image on the dashboard each time. The dashboard shows the key and,
as a detail, the latest name with its tag, for example `Latest:
web-frontend:4`. A link to the findings page that names an image with its tag
still works.

### Keep two variants apart

Two images whose names differ only by the tag are one artifact, so the newer
scan replaces the older one as the current state. That is what you want for
`web-frontend:4` and `web-frontend:5`. It is not what you want for two builds
of one release, such as `registry.example.com/team/web-frontend:4-amd64` and
`registry.example.com/team/web-frontend:4-arm64`, which you scan in the same
job. Give each upload its own name with the optional `artifact` parameter. In
the loop of step 2, the file name is the image name with `/`, `:` and `@`
replaced by `_`:

```sh
upload "$out/registry.example.com_team_web-frontend_4-amd64.json" image web-frontend-amd64
upload "$out/registry.example.com_team_web-frontend_4-arm64.json" image web-frontend-arm64
```

Variants with exactly the same image name cannot be kept apart inside one job,
because a job's second upload of the same reported name replaces its first.
Scan them in separate jobs.

This uses the optional third argument of `upload` from step 2. The value
becomes the key. Rules: 1 to 200 printable characters, no control characters,
no spaces other than the plain space, and not only spaces. Use letters, digits,
`-`, `_` and `.` in a URL as it is, and percent-encode anything else. A value
that breaks a rule is answered with `400` `invalid_parameter`.

A retried upload replaces the earlier upload of the same job, reported name and
kind, whatever the `artifact` value.

## Step 5. Choose the dashboard branch

Scanopsis keeps the scans of every branch that uploads, but a dashboard shows
one branch. Until an administrator chooses that branch, the dashboard follows
the most recent scan, so a scan of a feature branch changes what everyone sees.

Right after the first upload, an administrator opens **Services**, finds the new
service and selects its branch tag to choose the dashboard branch, usually your
default branch.
[Operations](../operations.md) describes the Services page.

If your pipelines scan only on a deployment branch, for example `production`
or `release`, choose that branch. A dashboard that follows the most recent scan
works too, but a fixed branch keeps the numbers from jumping when another
branch uploads.

## A central scan script with separate jobs

Many teams keep one scan script for all projects, for example in
`/opt/platform-trivy/scan.sh` on the runner, with one job that scans the image
and another that scans the source for secrets. You can keep that shape. Change
the script so that it:

- writes one JSON report per image and per folder, with the full set of flags
  from step 2, so job variables cannot cut the report;
- uploads each report, with `kind=image` or `kind=secret-fs`;
- decides its exit code from the same report, as before, so that a failed
  upload never changes whether the job passes.

This example does all three. It builds two gates from the one full report: the
vulnerability gate fails on HIGH and CRITICAL vulnerabilities, unfixed ones
included, and the secret gate fails on any secret in the image layers. It exits
with 0 when the job passes and 1 when a gate fails or Trivy cannot scan (2
means wrong use). The gates give the same result as a plain `trivy image` scan
with the job's variables, as long as gate 1's `--severity` equals the flag of
your old script (if the old script used `TRIVY_SEVERITY` instead, drop
`--severity` from gate 1, as gate 2 has none): `trivy convert` does not read `TRIVY_IGNORE_UNFIXED`,
so the script applies the `select(.Status == "fixed")` filter itself when the
variable is `true`, and `TRIVY_SEVERITY` reaches the secret gate. Keep the `jq`
filters equal to the flags your old script used: if it passed
`--ignore-unfixed` as a flag, not as a variable, add that filter without the
`case`; if it did not scan secrets, drop the secret gate. A `.trivyignore` in the folder the job runs in
applies to the gates as it did to the scan.

```sh
#!/bin/sh
# /opt/platform-trivy/scan.sh: the shared scan script of the platform team.
#
#   scan.sh image <image>      scan one image, upload it, apply both gates
#   scan.sh source [folder]    scan a folder (default: .) for secrets
#
# Needs: trivy, curl, jq, gzip. Variables from the job: SCANOPSIS_URL and
# SCANOPSIS_ID_TOKEN (from id_tokens).
# Exit codes: 0 passed, 1 a gate failed or Trivy could not scan, 2 wrong use.
# A problem with the upload never changes the exit code.
set -u
out=${SCANOPSIS_OUT:-scanopsis-out}
mkdir -p "$out"

# curl 7.71 or later retries every failed connection; an older one only a
# refused connection.
retry=--retry-all-errors
curl --retry-all-errors --version >/dev/null 2>&1 || retry=--retry-connrefused

# upload <report.json> <image|secret-fs>: prints what happened, always returns 0.
upload() {
  gzip -c "$1" >"$1.gz"
  rm -f "$out/response.json"
  code=$(curl -sS --max-time 120 --retry 8 "$retry" --retry-max-time 300 -X POST \
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

case "${1:-}" in
image)
  if [ -z "${2:-}" ]; then
    echo "usage: scan.sh image <image> | scan.sh source [folder]" >&2
    exit 2
  fi
  image=$2
  name=$(printf '%s' "$image" | tr '/:@' '___')
  status=0
  # Put your own checks here, for example the age of the vulnerability
  # database, before the scan.

  # One full scan for Scanopsis: every severity, unfixed findings, secrets in
  # the image layers. The flags win over TRIVY_SEVERITY and
  # TRIVY_IGNORE_UNFIXED set in the job.
  trivy image --quiet --scanners vuln,secret --format json --show-suppressed \
    --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL --ignore-unfixed=false \
    --output "$out/$name.json" "$image" || { echo "trivy could not scan $image"; exit 1; }
  upload "$out/$name.json" image

  # Gate 1, vulnerabilities: fail on HIGH and CRITICAL, unfixed ones included.
  # Keep --severity equal to the flag of your old script. If the old script set
  # TRIVY_SEVERITY instead of the flag, drop --severity so the variable applies.
  # trivy convert does not read TRIVY_IGNORE_UNFIXED, which trivy image does,
  # so a job that sets it to true gets the same effect from the jq filter.
  # A .trivyignore in the current folder is applied by trivy convert too.
  filter='del(.Results[]?.Secrets)'
  case "${TRIVY_IGNORE_UNFIXED:-}" in
  1 | t | T | true | TRUE | True)
    filter="$filter | (.Results[]? | select(.Vulnerabilities) | .Vulnerabilities) |= map(select(.Status == \"fixed\"))"
    ;;
  esac
  jq "$filter" "$out/$name.json" >"$out/$name.vuln.json"
  trivy convert --format table --severity HIGH,CRITICAL --exit-code 1 "$out/$name.vuln.json" || status=1
  # Gate 2, secrets in the image layers: fail on any secret. No --severity
  # here, so a TRIVY_SEVERITY of the job applies, as it would to a plain
  # secret scan with trivy image.
  jq 'del(.Results[]?.Vulnerabilities)' "$out/$name.json" >"$out/$name.secret.json"
  trivy convert --format table --exit-code 1 "$out/$name.secret.json" || status=1
  exit "$status"
  ;;
source)
  dir=${2:-.}
  trivy fs --quiet --scanners secret --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
    --skip-dirs "$out" --format json --output "$out/secrets.json" "$dir" || { echo "trivy could not scan $dir"; exit 1; }
  upload "$out/secrets.json" secret-fs
  trivy convert --format table --exit-code 1 "$out/secrets.json"
  ;;
*)
  echo "usage: scan.sh image <image> | scan.sh source [folder]" >&2
  exit 2
  ;;
esac
```

Make it executable on the runner (`chmod 755 /opt/platform-trivy/scan.sh`).
Then call it from two jobs. They run on a runner with the shell executor, so
they use the tools installed on the runner and ignore `image:`:

```yaml
image-scan:
  stage: test
  variables:
    SCANOPSIS_URL: https://scanopsis.example.com
    IMAGE: registry.example.com/team/web-frontend:$CI_PIPELINE_IID
  id_tokens:
    SCANOPSIS_ID_TOKEN:
      aud: "$SCANOPSIS_URL"
  script:
    - /opt/platform-trivy/scan.sh image "$IMAGE"

source-scan:
  stage: test
  variables:
    SCANOPSIS_URL: https://scanopsis.example.com
  id_tokens:
    SCANOPSIS_ID_TOKEN:
      aud: "$SCANOPSIS_URL"
  script:
    - /opt/platform-trivy/scan.sh source "$CI_PROJECT_DIR"
```

Both jobs may set `TRIVY_SEVERITY` or `TRIVY_IGNORE_UNFIXED`. The script's flags
win for the report. Check that the new script ends with the same exit code as
your old one on a few images of your own: a clean one, one with HIGH findings
and one with a secret. Scanning `.` and scanning `$CI_PROJECT_DIR` give the same
artifact, `.`.

## Restrict who may upload

> **On a shared or public GitLab, such as gitlab.com, set the allowed
> namespaces.** With an empty list, any project on that GitLab can upload,
> because every token it signs is valid for Scanopsis. Scanopsis logs this
> warning at start-up and whenever the settings change into that state: `ingest
> accepts uploads from every project of the GitLab instance; set
> ingest.allowed_namespaces to restrict it`.

Two settings, in the **Scan uploads** group on the Settings page, limit which
jobs are accepted:

| Setting | Environment variable | Effect |
| --- | --- | --- |
| `ingest.allowed_namespaces` | `SCANOPSIS_INGEST_ALLOWED_NAMESPACES` | Full group paths, one per line on the Settings page or comma-separated in the variable, for example `platform` and `security/appsec`. Only projects in these groups, or in groups below them, may upload. Case does not matter. Empty accepts every project. |
| `ingest.protected_refs_only` | `SCANOPSIS_INGEST_PROTECTED_REFS_ONLY` | `true` accepts only pipelines on protected branches and tags, so a feature branch cannot add or replace results. |

A refused upload is written to the audit log as `ingest.rejected`, with the
reason and, when the token was valid, the project path. Anyone who can run a job
on your GitLab can cause a refusal, so Scanopsis keeps them from flooding the
log:

- Refusals before a project has passed all checks (bad token, GitLab
  unreachable, namespace not allowed, ref not protected) are recorded at most 10
  at once per client address, then one per minute. The rest are answered the
  same way and appear only in the server log at `debug` level.
- Once you set allowed namespaces, later refusals (wrong media type, wrong kind,
  report too large or invalid, archived service, service conflict, timeout) come
  from projects you chose to accept, and every one is recorded. Without allowed
  namespaces they are limited per address like the first group.
- `project_rate_limited`, `job_artifact_limit` and `busy` are always limited per
  address, because they happen under load. `busy` has its own limit, so a busy
  period never hides refused tokens.
- `too_many_requests` is never recorded.

The **Integrations** page counts what was recorded as "Refused, 24 h", charts
refused uploads per day, and lists the 20 most recent with the service, if one
is registered for the project path, and the reason.

## Retries and limits

Most problems on the Scanopsis side are short: a restart, a database that is not
ready yet, a burst of uploads. The script retries for about four minutes, so a
short outage does not lose a scan. curl decides what to retry:

| Answer | Cause | Retried |
| --- | --- | --- |
| `408` `request_timeout` | The body did not arrive within 5 minutes. | Yes |
| `429` | Too many uploads from one address (`too_many_requests`) or from one project (`project_rate_limited`), or the job is over its artifact limit (`job_artifact_limit`). | Yes, after `Retry-After`. For `job_artifact_limit`, `Retry-After` is an hour, so curl gives up. |
| `500`, `502`, `504` | An error in Scanopsis, or a proxy that could not reach it. | Yes |
| `503` | Scanopsis is busy (`busy`), restoring a backup (`restoring`), cannot reach its database (`unavailable`) or cannot reach GitLab (`gitlab_unavailable`). All but the GitLab one carry `Retry-After`. A short restore ends within the retry time; during a long one the scan is not kept. | Yes |
| No answer: connection refused, network unreachable, timeout | Scanopsis is down or restarting. | Yes (`--retry-all-errors`; with curl older than 7.71, only a refused connection) |
| `400`, `401`, `403`, `409`, `413`, `415`, `422` | The request itself is wrong. The same request gets the same answer. | No |

A retry is safe: the same job, artifact and kind replaces the earlier upload.
curl 7.66 or later honours `Retry-After`. Eight retries with curl's doubling
wait (1 s, 2 s, 4 s, up to 128 s) and `--retry-max-time 300` give the four
minutes.

The script uses `--retry-all-errors` (curl 7.71 or later) because a stopped
Scanopsis does not always look like a refused connection. When its name has an
IPv6 address that the runner cannot reach and an IPv4 address that refuses,
curl reports the last error, "Network is unreachable", and
`--retry-connrefused` gives up at once: the scan is lost. `--retry-all-errors`
repeats every upload that got no answer. It does not repeat an answer such as
`401` or `409`, because the script does not pass `--fail`. With an older curl
the script falls back to `--retry-connrefused`; `curl --version | head -1`
shows yours.

Every upload that gets no answer waits the full four minutes before it
prints `HTTP 000`: also one with a wrong URL, an unknown host name or a
certificate the runner does not trust. Check the URL with one job before you
roll the script out.

These limits protect Scanopsis and its database. You cannot change them:

| Limit | Value | Answer when exceeded |
| --- | --- | --- |
| Uploads per client address | 60 at once, then 1 per second | `429` `too_many_requests` |
| Uploads per GitLab project | 100 at once, then 1 every 10 seconds (360 an hour), however many runners the project uses | `429` `project_rate_limited` |
| Artifacts per job | 50 different artifacts (artifact name and kind). Uploading an artifact again replaces it and does not count. | `429` `job_artifact_limit` |
| Report size | 64 MiB after decompression | `413` `report_too_large` |
| Findings per report | 200,000 vulnerabilities and secrets, suppressed ones included | `413` `report_too_large` |
| Time to send the body | 5 minutes from the moment the upload is admitted | `408` `request_timeout` |
| Wait for memory or a database slot | 30 seconds | `503` `busy` |

The project limit fits a busy monorepo: a pipeline that scans 30 images uploads
31 reports, so three such pipelines can finish at the same moment, and 11 an
hour can run all day. It applies only to projects that passed the namespace and
protected-ref checks.

The artifact limit exists because scans of different artifacts never replace
each other. Without it, one job could create scans without end. Split a job that
scans more than 50 images into several jobs.

Uploads are admitted by the memory they need, which grows with their size. Many
small reports run at once, large ones fewer at a time. A report of up to 4 MiB,
which is most of them, can always pass a large upload that is waiting. At most a
quarter of the database connections store uploads at the same time. For sizing
the server, see [deployment](../deployment.md).

## What the dashboard shows

The dashboard shows exactly what Trivy reported. Nothing is added, merged or
re-rated:

- The counts per severity are the unsuppressed findings of the report.
- A suppressed finding is shown as suppressed, with the reason Trivy gave.
- Duplicates stay as Trivy listed them. Trivy can report one CVE for several
  targets.
- The matched text of a secret is never stored. Scanopsis keeps the rule, the
  file and the line numbers.
- Every scan links to the pipeline, job, branch and commit from the token.

Scanopsis also keeps the report as uploaded, compressed. Trivy masks secrets in
it. An administrator can export it for 30 days after the scan, and for as long
as the scan is current. [Operations](../operations.md) has the command.

## Troubleshooting

Each upload prints `scanopsis: upload of ... returned HTTP <code>`, followed by
the answer of Scanopsis. HTTP `000` means curl got no answer at all, even after
about four minutes of retries. The line above it says why: Scanopsis is down, a
wrong URL, DNS, TLS or a firewall.

If the answer is `500` `internal_error`, the upload was not stored and the cause
is in the Scanopsis log. The script retries it. If the retries fail too, run the
job again.

| Answer | Cause | Fix |
| --- | --- | --- |
| `503` `ingest_not_configured`: `Uploads are not enabled: the GitLab URL is not configured in Scanopsis.` | `gitlab.url` is not set. | Set it, see [step 1](#step-1-tell-scanopsis-where-your-gitlab-is). |
| `401` `invalid_token`: `The Authorization header is missing. Send Authorization: Bearer <ID token>.` | The request had no `Authorization` header. | Send `Authorization: Bearer $SCANOPSIS_ID_TOKEN`. |
| `401` `invalid_token`: `The Authorization header must be: Bearer <ID token>.` | The token variable is empty: the job has no `id_tokens` entry with that name. | Add `id_tokens: SCANOPSIS_ID_TOKEN:` to the job, see [step 3](#step-3-call-the-script-from-gitlab-ciyml). |
| `401` `invalid_token`: `The token is malformed: it is not a signed JWT (RS256).` | The variable holds something other than an ID token. | Use the variable named under `id_tokens`, not a personal or job token. |
| `401` `invalid_token`: `The token audience (aud) does not contain https://scanopsis.example.com. Set aud to the Scanopsis URL in id_tokens.` | `aud` differs from `SCANOPSIS_PUBLIC_URL`, even by one character: `http` instead of `https`, or a trailing slash. | Set `aud: "$SCANOPSIS_URL"` with the exact public URL. |
| `401` `invalid_token`: `The token issuer (iss) is not the GitLab URL configured in Scanopsis.` | The job runs on a different GitLab than the one in the settings. | Correct `gitlab.url`, or upload from the right GitLab. |
| `401` `invalid_token`: `The token signature is not valid for the signing keys of the GitLab configured in Scanopsis.` | The token was changed, or GitLab has just started to sign with a new key. Scanopsis fetches the keys again only for a key it does not know, and at most every 10 seconds. | Use the unchanged token of the current job. If GitLab just rotated its keys, run the job again. |
| `503` `gitlab_unavailable`: `The signature could not be checked: Scanopsis could not fetch the signing keys from GitLab. Try again later.` | Scanopsis cannot reach GitLab, does not trust its certificate, or GitLab did not answer within 10 seconds. After a failure Scanopsis waits 10 seconds before it asks again. The answer never names the GitLab address. The server log does, in the `upload rejected` warning (`gitlab_url`, `cause`). | Open the network path. For an internal certificate authority, set `SCANOPSIS_CA_FILE`, see [configuration](../configuration.md). |
| `401` `invalid_token`: `The token has expired. ID tokens are valid for the job's timeout; upload from within the job.` | The upload ran after the job timeout, or a clock is far off. | Upload from inside the job. Check the clocks. |
| `401` `invalid_token`: `The token is not valid yet (nbf is in the future). ...` or `The token was issued in the future (iat is ahead of the clock). ...` | The clock of the Scanopsis host is more than 10 seconds behind GitLab. | Synchronise the clocks with NTP. |
| `401` `invalid_token`: `The token has no job_id claim.` (or another claim) | The token is not a GitLab CI/CD ID token. | Use a token from `id_tokens`. |
| `403` `namespace_not_allowed`: `The project namespace platform is not in the allowed namespaces of this Scanopsis.` | The project is outside the allowed groups. | Add its group to `ingest.allowed_namespaces`, or leave the list empty. |
| `403` `ref_not_protected`: `Only protected branches and tags may upload, and feature/login is not protected.` | `ingest.protected_refs_only` is on and the pipeline runs on an unprotected branch or tag. | Protect the branch or tag, or turn the setting off. |
| `415` `unsupported_media_type`: `Content-Type must be application/json.` | The `Content-Type` header is missing or different. | Send `-H 'Content-Type: application/json'`. |
| `415` `unsupported_media_type`: `Content-Encoding must be gzip or identity.` | Another compression was used. | Use gzip, or send plain JSON without `Content-Encoding`. |
| `400` `invalid_report`: `The body is not valid JSON: ...` | The file is cut off or is not JSON, for example because Trivy failed and wrote nothing. | Check the Trivy step. The script skips an image that Trivy could not scan. |
| `400` `invalid_report`: `The body is not a complete, valid gzip stream.` | `Content-Encoding: gzip` was sent with data that is not gzip, or the file was cut off. | Compress with `gzip -c`, or drop the header. |
| `400` `invalid_parameter`: `kind must be image or secret-fs.` | The `kind` parameter has another value. | Use `kind=image` for `trivy image` and `kind=secret-fs` for the secret scan. |
| `400` `invalid_parameter`: `artifact must be 1 to 200 printable characters: no control characters, no non-ASCII spaces, and not only spaces.` | The optional `artifact` parameter is empty, only spaces, longer than 200 characters, or holds a control character or a non-ASCII space such as a no-break space. | Use a plain name, see [keep two variants apart](#keep-two-variants-apart). |
| `422` `not_a_trivy_report`: `This is not a Trivy JSON report (SchemaVersion 2): SchemaVersion is missing` (or `SchemaVersion 1 is not supported, only 2 is`) | The file is not a Trivy JSON report, or has an old schema. | Create it with `--format json` from Trivy 0.50 or later. |
| `413` `report_too_large`: `The report is larger than the limit of 67108864 bytes (decompressed).` | The report is above 64 MiB after decompression. The limit is fixed. | Scan fewer targets per report, for example one report per image. |
| `413` `report_too_large`: `The report has too many findings: at most 200000 vulnerabilities and secrets (suppressed ones included) are accepted.` | The report holds more than 200,000 findings. | Split the scan, for example one report per image. |
| `503` `busy`: `The server is processing other uploads. Retry later.` | No memory or database slot was free within 30 seconds. | The script retries after `Retry-After`. If it keeps happening, spread the uploads of many jobs over time. |
| `503` `unavailable`: `The database is not available, so the report was not stored. Retry later.` | Scanopsis cannot reach its database, or the database cannot take the work now. | The script retries. If it keeps happening, check the database and look for `cannot store scan: the database is not available` in the Scanopsis log. |
| `408` `request_timeout`: `The body did not arrive in time. Upload it faster, or retry.` | The whole body did not arrive within 5 minutes. | The script retries. Check the link between the runner and Scanopsis, or the size of the report. |
| `409` `service_archived`: `The service for this project is archived. Ask an administrator to unarchive it.` | An administrator archived the service. Archived services take no new data. | Ask an administrator to unarchive it on the Services page. |
| `409` `service_conflict`: `A different service already uses the project path ... Archive or delete it in Scanopsis.` | Another service has this project path, for example one left from a deleted project with the same path. | Archive or delete the old service in Scanopsis. |
| `429` `too_many_requests`: `Too many uploads from this address. Retry later.` | More than 60 uploads at once, then more than one per second, from one address. | Upload fewer reports at once. The script waits `Retry-After` and retries. |
| `429` `project_rate_limited`: `This project uploaded too often. Retry later.` | The project used its 100 uploads and now uploads faster than one every 10 seconds. | The script retries. Upload fewer reports per pipeline. |
| `429` `job_artifact_limit`: `This job already uploaded 50 different artifacts, which is the most one job may upload. ...` | One job tried to store more than 50 different artifacts. | Split the images over several jobs. |

---

[Back to the README](../../README.md) · [All guides](../../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
