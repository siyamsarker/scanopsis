# Verifying the numbers

Scanopsis is only useful if its numbers are right. This guide describes the
accuracy check that you run with real data: you compare what Scanopsis shows
with what Trivy and SonarQube report for the same scans. Operators run it after
setting up Scanopsis and after each upgrade. The project runs it before every
release.

The result is a table of numbers and one decision. The acceptance rule is
strict:

> **Any mismatch blocks the release.** A number that differs by even one finding
> is a defect. Do not round, and do not explain it away. Record it, find the
> cause, and run the check again after the fix.

The outputs in this guide come from a test instance with a fake GitLab, using
the real Trivy 0.76.2 reports in `internal/ingest/testdata`. Commands that need
your GitLab or SonarQube are marked "example". Adapt them. They were not run
against your systems.

## What you need

- A test instance of Scanopsis, with the GitLab URL set, that you can upload to.
  Do not use the production instance: the check adds services.
- A GitLab project where you can run a pipeline, to upload through a real ID
  token. A token cannot be made by hand.
- An administrator account in Scanopsis, for the integration page.
- `trivy` (0.50 or later), `jq`, `curl` and `awk` on your machine.
- For the SonarQube part: a user who can open the projects in SonarQube.

## Step 1. Choose the services

Pick at least five services, or 10% of the services if that is more. Cover
what differs between services:

| Pick | Why |
|---|---|
| A service with many findings (hundreds) | Tests volume. |
| A service with fixed and unfixed findings | Tests the fixable count. |
| A service with an ignore file (`.trivyignore`) or VEX document | Tests suppressed findings. |
| A service with more than one image | Tests the sum over artifacts. |
| A service with a secret scan | Tests the secret count. |
| A clean image (Trivy writes no `Results`) | Tests the zero case. |
| A service with a SonarQube project: one with a passing gate, one failing, one without coverage | Tests the SonarQube values and the empty values. |

Write the choice in the results table (see [the end of this page](#record-the-results)).

## Step 2. Export the Trivy reports

The reports must be the ones that the pipeline uploaded. Make the job keep
them as artifacts. The script in the [GitLab CI guide](integrations/gitlab-ci.md)
writes them to `scanopsis-out/`. Add this to the job:

```yaml
  artifacts:
    paths:
      - scanopsis-out/*.json
    expire_in: 1 week
    when: always
```

After the pipeline, download the artifact of the job: in GitLab, open the job
and select **Download** under **Job artifacts**, or use the API (example, with
your own project ID, job ID and token):

```sh
curl --fail --header "PRIVATE-TOKEN: $GITLAB_TOKEN" --output artifacts.zip \
  "https://gitlab.example.com/api/v4/projects/123/jobs/456/artifacts"
unzip artifacts.zip -d reports
```

Write down the project, the pipeline ID and the job ID of every report.

You can practise every later step with the fixtures in the repository, which
are real reports: `internal/ingest/testdata/image-full.json`,
`image-suppressed.json` and `secret-fs.json`.

## Step 3. Anonymise the reports if needed

Skip this step if the reports may stay on the test instance as they are. If
they must not, change only what identifies you, and keep everything that
Scanopsis reads.

**What to replace:**

- The image name and registry host, in `ArtifactName` and in every `Target`.
- Internal project or path names in `Target`, `PkgPath` or `Results[].Secrets[].Target`.
- `Metadata.RepoTags`, `Metadata.RepoDigests`, `Metadata.ImageConfig`,
  `Metadata.DiffIDs` and `Metadata.Layers`: they name your registry and build
  steps. Scanopsis does not use them.
- `Code` and `Match` of secret findings. Scanopsis never reads them. Trivy
  masks the value in `Match`, but `Code` can show the lines around it.

**What must stay unchanged** (Scanopsis reads these, and so does `trivy convert`):

- `SchemaVersion` and `CreatedAt`. `CreatedAt` decides which scan is current.
- In every result: `Class`, `Type`, `Target` (its structure, not necessarily its words).
- In every vulnerability: `VulnerabilityID`, `PkgName`, `InstalledVersion`,
  `FixedVersion`, `Severity`, `Status`, `Title`, `PrimaryURL`, `PkgPath`.
- In every secret finding: `RuleID`, `Category`, `Severity`, `Title`, `StartLine`, `EndLine`.
- `ExperimentalModifiedFindings` with its `Type`, `Status`, `Statement` and
  `Source`: these are the suppressed findings.

Save this filter as `anonymise.jq`:

```text
walk(if type == "string" then split($old) | join($new) else . end)
| del(.Metadata.RepoTags, .Metadata.RepoDigests, .Metadata.ImageConfig, .Metadata.DiffIDs, .Metadata.Layers)
| del(.Results[]?.Secrets[]? | .Code, .Match)
```

and apply it. `$old` is the text to replace, `$new` its replacement. The match
is literal text, not a pattern:

```sh
jq --arg old 'python:3.9-slim-buster' --arg new 'registry.example.com/team/app:1.0' \
  -f anonymise.jq internal/ingest/testdata/image-full.json > anon-image-full.json
jq -r '.ArtifactName, .Results[0].Target' anon-image-full.json
```

```text
registry.example.com/team/app:1.0
registry.example.com/team/app:1.0 (debian 10.13)
```

Check that the anonymised file still gives the same totals as the original, in
Trivy's own words:

```sh
trivy convert --format table --quiet --output anon.txt anon-image-full.json
grep -E '^Total:' anon.txt
```

```text
Total: 125 (UNKNOWN: 3, LOW: 29, MEDIUM: 51, HIGH: 40, CRITICAL: 2)
Total: 13 (UNKNOWN: 0, LOW: 2, MEDIUM: 7, HIGH: 4, CRITICAL: 0)
```

These are the same two lines as for the original report. Search the result for
anything that should not be there, for example your domain name:
`grep -c 'corp.example' anon-image-full.json` must print `0`.

## Step 4. Upload to the test instance

Upload through a real pipeline. In a test project of your GitLab, add a job
that sends the anonymised file with the job's ID token (example; see the
[GitLab CI guide](integrations/gitlab-ci.md) for the settings that make it work):

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
stored counts. This is the first number to compare:

```text
{"scan_id":5,"service_id":4,"service_created":true,"replaced":false,"counts":{"critical":2,"high":44,"medium":58,"low":31,"unknown":3},"fixable":66,"secrets":0,"suppressed":0}
```

If the answer is an error, use the table in the GitLab CI guide.

Remember how Scanopsis decides what to show, because the comparison depends on it:

- The dashboards show the **current state** of a **ref**: the latest scan of
  each artifact and kind, by the time in the report (`CreatedAt`). The default
  ref is the ref of the most recent scan.
- **Severity counts** are the **unsuppressed** vulnerabilities. Suppressed
  findings (those in `ExperimentalModifiedFindings`) are counted separately.
- **Fixable** is the number of unsuppressed vulnerabilities that have a fixed version.
- **Secrets** are the unsuppressed secret findings of the `secret-fs` scans.
  They are not in the severity counts.
- Several artifacts in one service add up.

Upload every report of a service from the same ref. If the reports have
different `CreatedAt` times, the newest one for each artifact is the current one.

## Step 5. Compare the Trivy numbers

For every report, get three sets of numbers and compare them.

### A. What Trivy says

Trivy prints one `Total:` line for each result (for example the operating
system packages, then the Python packages). The numbers for the artifact are the sums:

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

Sum the `Total:` lines per severity:

```sh
grep -E '^Total:' table.txt \
  | sed -E 's/.*UNKNOWN: ([0-9]+), LOW: ([0-9]+), MEDIUM: ([0-9]+), HIGH: ([0-9]+), CRITICAL: ([0-9]+).*/\1 \2 \3 \4 \5/' \
  | awk '{u+=$1;l+=$2;m+=$3;h+=$4;c+=$5} END{print "critical="c, "high="h, "medium="m, "low="l, "unknown="u}'
```

```text
critical=1 high=43 medium=57 low=31 unknown=3
```

The suppressed total is the sum of the `Suppressed ... (Total: N)` lines: 1 + 2 = 3.

### B. The independent count with jq

This counts straight from the JSON and does not depend on Trivy's table:

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

Real output for the three fixtures:

```text
image-full.json:       critical=2 high=44 medium=58 low=31 unknown=3 fixable=66 secrets=0 suppressed=0
image-suppressed.json: critical=1 high=43 medium=57 low=31 unknown=3 fixable=64 secrets=0 suppressed=3
secret-fs.json:        critical=0 high=0 medium=0 low=0 unknown=0 fixable=0 secrets=2 suppressed=0
```

For the secret scan, Trivy's table says `Total: 2 (UNKNOWN: 0, LOW: 0, MEDIUM: 0, HIGH: 0, CRITICAL: 2)`. Scanopsis keeps
secrets out of the severity counts and shows the number `secrets=2`.

The table (A) and the jq count (B) must agree with each other for every report.
If they do not, the report is unusual. Examine it before you go on.

### C. What Scanopsis shows

Read the same numbers in three places. Compare each one with A and B.

**1. The portfolio** (home page). The tile of each service shows the severity
bar and the number of fixable findings. These are the sums over the
artifacts of the default ref. Secrets are not on the tile: read them on the
service dashboard.

**2. The service dashboard.** Open the service. **Findings by severity** and
the tiles **Critical**, **High**, **Fixable** and **Secrets** show the same
numbers, and the artifacts of the current state are listed with their own
counts.

**3. The findings explorer.** On the service page, open the findings. Select a
ref. Filter by severity and count the rows, or open the **Secrets** tab. The rows
are the unsuppressed findings of the current state. The number of rows with a
severity must be the count of that severity.

To read the same numbers without the browser, sign in with curl. The password
and the code are the ones you use in the browser. The code changes every 30
seconds, and a code works once:

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

Then the portfolio:

```sh
curl -sS -b jar.txt $URL/api/v1/portfolio \
  | jq -c '.items[] | select(.service.name | startswith("verification")) | {id: .service.id, name: .service.name, ref, counts, fixable, secrets}'
```

```text
{"id":2,"name":"verification-demo","ref":"main","counts":{"critical":2,"high":44,"medium":58,"low":31,"unknown":3},"fixable":66,"secrets":2}
{"id":3,"name":"verification-demo-2","ref":"main","counts":{"critical":1,"high":43,"medium":57,"low":31,"unknown":3},"fixable":64,"secrets":0}
```

The counts per artifact, from the service dashboard (replace `2` with the service ID):

```sh
curl -sS -b jar.txt $URL/api/v1/services/2/overview \
  | jq -c '.artifacts[] | {kind, artifact_name, counts, fixable, secrets, suppressed}'
```

```text
{"kind":"secret-fs","artifact_name":".","counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"fixable":0,"secrets":2,"suppressed":0}
{"kind":"image","artifact_name":"python:3.9-slim-buster","counts":{"critical":2,"high":44,"medium":58,"low":31,"unknown":3},"fixable":66,"secrets":0,"suppressed":0}
```

For service 3, which was made from `image-suppressed.json`, the same command
shows `"suppressed":3`, which is the third number of that fixture.

The findings explorer lists the vulnerabilities page by page. Save this as
`collect.sh` and run it with the service ID. It writes every vulnerability of
the current state to a file:

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

138 rows, and the same five numbers as in A and B. The secrets list works the
same way, at `/api/v1/services/<id>/secrets`.

### Record and decide

For every artifact, write the numbers of A, B and C next to each other. They
must be equal in every column: critical, high, medium, low, unknown, fixable,
secrets and suppressed.

## Step 6. Compare the SonarQube numbers

Scanopsis shows what SonarQube reports for the whole project (**Overall
Code**), not for new code. Compare with the **Overall Code** tab in SonarQube.

For every project in the sample:

1. In SonarQube, open the project. Write down:
   - the **Quality Gate** status (Passed, Failed, or no value),
   - the ratings **Reliability**, **Security**, **Maintainability** and **Security Review** (A to E),
   - **Coverage** and **Duplications** (percent, one decimal).
2. In Scanopsis, open the service. The **Quality gate** card shows the gate, the
   four ratings, **Coverage** and **Duplication**.
3. Compare. Percentages are shown with at most one decimal, as in SonarQube. A
   value that SonarQube does not report must show "Not available", never `0%`.
4. Check that Scanopsis has the same analysis as SonarQube. **Last analysis** on
   the portfolio tile, and `analysed_at` in the API output below, must be the
   time of the last analysis in SonarQube. If SonarQube has a newer analysis,
   wait for the webhook, or for the next sync (15 minutes by default), and look
   again.

To read SonarQube's numbers without the browser, ask its Web API (example;
replace the URL, the token and the project key, and use the metric keys of your
SonarQube mode, see [modes](integrations/sonarqube.md#5-modes-mqr-and-standard)):

```sh
curl -s -u "$SONAR_TOKEN:" "https://sonarqube.example.com/api/measures/component?component=platform:payments-api&metricKeys=alert_status,software_quality_reliability_rating,software_quality_security_rating,software_quality_maintainability_rating,security_review_rating,coverage,duplicated_lines_density" \
  | jq -c '.component.measures[] | {metric, value}'
```

In Scanopsis, the same analysis, with every measure as SonarQube reported it
(example; a SonarQube instance was not part of the test of this page):

```sh
curl -sS -b jar.txt $URL/api/v1/services/2/sonar \
  | jq '.latest | {analysed_at, quality_gate_status, reliability_rating, security_rating, maintainability_rating, security_review_rating, coverage, duplicated_lines_density, measures}'
```

Ratings in the Web API are numbers from `1.0` to `5.0`. Scanopsis shows A to E:
`1.0` is A, `2.0` is B, `3.0` is C, `4.0` is D and `5.0` is E.

## Record the results

Copy this table into the release record. Fill one row for each artifact and one
for each SonarQube project. Keep the exported reports (anonymised) and the
screenshots or API output with it.

| Field | Value |
|---|---|
| Scanopsis version and commit | |
| Trivy version | |
| SonarQube version | |
| Date, and who ran the check | |
| Test instance | |

Trivy, one row for each artifact:

| Service | Artifact | Ref | Pipeline / job | Source | Critical | High | Medium | Low | Unknown | Fixable | Secrets | Suppressed | Match |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| | | | | Trivy table | | | | | | n/a | | | |
| | | | | jq count | | | | | | | | | |
| | | | | Portfolio | | | | | | | | n/a | |
| | | | | Service dashboard | | | | | | | | | |
| | | | | Findings explorer | | | | | | | | n/a | |

SonarQube, one row for each project:

| Service | Project key | Source | Gate | Reliability | Security | Maintainability | Security review | Coverage | Duplication | Analysis time | Match |
|---|---|---|---|---|---|---|---|---|---|---|---|
| | | SonarQube | | | | | | | | | |
| | | Scanopsis | | | | | | | | | |

"Match" is yes or no, for the whole row, with no exceptions.

**Decision.** If every Match is yes, the numbers are accurate for this sample,
and you write "accepted" with the date. If one Match is no:

1. Write down the report, the service and the two numbers.
2. Do not edit data or settings to make them agree.
3. Find the cause: the data (a report with an unusual shape, a different ref,
   an older `CreatedAt`), the comparison (a suppressed finding, a second
   artifact), or Scanopsis. Open an issue with the report, anonymised.
4. The release is blocked until the cause is fixed and the whole check passes
   again on the same sample.
