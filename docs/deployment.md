# Deployment

This page takes the Compose deployment from the [getting started guide](getting-started.md)
to production: host settings, a reverse proxy with TLS, an internal
certificate authority, an existing PostgreSQL server, other platforms, sizing,
upgrades and rollback.

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

## Host settings

These settings belong to the host and to `compose.yaml`, not to Scanopsis.
Go through them once.

### Outbound access

Scanopsis needs no outbound access except to your GitLab instance (sign-in and
the keys that check CI tokens) and, if you use it, to your SonarQube. It does
not check for updates, does not send telemetry and downloads nothing while it
runs. The database, which is PostgreSQL, is the only other connection. You
need internet access only on the machine where you build the image. Firewall
rules can therefore deny everything else.

### Start after a reboot

Both services have `restart: unless-stopped`. Docker starts them again after a
reboot when the Docker daemon starts at boot. On a Linux host with systemd,
this is the default after you install Docker Engine from Docker's packages.
Check it, and turn it on if it is off (example, needs `sudo`):

```sh
systemctl is-enabled docker
sudo systemctl enable docker
```

Check the restart policy that Compose set:

```sh
docker inspect scanopsis-scanopsis-1 --format '{{.HostConfig.RestartPolicy.Name}}'
```

```text
unless-stopped
```

A container that you stopped yourself (`docker compose stop`) stays stopped
after a reboot. The reboot itself was not tested for this page, because it
needs a machine you can restart.

### Publish address and port

The Compose file publishes Scanopsis on `127.0.0.1:8080`, so only programs on
the host can reach it. Change the host side with `SCANOPSIS_BIND` and
`SCANOPSIS_PORT` in `.env`. The container always listens on 8080. For example,
with `SCANOPSIS_PORT=28581`:

```sh
docker compose up -d
docker compose ps --format 'table {{.Service}}\t{{.Ports}}'
```

```text
SERVICE     PORTS
db          5432/tcp
scanopsis   127.0.0.1:28581->8080/tcp
```

Set `SCANOPSIS_BIND` to another address only when the proxy runs on another
machine, and then to the private address of the host, not to `0.0.0.0`:
Scanopsis speaks plain HTTP, and everyone who can reach the port can bypass
the proxy and its limits. Set `SCANOPSIS_PUBLIC_URL` to the address of the
proxy, not to this port.

### Logs and rotation

Both services write their logs to Docker's `json-file` driver. `compose.yaml`
rotates them: five files of 10 MB for each service, so logs cannot fill the
disk. Check the setting:

```sh
docker inspect scanopsis-scanopsis-1 --format '{{.HostConfig.LogConfig}}'
```

```text
{json-file map[max-file:5 max-size:10m]}
```

To keep more, change `max-size` and `max-file` in the `x-logging` block at the
top of `compose.yaml` and run `docker compose up -d`. Compose recreates the
containers, and the old log files go with the old containers. Copy the logs to
your log system before that, if you need them. With `docker run`, use
`--log-opt max-size=10m --log-opt max-file=5`.

### Time zone

The container runs in UTC. The timestamps in the log use its time zone.
Set `TZ` in `.env` to use another one, then run `docker compose up -d`:

```text
TZ=Europe/Berlin
```

```text
{"time":"2026-11-22T06:31:26.853177503+01:00","level":"INFO","msg":"scanopsis listening","addr":"[::]:8080"}
```

Only the log changes. The database stores times as points in time, so its
data is the same in any time zone. Set `TZ` only if your log system expects
local time.

### Memory and process limits

`compose.yaml` limits Scanopsis to 512 MB of memory (`mem_limit`) and tells
the Go runtime to collect garbage before that, with `GOMEMLIMIT=400MiB`, which
is 20% below the limit. It limits the processes of Scanopsis to 100 and of the
database to 200 (`pids_limit`), so that a runaway process cannot exhaust the
host. Check the limits:

```sh
docker inspect scanopsis-scanopsis-1 --format 'memory={{.HostConfig.Memory}} pids={{.HostConfig.PidsLimit}}'
```

```text
memory=536870912 pids=100
```

If Docker kills the container for lack of memory,
`docker inspect scanopsis-scanopsis-1 --format '{{.State.OOMKilled}}'` prints
`true`. Raise `mem_limit` and keep `GOMEMLIMIT` about 20% below it. The
[sizing](#sizing) section has the memory that Scanopsis used in tests.

## Reverse proxy and TLS

Scanopsis speaks plain HTTP. Put a proxy in front of it that terminates TLS,
and set `SCANOPSIS_PUBLIC_URL` to the HTTPS address of the proxy. Then:

- the session cookie gets the `Secure` attribute and the `__Host-` prefix,
- the server sends `Strict-Transport-Security`,
- GitLab and the CI jobs use the same address (the redirect URI and the token
  audience are built from it).

The proxy must pass the `Host` header, the path and the query unchanged, and
it must set `X-Forwarded-For` to the address of the client.

Serve Scanopsis at the root of its own host name, for example
`https://scanopsis.example.com/`. A port is allowed
(`https://scanopsis.example.com:8443/`). A sub-path such as
`https://example.com/scanopsis/` is not supported: the web interface loads its
files from absolute paths (`/assets/...`) and the public URL cannot contain a
path. Give Scanopsis its own host name instead.

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
   	encode zstd gzip
   	reverse_proxy scanopsis:8080
   }
   ```

   `encode zstd gzip` compresses the web interface and the JSON answers.
   Scanopsis does not compress them itself.

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
look-up that follows container restarts, and the forwarded address. It also
needs `gzip` to compress responses.

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

       # Compress the web interface and the JSON answers. HTML is always
       # included. Scanopsis does not compress them itself.
       gzip on;
       gzip_types text/css text/javascript application/javascript application/json image/svg+xml;

       # These apply to every location, as long as a location sets none of its own.
       proxy_set_header Host $host;
       proxy_set_header X-Forwarded-For $remote_addr;
       proxy_read_timeout 90s;

       location / {
           proxy_pass $upstream;
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

### Limit rates and restrict the webhook (optional)

Scanopsis limits the rate of sign-in, setup and upload requests for each client
address. A limit at the proxy adds a second layer in front of it and keeps
floods away from the application. The examples below are for nginx. They were
checked with `nginx -t` and run against the stack, as shown below. Replace `203.0.113.20` with the address from which SonarQube calls
Scanopsis. Runners that share one NAT address share one limit, so keep the
upload limit high.

Add this to the top of `nginx.conf`, outside the `server` block:

```nginx
limit_req_zone $binary_remote_addr zone=scanopsis_auth:10m rate=10r/m;
limit_req_zone $binary_remote_addr zone=scanopsis_upload:10m rate=2r/s;
```

Inside the `server` block, next to `proxy_read_timeout`, add the status that
a refused request gets, and put the new locations above `location /`:

```nginx
    limit_req_status 429;

    # Sign-in and second factor: 10 a minute for each address, and a burst of 10.
    location ~ ^/api/v1/(auth/local/login|auth/mfa/) {
        limit_req zone=scanopsis_auth burst=10 nodelay;
        proxy_pass $upstream;
    }

    # Uploads from CI: 2 a second for each address, and a burst of 100.
    location = /api/v1/ingest/trivy {
        limit_req zone=scanopsis_upload burst=100 nodelay;
        proxy_pass $upstream;
    }

    # Only SonarQube may call the webhook. Others get 403.
    location = /api/v1/hooks/sonarqube {
        allow 203.0.113.20;
        deny all;
        proxy_pass $upstream;
    }
```

The webhook is also protected by its HMAC signature. The address restriction
keeps everyone else from even trying. If a proxy stands in front of nginx, the
address that nginx sees is that proxy: use the `realip` module or restrict at
the outer proxy instead.

Run the syntax check, restart nginx and test:

```sh
docker compose exec -T nginx nginx -t
docker compose restart nginx
```

```text
nginx: the configuration file /etc/nginx/nginx.conf syntax is ok
nginx: configuration file /etc/nginx/nginx.conf test is successful
```

A request to the webhook from an address that is not listed is refused. The
test sent it from the machine, which is not `203.0.113.20`:

```sh
curl -s -o /dev/null -w "%{http_code}\n" -X POST \
  --resolve scanopsis.example.com:9443:127.0.0.1 --cacert tls/cert.pem \
  https://scanopsis.example.com:9443/api/v1/hooks/sonarqube
```

```text
403
```

Thirteen sign-in requests in a row, with wrong credentials. The first ten
reach Scanopsis (`401`), then the proxy answers `429` (the exact number
depends on timing, because the proxy refills the allowance while you test):

```text
401 401 401 401 401 401 401 401 401 401 429 429 429
```

The same restriction in Caddy, for the webhook only (the standard Caddy build
has no rate limit module, so use nginx or a firewall for the limits). Put it
above `reverse_proxy` in the site block:

```text
	@sonarqube_webhook_denied {
		path /api/v1/hooks/sonarqube
		not remote_ip 203.0.113.20
	}
	respond @sonarqube_webhook_denied 403
```

`GET /api/v1/version` needs no sign-in and tells everyone the version that you
run. If you do not want that, restrict it in the same way (nginx):

```nginx
    location = /api/v1/version {
        allow 10.0.0.0/8;
        deny all;
        proxy_pass $upstream;
    }
```

The version check in [versions and branches](versions.md#check-the-running-version)
then works from those addresses, or directly on `127.0.0.1:8080`. From
another address, such as the test machine, the proxy answers `403`.

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

5. Responses are compressed. Ask for the largest file of the web interface
   (its name changes with each build; read it from the page) with and without
   `Accept-Encoding`:

   ```sh
   ASSET=$(curl -s http://127.0.0.1:8080/ | grep -o '/assets/index-[^"]*\.js' | head -1)
   curl -s -o /dev/null -w "direct: %{size_download} bytes\n" http://127.0.0.1:8080$ASSET
   curl -s -o /dev/null -H 'Accept-Encoding: gzip' -w "gzip: %{size_download} bytes\n" \
     --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     https://scanopsis.example.com:8443$ASSET
   curl -sI -H 'Accept-Encoding: gzip' \
     --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     https://scanopsis.example.com:8443$ASSET | grep -i content-encoding
   ```

   Expected result, through Caddy (nginx on port 9443 gives about 99,600 bytes
   and `Content-Encoding: gzip`; the sizes change with each build):

   ```text
   direct: 274441 bytes
   gzip: 88211 bytes
   content-encoding: gzip
   ```

   With `Accept-Encoding: zstd, gzip`, Caddy answers with `content-encoding: zstd`.
   A JSON answer such as `/readyz` is too small to be compressed, which is
   normal.

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
{"error":{"code":"gitlab_unavailable","message":"The signature could not be checked: Scanopsis could not fetch the signing keys from GitLab. Try again later."}}
HTTP 503
```

The answer does not name the cause, on purpose. The Scanopsis log does:

```sh
docker compose logs scanopsis | grep 'upload rejected' | tail -1
```

```text
scanopsis-1  | {"time":"2026-11-19T13:18:55.621785344Z","level":"WARN","msg":"upload rejected","status":503,"reason":"gitlab_unavailable","ip":"172.30.0.1","gitlab_url":"https://gitlab.example.com","cause":"failed to verify signature: fetching keys oidc: get keys failed Get \"https://gitlab.example.com/oauth/discovery/keys\": tls: failed to verify certificate: x509: certificate signed by unknown authority (https://gitlab.example.com/oauth/discovery/keys)"}
```

`x509: certificate signed by unknown authority` means that the container does
not trust the certificate. You can also test the connection yourself from a
throwaway container on the same network. This uses the `caddy` image only
because it has a `wget` that prints TLS errors. Replace the network name
(`<project>_frontend`) and the address:

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

## Use an existing PostgreSQL

Use this when your organization runs PostgreSQL for you (a managed service or
a shared server) and you do not want the `db` container. Scanopsis was tested
with PostgreSQL 18. It needs no extension and no superuser rights. The steps
below were run against a separate PostgreSQL 18 server with TLS and a
certificate from a self-signed certificate authority. Host names and the
password are examples.

### Step 1. Create the role and the database

Create a role that can log in and a database that it owns. Run this as an
administrator of the server (`psql` example):

```sql
CREATE ROLE scanopsis LOGIN PASSWORD 'p@ss/w:rd#1?x%';
CREATE DATABASE scanopsis OWNER scanopsis;
```

Expected result:

```text
CREATE ROLE
CREATE DATABASE
```

Scanopsis creates its tables and indexes when it starts, and changes them in
later versions. The role needs these rights in the schema it uses (`public`,
unless your URL sets another `search_path`):

- `CREATE` on the schema. The migrations run `CREATE TABLE`, `CREATE INDEX` and
  `ALTER TABLE`, and the migration tool creates its table `goose_db_version`.
- `SELECT`, `INSERT`, `UPDATE` and `DELETE` on the tables, which the role has
  as their owner.

Nothing more is needed: the migrations create no extension, role or function.
The simplest way to get these rights is to make the role the owner of the
database, as above. In PostgreSQL 15 and later, the owner of a database can
create objects in its `public` schema. Check it as the new role:

```sql
SELECT current_user, has_schema_privilege('public', 'CREATE') AS can_create_in_public;
```

```text
 current_user | can_create_in_public
--------------+----------------------
 scanopsis    | t
(1 row)
```

If the database belongs to someone else and the role cannot create tables,
Scanopsis stops at start with this error:

```text
"error":"apply migrations: failed to initialize: create version table: failed to create version table \"goose_db_version\": ERROR: permission denied for schema public (SQLSTATE 42501)"
```

Fix it as an administrator, in that database. Scanopsis starts at its next
restart:

```sql
GRANT CREATE ON SCHEMA public TO scanopsis;
```

Connect to PostgreSQL directly, or through a pooler in session mode.
Scanopsis takes session-level advisory locks while it migrates and in its
background jobs. A pooler in transaction mode, such as PgBouncer with
`pool_mode = transaction`, does not keep them. This follows from the code and
was not tested.

### Step 2. Write the database URL

The URL goes in `secrets/database_url`, as in the
[getting started guide](getting-started.md#step-2-create-the-secrets). You do
not need `secrets/postgres_password`.

Encrypt the connection. `sslmode` decides how:

| `sslmode` | What it does |
|---|---|
| `disable` | No TLS. Only for the bundled database on the private Compose network. |
| `require` | Encrypts the connection and does not check who answers. It protects against listening in, not against a server that pretends to be yours. |
| `verify-full` | Encrypts, checks the certificate chain, and checks that the host name in the URL is in the certificate. Use this in production. |

For `verify-full` with a certificate from your own certificate authority, add
`sslrootcert=` with the path of the CA file inside the container. Managed
services publish their CA certificate. Save it as `db-ca.pem` next to
`compose.yaml`. A certificate from a public authority is checked against the
system roots of the image and needs no `sslrootcert`.

Special characters in the password break the URL. The characters `@`, `/`,
`:`, `?`, `#` and `%` must be percent-encoded. Encode the password with one of
these commands (the second needs `jq`):

```sh
python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' 'p@ss/w:rd#1?x%'
jq -rn --arg p 'p@ss/w:rd#1?x%' '$p|@uri'
```

```text
p%40ss%2Fw%3Ard%231%3Fx%25
p%40ss%2Fw%3Ard%231%3Fx%25
```

Then write the URL. The host name must be one that is in the certificate of
the server:

```sh
printf 'postgres://scanopsis:%s@db.example.com:5432/scanopsis?sslmode=verify-full&sslrootcert=/etc/scanopsis/db-ca.pem' \
  'p%40ss%2Fw%3Ard%231%3Fx%25' > secrets/database_url
chmod 644 secrets/database_url
```

If you skip the encoding, Scanopsis stops at start:

```text
"error":"parse database URL: cannot parse `postgres://scanopsis:xxxxx@ss/w:xxxxxx@db.example.com:5432/scanopsis?sslmode=require`: failed to parse as URL (invalid percent-encoded token: \"x%@db.example.com:5432/scanopsis?sslmode\")"
```

`SCANOPSIS_CA_FILE` does not apply to the database. It adds authorities for
GitLab and SonarQube only. The database CA goes in `sslrootcert`. Without it,
`verify-full` fails for a certificate from your own authority:

```text
"error":"connect to database: failed to connect to `user=scanopsis database=scanopsis`: 172.20.0.2:5432 (db.example.com): failed to write startup message: write failed: tls: failed to verify certificate: x509: certificate signed by unknown authority"
```

### Step 3. Remove the `db` service

The file `deploy/compose/compose.external-db.yaml` is an override. It removes
the `db` service, its password secret and its volume, and puts Scanopsis on
the `frontend` network only, which has a route to your server. It needs
Docker Compose 2.24 or later, for the `!reset` and `!override` tags.

1. Open `compose.external-db.yaml`. If you use `sslrootcert`, uncomment the
   `volumes` lines in it, so that Compose mounts `db-ca.pem` at
   `/etc/scanopsis/db-ca.pem`. The server must be reachable from the Docker
   host at the host name in the URL.
2. Tell Compose to use both files. Add this line to `.env`, so that every
   `docker compose` command uses them:

   ```text
   COMPOSE_FILE=compose.yaml:compose.external-db.yaml
   ```

   If you use a `compose.override.yaml` for a proxy as well, add it to the
   list. Compose reads `compose.override.yaml` on its own only when you name
   no file.
3. Check the result. The list has no `db` service:

   ```sh
   docker compose config --services
   ```

   ```text
   scanopsis
   ```

4. Start Scanopsis. If you ran the bundled database before, add
   `--remove-orphans`, so that Compose removes the old `db` container:

   ```sh
   docker compose up -d --remove-orphans
   ```

   Without the option, Compose only warns:

   ```text
   level=warning msg="Found orphan containers (scanopsis-db-1) for this project. If you removed or renamed this service in your compose file, you can run this command with the --remove-orphans flag to clean it up."
   ```

   Expected result with the option (Compose removes the `db` container, not
   its volume):

   ```text
    Container scanopsis-scanopsis-1 Running
    Container scanopsis-db-1 Stopping
    Container scanopsis-db-1 Stopped
    Container scanopsis-db-1 Removing
    Container scanopsis-db-1 Removed
   ```

5. Check that Scanopsis is healthy:

   ```sh
   docker compose ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}'
   curl -s http://127.0.0.1:8080/readyz
   ```

   ```text
   SERVICE     STATUS                    PORTS
   scanopsis   Up 12 seconds (healthy)   127.0.0.1:8080->8080/tcp
   {"status":"ready"}
   ```

6. Check that the connection is encrypted. Ask the server, as an
   administrator:

   ```sql
   SELECT usename, ssl, version FROM pg_stat_ssl s JOIN pg_stat_activity a USING (pid) WHERE usename = 'scanopsis' LIMIT 1;
   ```

   ```text
     usename  | ssl | version
   -----------+-----+---------
    scanopsis | t   | TLSv1.3
   ```

The old volume with the bundled data stays on the host. Remove it with
`docker volume rm <project>_db-data` only when you are sure that you have
moved everything you need.

### Move the data of the bundled database

To keep the users, settings and scans of a running installation, take a backup
before you remove the bundled database, and restore it into the new one. The
backup commands are in [backup and restore](operations.md#backup-and-restore).
The restore needs a PostgreSQL client of the same major version or newer. This
example runs it from the PostgreSQL image, on a Docker network that can reach
the server. Run it before step 4 above, or while Scanopsis is stopped:

```sh
docker run --rm -i -e PGPASSWORD='p@ss/w:rd#1?x%' -e PGSSLMODE=require \
  postgres:18.7-alpine pg_restore -h db.example.com -U scanopsis -d scanopsis \
  --no-owner --exit-on-error < scanopsis-2026-11-19.dump
echo "restore exit=$?"
```

```text
restore exit=0
```

Use the same `secrets/secret_key` as before. When Scanopsis then starts on the
new database, its log has no `setup_token` line, because the restored database
already has an administrator.

## Other platforms

Compose is the way that this documentation supports. The container does not
depend on it: it needs a database URL, a secret key and a public URL. The
rules are the same everywhere:

- Run one instance. Scanopsis keeps its state in PostgreSQL, but it keeps its
  rate limits in memory for each instance, and an upgrade must not run two
  versions on one database while the new one migrates it.
- Give it the database URL and the key as files (`SCANOPSIS_DATABASE_URL_FILE`
  and `SCANOPSIS_SECRET_KEY_FILE`), not as environment variables. The
  [configuration reference](configuration.md#file-variants-and-docker-secrets)
  lists every `_FILE` variant.
- Run it as user 65532 (the image does), with a read-only root file system and
  no capabilities. It writes nothing to disk.
- It applies its migrations before it listens. There is no separate migration
  step.

### Plain `docker run`

These commands were run with Docker 29.9.3. They start a database and Scanopsis
on one network, with the same settings as the Compose file. Create the secret
files first, as in
[step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets),
but with the host name `scanopsis-db` and not `db` in `secrets/database_url`.
The network, volume and container names are examples.

```sh
docker network create scanopsis-net
docker volume create scanopsis-db-data

docker run -d --name scanopsis-db --network scanopsis-net \
  -e POSTGRES_USER=scanopsis -e POSTGRES_DB=scanopsis \
  -e POSTGRES_PASSWORD_FILE=/run/secrets/postgres_password \
  -v "$PWD/secrets/postgres_password:/run/secrets/postgres_password:ro" \
  -v scanopsis-db-data:/var/lib/postgresql \
  postgres:18.7-alpine

docker run -d --name scanopsis --network scanopsis-net \
  --restart unless-stopped \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --pids-limit 100 --memory 512m --stop-timeout 20 \
  -p 127.0.0.1:8080:8080 \
  -e SCANOPSIS_PUBLIC_URL=https://scanopsis.example.com \
  -e SCANOPSIS_DATABASE_URL_FILE=/run/secrets/database_url \
  -e SCANOPSIS_SECRET_KEY_FILE=/run/secrets/secret_key \
  -e GOMEMLIMIT=400MiB \
  -v "$PWD/secrets/database_url:/run/secrets/database_url:ro" \
  -v "$PWD/secrets/secret_key:/run/secrets/secret_key:ro" \
  scanopsis:v0.1.0
```

Pin the database image by digest, as `compose.yaml` does. Wait about 30
seconds, then check:

```sh
docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
curl -s http://127.0.0.1:8080/readyz
```

```text
NAMES          STATUS                    PORTS
scanopsis      Up 25 seconds (healthy)   127.0.0.1:8080->8080/tcp
scanopsis-db   Up 35 seconds             5432/tcp
{"status":"ready"}
```

Unlike Compose, `docker run` does not wait for the database. If Scanopsis
starts first, it exits, and `--restart unless-stopped` starts it again until
the database answers.

Environment variables work as well, for example
`-e SCANOPSIS_DATABASE_URL=... -e SCANOPSIS_SECRET_KEY=...`. Do not use them
in production. Everyone who can run `docker inspect` then reads the values.
With files, `docker inspect` shows only
`SCANOPSIS_SECRET_KEY_FILE=/run/secrets/secret_key`. Set only one form of a
variable: the server refuses to start when both `SCANOPSIS_SECRET_KEY` and
`SCANOPSIS_SECRET_KEY_FILE` are set.

The container has no shell. Run the
[administration commands](operations.md#administration-commands) with
`docker exec scanopsis /scanopsis admin ...`.

### Podman

Podman runs the same image. This part was not tested, because no Podman was
available. Treat it as a starting point:

- `podman run` accepts the options above (`--read-only`, `--cap-drop`,
  `--pids-limit`, `--memory`, `-p`, `-e`, `-v`).
- Podman secrets replace the secret files. Create one secret for each file and
  mount it at the path that the `_FILE` variable names:

  ```sh
  podman secret create scanopsis_database_url secrets/database_url
  podman secret create scanopsis_secret_key secrets/secret_key
  podman run -d --name scanopsis \
    --secret scanopsis_database_url,target=/run/secrets/database_url \
    --secret scanopsis_secret_key,target=/run/secrets/secret_key \
    -e SCANOPSIS_DATABASE_URL_FILE=/run/secrets/database_url \
    -e SCANOPSIS_SECRET_KEY_FILE=/run/secrets/secret_key \
    ...
  ```

  User 65532 must be able to read the files. If the server reports that it
  cannot read one, add `,mode=0444` to the `--secret` option.
- Rootless Podman maps the users of a container to your sub-user ids. Files
  that you bind-mount need the `U` option (`:ro,U`), or use a Podman secret as
  above.
- To start the container at boot, use Quadlet (a systemd unit that Podman
  generates) and not `--restart`.

### Kubernetes

Scanopsis needs no operator and no custom resources. The model is one pod:

- A `Deployment` with `replicas: 1` and `strategy: Recreate`. `Recreate` stops
  the old pod before it starts the new one. With a rolling update, two
  versions run on one database while the new one migrates it.
- A `Secret` with the keys `database_url` and `secret_key`, mounted as files.
  `SCANOPSIS_DATABASE_URL_FILE` and `SCANOPSIS_SECRET_KEY_FILE` point at them.
  Create it from the same files as for Compose:

  ```sh
  kubectl create secret generic scanopsis \
    --from-file=database_url=secrets/database_url \
    --from-file=secret_key=secrets/secret_key
  ```

  With `--dry-run=client -o yaml`, the command prints a `Secret` with
  `data.database_url` and `data.secret_key`, both base64 encoded. Do not
  commit that output.
- Probes: `/healthz` for liveness (the process is alive, no dependencies) and
  `/readyz` for readiness (the database answers). A startup probe on `/healthz`
  gives the start, with its migrations, time.
- A security context: `runAsNonRoot`, user and group 65532, `fsGroup` 65532 so
  that the secret files are readable, `readOnlyRootFilesystem: true`,
  `allowPrivilegeEscalation: false` and `capabilities.drop: ["ALL"]`.
- Resources: a memory request and limit, and `GOMEMLIMIT` about 20% below the
  limit. A CPU limit can slow uploads down, so set one only if your cluster
  requires it.
- A `terminationGracePeriodSeconds` of 20, above the 15 seconds that the
  server needs to finish its requests.
- Migrations run when the pod starts. The first start of a new version can
  take longer than the others.
- An Ingress or Gateway that terminates TLS, with the rules of the
  [reverse proxy](#reverse-proxy-and-tls): pass `Host` unchanged, set
  `X-Forwarded-For`, allow a body of 100 MB, and serve Scanopsis at the root of
  its own host name. Set `SCANOPSIS_TRUSTED_PROXIES` to the addresses of the
  ingress controller pods.
- PostgreSQL outside the pod, see
  [use an existing PostgreSQL](#use-an-existing-postgresql). Run a managed
  database or an operator of your choice. A database in the same namespace is
  out of scope here.

The manifest below follows this model. Replace the image with the one that you
built and pushed to a registry that the cluster can pull from, and replace the
public URL. It passed `kubectl apply --dry-run=client -f` with kubectl 1.37.2.
It was not run on a cluster, so check the probes and the first start in yours.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: scanopsis
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: scanopsis
  template:
    metadata:
      labels:
        app: scanopsis
    spec:
      terminationGracePeriodSeconds: 20
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: scanopsis
          image: registry.example.com/platform/scanopsis:v0.1.0
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: SCANOPSIS_PUBLIC_URL
              value: https://scanopsis.example.com
            - name: SCANOPSIS_DATABASE_URL_FILE
              value: /run/secrets/scanopsis/database_url
            - name: SCANOPSIS_SECRET_KEY_FILE
              value: /run/secrets/scanopsis/secret_key
            - name: GOMEMLIMIT
              value: 400MiB
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            periodSeconds: 10
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            periodSeconds: 5
          startupProbe:
            httpGet:
              path: /healthz
              port: http
            periodSeconds: 2
            failureThreshold: 30
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              memory: 512Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: secrets
              mountPath: /run/secrets/scanopsis
              readOnly: true
      volumes:
        - name: secrets
          secret:
            secretName: scanopsis
            defaultMode: 0440
---
apiVersion: v1
kind: Service
metadata:
  name: scanopsis
spec:
  selector:
    app: scanopsis
  ports:
    - name: http
      port: 80
      targetPort: http
```

After `kubectl apply`, wait for the pod and check it:

```sh
kubectl rollout status deployment/scanopsis
kubectl get pods -l app=scanopsis
```

Expect `1/1` in the `READY` column. This output was not captured, because no
cluster was available.

## Sizing

The figures below are for up to 500 services and 3,000 pipelines a day. They
were measured with Scanopsis `7e53111` (Go 1.27.4) and PostgreSQL 18.7 on one
Docker Desktop machine. Measure again on your hardware if you are close to
a limit.

### Memory and CPU

Scanopsis keeps nothing but small caches in memory. The container's memory,
read from its control group:

| State | Memory |
|---|---|
| Idle, a minute after start | 11 to 14 MiB (`docker stats` shows 12 MiB) |
| Peak during start (migrations, first requests) | 28 MiB |
| One upload of a 20.3 MB report (uncompressed) | peak 34 MiB |
| Four uploads of 20.3 MB reports at the same time | peak 73 MiB |
| Eight uploads of 20.3 MB reports at the same time | peak 94 MiB |

The 20.3 MB report is the fixture `internal/ingest/testdata/image-full.json`
with its findings repeated 35 times, so it holds 4,830 findings. It is a
synthetic report, sent without compression. A single 20 MB upload took 0.2 to 0.4
seconds from request to the `201` answer. Scanopsis processes at most four
uploads at a time, all addresses together. Others wait for a free slot, as long
as their request lasts, and then get `503 busy` with `Retry-After: 5`. In the
test with eight uploads at once, all eight were stored, and memory peaked at
94 MiB.

At 3,000 uploads a day, the average is two a minute, so memory stays close to
the idle value. Give the Scanopsis container a limit of 256 MiB. The database
container needs more: reserve 1 GiB or more of RAM for PostgreSQL, so that it can
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

Retention also keeps, beyond the period, the latest scans of the branch each
dashboard shows and of protected branches and tags. Scans of other short-lived
branches expire like the rest. For 500 services with a few protected refs
each, that adds well under 1 GB.

Size the disk for the estimate plus 50% for index growth, write-ahead log and
vacuum, and reserve the same space again for backups. Shorten `retention.days` if
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
separate migration step. You deploy a version by building it from its branch,
see [versions and branches](versions.md). The steps below show the checks around
an upgrade.

### Upgrade

1. Read the [changelog](../CHANGELOG.md) of the new version branch, see
   [find the newest version](versions.md#find-the-newest-version).
2. Back up the database and the key, as in
   [backup and restore](operations.md#backup-and-restore). This is the only
   safe way back.

   ```sh
   (umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
     --format=custom > "scanopsis-before-upgrade.dump")
   ls -l scanopsis-before-upgrade.dump
   ```

   ```text
   -rw-------  1 siyam  wheel  166549 Nov 19 19:14 scanopsis-before-upgrade.dump
   ```

3. Note the schema version, so that you can check the upgrade:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) AS schema_version FROM goose_db_version"
   ```

   ```text
                 3
   ```

4. Switch to the new version branch and build its image, then set it in
   `.env` (`SCANOPSIS_IMAGE=...`), as in
   [upgrade to a newer version](versions.md#upgrade-to-a-newer-version).
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
   caddy         Up 40 seconds
   db            Up 40 seconds (healthy)
   fake-gitlab   Up 40 seconds
   nginx         Up 40 seconds
   scanopsis     Up 14 seconds (healthy)
   {"version":"7e53111","commit":"7e53111","go":"go1.27.4"}
                 5
   ```

   The schema version went from 3 to 5. (The other services in the output are
   those of the test setup.) Sign in and open the portfolio:
   the earlier data is still there. The test upgraded a database with one
   scan, and the scan was unchanged.

If the new container does not become healthy, read `docker compose logs
scanopsis`. A failed migration stops the server with `apply migrations:` and
the database error. Migrations run under a database lock and in a
transaction, so a failed one leaves the schema as it was.

### Roll back

Go back to the backup. Do not rely on starting the old image against the
migrated database. The test did that once and the old version started, because
the two new migrations only added indexes and a column with a default, but this
holds for no migration in general and the old version does not know what the
new ones changed.

1. Build the image of the previous version branch and set it in `.env`, as in
   [roll back to the previous version](versions.md#roll-back-to-the-previous-version).
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
