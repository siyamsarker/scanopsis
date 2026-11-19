# Deployment

This page takes the Compose deployment from the [getting started guide](getting-started.md)
to production: a reverse proxy with TLS, an internal certificate authority,
sizing, upgrades and rollback.

Run every `docker compose` command in `deploy/compose`. Examples marked
"example" need systems that this page cannot provide, such as public DNS.
Adapt them. Everything else was run, and the output is shown, shortened where
marked. The output was captured on Docker Desktop. Container names in your
output differ with the project name. Client addresses in your output differ
with your network.

Host names are placeholders: `scanopsis.example.com`, `gitlab.example.com`.

## What you deploy

```text
Internet or intranet -> reverse proxy (TLS, port 443) -> Scanopsis (HTTP, port 8080) -> PostgreSQL
```

The Compose file runs Scanopsis and PostgreSQL. It publishes Scanopsis on
`127.0.0.1:8080` only. You add the reverse proxy, either in the same Compose
project, as shown below, or on the host.

Before you continue, you need a working stack from the getting started guide
(steps 1 to 5) and the DNS name that users will open.

## Reverse proxy and TLS

Scanopsis speaks plain HTTP. Put a proxy in front of it that terminates TLS,
and set `SCANOPSIS_PUBLIC_URL` to the HTTPS address of the proxy. Then:

- the session cookie gets the `Secure` attribute and the `__Host-` prefix,
- the server sends `Strict-Transport-Security`,
- GitLab and the CI jobs use the same address (the redirect URI and the token
  audience are built from it).

The proxy must pass the `Host` header, the path and the query unchanged, and
it must set `X-Forwarded-For` to the address of the client.

Both examples below were run with a self-signed certificate on a local
machine. In production, use a certificate from your certificate authority.

### Create a test certificate

Skip this step in production. Create a self-signed certificate for the name
that users open:

```sh
mkdir tls
openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -keyout tls/key.pem -out tls/cert.pem \
  -subj "/CN=scanopsis.example.com" \
  -addext "subjectAltName=DNS:scanopsis.example.com"
ls -l tls
```

Expected result:

```text
-rw-r--r--  1 siyam  wheel  1176 Nov 19 16:41 cert.pem
-rw-------  1 siyam  wheel  1704 Nov 19 16:41 key.pem
```

The key is readable only by you. The proxy containers run as root and can
read it.

### Option 1: Caddy

Caddy is the shortest way to get TLS. The image is pinned by digest, so that
the proxy you tested is the proxy you run. At the time of writing, `caddy:2`
was version 2.11.9. Look up the current digest with
`docker pull caddy:2` and `docker image inspect caddy:2 --format '{{index .RepoDigests 0}}'`.

1. Create `Caddyfile` next to `compose.yaml`:

   ```text
   scanopsis.example.com {
   	tls /etc/caddy/tls/cert.pem /etc/caddy/tls/key.pem
   	reverse_proxy scanopsis:8080
   }
   ```

   Caddy replaces any `X-Forwarded-For` header that the client sends with the
   address it sees, so a client cannot forge its address through Caddy.

   Example for production with public DNS: delete the `tls` line. Caddy then
   obtains and renews a certificate by itself. This needs ports 80 and 443 to
   be reachable from the internet. It was not run for this page.

2. Create `compose.override.yaml` next to `compose.yaml`. Compose reads it
   automatically:

   ```yaml
   services:
     caddy:
       image: caddy:2@sha256:4851e54a46b206517c574821bb5cc67f170d2156041e9fbc4c9193b45e84a75b
       restart: unless-stopped
       ports:
         - "127.0.0.1:8443:443"
       volumes:
         - ./Caddyfile:/etc/caddy/Caddyfile:ro
         - ./tls:/etc/caddy/tls:ro
         - caddy-data:/data
       networks:
         frontend:
           ipv4_address: 172.30.0.10
   networks:
     frontend:
       ipam:
         config:
           - subnet: 172.30.0.0/24
   volumes:
     caddy-data:
   ```

   This gives the `frontend` network a fixed address range and Caddy a fixed
   address in it, so that you can tell Scanopsis which proxy to trust. Choose a
   range that does not overlap with your own networks. To serve users on the
   network, publish `"443:443"` instead of the loopback address.

3. In `.env`, set the public URL and the proxy address:

   ```text
   SCANOPSIS_PUBLIC_URL=https://scanopsis.example.com
   SCANOPSIS_TRUSTED_PROXIES=172.30.0.10
   ```

4. Start everything:

   ```sh
   docker compose up -d
   docker compose ps --format 'table {{.Service}}\t{{.Status}}'
   ```

   Expected result:

   ```text
   SERVICE     STATUS
   caddy       Up About a minute
   db          Up 3 minutes (healthy)
   scanopsis   Up About a minute (healthy)
   ```

If Compose reports that the address pool overlaps with another network, the
range collides with an existing Docker network. Choose another range in the
`ipam` section and in the proxy's `ipv4_address`.

### Option 2: nginx

nginx needs three settings that Caddy does not: the upload size, a name
look-up that follows container restarts, and the forwarded address.

1. Create `nginx.conf`:

   ```nginx
   server {
       listen 443 ssl;
       server_name scanopsis.example.com;

       ssl_certificate     /etc/nginx/tls/cert.pem;
       ssl_certificate_key /etc/nginx/tls/key.pem;

       # Docker's DNS. The variable makes nginx look the name up again, so a
       # recreated Scanopsis container is found without restarting nginx.
       resolver 127.0.0.11 valid=10s;
       set $upstream http://scanopsis:8080;

       # Trivy reports can be tens of megabytes. nginx refuses more than 1m by default.
       client_max_body_size 100m;

       location / {
           proxy_pass $upstream;
           proxy_set_header Host $host;
           proxy_set_header X-Forwarded-For $remote_addr;
           proxy_read_timeout 90s;
       }
   }
   ```

   `X-Forwarded-For $remote_addr` replaces what the client sent. Do not use
   `$proxy_add_x_forwarded_for` unless you also list the proxies in front of
   nginx in `SCANOPSIS_TRUSTED_PROXIES`.

2. Use this `compose.override.yaml` instead of the one for Caddy:

   ```yaml
   services:
     nginx:
       image: nginx:stable-alpine@sha256:3bc3836dc0be3b811f238210360bf8e7ca8a2d0effdae46160bf1f3578710051
       restart: unless-stopped
       ports:
         - "127.0.0.1:9443:443"
       volumes:
         - ./nginx.conf:/etc/nginx/conf.d/default.conf:ro
         - ./tls:/etc/nginx/tls:ro
       networks:
         frontend:
           ipv4_address: 172.30.0.11
   networks:
     frontend:
       ipam:
         config:
           - subnet: 172.30.0.0/24
   ```

3. Set `SCANOPSIS_TRUSTED_PROXIES=172.30.0.11` and run `docker compose up -d`.

When you edit `nginx.conf`, restart the container with
`docker compose restart nginx`. A reload (`nginx -s reload`) can miss the
change when your editor replaces the file instead of changing it in place.

### Verify the proxy

The examples use port 8443 (Caddy). For nginx in this page, the port is 9443.
`--resolve` makes curl send the request for `scanopsis.example.com` to your
machine without a DNS entry. `--cacert` trusts the test certificate.

1. HTTPS works:

   ```sh
   curl -sS --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     https://scanopsis.example.com:8443/readyz
   ```

   Expected result:

   ```text
   {"status":"ready"}
   ```

   Without `--cacert`, curl refuses the self-signed certificate, which proves
   that the connection is verified:

   ```text
   curl: (60) SSL certificate problem: self signed certificate
   ```

2. HSTS and the other security headers are present, because the public URL
   uses `https`:

   ```sh
   curl -sSI --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     https://scanopsis.example.com:8443/healthz | grep -i -E '^(HTTP|strict|content-security|x-frame)'
   ```

   Expected result (the Content-Security-Policy line is shortened):

   ```text
   HTTP/2 200
   content-security-policy: default-src 'self'; script-src 'self'; style-src 'self'; ...
   strict-transport-security: max-age=63072000; includeSubDomains
   x-frame-options: DENY
   ```

   If `strict-transport-security` is missing, `SCANOPSIS_PUBLIC_URL` does not
   start with `https://`.

3. `X-Forwarded-For` is believed only from a trusted proxy. The test sends the
   request through the proxy and reads the client address that Scanopsis logs.

   First, with `SCANOPSIS_TRUSTED_PROXIES` empty, restart and send a request:

   ```sh
   curl -sS --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     -H 'X-Forwarded-For: 203.0.113.9' https://scanopsis.example.com:8443/healthz
   docker compose logs scanopsis | grep '"client_ip"' | grep -v '"127.0.0.1"' | tail -1
   ```

   ```text
   scanopsis-1  | {"time":"2026-11-19T10:42:39.552154966Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.30.0.10"}
   ```

   Scanopsis logs the address of the proxy (`172.30.0.10`) for every user. All
   users share one rate limit. The header is ignored.

   Then set `SCANOPSIS_TRUSTED_PROXIES=172.30.0.10`, run `docker compose up -d`
   and repeat the request. The startup line now shows `"trusted_proxies":1`:

   ```text
   scanopsis-1  | {"time":"2026-11-19T10:42:55.736077168Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.30.0.1"}
   ```

   The logged address is now the one that Caddy saw (`172.30.0.1` is the
   gateway of the Docker network, which is how Docker Desktop presents your
   machine; on a Linux server you see the real client address). The forged
   `203.0.113.9` was replaced by Caddy and is not used.

   A request that reaches Scanopsis directly from an address that is not a
   trusted proxy cannot choose its address:

   ```sh
   curl -s -H 'X-Forwarded-For: 203.0.113.9' http://127.0.0.1:8080/healthz
   docker compose logs scanopsis | grep '"client_ip"' | grep -v '"127.0.0.1"' | tail -1
   ```

   ```text
   scanopsis-1  | {"time":"2026-11-19T10:42:55.845838793Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.30.0.1"}
   ```

   The logged address is the real peer, not `203.0.113.9`.

4. Uploads of realistic size pass the proxy. Upload a report through a real
   pipeline (see the [GitLab CI guide](integrations/gitlab-ci.md)) and read the
   status. With nginx and without `client_max_body_size`, the status is `413`
   and the body is an HTML page from nginx, not JSON from Scanopsis:

   ```text
   <html>
   <head><title>413 Request Entity Too Large</title></head>
   <body>
   <center><h1>413 Request Entity Too Large</h1></center>
   <hr><center>nginx/1.30.7</center>
   </body>
   </html>
   HTTP 413
   ```

   With `client_max_body_size 100m;` the same 2.4 MiB upload returns `201`.
   Scanopsis accepts 64 MiB after decompression. Keep the proxy limit above
   the largest compressed report you send.

If a step fails, see [troubleshooting](troubleshooting.md#reverse-proxy-and-tls).

### Timeouts

Scanopsis reads a request within 30 seconds and writes a response within 60
seconds. A report that takes longer to upload is cut off. A 20 MB report
uploads in well under a second on a local network. Slow links are the only
cause. Set the proxy's read timeout above these values (nginx:
`proxy_read_timeout`).

## Internal certificate authority

If GitLab (or SonarQube) uses a certificate from your own certificate
authority, Scanopsis cannot verify it, and every upload and GitLab sign-in
fails. Scanopsis never turns certificate checks off. Give it the CA instead.

1. Put the certificate of the CA (the root, and any intermediate certificate
   that the server does not send), in PEM format, in one file, for example
   `ca.pem` next to `compose.yaml`.
2. Mount it into the container. In `compose.override.yaml`, add to the
   `scanopsis` service:

   ```yaml
   services:
     scanopsis:
       volumes:
         - ./ca.pem:/etc/scanopsis/ca.pem:ro
   ```

3. In `.env`, set `SCANOPSIS_CA_FILE=/etc/scanopsis/ca.pem` and run
   `docker compose up -d`.
4. Verify that the file was read: the startup line shows `"custom_ca":true`.

   ```sh
   docker compose logs scanopsis | grep 'scanopsis starting' | tail -1
   ```

   ```text
   ...,"config":{"listen_addr":":8080","public_url":"https://scanopsis.example.com","seeded_settings":1,"trusted_proxies":1,"custom_ca":true}}
   ```

The test used a fake GitLab behind Caddy, with a certificate that the
container did not trust. Before the CA was set, an upload was refused:

```text
{"error":{"code":"gitlab_unavailable","message":"The signature could not be checked: Scanopsis could not fetch the signing keys from https://gitlab.example.com/oauth/discovery/keys. Try again later."}}
HTTP 503
```

The Scanopsis log says only `cannot fetch GitLab signing keys`. To see the
cause, make the same request from a throwaway container on the same network.
This uses the `caddy` image only because it has a `wget` that prints TLS errors.
Replace the network name (`<project>_frontend`) and the address:

```sh
docker run --rm --network scanopsis_frontend caddy:2 wget -q -O- https://gitlab.example.com/oauth/discovery/keys
```

```text
28EDF092FFFF0000:error:0A000086:SSL routines:tls_post_process_server_certificate:certificate verify failed:ssl/statem/statem_clnt.c:2124:
ssl_client: SSL_connect
wget: error getting response: Connection reset by peer
```

After setting `SCANOPSIS_CA_FILE` to the certificate, the same upload returned
`201` and the signing keys were fetched.

If the file is missing, or holds no certificate, the server does not start:

```text
"error":"read SCANOPSIS_CA_FILE: open /etc/none.pem: no such file or directory"
"error":"SCANOPSIS_CA_FILE /ca.pem contains no PEM certificates"
```

The CA file applies to GitLab and to SonarQube.

## Sizing

The figures below are for up to 500 services and 3,000 pipelines a day. They
were measured with Scanopsis `42e0377` (Go 1.27.4) and PostgreSQL 18.7 on one
Docker Desktop machine. Measure again on your hardware if you are close to
a limit.

### Memory and CPU

Scanopsis keeps nothing but small caches in memory. The container's memory,
read from its control group:

| State | Memory |
|---|---|
| Idle, a minute after start | 11 to 14 MiB (`docker stats` shows 12 MiB) |
| Peak during start (migrations, first requests) | 26 MiB |
| One upload of a 20.3 MB report (uncompressed) | peak 33 MiB |
| Four uploads of 20.3 MB reports at the same time | peak 72 MiB |

The 20.3 MB report is the fixture `internal/ingest/testdata/image-full.json`
with its findings repeated 35 times, so it holds 4,830 findings. It is a
synthetic report, sent without compression. A single 20 MB upload took 0.4
seconds from request to the `201` answer.

At 3,000 uploads a day, the average is two a minute, so memory stays close to
the idle value. Give the Scanopsis container a limit of 256 MiB. The database
container needs more: plan 1 GiB or more of RAM for PostgreSQL, so that it can
keep its working set in memory.

The rate limit for uploads is 60 at once per client address and then one a
second. Runners behind one NAT address share it. A burst of more than 60
pipelines that finish together from one address gets `429`; the script in the
GitLab CI guide retries.

### Database size

The test uploaded each report 30 times, with a different job each time, into
an empty database and then read `pg_total_relation_size` (table, indexes and
compressed report storage) of `scans`, `vulnerabilities` and `secret_findings`.

```sh
docker compose exec -T db psql -U scanopsis -d scanopsis -c "
SELECT relname AS table, pg_size_pretty(pg_total_relation_size(oid)) AS total,
       pg_size_pretty(pg_indexes_size(oid)) AS indexes
FROM pg_class WHERE relname IN ('scans','vulnerabilities','secret_findings') ORDER BY 1"
```

Output after 30 uploads of `image-full.json`:

```text
      table      |  total  | indexes
-----------------+---------+---------
 scans           | 3584 kB | 64 kB
 secret_findings | 24 kB   | 16 kB
 vulnerabilities | 1520 kB | 312 kB
```

The results, as the size per scan (all three tables, divided by 30):

| Report | Findings | JSON size | Size in the database per scan |
|---|---|---|---|
| `image-full.json` (python:3.9-slim-buster, fixture) | 138 | 951 KB | 170.9 KiB |
| `image-suppressed.json` (same, 3 findings suppressed, fixture) | 138 | 952 KB | 170.9 KiB |
| `secret-fs.json` (fixture) | 2 secrets | 3 KB | 6.7 KiB |
| alpine:3.17.0, scanned with Trivy 0.76.2 | 92 | 589 KB | 118.4 KiB |
| node:16-alpine, scanned with Trivy 0.76.2 | 91 | 631 KB | 114.7 KiB |
| alpine:3.20, scanned with Trivy 0.76.2 | 2 | 26 KB | 13.1 KiB |

The last three rows come from reports made for this page, with the vulnerability
data of that day. They were not committed.

The size grows with the number of findings. A line through the measurements is

```text
size per image scan  ~  11 KiB + 1.2 KiB x findings
```

About two thirds of it is the compressed copy of the report that Scanopsis
keeps (113 KB of the 171 KiB for `image-full.json`). The findings themselves
take about 376 bytes each, with indexes.

**Estimate for 90 days.** Assumptions: 3,000 pipelines a day, each uploading
one image report and one secret report; 90 days of history (the default
[retention](operations.md#data-retention) period) and no growth in the number
of pipelines; the figures above. That makes 270,000 pipelines.

| Findings per image report | Per pipeline (image + secrets) | 90 days |
|---|---|---|
| 2 | 20 KiB | 5.5 GB |
| 30 | 54 KiB | 14.8 GB |
| 138 (like the fixture, an old image) | 182 KiB | about 50 GB |

Retention also keeps the latest scan of every service, ref, artifact and kind
beyond the period. For 500 services with a few refs each, that adds well under
1 GB.

Plan the disk for the estimate plus 50% for indexes growth, write-ahead log and
vacuum, and plan the same space again for backups. Shorten `retention.days` if
you do not need the history: the database size scales with it. Most pipelines
scan fewer images than one per pipeline, or scan only the main branch, which
makes the real figure lower. Count your own: the number of uploads a day is on
the **Integrations** page.

Reads are served from the counts stored on each scan row and do not read the
compressed reports, so the portfolio does not slow down with the size of the
reports. The response time of the portfolio and of the findings pages with 500
services was not measured for this page.

## Upgrades and rollback

Schema changes are applied by the new version when it starts. There is no
separate migration step.

### Upgrade

1. Read the [changelog](../CHANGELOG.md) for the new version.
2. Back up the database and the key, as in
   [backup and restore](operations.md#backup-and-restore). This is the only
   safe way back.

   ```sh
   (umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
     --format=custom > "scanopsis-before-upgrade.dump")
   ls -l scanopsis-before-upgrade.dump
   ```

   ```text
   -rw-------  1 siyam  wheel  166551 Nov 19 19:01 scanopsis-before-upgrade.dump
   ```

3. Note the schema version, so that you can check the upgrade:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) AS schema_version FROM goose_db_version"
   ```

   ```text
                 3
   ```

4. Set the new image in `.env` (`SCANOPSIS_IMAGE=...`, by digest once releases
   exist). If the image comes from a registry, pull it first with
   `docker compose pull scanopsis` (example; the test used a local image).
5. Recreate the container:

   ```sh
   docker compose up -d
   ```

   Expected result: Compose recreates `scanopsis` and leaves the database
   alone. The server applies the migrations before it listens. Wait about 30
   seconds.

6. Verify:

   ```sh
   docker compose ps --format 'table {{.Service}}\t{{.Status}}'
   curl -s http://127.0.0.1:8080/api/v1/version
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) AS schema_version FROM goose_db_version"
   ```

   ```text
   SERVICE       STATUS
   db            Up 3 minutes (healthy)
   scanopsis     Up About a minute (healthy)
   {"version":"42e0377","commit":"42e0377","go":"go1.27.4"}
                 4
   ```

   The schema version went from 3 to 4. Sign in and open the portfolio:
   the earlier data is still there. The test upgraded a database with one
   scan, and the scan was unchanged.

If the new container does not become healthy, read `docker compose logs
scanopsis`. A failed migration stops the server with `apply migrations:` and
the database error. Migrations run under a database lock and in a
transaction, so a failed one leaves the schema as it was.

### Roll back

Go back to the backup. Do not rely on starting the old image against the
migrated database. The test did that once and the old version started, because
the new migration only added indexes, but this holds for no migration in
general and the old version does not know what the new one changed.

1. Set the old image in `.env`.
2. Stop Scanopsis and restore the backup over the database, as in
   [restore](operations.md#restore-into-the-existing-database):

   ```sh
   docker compose stop scanopsis
   docker compose exec -T db pg_restore -U scanopsis -d scanopsis \
     --clean --if-exists --no-owner --exit-on-error < scanopsis-before-upgrade.dump
   echo "restore exit=$?"
   ```

   ```text
   restore exit=0
   ```

3. Start the old version and check it:

   ```sh
   docker compose up -d scanopsis
   curl -s http://127.0.0.1:8080/api/v1/version
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) AS schema_version FROM goose_db_version"
   ```

   ```text
   {"version":"0.0.1-old","commit":"6ddea02","go":"go1.27.4"}
                 3
   ```

Everything that changed after the backup is lost, including scans uploaded
while the new version ran. Pipelines that run later upload again. The
secret key does not change in an upgrade.
