# Deploy to production

This guide takes the Compose stack from the getting started guide to a server
that real users and pipelines rely on: HTTPS, host settings, your own
PostgreSQL, other platforms, sizing and upgrades.

**Audience:** Operators · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## Before you start

You need:

- a running stack from the [getting started guide](getting-started.md), and
- the DNS name your users will open, for example `scanopsis.example.com`.

Run every `docker compose` command in `deploy/compose`. The host names in this
guide (`scanopsis.example.com`, `gitlab.example.com`, `db.example.com`) are
placeholders. Commands marked **example** need systems outside your machine,
such as public DNS or a cluster, and were not run for this guide. Everything
else was run on Docker Desktop with Docker 29.9.3 and Compose 5.7.0. Names,
times and addresses in your output will differ.

This is what you build:

```text
users and CI jobs -> reverse proxy (HTTPS, port 443) -> Scanopsis (HTTP, port 8080) -> PostgreSQL
```

## What the Compose files give you

`deploy/compose` holds three files:

| File | What it is |
|---|---|
| `compose.yaml` | The `db` (PostgreSQL 18) and `scanopsis` services. |
| `.env.example` | Settings to copy into `.env`. |
| `compose.external-db.yaml` | An add-on that removes the `db` service, for [an existing PostgreSQL server](#use-an-existing-postgresql). |

Scanopsis publishes no container image. You build it from a version branch
with `make image` in the repository root, see
[install the newest version](versions.md#install-the-newest-version).

`compose.yaml` comes with safe defaults:

| Setting | Default | How to change it |
|---|---|---|
| Published address and port | `127.0.0.1:8080` | `SCANOPSIS_BIND` and `SCANOPSIS_PORT` in `.env`. The container always listens on 8080. |
| Restart | `unless-stopped` for both services | `restart:` in `compose.yaml`. |
| Container logs | `json-file`, 5 files of 10 MB per service | The `x-logging` block at the top. |
| Memory | 512 MB for `scanopsis`, with `GOMEMLIMIT=400MiB` | `mem_limit` and `GOMEMLIMIT`. |
| Process limit | 100 for `scanopsis`, 200 for `db` | `pids_limit`. |
| Stop grace period | 30 seconds for `scanopsis`, 2 minutes for `db` | Scanopsis finishes running requests within 15 seconds; the database needs time for a clean shutdown. `stop_grace_period`. |
| Data | The folders `data/postgres` and `backups` | The `volumes:` lines of `db` and `scanopsis`. |
| Root file system | Read-only for both services | The database writes only to `data/postgres` and two `tmpfs` mounts. |

Both services also drop all Linux capabilities (the database keeps the five
its start script needs) and set `no-new-privileges`. The database sits on the
`backend` network, which has no route out. Only `scanopsis` is also on
`frontend`, which reaches the published port and GitLab.

## Host settings

Go through these once. They are about the host and `compose.yaml`, not about
Scanopsis settings.

### Open only the connections Scanopsis needs

Scanopsis connects to PostgreSQL, to your GitLab (sign-in and the keys that
check CI tokens) and, if you use it, to your SonarQube. Nothing else. It does
not check for updates, send telemetry or download anything while it runs.
Only the machine that builds the image needs the internet. Your firewall can
deny everything else.

### Start Scanopsis after a reboot

Both services restart on their own (`restart: unless-stopped`) as long as the
Docker daemon starts at boot. On Linux with systemd, check that it does
(example, needs `sudo`):

```sh
systemctl is-enabled docker
sudo systemctl enable docker
```

Check the policy that Compose set:

```sh
docker inspect "$(docker compose ps -q scanopsis)" --format '{{.HostConfig.RestartPolicy.Name}}'
```

```text
unless-stopped
```

A container you stopped yourself with `docker compose stop` stays stopped
after a reboot.

### Change the published address or port

Set `SCANOPSIS_BIND` and `SCANOPSIS_PORT` in `.env`, then run
`docker compose up -d`. For example, with `SCANOPSIS_PORT=28691`:

```sh
docker compose ps --format 'table {{.Service}}\t{{.Ports}}'
```

```text
SERVICE     PORTS
db          5432/tcp
scanopsis   127.0.0.1:28691->8080/tcp
```

Keep the loopback address when the proxy runs on the same host. If the proxy
runs on another machine, set `SCANOPSIS_BIND` to the host's private address,
never to `0.0.0.0`. Scanopsis speaks plain HTTP, and anyone who can reach the
port can skip the proxy and its limits.

### Keep logs from filling the disk

`compose.yaml` already rotates the logs of both services. Check it:

```sh
docker inspect "$(docker compose ps -q scanopsis)" --format '{{.HostConfig.LogConfig}}'
```

```text
{json-file map[max-file:5 max-size:10m]}
```

To keep more, change `max-size` and `max-file` in the `x-logging` block and
run `docker compose up -d`. Compose recreates the containers and their old
log files go with them, so ship the logs to your log system first if you need
them.

### Show local time in the log

The container runs in UTC. To log in another time zone, add `TZ` to `.env`
and run `docker compose up -d`:

```text
TZ=Europe/Berlin
```

```text
{"time":"2026-11-22T12:14:46.852206508+01:00","level":"INFO","msg":"scanopsis listening","addr":"[::]:8080"}
```

Only the log changes. The database stores points in time, so the data is the
same in every time zone.

### Check the memory and process limits

`compose.yaml` gives Scanopsis 512 MB of memory and tells Go to clean up
before it gets there (`GOMEMLIMIT=400MiB`, about 20% below). It also caps the
number of processes, so a runaway process cannot exhaust the host:

```sh
docker inspect "$(docker compose ps -q scanopsis)" --format 'memory={{.HostConfig.Memory}} pids={{.HostConfig.PidsLimit}}'
```

```text
memory=536870912 pids=100
```

If Docker kills the container for lack of memory,
`docker inspect "$(docker compose ps -q scanopsis)" --format '{{.State.OOMKilled}}'`
prints `true`. Raise `mem_limit` and keep `GOMEMLIMIT` about 20% below it.
[Sizing](#sizing) has the measured figures.

## Reverse proxy and TLS

Scanopsis speaks plain HTTP. A reverse proxy in front of it handles TLS
(HTTPS). Set `SCANOPSIS_PUBLIC_URL` to the HTTPS address of the proxy. Then:

- the session cookie gets the `Secure` attribute and the `__Host-` prefix,
- the server sends `Strict-Transport-Security` (HSTS), and
- GitLab and the CI jobs use the same address, because the sign-in redirect
  and the token audience are built from it.

Every proxy must:

- pass the `Host` header, the path and the query unchanged,
- set `X-Forwarded-For` to the client's address, and
- serve Scanopsis at the root of its own host name, such as
  `https://scanopsis.example.com/`. A port is fine. A sub-path such as
  `https://example.com/scanopsis/` does not work: the web interface loads its
  files from `/assets/...`, and the public URL cannot contain a path.

Pick Caddy or nginx below. Both were run on a local machine with a test
certificate. In production, use a certificate from your certificate authority.

### Make a test certificate

Skip this in production. In `deploy/compose`:

```sh
mkdir tls
openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -keyout tls/key.pem -out tls/cert.pem \
  -subj "/CN=scanopsis.example.com" \
  -addext "subjectAltName=DNS:scanopsis.example.com"
```

Only you can read `tls/key.pem`. The proxy containers run as root, so they can
read it too.

### Use Caddy

Caddy is the shortest way to HTTPS. Pin the image by digest so you run the
proxy you tested. The digest below is `caddy:2` version 2.11.9. To look up the
current one, run `docker pull caddy:2` and
`docker image inspect caddy:2 --format '{{index .RepoDigests 0}}'`.

1. Create `Caddyfile` next to `compose.yaml`:

   ```text
   scanopsis.example.com {
   	tls /etc/caddy/tls/cert.pem /etc/caddy/tls/key.pem
   	encode zstd gzip
   	reverse_proxy scanopsis:8080
   }
   ```

   `encode` compresses the web interface and the JSON answers, which
   Scanopsis does not do itself. Caddy replaces any `X-Forwarded-For` a client
   sends, so nobody can fake an address through it.

   With public DNS, delete the `tls` line and Caddy gets and renews a
   certificate on its own. That needs ports 80 and 443 reachable from the
   internet (example).

2. Create `compose.override.yaml` next to `compose.yaml`. Compose reads it on
   its own:

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

   The fixed address lets you tell Scanopsis which proxy to trust. Pick a
   range that does not overlap your own networks. Port 8443 on the loopback
   address is for testing; publish `"443:443"` to serve users.

3. In `.env`, set the public URL and the proxy's address:

   ```text
   SCANOPSIS_PUBLIC_URL=https://scanopsis.example.com
   SCANOPSIS_TRUSTED_PROXIES=172.30.0.10
   ```

4. Start everything with `docker compose up -d`, then check:

   ```sh
   docker compose ps --format 'table {{.Service}}\t{{.Status}}'
   ```

   ```text
   SERVICE     STATUS
   caddy       Up 13 seconds
   db          Up 13 seconds (healthy)
   scanopsis   Up 8 seconds (healthy)
   ```

If Compose says the address pool overlaps another network, pick another
`subnet` and a matching `ipv4_address`.

### Use nginx

nginx needs a few lines that Caddy does not: a larger upload size, a name
lookup that survives container restarts, the forwarded address and
compression.

1. Create `nginx.conf` next to `compose.yaml`:

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

       # Compress the web interface and the JSON answers (HTML is always included).
       gzip on;
       gzip_types text/css text/javascript application/javascript application/json image/svg+xml;

       # These apply to every location below that sets none of its own.
       proxy_set_header Host $host;
       proxy_set_header X-Forwarded-For $remote_addr;
       proxy_read_timeout 90s;

       location / {
           proxy_pass $upstream;
       }
   }
   ```

   `X-Forwarded-For $remote_addr` replaces whatever the client sent. Use
   `$proxy_add_x_forwarded_for` only if you also list the proxies in front of
   nginx in `SCANOPSIS_TRUSTED_PROXIES`.

2. Use this `compose.override.yaml` instead of the Caddy one:

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

3. Set `SCANOPSIS_TRUSTED_PROXIES=172.30.0.11` in `.env` and run
   `docker compose up -d`. Check the configuration:

   ```sh
   docker compose exec -T nginx nginx -t
   ```

   ```text
   nginx: the configuration file /etc/nginx/nginx.conf syntax is ok
   nginx: configuration file /etc/nginx/nginx.conf test is successful
   ```

After you edit `nginx.conf`, run `docker compose restart nginx`. A reload
(`nginx -s reload`) can miss the change when your editor replaces the file
instead of changing it in place.

### Check the proxy

The commands use the Caddy port 8443; for nginx use 9443. `--resolve` sends
the request for `scanopsis.example.com` to your machine without a DNS entry,
and `--cacert` trusts the test certificate.

1. **HTTPS works:**

   ```sh
   curl -sS --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     https://scanopsis.example.com:8443/readyz
   ```

   ```text
   {"status":"ready"}
   ```

   Without `--cacert`, curl refuses the certificate with
   `curl: (60) SSL certificate problem: self signed certificate`. That proves
   the connection is checked.

2. **The security headers are there:**

   ```sh
   curl -sSI --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     https://scanopsis.example.com:8443/healthz | grep -i -E '^(HTTP|strict|x-frame)'
   ```

   ```text
   HTTP/2 200
   strict-transport-security: max-age=63072000; includeSubDomains
   x-frame-options: DENY
   ```

   No `strict-transport-security`? Then `SCANOPSIS_PUBLIC_URL` does not start
   with `https://`.

3. **Scanopsis sees the real client address.** It believes `X-Forwarded-For`
   only from a proxy listed in `SCANOPSIS_TRUSTED_PROXIES`. Send a request
   with a fake address and read what Scanopsis logged:

   ```sh
   curl -sS --resolve scanopsis.example.com:8443:127.0.0.1 --cacert tls/cert.pem \
     -H 'X-Forwarded-For: 203.0.113.9' https://scanopsis.example.com:8443/healthz
   docker compose logs scanopsis | grep '"client_ip"' | grep -v '"127.0.0.1"' | tail -1
   ```

   With `SCANOPSIS_TRUSTED_PROXIES` empty, the log shows the proxy's address
   for every user, and all users share one rate limit:

   ```text
   {"time":"2026-11-22T11:13:35.975316503Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.30.0.10"}
   ```

   With the proxy listed, the start-up line shows `"trusted_proxies":1`, and
   `client_ip` is the address the proxy saw: `172.30.0.1` (the network gateway)
   on Docker Desktop, the real client on a Linux server. The fake
   `203.0.113.9` is never used.

4. **Large uploads pass.** Without `client_max_body_size`, nginx answers a
   3 MB upload itself with `413` and an HTML page, not JSON from Scanopsis:

   ```text
   <html>
   <head><title>413 Request Entity Too Large</title></head>
   ...
   HTTP 413
   ```

   With `client_max_body_size 100m;` the upload reaches Scanopsis. Scanopsis
   accepts reports of up to 64 MiB after decompression, so keep the proxy limit
   above your largest compressed report.

5. **Answers are compressed.** Ask for the main script of the web interface
   (its name changes with each build, so read it from the page):

   ```sh
   ASSET=$(curl -s http://127.0.0.1:8080/ | grep -o '/assets/index-[^"]*\.js' | head -1)
   curl -sI -H 'Accept-Encoding: gzip' --resolve scanopsis.example.com:8443:127.0.0.1 \
     --cacert tls/cert.pem https://scanopsis.example.com:8443$ASSET | grep -i content-encoding
   ```

   ```text
   content-encoding: gzip
   ```

   The script shrank from 278,866 bytes to 89,238 through Caddy and about
   100,900 through nginx. Small JSON answers such as `/readyz` stay
   uncompressed, which is normal.

If a check fails, see [reverse proxy problems](troubleshooting.md#reverse-proxy-and-tls).

### Add rate limits and lock down the webhook (optional)

Scanopsis already limits sign-in, setup and upload requests per client
address. A limit at the proxy adds a second layer and keeps floods away from
the application. These nginx lines were checked with `nginx -t` and run
against the stack. Replace `203.0.113.20` with the address SonarQube calls
from.

Add this at the top of `nginx.conf`, outside the `server` block:

```nginx
limit_req_zone $binary_remote_addr zone=scanopsis_auth:10m rate=10r/m;
limit_req_zone $binary_remote_addr zone=scanopsis_upload:10m rate=2r/s;
```

Inside the `server` block, after `proxy_read_timeout`, add these lines above
`location /`:

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

Runners behind one NAT address share one upload limit, so keep it generous.
After `docker compose restart nginx`, a webhook call from any other address
gets `403`, and 13 wrong sign-ins in a row from one address look like this:

```text
401 401 401 401 401 401 401 401 401 401 429 429 429
```

The first ten reach Scanopsis (`401`), then nginx answers `429`. The exact
count depends on timing, because the allowance refills while you test.

The webhook is also protected by its HMAC signature; the address rule stops
everyone else from even trying. If another proxy stands in front of nginx,
nginx sees that proxy's address: use the `realip` module, or restrict at the
outer proxy.

Caddy's standard build has no rate limit module. It can still restrict the
webhook. Put this above `reverse_proxy` in the site block:

```text
	@sonarqube_webhook_denied {
		path /api/v1/hooks/sonarqube
		not remote_ip 203.0.113.20
	}
	respond @sonarqube_webhook_denied 403
```

`GET /api/v1/version` needs no sign-in and tells anyone which version you run.
To hide it, restrict it the same way, for example with
`allow 10.0.0.0/8; deny all;` in a `location = /api/v1/version` block.

### Let backups pass the proxy

Backups cross the proxy in two ways:

- **Uploads** go in parts of at most 8 MiB, one request each, so the proxy
  limit `client_max_body_size 100m;` above is plenty. A proxy that limits the
  body to less than 8 MiB (nginx allows 1 MiB by default) refuses the parts.
- **Downloads** can be large files. Scanopsis serves them with `Range`
  support and keeps a slow download going as long as it makes progress. nginx
  buffers a proxied answer by default, which can fill its temporary folder
  with a big file when the client is slow. For the download route you can turn
  that off:

  ```nginx
  location ~ ^/api/v1/admin/backups/[^/]+/download$ {
      proxy_pass $upstream;
      proxy_buffering off;
  }
  ```

  `proxy_max_temp_file_size 0;` is the other option: nginx then never writes
  the answer to disk. A test proxied a 400 MB file through nginx 1.29 to a
  client limited to 5 MB/s, once with the default settings and once with
  `proxy_buffering off`. Both downloads were complete, and a `Range` request
  answered `206`. That test used a plain file server behind nginx, not
  Scanopsis, and no temporary file showed up. So the settings above
  are a precaution that you do not need to apply to get a working download.
  Watch the disk of the proxy during the first large download if you keep the
  default.

### Set proxy timeouts

Scanopsis reads a request within 30 seconds and writes the answer within 60.
A Trivy upload gets longer: up to 30 seconds to wait for memory, then 5
minutes to send its body, then 60 seconds for the answer. A late body gets
`408`. A 20 MB report crosses a local network in well under a second, so only
slow links come near these limits.

- **nginx** reads the whole body before it passes the request on, so a slow
  client never holds Scanopsis. Keep `proxy_read_timeout` above the time an
  upload takes to store, such as the 90 seconds above.
- **Caddy** streams the body and has no timeout of its own by default, so the
  limits of Scanopsis apply.

## Trust an internal certificate authority

If GitLab or SonarQube uses a certificate from your own certificate authority
(CA), Scanopsis cannot check it, and GitLab sign-in and every upload fail.
Scanopsis never turns certificate checks off. Give it your CA instead.

1. Put the CA certificate (the root, plus any intermediate the server does not
   send) in PEM format in one file, `ca.pem`, next to `compose.yaml`.
2. Mount it. In `compose.override.yaml`, add under `services:`:

   ```yaml
     scanopsis:
       volumes:
         - ./ca.pem:/etc/scanopsis/ca.pem:ro
   ```

3. In `.env`, set `SCANOPSIS_CA_FILE=/etc/scanopsis/ca.pem` and run
   `docker compose up -d`.
4. Check that it was read. The start-up line shows `"custom_ca":true`:

   ```sh
   docker compose logs scanopsis | grep 'scanopsis starting' | tail -1
   ```

   ```text
   ...,"config":{"listen_addr":":8080","public_url":"https://scanopsis.example.com","seeded_settings":3,"trusted_proxies":1,"custom_ca":true}}
   ```

The CA applies to GitLab and SonarQube, not to the database (see
[step 2](#step-2-write-the-database-url) below for that).

**How to spot the problem.** Without the CA, the log says
`certificate signed by unknown authority`. For GitLab sign-in it looks like
this:

```text
{"time":"2026-11-22T11:14:22.743969677Z","level":"WARN","msg":"GitLab discovery failed","error":"Get \"https://gitlab.example.com/.well-known/openid-configuration\": tls: failed to verify certificate: x509: certificate signed by unknown authority"}
```

For an upload, the CI job gets `503` with the code `gitlab_unavailable`, and
the log line `upload rejected` has the same TLS error in its `cause` field.

To test the connection yourself, run a throwaway container on the same
network (`<project>_frontend`). The `caddy` image is used only because its
`wget` prints TLS errors; without the CA it fails with
`certificate verify failed`:

```sh
docker run --rm --network scanopsis_frontend caddy:2 wget -q -O- https://gitlab.example.com/
```

If the file is missing or holds no certificate, the server does not start:

```text
"error":"read SCANOPSIS_CA_FILE: open /etc/none.pem: no such file or directory"
"error":"SCANOPSIS_CA_FILE /etc/scanopsis/ca.pem contains no PEM certificates"
```

## Use an existing PostgreSQL

Use this when a managed service or a shared server runs PostgreSQL for you and
you do not want the `db` container. Scanopsis was tested with PostgreSQL 18. It
needs no extension and no superuser. The steps below were run against a
separate PostgreSQL 18 server with TLS and a certificate from a private CA.
The host name and the password are examples.

### Step 1. Create the role and the database

As an administrator of the server, create a role that can log in and a
database it owns:

```sql
CREATE ROLE scanopsis LOGIN PASSWORD 'p@ss/w:rd#1?x%';
CREATE DATABASE scanopsis OWNER scanopsis;
```

Scanopsis creates and changes its own tables when it starts. In the schema it
uses (`public` unless your URL sets another `search_path`) the role needs
`CREATE`, and it owns the tables it creates. Owning the database is the
easiest way to get this: since PostgreSQL 15, the owner of a database may
create objects in its `public` schema. Check it as the new role:

```sql
SELECT current_user, has_schema_privilege('public', 'CREATE') AS can_create_in_public;
```

```text
 current_user | can_create_in_public
--------------+----------------------
 scanopsis    | t
```

If the role cannot create tables, Scanopsis stops at start:

```text
"error":"read migration status: failed to initialize: create version table: failed to create version table \"goose_db_version\": ERROR: permission denied for schema public (SQLSTATE 42501)"
```

Fix it as an administrator in that database, then start Scanopsis again:

```sql
GRANT CREATE ON SCHEMA public TO scanopsis;
```

Connect directly, or through a pooler in session mode. Scanopsis holds
session-level advisory locks while it migrates and in background jobs. A
pooler in transaction mode, such as PgBouncer with `pool_mode = transaction`,
does not keep them. This follows from the code and was not tested.

Backups and restores need more from the role. A restore drops and creates the
Scanopsis tables, so the role in `secrets/database_url` must **own every
Scanopsis table**. Owning the database is enough when Scanopsis created the
tables. A table owned by another role, even a group role the user belongs to,
makes a restore refuse with a message naming the table. A restore also
refuses when another database object, such as a view or foreign key of another
application, depends on a Scanopsis table. It touches no other table. A
restore needs room in PostgreSQL for the old and the new data until it
commits.

### Step 2. Write the database URL

The URL goes in `secrets/database_url`, like in
[step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets).
You do not need `secrets/postgres_password`.

Always encrypt the connection. `sslmode` decides how:

| `sslmode` | What it does |
|---|---|
| `disable` | No TLS. Only for the bundled database on the private Compose network. |
| `require` | Encrypts, but does not check who answers. Stops eavesdropping, not a server that pretends to be yours. |
| `verify-full` | Encrypts, checks the certificate chain and checks that the host name is in the certificate. Use this in production. |

With `verify-full` and a certificate from your own CA, add `sslrootcert=` with
the path of the CA file inside the container. Managed services publish their
CA certificate. Save it as `db-ca.pem` next to `compose.yaml`. A certificate
from a public CA needs no `sslrootcert`.

The characters `@`, `/`, `:`, `?`, `#` and `%` in the password break the URL,
so percent-encode the password. Either command works (the second needs `jq`):

```sh
python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' 'p@ss/w:rd#1?x%'
jq -rn --arg p 'p@ss/w:rd#1?x%' '$p|@uri'
```

```text
p%40ss%2Fw%3Ard%231%3Fx%25
p%40ss%2Fw%3Ard%231%3Fx%25
```

Write the URL. The host name must be one that is in the server's certificate:

```sh
printf 'postgres://scanopsis:%s@db.example.com:5432/scanopsis?sslmode=verify-full&sslrootcert=/etc/scanopsis/db-ca.pem' \
  'p%40ss%2Fw%3Ard%231%3Fx%25' > secrets/database_url
chmod 644 secrets/database_url
```

Two mistakes stop the server at start. A password that is not encoded:

```text
"error":"parse database URL: cannot parse `postgres://scanopsis:xxxxx@ss/w:xxxxxx@db.example.com:5432/scanopsis?sslmode=require`: failed to parse as URL (invalid percent-encoded token: \"x%@db.example.com:5432/scanopsis?sslmode\")"
```

And `verify-full` with a private CA but no `sslrootcert` (`SCANOPSIS_CA_FILE`
does not help here):

```text
"error":"connect to database: failed to connect to `user=scanopsis database=scanopsis`: 172.30.0.3:5432 (db.example.com): failed to write startup message: write failed: tls: failed to verify certificate: x509: certificate signed by unknown authority"
```

### Step 3. Remove the bundled database

`compose.external-db.yaml` removes the `db` service and its password secret,
and puts Scanopsis on the `frontend` network only, which can reach
your server. It needs Docker Compose 2.24 or later.

1. If you use `sslrootcert`, open `compose.external-db.yaml` and uncomment the
   two `volumes` lines, so Compose mounts `db-ca.pem` at
   `/etc/scanopsis/db-ca.pem`.
2. Tell Compose to use both files, so every `docker compose` command picks them
   up. Add this line to `.env`:

   ```text
   COMPOSE_FILE=compose.yaml:compose.external-db.yaml
   ```

   Compose reads `compose.override.yaml` on its own only when you name no
   files, so add it to the list if you have one:
   `COMPOSE_FILE=compose.yaml:compose.override.yaml:compose.external-db.yaml`.
3. Check that `db` is gone from the list of services:

   ```sh
   docker compose config --services
   ```

   ```text
   scanopsis
   ```

4. Start Scanopsis. `--remove-orphans` removes the old `db` container (not its
   volume); without it, Compose only warns about `orphan containers`:

   ```sh
   docker compose up -d --remove-orphans
   ```

   ```text
    Container scanopsis-scanopsis-1 Running
    Container scanopsis-db-1 Stopping
    Container scanopsis-db-1 Stopped
    Container scanopsis-db-1 Removing
    Container scanopsis-db-1 Removed
   ```

5. Check that it is ready:

   ```sh
   curl -s http://127.0.0.1:8080/readyz
   ```

   ```text
   {"status":"ready"}
   ```

6. Check that the connection is encrypted. As an administrator of the server:

   ```sql
   SELECT usename, ssl, version FROM pg_stat_ssl s JOIN pg_stat_activity a USING (pid) WHERE usename = 'scanopsis' LIMIT 1;
   ```

   ```text
     usename  | ssl | version
   -----------+-----+---------
    scanopsis | t   | TLSv1.3
   ```

The folder `data/postgres` with the bundled data stays on the host. Delete it
only once you are sure you moved everything you need.

### Move your data from the bundled database

To keep users, settings and scans, [back up](operations.md#back-up-the-database)
the bundled database first, then restore the file into the new server before
step 4 above (or while Scanopsis is stopped). The restore needs a PostgreSQL
client of the same major version or newer. This runs it from the PostgreSQL
image, on a Docker network that can reach the server:

```sh
docker run --rm -i --network scanopsis_frontend \
  -e PGPASSWORD='p@ss/w:rd#1?x%' -e PGSSLMODE=require \
  postgres:18.7-alpine pg_restore -h db.example.com -U scanopsis -d scanopsis \
  --no-owner --exit-on-error < scanopsis-2026-11-22.dump
echo "restore exit=$?"
```

```text
restore exit=0
```

Keep the same `secrets/secret_key`. When Scanopsis starts on the new database,
its log has no `setup_token` line, because the restored database already has
an administrator.

## Run without Compose

Compose is the supported way, but the container does not depend on it. It
needs a database URL, a secret key and a public URL. The rules are the same
everywhere:

- **Run one instance.** Scanopsis keeps rate limits and the setup token in
  memory, and two versions must never run on one database while the new one
  migrates it.
- **Pass the database URL and the key as files** (`SCANOPSIS_DATABASE_URL_FILE`
  and `SCANOPSIS_SECRET_KEY_FILE`), not as environment variables, which anyone
  with `docker inspect` can read. Setting both forms of one variable stops the
  server: `SCANOPSIS_SECRET_KEY and SCANOPSIS_SECRET_KEY_FILE are both set; set only one`.
- **Keep it locked down.** The image runs as user 65532. Add a read-only root
  file system and drop all capabilities; Scanopsis writes nothing to disk.
- **No migration step.** Scanopsis applies schema changes before it listens.

### Use plain `docker run`

These commands start a database and Scanopsis on one network, with the same
limits as the Compose file. Create the three secret files first, like in
[step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets), but use the host name `scanopsis-db` instead of `db`
in `secrets/database_url`. The names are examples.

```sh
# 70 is the user the PostgreSQL image runs as; only it may open the database files.
docker network create scanopsis-net
sudo install -d -m 700 -o 70 -g 70 data/postgres
sudo install -d -m 700 -o 65532 -g 65532 backups

docker run -d --name scanopsis-db --network scanopsis-net \
  -e POSTGRES_USER=scanopsis -e POSTGRES_DB=scanopsis \
  -e POSTGRES_PASSWORD_FILE=/run/secrets/postgres_password \
  -v "$PWD/secrets/postgres_password:/run/secrets/postgres_password:ro" \
  --stop-timeout 120 --shm-size 256m \
  -v "$PWD/data/postgres:/var/lib/postgresql" \
  postgres:18.7-alpine@sha256:ff64c9e3bdf8e2120107d3559de8db314e8305f7e38c5105903c6295876738ff

docker run -d --name scanopsis --network scanopsis-net \
  --restart unless-stopped \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --pids-limit 100 --memory 512m --stop-timeout 30 \
  --log-opt max-size=10m --log-opt max-file=5 \
  -p 127.0.0.1:8080:8080 \
  -e SCANOPSIS_PUBLIC_URL=https://scanopsis.example.com \
  -e SCANOPSIS_DATABASE_URL_FILE=/run/secrets/database_url \
  -e SCANOPSIS_SECRET_KEY_FILE=/run/secrets/secret_key \
  -v "$PWD/secrets/database_url:/run/secrets/database_url:ro" \
  -v "$PWD/secrets/secret_key:/run/secrets/secret_key:ro" \
  -v "$PWD/backups:/var/lib/scanopsis/backups" \
  scanopsis:v0.4.0
```

The image already sets `GOMEMLIMIT=400MiB`, which suits `--memory 512m`. Wait
about 30 seconds, then check:

```sh
docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
curl -s http://127.0.0.1:8080/readyz
```

```text
NAMES          STATUS                    PORTS
scanopsis      Up 29 seconds (healthy)   127.0.0.1:8080->8080/tcp
scanopsis-db   Up 30 seconds             5432/tcp
{"status":"ready"}
```

Unlike Compose, `docker run` does not wait for the database. If Scanopsis
starts first, it exits, and `--restart unless-stopped` starts it again until
the database answers.

The container has no shell. Run the
[administration commands](operations.md#administration-commands) with
`docker exec scanopsis /scanopsis admin ...`.

### Use Podman

Podman runs the same image and accepts the same `run` options. This was not
tested, so treat these notes as a starting point:

- Instead of bind-mounted files you can use Podman secrets, for example
  `podman secret create scanopsis_secret_key secrets/secret_key` and
  `--secret scanopsis_secret_key,target=/run/secrets/secret_key,mode=0444`.
  User 65532 must be able to read them.
- Rootless Podman maps container users to your sub-user IDs, so bind-mounted
  files need the `U` option (`:ro,U`).
- To start at boot, use a Quadlet unit (systemd) instead of `--restart`.

### Use Kubernetes

Scanopsis needs no operator and no custom resources. Run it as one pod:

- A `Deployment` with `replicas: 1` and `strategy: Recreate`, which stops the
  old pod before the new one starts. A rolling update would run two versions
  on one database while the new one migrates it.
- A `Secret` with `database_url` and `secret_key`, mounted as files. Create it
  from the same files as for Compose (example). Do not commit its YAML: the
  values are only base64 encoded.

  ```sh
  kubectl create secret generic scanopsis \
    --from-file=database_url=secrets/database_url \
    --from-file=secret_key=secrets/secret_key
  ```

- Probes: `/healthz` for liveness (the process runs) and `/readyz` for
  readiness (the database answers). A startup probe gives the first start of a
  new version, with its migrations, time.
- A `terminationGracePeriodSeconds` of 30, above the 15 seconds Scanopsis
  needs to finish its requests and the time a running backup needs to close
  its file.
- A second `PersistentVolumeClaim` for the backups, mounted at
  `/var/lib/scanopsis/backups`. `fsGroup: 65532` makes the volume writable for
  the user that runs Scanopsis. Keep the backups on storage that is not the
  database's own, and copy them off the cluster.
- An Ingress or Gateway that follows the [proxy rules](#reverse-proxy-and-tls):
  `Host` unchanged, `X-Forwarded-For` set, a body of up to 100 MB, Scanopsis at
  the root of its own host name. Set `SCANOPSIS_TRUSTED_PROXIES` to the
  addresses of the ingress controller pods.
- PostgreSQL outside the pod, see
  [use an existing PostgreSQL](#use-an-existing-postgresql).

The manifest below follows this model. Replace the image with one you built
and pushed to a registry the cluster can pull from, and the public URL. It
passes `kubectl apply --dry-run=client` with kubectl 1.37.2, but it was not
run on a cluster, so check the probes and the first start in yours.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: scanopsis-backups
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 20Gi
---
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
      terminationGracePeriodSeconds: 30
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: scanopsis
          image: registry.example.com/platform/scanopsis:v0.4.0
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
            - name: backups
              mountPath: /var/lib/scanopsis/backups
      volumes:
        - name: secrets
          secret:
            secretName: scanopsis
            defaultMode: 0440
        - name: backups
          persistentVolumeClaim:
            claimName: scanopsis-backups
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

A CPU limit can slow uploads down, so set one only if your cluster requires
it. After `kubectl apply`, `kubectl rollout status deployment/scanopsis`
should end with the pod `1/1` ready (example).

## Sizing

The figures below are for up to 500 services and 3,000 pipelines a day. They
were measured with PostgreSQL 18.7 on one Docker Desktop machine during the
work on version 0.2.0. Measure again on your hardware if you get close to a
limit.

### Memory

Give the Scanopsis container **512 MiB** with `GOMEMLIMIT=400MiB`.
`compose.yaml` sets both, and the image sets `GOMEMLIMIT` on its own. For
another limit, set `GOMEMLIMIT` to about 80% of it. Reserve **1 GiB or more**
for PostgreSQL, so it can keep its working set in memory.

Apart from small caches, Scanopsis holds only the uploads in progress. An
upload is kept compressed, and its findings are streamed into the database in
batches, so its memory follows the size of the report, not the number of
findings. The test used the largest report the default limits allow: 60 MiB
of JSON with 200,000 findings, sent as 10 MiB of gzip. The peak is the
process's highest resident memory (`VmHWM` in `/proc/<pid>/status`);
`docker stats` samples once a second and misses it.

| Load | Peak memory |
|---|---|
| Idle, a minute after start | 19.5 MiB |
| One maximum upload | 81 MiB |
| Eight maximum uploads at once | 256 MiB, back to 27 MiB afterwards |
| Eight worst-case uploads at once (43 MiB of gzip that does not compress further) | 220 MiB |
| Eight maximum uploads without gzip (60 MiB each) | 98 MiB, processed one at a time |
| Eight maximum uploads with a 256 MiB limit and `GOMEMLIMIT=200MiB` | 129 MiB (worst case 207 MiB) |

Every upload was stored with `201` and no container was killed. One maximum
upload takes about 3 seconds from request to answer. Eight at once take 7
seconds with gzip, 15 for the worst case and 25 without gzip, because uploads
wait for memory. 256 MiB is the smallest limit that survived, with little room
to spare, so use 512 MiB.

At 3,000 uploads a day, two arrive per minute on average, so memory stays
close to the idle value.

### How uploads share memory

Uploads use a memory budget of two fifths of `GOMEMLIMIT`: 160 MiB with the
default.

- **Admission by size.** Scanopsis estimates an upload at twice its
  `Content-Length` plus 10 MiB. A request without a length counts as the
  largest allowed. Uploads run together as long as their estimates fit the
  budget, so a 10 MiB gzip upload (30 MiB) runs five at a time, and the worst
  case above (96 MiB) one at a time.
- **Small reports go first.** An upload that fits the free memory starts at
  once, even when a larger one waits. Once the oldest waiting upload has waited
  5 seconds, newer ones queue behind it so it is not starved. Reports of up to
  4 MiB, which most are, still pass whenever they fit.
- **Waiting is bounded.** An upload that does not fit within 30 seconds gets
  `503` with the code `busy` and `Retry-After: 5`. The CI script in the
  [GitLab CI guide](integrations/gitlab-ci.md) retries it.
- **A quarter of the database pool.** At most a quarter of the database
  connections (at least 2) store uploads at the same time, so uploads never
  take every connection. An upload that waits 30 seconds for one also gets
  `503 busy`.
- **Memory goes back.** After a burst of large uploads, Scanopsis returns the
  memory to the operating system and closes the database connections that
  sent big reports.
- **Slow links are fine.** An upload has 5 minutes to send its body and holds
  only the memory its own report needs while it does. 10 MiB sent at 200 KB/s
  (52 seconds) was stored with `201`.

A report may hold at most 200,000 findings. The largest is stored in about 3
seconds.

### Upload limits per address and project

- **Per client address:** 60 uploads at once, then one a second. Runners behind
  one NAT address share it, so more than 60 pipelines that finish together
  from one address get `429`.
- **Per GitLab project:** 100 uploads at once, then one every 10 seconds (360
  an hour), whatever the runner addresses. A monorepo that scans 30 images
  uploads 31 reports per pipeline: three pipelines can finish together, and 11
  an hour can run all day. Above that, the job gets `429` with
  `project_rate_limited`.
- **Per job:** at most 50 different artifacts (`job_artifact_limit`).

The [GitLab CI guide](integrations/gitlab-ci.md) explains these limits from
the pipeline side.

### When the server is busy

Scanopsis serves as many API requests at once as its pool has database
connections: 16 by default, set with `pool_max_conns` in the database URL (see
[database URL parameters](configuration.md#database-url-parameters)). A request that finds every slot
taken waits up to 3 seconds. If none frees up, it gets `503` with the code
`busy` and `Retry-After: 2`, and the log has the warning
`request rejected: too many concurrent requests`.

Some rules keep one client from crowding out the rest:

- **One address, a quarter of the slots.** One client address (an IPv4
  address, or an IPv6 `/64`) holds at most 4 of 16 slots. Behind a proxy that
  is not in `SCANOPSIS_TRUSTED_PROXIES`, every user has the proxy's address
  and shares that quarter, so set it.
- **Bodies are read first.** A request body is read in full before the
  request takes a slot, so a slow client never holds one. Waiting and running
  bodies may use 32 MiB together, and one address a quarter of that. Each body
  counts by its `Content-Length`, at least 64 KiB. A body must arrive within 2
  seconds or the request gets `408` (`request_timeout`). A body over 1 MiB gets
  `413` (`payload_too_large`).
- **Not counted:** `/healthz`, `/readyz`, the web interface files and the
  Trivy upload, which has its own rules (above).

`/readyz` checks the database over a connection of its own, so a busy server
stays ready and an orchestrator does not restart it. That connection is one
more than `pool_max_conns` against the database's `max_connections`. The
check runs at most once a second and callers share its result, so a hanging
database cannot pile up probes; each caller gets an answer within 2 seconds.

The integration test in `cmd/scanopsis` sends 100 requests at once from one
address against a pool of 16 whose connections are all locked: 4 were served,
96 got `503 busy` after 3.0 seconds, and `/readyz` answered in about 2 ms at
the 95th percentile throughout.

If you see `503 busy` often, the database is the bottleneck, not the pool.
Look for slow statements (`pg_stat_activity`, `pg_stat_statements`) and raise
`pool_max_conns` only if the database has capacity to spare. If your proxy
takes a server out of rotation on its first `503`, make it retry or ignore
this status instead.

### Database size

The size grows with the number of findings. Measured by uploading each report
30 times into an empty database and reading `pg_total_relation_size` of
`scans`, `vulnerabilities` and `secret_findings`:

| Report | Findings | JSON size | Database size per scan |
|---|---|---|---|
| `image-full.json` (test fixture, python:3.9-slim-buster) | 138 | 951 KB | 170.9 KiB |
| `secret-fs.json` (test fixture) | 2 secrets | 3 KB | 6.7 KiB |
| alpine:3.17.0, Trivy 0.76.2 | 92 | 589 KB | 118.4 KiB |
| node:16-alpine, Trivy 0.76.2 | 91 | 631 KB | 114.7 KiB |
| alpine:3.20, Trivy 0.76.2 | 2 | 26 KB | 13.1 KiB |

As a rule of thumb:

```text
size per image scan  ~  11 KiB + 1.2 KiB x findings
```

About two thirds of that is the compressed copy of the original report, which
Scanopsis keeps for 30 days unless the scan is current (see
[data retention](operations.md#data-retention)).

**Estimate for 90 days**, with 3,000 pipelines a day, each uploading one image
report and one secret report, and the default retention of 90 days:

| Findings per image report | Per pipeline | 90 days |
|---|---|---|
| 2 | 20 KiB | 5.5 GB |
| 30 | 54 KiB | 14.8 GB |
| 138 (an old image, like the fixture) | 182 KiB | about 50 GB |

Retention also keeps the latest scans of each dashboard branch and of
protected branches and tags beyond the period: well under 1 GB for 500
services. Size the disk for the estimate plus 50% (index growth, write-ahead
log, vacuum), and the same again for backups. The stored reports are most of every backup too, and
scheduled backups are kept for 7 days by default. Figures for how long a backup or
restore takes and how much memory they use are not measured yet; they will be
added here. Most pipelines scan less, so
your real figure is likely lower. The **Integrations** page shows your uploads
per day. Shorten `retention.days` if you need less history.

Dashboards read the counts stored on each scan and never the stored reports,
so they do not slow down as reports pile up.

## Upgrade and roll back

You deploy a new version by building its image from its version branch, see
[upgrade to the newest version](versions.md#upgrade-to-the-newest-version) for the
build steps, and [roll back to the previous version](versions.md#roll-back-to-the-previous-version)
for the way back. Schema changes run when the new version starts; there is no
separate migration step. The recorded run below
upgraded from 0.1.0 straight to 0.3.0. 0.4.0 adds no migration, so an upgrade
to 0.4.0 ends at the same schema version, 10; the version line in the output
is the one of that run, and yours shows `v0.4.0` and another commit. From
0.2.0 it looks the same, except that the schema version is already 10 and
stays 10. Before an upgrade from a version before 0.4.0, also
[move the database into a folder](versions.md#move-the-database-into-a-folder).

### Upgrade

1. **Back up the database.** From 0.4.0 on, use **Back up now** on the
   Backups page (see [operations](operations.md#backup-and-restore)). If you
   run an older version, use `pg_dump`
   (see [operations](operations.md#back-up-the-database)). Either is your only
   safe way back. The `pg_dump` command:

   ```sh
   (umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
     --format=custom > scanopsis-before-upgrade.dump)
   ```

2. **Note the schema version**, so you can see the upgrade happen:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) FROM goose_db_version"
   ```

   ```text
      6
   ```

3. **Switch the image.** Set `SCANOPSIS_IMAGE` in `.env` to the new image and
   run `docker compose up -d`. Compose recreates `scanopsis`, and `db` too if
   you took over new `db` settings from the new `compose.yaml`; the data stays
   in `data/postgres`. Scanopsis applies the migrations, then listens.
4. **Check it.** After about 30 seconds:

   ```sh
   docker compose ps --format 'table {{.Service}}\t{{.Status}}'
   curl -s http://127.0.0.1:8080/api/v1/version
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) FROM goose_db_version"
   ```

   ```text
   SERVICE     STATUS
   db          Up 24 seconds (healthy)
   scanopsis   Up 10 seconds (healthy)
   {"version":"v0.3.0","commit":"55748a2","go":"go1.27.4"}
     10
   ```

   The log has one `applying migration` and one `applied migration` line for
   each change, with its duration. Sign in: your users and scans are still
   there.

If the container does not become healthy, read `docker compose logs scanopsis`.
A failed migration stops the server with `apply migration <file>:` and the
database error. Most migrations run in a transaction, and the large index
builds can safely run again, so the next start continues where the last one
stopped. A migration that cannot lock a table within 10 seconds stops the
start the same way; start again when the database is quieter.

### Roll back

Go back to your backup. Do not start the old image on the migrated database:
the old version does not know what the new migrations changed.

1. Set `SCANOPSIS_IMAGE` in `.env` back to the old image.
2. Stop Scanopsis and restore the backup over the database:

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
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) FROM goose_db_version"
   ```

   ```text
   {"version":"v0.1.0","commit":"6eaa325","go":"go1.27.4"}
      6
   ```

Everything that changed after the backup is gone, including scans uploaded
while the new version ran; later pipelines upload again. Then do the
[checks after a restore](operations.md#check-everything-after-a-restore). The
secret key does not change in an upgrade.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
