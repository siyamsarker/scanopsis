# Check that the numbers are right

Scanopsis is only useful if its numbers are right. This guide walks you
through the accuracy check: compare what Scanopsis shows with what Trivy and
SonarQube report for the same scans, and record the result.

**Audience:** Operators · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

Run the check after you set Scanopsis up and after each upgrade. The project
runs it before it publishes a version. The rule is strict:

> **Any mismatch blocks the version.** A number that is off by even one
> finding is a defect. Do not round it and do not explain it away. Record it,
> find the cause, and run the check again after the fix.

The Trivy and `jq` commands below were run with Trivy 0.76.2 on the real
reports in `internal/ingest/testdata`. The upload and API outputs come from a
test instance with a stand-in GitLab. Commands marked **example** need your
GitLab or SonarQube.

## What you need

- A test instance of Scanopsis with the GitLab URL set. Do not use
  production: the check adds services.
- A GitLab project where you can run a pipeline. Uploads need a real ID
  token, which you cannot make by hand.
- An administrator account in Scanopsis.
- `trivy` (0.50 or later), `jq`, `curl` and `awk`.
- For SonarQube: a user who can open the projects there.

## Step 1. Pick the services

Pick at least five services, or 10% of them if that is more. Cover what makes
services different:

| Pick | What it tests |
|---|---|
| A service with hundreds of findings | Volume. |
| A service with fixed and unfixed findings | The fixable count. |
| A service with a `.trivyignore` or VEX document | Suppressed findings. |
| A service with more than one image, and one image scanned under two tags | The sum over artifacts; each image name counts once, with its newest scan. |
| A service with a secret scan | The secret count. |
| A clean image (Trivy writes no `Results`) | The zero case. |
| SonarQube projects with a passing gate, a failing gate, and no coverage | SonarQube values and empty values. |

Write your picks in the [results table](#record-the-results).

## Step 2. Keep the Trivy reports

Compare against the very reports the pipeline uploaded. The script in the
[GitLab CI guide](integrations/gitlab-ci.md) writes them to `scanopsis-out/`;
keep them as job artifacts by adding this to the job:

```yaml
  artifacts:
    paths:
      - scanopsis-out/*.json
    expire_in: 1 week
    when: always
```

After the pipeline, download them from the job page (**Download** under
**Job artifacts**), or with the API (example):

```sh
curl --fail --header "PRIVATE-TOKEN: $GITLAB_TOKEN" --output artifacts.zip \
  "https://gitlab.example.com/api/v4/projects/123/jobs/456/artifacts"
unzip artifacts.zip -d reports
```

Note the project, pipeline ID and job ID of every report.

To practise, use the real reports in the repository:
`internal/ingest/testdata/image-full.json`, `image-suppressed.json` and
`secret-fs.json`.

## Step 3. Anonymise the reports (if you must)

Skip this if the reports may stay on the test instance as they are. Otherwise
change only what identifies you, and keep everything Scanopsis reads.

**Replace:**

- the image name and registry host, in `ArtifactName` and every `Target`;
- internal project or path names in `Target`, `PkgPath` and secret targets;
- `Metadata.RepoTags`, `RepoDigests`, `ImageConfig`, `DiffIDs` and `Layers`,
  which name your registry and build steps (Scanopsis ignores them);
- `Code` and `Match` of secret findings. Scanopsis never reads them, and
  `Code` can show the lines around a secret.

**Keep unchanged** (Scanopsis and `trivy convert` read them):

- `SchemaVersion` and `CreatedAt` (it decides which scan is current);
- `Class`, `Type` and the structure of `Target` in every result;
- `VulnerabilityID`, `PkgName`, `InstalledVersion`, `FixedVersion`,
  `Severity`, `Status`, `Title`, `PrimaryURL` and `PkgPath` of every
  vulnerability;
- `RuleID`, `Category`, `Severity`, `Title`, `StartLine` and `EndLine` of
  every secret;
- `ExperimentalModifiedFindings` with `Type`, `Status`, `Statement` and
  `Source`: these are the suppressed findings.

Save this filter as `anonymise.jq`:

```text
walk(if type == "string" then split($old) | join($new) else . end)
| del(.Metadata.RepoTags, .Metadata.RepoDigests, .Metadata.ImageConfig, .Metadata.DiffIDs, .Metadata.Layers)
| del(.Results[]?.Secrets[]? | .Code, .Match)
```

Apply it. `$old` is literal text to replace (not a pattern), `$new` its
replacement:

```sh
jq --arg old 'python:3.9-slim-buster' --arg new 'registry.example.com/team/app:1.0' \
  -f anonymise.jq internal/ingest/testdata/image-full.json > anon-image-full.json
jq -r '.ArtifactName, .Results[0].Target' anon-image-full.json
```

```text
registry.example.com/team/app:1.0
registry.example.com/team/app:1.0 (debian 10.13)
```

Check that Trivy still counts the same as for the original:

```sh
trivy convert --format table --quiet --output anon.txt anon-image-full.json
grep -E '^Total:' anon.txt
```

```text
Total: 125 (UNKNOWN: 3, LOW: 29, MEDIUM: 51, HIGH: 40, CRITICAL: 2)
Total: 13 (UNKNOWN: 0, LOW: 2, MEDIUM: 7, HIGH: 4, CRITICAL: 0)
```

Then search the result for anything that must not be there, for example
`grep -c 'corp.example' anon-image-full.json` must print `0`.

## Step 4. Upload to the test instance

Upload through a real pipeline. In a test project, add a job that sends the
file with the job's ID token (example; the [GitLab CI guide](integrations/gitlab-ci.md)
has the settings that make it work):

```yaml
verify-upload:
  image: registry.example.com/ci/scanner:latest   # any image with curl and gzip
  variables:
    SCANOPSIS_URL: https://scanopsis-test.example.com
  id_tokens:
    SCANOPSIS_ID_TOKEN:
      aud: "$SCANOPSIS_URL"
  script:
    - gzip -c reports/anon-image-full.json > report.json.gz
    - >
      curl -sS --fail-with-body -X POST
      -H "Authorization: Bearer $SCANOPSIS_ID_TOKEN"
      -H 'Content-Type: application/json' -H 'Content-Encoding: gzip'
      --data-binary @report.json.gz
      "$SCANOPSIS_URL/api/v1/ingest/trivy?kind=image"
```

Use `kind=secret-fs` for a secret scan. Each upload answers `201` with the
counts it stored. That is your first number to compare:

```text
{"scan_id":5,"service_id":4,"service_created":true,"replaced":false,"counts":{"critical":2,"high":44,"medium":58,"low":31,"unknown":3},"fixable":66,"secrets":0,"suppressed":0}
```

If you get an error, see the [GitLab CI guide](integrations/gitlab-ci.md).

Keep in mind how Scanopsis counts, because the comparison depends on it:

- Dashboards show the **current state of one branch**: the latest scan of
  each artifact and kind, by the report's `CreatedAt`. An image is counted
  once per image name: `registry.example.com/team/web-frontend:4` and
  `registry.example.com/team/web-frontend:5` are one artifact, so only the
  newer scan counts. Count the images of a service the same way when you
  compare: the latest scan of each image name, not of each tag. Scans made
  before 0.4.1 with older tags stay stored but stop counting. The branch is the one
  you pick, else the dashboard branch an administrator chose, else the branch
  of the most recent scan. The page names it.
- **Severity counts** are the **unsuppressed** vulnerabilities. Suppressed
  ones (in `ExperimentalModifiedFindings`) are counted apart.
- **Fixable** counts unsuppressed vulnerabilities that have a fixed version.
- **Secrets** are the unsuppressed secret findings: those of `secret-fs`
  scans and those Trivy found in image layers (an image report made with
  `--scanners vuln,secret`). They are never part of the severity counts.
- The artifacts of one service add up.
- The artifact is the image name without its tag, or the scanned folder
  relative to the repository (`.` for the repository itself). The
  [GitLab CI guide](integrations/gitlab-ci.md#how-scanopsis-tells-artifacts-apart)
  has the rules.

Upload every report of a service from the same branch. If two reports of one
artifact have different `CreatedAt` times, the newer one is current.

## Step 5. Compare the Trivy numbers

For every report, get three sets of numbers: A, B and C.

### A. What Trivy says

Trivy prints one `Total:` line per result (for example the OS packages, then
the Python packages). The artifact's numbers are their sums:

```sh
trivy convert --format table --quiet --show-suppressed --output table.txt image-suppressed.json
grep -E '^(Total|Suppressed )' table.txt
```

```text
Total: 124 (UNKNOWN: 3, LOW: 29, MEDIUM: 51, HIGH: 40, CRITICAL: 1)
Suppressed Vulnerabilities (Total: 1)
Total: 11 (UNKNOWN: 0, LOW: 2, MEDIUM: 6, HIGH: 3, CRITICAL: 0)
Suppressed Vulnerabilities (Total: 2)
```

Add up the `Total:` lines per severity:

```sh
grep -E '^Total:' table.txt \
  | sed -E 's/.*UNKNOWN: ([0-9]+), LOW: ([0-9]+), MEDIUM: ([0-9]+), HIGH: ([0-9]+), CRITICAL: ([0-9]+).*/\1 \2 \3 \4 \5/' \
  | awk '{u+=$1;l+=$2;m+=$3;h+=$4;c+=$5} END{print "critical="c, "high="h, "medium="m, "low="l, "unknown="u}'
```

```text
critical=1 high=43 medium=57 low=31 unknown=3
```

The suppressed total is the sum of the `Suppressed ... (Total: N)` lines:
1 + 2 = 3.

**Already uploaded?** You do not need the original file. Scanopsis keeps the
report of every scan while it is current, and for 30 days otherwise. Get it
back by scan ID (`scan_id` in the upload answer), on the server:

```sh
docker compose exec scanopsis /scanopsis admin export-report 42 > report.json
trivy convert --format table --quiet --show-suppressed --output table.txt report.json
```

If retention already emptied the report, the command says so and exits with
1; upload the report again. See
[export the original report of a scan](operations.md#export-the-original-report-of-a-scan).

### B. An independent count with jq

This counts straight from the JSON, without Trivy's table:

The `secrets=` count takes the secrets of every result, so it covers the
secrets in image layers of an image report (made with `--scanners vuln,secret`)
as well as a `secret-fs` report.

```sh
jq -r '[.Results[]?.Vulnerabilities[]?.Severity] as $s
  | "critical=\([$s[]|select(.=="CRITICAL")]|length)
 high=\([$s[]|select(.=="HIGH")]|length)
 medium=\([$s[]|select(.=="MEDIUM")]|length)
 low=\([$s[]|select(.=="LOW")]|length)
 unknown=\([$s[]|select(.!="CRITICAL" and .!="HIGH" and .!="MEDIUM" and .!="LOW")]|length)
 fixable=\([.Results[]?.Vulnerabilities[]?|select((.FixedVersion // "") != "")]|length)
 secrets=\([.Results[]?.Secrets[]?]|length)
 suppressed=\([.Results[]?.ExperimentalModifiedFindings[]? | select(.Type=="vulnerability" or .Type=="secret")]|length)"' \
  internal/ingest/testdata/image-suppressed.json | tr -s ' \n' ' '; echo
```

The result for the three test reports:

```text
image-full.json:       critical=2 high=44 medium=58 low=31 unknown=3 fixable=66 secrets=0 suppressed=0
image-suppressed.json: critical=1 high=43 medium=57 low=31 unknown=3 fixable=64 secrets=0 suppressed=3
secret-fs.json:        critical=0 high=0 medium=0 low=0 unknown=0 fixable=0 secrets=2 suppressed=0
```

For the secret scan, Trivy's table says
`Total: 2 (UNKNOWN: 0, LOW: 0, MEDIUM: 0, HIGH: 0, CRITICAL: 2)`. Scanopsis
keeps secrets out of the severity counts and shows `secrets=2`.

A and B must agree for every report. If they do not, the report is unusual:
look at it closely before you go on.

### C. What Scanopsis shows

Read the same numbers in three places and compare each with A and B:

1. **The portfolio** (home page). Each service card shows the count of each
   severity next to its risk number: sums over the artifacts of the branch
   named on the card. Hover the risk number to see how it is counted.
   Secrets are not on the card. The list view shows the same counts per
   service.
2. **The service dashboard.** The **Open findings** panel: the severity bar
   (hover a segment for its count), the **fixable** and **secrets** counts,
   and the artifacts of the current state with their own counts.
3. **The findings explorer.** Pick the branch under **Ref** and select one
   severity. The summary above the table counts the findings in focus, and
   the rows must add up to that severity's count. The **Secrets** tab shows
   the secret findings.

To read them without a browser, sign in with curl. Use your own password and
a fresh two-factor code (each works once):

```sh
URL=https://scanopsis-test.example.com
curl -sS -c jar.txt -H 'Content-Type: application/json' \
  -d '{"username":"root","password":"..."}' -w 'login HTTP %{http_code}\n' $URL/api/v1/auth/local/login
curl -sS -b jar.txt -c jar.txt -H 'Content-Type: application/json' \
  -d '{"code":"123456"}' -w 'verify HTTP %{http_code}\n' $URL/api/v1/auth/mfa/verify
```

```text
login HTTP 204
verify HTTP 204
```

The server swaps the session cookie when it accepts the code, so keep
`-c jar.txt` on the second command.

The portfolio:

```sh
curl -sS -b jar.txt $URL/api/v1/portfolio \
  | jq -c '.items[] | select(.service.name | startswith("verification")) | {id: .service.id, name: .service.name, ref, counts, fixable, secrets}'
```

```text
{"id":2,"name":"verification-demo","ref":"main","counts":{"critical":2,"high":44,"medium":58,"low":31,"unknown":3},"fixable":66,"secrets":2}
{"id":3,"name":"verification-demo-2","ref":"main","counts":{"critical":1,"high":43,"medium":57,"low":31,"unknown":3},"fixable":64,"secrets":0}
```

The counts per artifact, from the service dashboard (replace `2` with your
service ID):

```sh
curl -sS -b jar.txt $URL/api/v1/services/2/overview \
  | jq -c '.artifacts[] | {kind, artifact_name, counts, fixable, secrets, suppressed}'
```

```text
{"kind":"secret-fs","artifact_name":".","counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"fixable":0,"secrets":2,"suppressed":0}
{"kind":"image","artifact_name":"python:3.9-slim-buster","counts":{"critical":2,"high":44,"medium":58,"low":31,"unknown":3},"fixable":66,"secrets":0,"suppressed":0}
```

Each artifact also has an `artifact_key` (the image name without its tag, or
the folder); `artifact_name` is the name as Trivy reported it.

For service 3, made from `image-suppressed.json`, the same command shows
`"suppressed":3`.

The findings explorer pages through the vulnerabilities. Save this as
`collect.sh`; it writes every vulnerability of the current state to a file:

```sh
#!/bin/sh
# collect.sh SERVICE_ID: needs URL and jar.txt from above
id=$1; after=0
: > "vulns-$id.jsonl"
while :; do
  page=$(curl -sS -b jar.txt "$URL/api/v1/services/$id/vulnerabilities?limit=500&after=$after")
  echo "$page" | jq -c '.items[]' >> "vulns-$id.jsonl"
  after=$(echo "$page" | jq -r '.next_after // empty')
  [ -n "$after" ] || break
done
```

```sh
sh collect.sh 2
wc -l < vulns-2.jsonl
jq -s 'group_by(.severity) | map({(.[0].severity | ascii_downcase): length}) | add' -c vulns-2.jsonl
```

```text
138
{"critical":2,"high":44,"low":31,"medium":58,"unknown":3}
```

138 rows, and the same five numbers as A and B. Secrets work the same way at
`/api/v1/services/<id>/secrets`.

Write A, B and C next to each other for every artifact. They must match in
every column: critical, high, medium, low, unknown, fixable, secrets and
suppressed.

## Step 6. Compare the SonarQube numbers

Scanopsis shows SonarQube's values for the whole project (**Overall Code**),
not for new code. Compare with the **Overall Code** tab in SonarQube.

For every project in your sample:

1. In SonarQube, note the **Quality Gate** (Passed, Failed or no value), the
   **Reliability**, **Security**, **Maintainability** and **Security Review**
   ratings (A to E), and **Coverage** and **Duplications** (percent, one
   decimal).
2. In Scanopsis, open the service. The quality gate card shows the gate, the
   four ratings, coverage and duplication.
3. Compare. Percentages have at most one decimal, as in SonarQube. A value
   SonarQube does not report must read "Not available", never `0%`.
4. Check that both show the same analysis: **Last analysis** on the portfolio
   card (or `analysed_at` in the API) must be SonarQube's last analysis time.
   If SonarQube has a newer one, wait for the webhook or the next sync (every
   15 minutes by default) and look again.

To read SonarQube's values without a browser (example; use the metric keys of
your SonarQube mode, see the [SonarQube guide](integrations/sonarqube.md)):

```sh
curl -s -u "$SONAR_TOKEN:" "https://sonarqube.example.com/api/measures/component?component=platform:payments-api&metricKeys=alert_status,software_quality_reliability_rating,software_quality_security_rating,software_quality_maintainability_rating,security_review_rating,coverage,duplicated_lines_density" \
  | jq -c '.component.measures[] | {metric, value}'
```

And the same analysis in Scanopsis (example):

```sh
curl -sS -b jar.txt $URL/api/v1/services/2/sonar \
  | jq '.latest | {analysed_at, quality_gate_status, reliability_rating, security_rating, maintainability_rating, security_review_rating, coverage, duplicated_lines_density, measures}'
```

SonarQube's API gives ratings as numbers from `1.0` to `5.0`. Scanopsis shows
letters: `1.0` is A, `2.0` B, `3.0` C, `4.0` D and `5.0` E.

## Record the results

Copy these tables into your record. Fill one row per artifact and per
SonarQube project, and keep the (anonymised) reports and the screenshots or
API output with it.

| Field | Value |
|---|---|
| Scanopsis version and commit | |
| Trivy version | |
| SonarQube version | |
| Date, and who ran the check | |
| Test instance | |

Trivy, per artifact:

| Service | Artifact | Ref | Pipeline / job | Source | Critical | High | Medium | Low | Unknown | Fixable | Secrets | Suppressed | Match |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| | | | | Trivy table | | | | | | n/a | | | |
| | | | | jq count | | | | | | | | | |
| | | | | Portfolio | | | | | | | | n/a | |
| | | | | Service dashboard | | | | | | | | | |
| | | | | Findings explorer | | | | | | | | n/a | |

SonarQube, per project:

| Service | Project key | Source | Gate | Reliability | Security | Maintainability | Security review | Coverage | Duplication | Analysis time | Match |
|---|---|---|---|---|---|---|---|---|---|---|---|
| | | SonarQube | | | | | | | | | |
| | | Scanopsis | | | | | | | | | |

"Match" is yes or no for the whole row, with no exceptions.

**Decide.** If every Match is yes, the numbers are right for this sample:
write "accepted" and the date. If one is no:

1. Write down the report, the service and the two numbers.
2. Do not change data or settings to make them agree.
3. Find the cause: the data (an unusual report, another branch, an older
   `CreatedAt`), the comparison (a suppressed finding, a second artifact), or
   Scanopsis. Open an issue with the anonymised report.
4. The version stays blocked until the cause is fixed and the whole check
   passes again on the same sample.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
