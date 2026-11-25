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
else was run on macOS with Docker Desktop (Docker 29.9.3, Compose 5.7.1),
except the steps marked Linux. Names, times and addresses in your output
will differ.

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
(Linux, needs `sudo`, not run for this guide):

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

Check it:

```sh
docker compose logs scanopsis | grep 'scanopsis listening' | tail -1
```

```text
scanopsis-1  | {"time":"2026-11-25T05:10:14.290979715+01:00","level":"INFO","msg":"scanopsis listening","addr":"[::]:8080"}
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
       # $http_host passes the Host header unchanged, with its port.
       proxy_set_header Host $http_host;
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

   Use `$http_host`, not `$host`: `$host` drops the port. On a port other
   than 443 (such as 9443 below), a request that names the port in `Origin`
   but sends no `Sec-Fetch-Site` header, such as one from an older browser or
   a script, is then refused with `403` and the code `cross_origin`. Current
   browsers send `Sec-Fetch-Site: same-origin` over HTTPS, and Scanopsis
   accepts that without comparing `Origin` with `Host`.

2. Use this `compose.override.yaml` instead of the Caddy one. If you tried
   Caddy first, see the note after step 3 to remove its container:

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

If you tried Caddy first, `docker compose up -d` warns
`Found orphan containers (scanopsis-caddy-1)`. Remove the container with
`docker compose up -d --remove-orphans`, and its volume with
`docker volume rm scanopsis_caddy-data`.

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

   Through nginx, the first line is `HTTP/1.1 200 OK` and the header names
   start with capitals (`Strict-Transport-Security`, `X-Frame-Options`).

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
   scanopsis-1  | {"time":"2026-11-25T04:11:47.48065955Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.30.0.10"}
   ```

   With the proxy listed, the start-up line shows `"trusted_proxies":1`, and
   `client_ip` is the address the proxy saw. For this curl that is the gateway
   of the Compose network (`172.30.0.1`), on Docker Desktop and on Linux
   alike, because the proxy port is published on `127.0.0.1`. Once you
   publish `"443:443"` for your users, it depends on the host. On Linux, a
   user on another machine shows up with their own address. On Docker
   Desktop, every connection through that port, from another machine or from
   the host itself, comes from one address of Docker Desktop's virtual
   network (`192.168.65.1` here), so all users share one address and one rate
   limit. The fake `203.0.113.9` is never used.

4. **Large uploads pass.** Send 3 MB to the upload address:

   ```sh
   head -c 3000000 /dev/zero | curl -sS --resolve scanopsis.example.com:8443:127.0.0.1 \
     --cacert tls/cert.pem -H 'Content-Type: application/json' --data-binary @- \
     -w '\nHTTP %{http_code}\n' https://scanopsis.example.com:8443/api/v1/ingest/trivy
   ```

   The request reaches Scanopsis, which answers in JSON. Until the GitLab URL
   is set on the Settings page it says that uploads are not enabled:

   ```text
   {"error":{"code":"ingest_not_configured","message":"Uploads are not enabled: the GitLab URL is not configured in Scanopsis."}}

   HTTP 503
   ```

   Once the GitLab URL is set, it answers `HTTP 401` with the code
   `invalid_token`, because the request has no token.

   Through nginx without `client_max_body_size`, nginx answers itself with an
   HTML page:

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

   nginx writes the header as `Content-Encoding: gzip`. The script shrank
   from 518,340 bytes to 170,172 through Caddy and to 192,035 through nginx.
   Small JSON answers such as `/readyz` stay uncompressed, which is normal.

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

The first ten get `401` from Scanopsis. The eleventh gets `429` from
Scanopsis itself, which allows 10 sign-in attempts per address (JSON with the
code `too_many_attempts`). From the twelfth on, nginx answers `429` with an
HTML page and the request never reaches Scanopsis. The exact count depends on
timing, because both allowances refill while you test.

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
  When every part has arrived, Scanopsis checks the whole file in the
  background and the page asks for the result every few seconds. The same
  goes for the check that comes before a restore. No request waits while a
  large backup is read, so these checks need no longer proxy timeout.
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
  the answer to disk. A 200 MiB file in the backup folder was downloaded from
  Scanopsis with `curl --limit-rate 5M`: through nginx 1.30 with the default
  settings, through nginx with `proxy_buffering off`, and through Caddy 2.11.
  Each download was complete (209,715,200 bytes in 40 seconds, the same bytes
  as the file), a `Range` request for the second half answered `206`, and
  nginx kept no temporary file open while it ran. So the settings above are a
  precaution that you do not need to apply to get a working download. Watch
  the disk of the proxy during the first large download if you keep the
  default.

### Set proxy timeouts

Scanopsis reads a request within 30 seconds and writes the answer within 60.
A Trivy upload gets longer: up to 30 seconds to wait for memory, then 5
minutes to send its body, then 60 seconds for the answer. A late body gets
`408`. A 20 MB report crosses a local network in well under a second, so only
slow links come near these limits.

- **nginx** reads the whole body before it passes the request on, so a slow
  client never holds Scanopsis. Keep `proxy_read_timeout` above the time an
  upload takes to store, such as the 90 seconds above. Backups need nothing
  more: their parts are small requests, the checks of a whole backup run in
  the background, and a download keeps sending data.
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

   If `compose.override.yaml` already has a `scanopsis:` entry (for example
   `extra_hosts`, or the CA of your database from
   [step 3](#step-3-remove-the-bundled-database) below), add the line to its
   `volumes:` list instead. A second `scanopsis:` key is an error:
   `mapping key "scanopsis" already defined`. All three together look like
   this:

   ```yaml
   services:
     scanopsis:
       extra_hosts:
         - "gitlab.example.com:10.0.0.5"
       volumes:
         - ./ca.pem:/etc/scanopsis/ca.pem:ro
         - ./db-ca.pem:/etc/scanopsis/db-ca.pem:ro
   ```

3. In `.env`, set `SCANOPSIS_CA_FILE=/etc/scanopsis/ca.pem` and run
   `docker compose up -d`.
4. Check that it was read. The start-up line shows `"custom_ca":true`:

   ```sh
   docker compose logs scanopsis | grep 'scanopsis starting' | tail -1
   ```

   ```text
   scanopsis-1  | {"time":"2026-11-25T04:17:12.923843173Z","level":"INFO","msg":"scanopsis starting","version":"v0.4.3","commit":"76b3b67","config":{"listen_addr":":8080","public_url":"https://scanopsis.example.com","seeded_settings":0,"trusted_proxies":1,"custom_ca":true}}
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

The order: step 1 creates the role and the database. To keep your users,
settings and scans, [move your data](#move-your-data-from-the-bundled-database)
next, while Scanopsis is stopped. Then step 2 writes the new URL and step 3
starts Scanopsis on the new server.

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
(1 row)
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
CA certificate. Save it as `db-ca.pem` next to `compose.yaml`; step 3 mounts
it at `/etc/scanopsis/db-ca.pem`. A certificate from a public CA needs no
`sslrootcert`.

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
your server. It needs Docker Compose 2.24.4 or later, the first version that
reads its `!override` tag (`docker compose version` shows yours).

1. If you use `sslrootcert`, mount `db-ca.pem` in `compose.override.yaml`
   (create the file if you have none). Do not edit `compose.external-db.yaml`:
   an upgrade replaces it, and a changed copy stops `git switch`.

   ```yaml
   services:
     scanopsis:
       volumes:
         - ./db-ca.pem:/etc/scanopsis/db-ca.pem:ro
   ```

   If the file already has a `scanopsis:` entry, add the line to its
   `volumes:` list instead, as
   [trust an internal certificate authority](#trust-an-internal-certificate-authority)
   shows. The URL must name exactly `/etc/scanopsis/db-ca.pem`, as in step 2.
2. Tell Compose which files to use, so every `docker compose` command picks
   them up. Compose reads `compose.override.yaml` on its own only when you
   name no files, so the list must include it. Add this line to `.env`:

   ```text
   COMPOSE_FILE=compose.yaml:compose.override.yaml:compose.external-db.yaml
   ```

   Only if you have no `compose.override.yaml` at all, leave it out:
   `COMPOSE_FILE=compose.yaml:compose.external-db.yaml`.
3. Check that `db` is gone from the list of services:

   ```sh
   docker compose config --services
   ```

   ```text
   scanopsis
   ```

   Services from `compose.override.yaml`, such as a proxy, are listed too.

4. Start Scanopsis. `--remove-orphans` removes the old `db` container (not
   its data folder); without it, Compose only warns about
   `orphan containers`:

   ```sh
   docker compose up -d --remove-orphans
   ```

   The output names the old `db` container as removed, for example:

   ```text
    Container scanopsis-scanopsis-1 Recreate
    Container scanopsis-db-1 Stopping
    Container scanopsis-scanopsis-1 Recreated
    Container scanopsis-db-1 Stopped
    Container scanopsis-db-1 Removing
    Container scanopsis-db-1 Removed
    Container scanopsis-scanopsis-1 Starting
    Container scanopsis-scanopsis-1 Started
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
   (1 row)
   ```

The folder `data/postgres` with the bundled data stays on the host. Delete it
only once you are sure you moved everything you need.

### Move your data from the bundled database

Do this after step 1 and before step 2, so that Scanopsis cannot start on the
new, empty server in between. The bundled `db` service still runs and
`secrets/database_url` still names it. Run the commands in `deploy/compose`.
If the new server uses a certificate from your own CA, save that CA as
`db-ca.pem` next to `compose.yaml` first (step 2 says where to get it).

1. Stop Scanopsis, so that nothing changes after the backup, and back up the
   bundled database. A container you stopped stays stopped, even after a
   reboot:

   ```sh
   docker compose stop scanopsis
   (umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
     --format=custom > scanopsis-move.dump)
   ```

2. Restore the file into the new server. This runs `pg_restore` from the
   PostgreSQL image that the bundled database uses, on the Docker network
   that reaches the server, with the password of the new role (not encoded).
   `PGSSLMODE=verify-full` checks the server's certificate against
   `db-ca.pem` before the password is sent:

   ```sh
   docker run --rm -i --network scanopsis_frontend \
     -v "$PWD/db-ca.pem:/db-ca.pem:ro" \
     -e PGPASSWORD='p@ss/w:rd#1?x%' -e PGSSLMODE=verify-full -e PGSSLROOTCERT=/db-ca.pem \
     postgres:18.7-alpine@sha256:0115267ab5a509022df9319c77be365cef335adb71556c75f60f5360ee247e71 \
     pg_restore -h db.example.com -U scanopsis -d scanopsis \
     --no-owner --exit-on-error < scanopsis-move.dump
   echo "restore exit=$?"
   ```

   ```text
   restore exit=0
   ```

   With a certificate from a public CA, use `-e PGSSLROOTCERT=system` and
   leave out the `-v` line.

3. Go on with [step 2](#step-2-write-the-database-url) and
   [step 3](#step-3-remove-the-bundled-database).
   `docker compose up -d --remove-orphans` in step 3 starts Scanopsis on the
   new server.

Keep the same `secrets/secret_key`. When Scanopsis starts on the new database,
its log has no `setup_token` line, because the restored database already has
an administrator. Sign in as before.

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
  file system and drop all capabilities; Scanopsis writes only to its backup
  folder (`/var/lib/scanopsis/backups`).
- **No migration step.** Scanopsis applies schema changes before it listens.

### Use plain `docker run`

These commands give the two containers the same settings as the Compose file:
the database on an internal network with no route out, health checks, limits,
log rotation, and a restart after a reboot. They were run on macOS with Docker
Desktop (Docker 29.9.3) and on Linux (Docker 29.10.1), and need Docker 25 or
newer: Docker 24 refuses the second `--network` with `Container cannot be
connected to network endpoints`. Build the image first, as in
[step 1 of the getting started guide](getting-started.md#step-1-build-the-image).
Then run the commands in an empty folder. The container and network names
are examples.

1. Create the three secret files. These are the commands of
   [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets),
   with the host name `scanopsis-db` instead of `db` in the database URL:

   ```sh
   mkdir secrets
   chmod 700 secrets
   openssl rand -base64 32 > secrets/secret_key
   openssl rand -hex 24 > secrets/postgres_password
   printf 'postgres://scanopsis:%s@scanopsis-db:5432/scanopsis?sslmode=disable' \
     "$(cat secrets/postgres_password)" > secrets/database_url
   chmod 644 secrets/*
   ```

2. Create the data folders. On Linux, they must belong to the users the
   containers run as (70 for PostgreSQL, 65532 for Scanopsis):

   ```sh
   sudo install -d -m 700 -o 70 -g 70 data/postgres
   sudo install -d -m 700 -o 65532 -g 65532 backups
   ```

   On macOS with Docker Desktop:

   ```sh
   mkdir -p data/postgres backups
   ```

3. Create the networks and start the containers:

   ```sh
   docker network create --internal scanopsis-backend
   docker network create scanopsis-frontend

   docker run -d --name scanopsis-db --network scanopsis-backend \
     --restart unless-stopped \
     --read-only --tmpfs /var/run/postgresql --tmpfs /tmp \
     --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
     --cap-add SETGID --cap-add SETUID --security-opt no-new-privileges:true \
     --pids-limit 200 --shm-size 256m --stop-timeout 120 \
     --log-driver json-file --log-opt max-size=10m --log-opt max-file=5 \
     --health-cmd 'pg_isready -U scanopsis -d scanopsis' \
     --health-interval 5s --health-timeout 5s --health-retries 10 \
     --health-start-period 10s \
     -e POSTGRES_USER=scanopsis -e POSTGRES_DB=scanopsis \
     -e POSTGRES_PASSWORD_FILE=/run/secrets/postgres_password \
     -v "$PWD/secrets/postgres_password:/run/secrets/postgres_password:ro" \
     -v "$PWD/data/postgres:/var/lib/postgresql" \
     postgres:18.7-alpine@sha256:0115267ab5a509022df9319c77be365cef335adb71556c75f60f5360ee247e71

   docker run -d --name scanopsis \
     --network scanopsis-backend --network scanopsis-frontend \
     --restart unless-stopped \
     --read-only --cap-drop ALL --security-opt no-new-privileges:true \
     --pids-limit 100 --memory 512m --stop-timeout 30 \
     --log-driver json-file --log-opt max-size=10m --log-opt max-file=5 \
     -p 127.0.0.1:8080:8080 \
     -e SCANOPSIS_PUBLIC_URL=https://scanopsis.example.com \
     -e SCANOPSIS_DATABASE_URL_FILE=/run/secrets/database_url \
     -e SCANOPSIS_SECRET_KEY_FILE=/run/secrets/secret_key \
     -v "$PWD/secrets/database_url:/run/secrets/database_url:ro" \
     -v "$PWD/secrets/secret_key:/run/secrets/secret_key:ro" \
     -v "$PWD/backups:/var/lib/scanopsis/backups" \
     scanopsis:v0.4.3
   ```

   Set `SCANOPSIS_PUBLIC_URL` to your address, as in
   [step 4 of the getting started guide](getting-started.md#step-4-create-the-configuration-file).
   The image already sets `GOMEMLIMIT=400MiB`, which suits `--memory 512m`.

4. Wait about 30 seconds, then check:

   ```sh
   docker ps --filter name=scanopsis --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
   curl -s http://127.0.0.1:8080/readyz
   ```

   Both containers are `(healthy)`:

   ```text
   NAMES          STATUS                    PORTS
   scanopsis      Up 31 seconds (healthy)   127.0.0.1:8080->8080/tcp
   scanopsis-db   Up 35 seconds (healthy)   5432/tcp
   {"status":"ready"}
   ```

5. Read the setup token with `docker logs scanopsis 2>&1 | grep setup_token`
   and go on with
   [step 8 of the getting started guide](getting-started.md#step-8-complete-the-setup-in-the-browser).

Unlike Compose, `docker run` does not wait for the database. If Scanopsis
starts first, it exits, and `--restart unless-stopped` starts it again until
the database answers. After a reboot, Docker starts both containers again, as
long as Docker itself starts at boot (see
[start Scanopsis after a reboot](#start-scanopsis-after-a-reboot)).

The container has no shell. Run the
[administration commands](operations.md#administration-commands) with
`docker exec scanopsis /scanopsis admin ...`.

### Use Podman

Podman runs the same image without a daemon and, by default, rootless: the
containers run under your own user. These steps were run with Podman 6.2.1,
rootless, on macOS (in the Podman machine) and on Linux (Fedora 45 with
SELinux enforcing). They are the [`docker run` steps](#use-plain-docker-run)
with four differences: the build, the data folders, an SELinux label on each
mount, and the restart after a reboot.

1. Build the image with Podman, in the clone of the version branch from
   [step 1 of the getting started guide](getting-started.md#step-1-build-the-image)
   (instead of `make image`, which calls Docker):

   ```sh
   podman build --format docker --build-arg VERSION=v0.4.3 \
     --build-arg COMMIT="$(git rev-parse --short HEAD)" -t scanopsis:v0.4.3 .
   ```

   `--format docker` keeps the health check of the image. Without it, Podman
   prints `HEALTHCHECK is not supported for OCI image format and will be
   ignored` and builds the image without a health check.

2. In an empty folder, create the three secret files as in
   [step 1 of the `docker run` steps](#use-plain-docker-run).

3. Create the data folders. On Linux, the users 70 and 65532 in the containers
   are sub-user IDs of your user, so set the owners with `podman unshare`:

   ```sh
   mkdir -p data/postgres backups
   chmod 700 data/postgres backups
   podman unshare chown 70:70 data/postgres
   podman unshare chown 65532:65532 backups
   ```

   Do not create them with `sudo install -o 70` as for Docker: rootless Podman
   cannot open them and stops with `Error: open .../data/postgres: permission
   denied`. On macOS, `mkdir -p data/postgres backups` is enough.

4. Create the networks and start the containers. The `Z` at the end of each
   mount labels the files for SELinux, so that the container may read them;
   without it, on a host with SELinux enforcing such as Fedora, both
   containers stop because they may not read their secret files. On macOS it
   does no harm.

   ```sh
   podman network create --internal scanopsis-backend
   podman network create scanopsis-frontend

   podman run -d --name scanopsis-db --network scanopsis-backend \
     --restart unless-stopped \
     --read-only --tmpfs /var/run/postgresql --tmpfs /tmp \
     --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
     --cap-add SETGID --cap-add SETUID --security-opt no-new-privileges:true \
     --pids-limit 200 --shm-size 256m --stop-timeout 120 \
     --log-driver json-file --log-opt max-size=10m --log-opt max-file=5 \
     --health-cmd 'pg_isready -U scanopsis -d scanopsis' \
     --health-interval 5s --health-timeout 5s --health-retries 10 \
     --health-start-period 10s \
     -e POSTGRES_USER=scanopsis -e POSTGRES_DB=scanopsis \
     -e POSTGRES_PASSWORD_FILE=/run/secrets/postgres_password \
     -v "$PWD/secrets/postgres_password:/run/secrets/postgres_password:ro,Z" \
     -v "$PWD/data/postgres:/var/lib/postgresql:Z" \
     postgres:18.7-alpine@sha256:0115267ab5a509022df9319c77be365cef335adb71556c75f60f5360ee247e71

   podman run -d --name scanopsis \
     --network scanopsis-backend --network scanopsis-frontend \
     --restart unless-stopped \
     --read-only --cap-drop ALL --security-opt no-new-privileges:true \
     --pids-limit 100 --memory 512m --stop-timeout 30 \
     --log-driver json-file --log-opt max-size=10m --log-opt max-file=5 \
     -p 127.0.0.1:8080:8080 \
     -e SCANOPSIS_PUBLIC_URL=https://scanopsis.example.com \
     -e SCANOPSIS_DATABASE_URL_FILE=/run/secrets/database_url \
     -e SCANOPSIS_SECRET_KEY_FILE=/run/secrets/secret_key \
     -v "$PWD/secrets/database_url:/run/secrets/database_url:ro,Z" \
     -v "$PWD/secrets/secret_key:/run/secrets/secret_key:ro,Z" \
     -v "$PWD/backups:/var/lib/scanopsis/backups:Z" \
     scanopsis:v0.4.3
   ```

5. Wait about a minute (Podman runs the first health check of Scanopsis 30
   seconds after its start), then check:

   ```sh
   podman ps --filter name=scanopsis --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
   curl -s http://127.0.0.1:8080/readyz
   ```

   ```text
   NAMES         STATUS                       PORTS
   scanopsis-db  Up About a minute (healthy)  5432/tcp
   scanopsis     Up 56 seconds (healthy)      127.0.0.1:8080->8080/tcp
   {"status":"ready"}
   ```

   Read the setup token with `podman logs scanopsis 2>&1 | grep setup_token`.

6. Let the containers start after a reboot. Podman has no daemon that starts
   them, so `--restart unless-stopped` alone does not bring them back. Its
   `podman-restart` service does. On Linux:

   ```sh
   systemctl --user enable podman-restart.service
   loginctl enable-linger
   ```

   `enable-linger` starts your user services at boot, without a login. On
   macOS, enable the service inside the Podman machine, which has linger on
   already, and start the machine after each login with `podman machine start`
   (Podman does not start it on its own):

   ```sh
   podman machine ssh systemctl --user enable podman-restart.service
   ```

On Linux, the backup files belong to a sub-user ID, so your user cannot read
them directly. Use `podman unshare`, for example
`podman unshare ls -l backups` and
`podman unshare cp backups/<name> ~/` (the copy belongs to you). Run the
[administration commands](operations.md#administration-commands) with
`podman exec scanopsis /scanopsis admin ...`.

### Use Kubernetes

Scanopsis needs no operator and no custom resources. Run it as one pod:

- A `Deployment` with `replicas: 1` and `strategy: Recreate`, which stops the
  old pod before the new one starts. A rolling update would run two versions
  on one database while the new one migrates it.
- PostgreSQL outside the pod, set up as in
  [use an existing PostgreSQL](#use-an-existing-postgresql) (steps 1 and 2).
- A `Secret` with `database_url` and `secret_key`, mounted as files. Do not
  commit its YAML: the values are only base64 encoded.
- Probes: `/healthz` for liveness (the process runs) and `/readyz` for
  readiness (the database answers). Scanopsis answers `/healthz` only after
  it has applied its migrations, so a startup probe gives the first start of
  a new version time.
- A `terminationGracePeriodSeconds` of 30, above the 15 seconds Scanopsis
  needs to finish its requests and the time a running backup needs to close
  its file.
- A `PersistentVolumeClaim` for the backups, mounted at
  `/var/lib/scanopsis/backups`. `fsGroup: 65532` makes the volume writable for
  the user that runs Scanopsis. Keep the backups on storage that is not the
  database's own, and copy them off the cluster.
- An Ingress or Gateway that follows the [proxy rules](#reverse-proxy-and-tls):
  `Host` unchanged, `X-Forwarded-For` set, a body of up to 100 MB, Scanopsis at
  the root of its own host name. Set `SCANOPSIS_TRUSTED_PROXIES` to the
  addresses of the ingress controller pods, or the range they get their
  addresses from.

These steps were run on a kind cluster (Kubernetes 1.37.0, kubectl 1.37.2,
the image loaded with `kind load docker-image`) with an external PostgreSQL
18.7 server that uses TLS and a certificate from a private CA.

1. Write `secrets/database_url` with the URL of your PostgreSQL server, as in
   [step 2 of use an existing PostgreSQL](#step-2-write-the-database-url), and
   create `secrets/secret_key` as in
   [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets).
   With `verify-full` and a certificate from your own CA, save the CA file as
   `db-ca.pem` in this folder. It goes into the Secret too, so set
   `sslrootcert=/run/secrets/scanopsis/db-ca.pem` in the URL. For example:

   ```sh
   printf 'postgres://scanopsis:%s@db.example.com:5432/scanopsis?sslmode=verify-full&sslrootcert=/run/secrets/scanopsis/db-ca.pem' \
     'p%40ss%2Fw%3Ard%231%3Fx%25' > secrets/database_url
   ```

2. Create the Secret. Leave out the `db-ca.pem` line if the URL has no
   `sslrootcert`:

   ```sh
   kubectl create secret generic scanopsis \
     --from-file=db-ca.pem=db-ca.pem \
     --from-file=database_url=secrets/database_url \
     --from-file=secret_key=secrets/secret_key
   ```

3. Save the manifest below as `scanopsis.yaml`. Replace the image with one you
   built and pushed to a registry the cluster can pull from, the public URL,
   and the range in `SCANOPSIS_TRUSTED_PROXIES` (`10.244.0.0/16` is the pod
   range of a kind cluster).

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
             image: registry.example.com/platform/scanopsis:v0.4.3
             ports:
               - name: http
                 containerPort: 8080
             env:
               - name: SCANOPSIS_PUBLIC_URL
                 value: https://scanopsis.example.com
               - name: SCANOPSIS_TRUSTED_PROXIES
                 value: 10.244.0.0/16
               - name: SCANOPSIS_DATABASE_URL_FILE
                 value: /run/secrets/scanopsis/database_url
               - name: SCANOPSIS_SECRET_KEY_FILE
                 value: /run/secrets/scanopsis/secret_key
               - name: GOMEMLIMIT
                 value: 400MiB
             startupProbe:
               httpGet:
                 path: /healthz
                 port: http
               periodSeconds: 10
               failureThreshold: 60
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

   The startup probe waits up to 10 minutes (60 checks, 10 seconds apart)
   for the migrations. They take a moment on a new or small database; the
   longest so far took about 25 seconds on a laptop with 540,000 scans (see
   [the upgrade notes](versions.md#from-041)), and a larger database or a
   slower disk takes longer. A probe that gives up restarts the container in
   the middle of the migrations: when a test held the start up for 90
   seconds, a probe that allowed 60 seconds stopped the container after 60
   seconds and it started over; with this probe the pod became ready without
   a restart.

   A CPU limit can slow uploads down, so set one only if your cluster
   requires it.

4. Apply it and wait for the pod:

   ```sh
   kubectl apply -f scanopsis.yaml
   kubectl rollout status deployment/scanopsis
   ```

   ```text
   persistentvolumeclaim/scanopsis-backups created
   deployment.apps/scanopsis created
   service/scanopsis created
   Waiting for deployment "scanopsis" rollout to finish: 0 of 1 updated replicas are available...
   deployment "scanopsis" successfully rolled out
   ```

5. Read the setup token:

   ```sh
   kubectl logs deployment/scanopsis | grep setup_token
   ```

   To reach Scanopsis before the Ingress is in place, forward a local port
   with `kubectl port-forward service/scanopsis 8080:80` and open
   `http://127.0.0.1:8080/readyz` (`{"status":"ready"}`). The setup itself
   needs the public URL, so finish it through the Ingress, as in
   [step 8 of the getting started guide](getting-started.md#step-8-complete-the-setup-in-the-browser).

Run the [administration commands](operations.md#administration-commands) with
`kubectl exec deployment/scanopsis -- /scanopsis admin ...`.

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
scheduled backups are kept for 7 days by default; see
[backup and restore](#backup-and-restore) for the time and memory they take.
Most pipelines scan less, so
your real figure is likely lower. The **Integrations** page shows your uploads
per day. Shorten `retention.days` if you need less history.

Dashboards read the counts stored on each scan and never the stored reports,
so they do not slow down as reports pile up.

### Backup and restore

Measured during the work on version 0.4.0 on an Apple M4 Pro with 24 GiB of
memory, in Docker Desktop with 10 CPUs and 15.6 GiB for its Linux machine, with
PostgreSQL 18.7. The Scanopsis container had the limits of `compose.yaml`:
512 MiB and `GOMEMLIMIT=400MiB`. The database held 503 services, 540,012 scans
and 4,321,242 findings: `pg_database_size` reported 1,583,809,039 bytes,
about 1.58 GB. The data
clean-up had already removed the stored reports of the 360,000 older scans, so
the backup is mostly findings.

| Operation | Time | Size | Scanopsis memory |
|---|---|---|---|
| Back up now | 2.5 to 2.6 seconds | 75.3 MiB file, 549 MB of table data inside | 67 to 90 MiB of process memory |
| Download over localhost | 0.35 seconds | 75.3 MiB | not measured |
| Upload and check of that file | 3 seconds | | not measured |
| Restore from the Backups page, with the backup of the current data first | 58 seconds | | 73 MiB of process memory |
| Restore from the Backups page, without that backup | 55 seconds | | 174 MiB of process memory |
| Restore on the setup screen of an empty installation | 56 seconds, and 3 seconds of upload and check before | restored database 1,549,883,071 bytes (about 1.55 GB) | 24 MiB of process memory |

Process memory is what Scanopsis itself holds (`anon` in the container's
`memory.stat`). The container's memory figure (`memory.current`) also counts
the page cache of the backup files the kernel keeps after reading or writing
them. During the restores it reached the 512 MiB limit: the kernel then frees
cache instead of stopping the process, and no out-of-memory kill and no
restart happened. PostgreSQL used up to 490 MiB during a restore.

A real backup with 30 days of stored original reports is larger and takes
longer than these figures: the reports are most of the data (see
[database size](#database-size)). Take a backup and a restore of your own data
on a test installation to know your figures.

## Upgrade and roll back

You deploy a new version by building its image from its version branch, see
[upgrade to the newest version](versions.md#upgrade-to-the-newest-version) for the
build steps, and [roll back to the previous version](versions.md#roll-back-to-the-previous-version)
for the way back. Schema changes run when the new version starts; there is no
separate migration step. The output below is from an upgrade from 0.4.2 to
0.4.3 and back; the commits on your build differ. 0.4.0 added no migration,
0.4.1 added two (00011 and 00012), 0.4.2 two (00013 and 00014), and 0.4.3
none, so an upgrade to 0.4.3 ends at schema version 14. Before the upgrade
the version is 14 from 0.4.2, 12 from 0.4.1 and 10 from 0.4.0. Before an
upgrade from a version before 0.4.0, also
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
     14
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
   db          Up About a minute (healthy)
   scanopsis   Up 30 seconds (healthy)
   {"version":"v0.4.3","commit":"7ad0f55","go":"go1.27.4"}
     14
   ```

   The log has one `applying migration` and one `applied migration` line for
   each change, with its duration; from 0.4.2 it has none. Sign in: your users and scans are still
   there.

If the container does not become healthy, read `docker compose logs scanopsis`.
A failed migration stops the server with `apply migration <file>:` and the
database error. Most migrations run in a transaction, and the large index
builds can safely run again, so the next start continues where the last one
stopped. A migration that cannot lock a table within 10 seconds stops the
start the same way; start again when the database is quieter. After a stop
during an upgrade, start the new version again; do not start the old one on
that database.

### Roll back

If the upgrade changed no schema (from 0.4.2), only set the old image again
in `.env` and run `docker compose up -d`. Otherwise go back to your backup. Do
not start the old image on the migrated database: the old version does not
know what the new migrations changed.

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

3. Start the old version, wait a few seconds until it listens, and check it:

   ```sh
   docker compose up -d scanopsis
   curl -s http://127.0.0.1:8080/api/v1/version
   docker compose exec -T db psql -U scanopsis -d scanopsis -t -c "SELECT max(version_id) FROM goose_db_version"
   ```

   ```text
   {"version":"v0.4.2","commit":"03f390f","go":"go1.27.4"}
     14
   ```

Everything that changed after the backup is gone, including scans uploaded
while the new version ran; later pipelines upload again. Then do the
[checks after a restore](operations.md#check-everything-after-a-restore). The
secret key does not change in an upgrade.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
