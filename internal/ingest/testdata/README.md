# Test fixtures

Real reports written by Trivy 0.76.2 (vulnerability database version 2,
downloaded 2026-11-18). Nothing was edited or stripped.

| File | Command |
| --- | --- |
| `image-full.json` | `trivy image --scanners vuln --format json --show-suppressed --quiet -o image-full.json python:3.9-slim-buster` |
| `image-suppressed.json` | the same, run in a directory that holds a `.trivyignore`, with `--ignorefile .trivyignore` |
| `secret-fs.json` | `trivy fs --scanners secret --format json --quiet -o secret-fs.json .` in a directory that holds `config.env` |
| `*.totals.txt` | `trivy convert --format table --quiet --show-suppressed -o x.txt <file>.json`, then `grep -E '^(Total\|Suppressed )' x.txt` |

`.trivyignore` used for `image-suppressed.json`:

```
CVE-2019-8457
CVE-2022-40897
CVE-2023-5752
```

`config.env` used for `secret-fs.json` (fake, non-working values with the shape
of AWS credentials):

```
# Split so that secret scanners do not flag these fake values.
printf 'AWS_ACCESS_KEY_ID=%s%s\n' AKIA Z7Q3N5XK2WPLM4RT > config.env
printf 'AWS_SECRET_ACCESS_KEY=%s%s\n' wJalrXUtnFEMI/K7MDENG/ bPxRfiCYzq4mNp8LdT >> config.env
```

The image has OS packages (Debian 10.13) and Python packages, with fixed and
unfixed findings. The tests compare the numbers stored for each file with an
independent count over the JSON and with the `Total:` lines Trivy prints.
