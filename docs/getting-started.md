# Getting started

This guide takes you from nothing to a running Scanopsis with one
administrator who signs in with two-factor authentication. It uses Docker
Compose on a single host. It takes about 15 minutes.

## Prerequisites

You need:

- A Linux or macOS host with Docker Engine and the Compose plugin
  (`docker compose`). The steps in this guide were run with Docker 29.9.3 and
  Compose 5.7.0.
- Git, GNU Make and OpenSSL on the host where you build the image.
- An authenticator app on your phone that supports time-based one-time
  passwords (TOTP), for example Aegis, 2FAS or Google Authenticator.
- For a production deployment: a DNS name such as `scanopsis.example.com` and
  a reverse proxy that terminates TLS. You can add the proxy after this guide.

Scanopsis listens on plain HTTP. Compose publishes it on `127.0.0.1:8080`
only, so nothing outside the host can reach it until you add a reverse proxy.
After the build, Scanopsis needs no outbound access except to your GitLab
instance and, if you use it, your SonarQube.

## Step 1. Build the image

Scanopsis publishes no container image, so you build one from a version
branch. Deploy a version branch, not `main`: `main` is development code. See
[versions and branches](versions.md) to find the newest version. This step is an
example to adapt: use your own clone location.

1. Clone the version branch and go to its root. Replace `X.Y.Z` with the
   version:

   ```sh
   git clone --branch release/X.Y.Z --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   ```

   Replace `X.Y.Z` with the newest version, for example `0.2.0`. See
   [find the newest version](versions.md#find-the-newest-version).

2. Build the image with a tag that names the version. Use the same
   version as in step 1:

   ```sh
   make image IMAGE=scanopsis:vX.Y.Z
   ```

   The build downloads dependencies and takes a few minutes the first time.
   The last lines look like this:

   ```text
   #24 unpacking to docker.io/library/scanopsis:vX.Y.Z 0.0s done
   #24 DONE 0.3s
   ```

3. Check that the image exists:

   ```sh
   docker image ls scanopsis:vX.Y.Z
   ```

   Expected result: one line with the repository `scanopsis` and the tag
   `vX.Y.Z`. You use this tag in step 3.

If the build fails with `VERSION and COMMIT may only contain ...`, you passed
a version string with unusual characters. Use a plain one, for example
`make image IMAGE=scanopsis:v1.0.0 VERSION=1.0.0`. If it fails while downloading, check the host's
network access and proxy settings for Docker.

## Step 2. Create the secrets

Scanopsis reads its database URL and its encryption key from files, not from
environment variables. Compose mounts them as Docker secrets. Run the
following commands in `deploy/compose`:

```sh
cd deploy/compose
mkdir secrets
chmod 700 secrets
openssl rand -base64 32 > secrets/secret_key
openssl rand -hex 24 > secrets/postgres_password
printf 'postgres://scanopsis:%s@db:5432/scanopsis?sslmode=disable' \
  "$(cat secrets/postgres_password)" > secrets/database_url
chmod 644 secrets/*
```

What each file holds:

| File | Content |
|---|---|
| `secrets/secret_key` | A random 32-byte key, base64 encoded. Scanopsis uses it to encrypt two-factor secrets and the secrets you enter on the Settings page. |
| `secrets/postgres_password` | The password of the database user `scanopsis`. |
| `secrets/database_url` | The connection string. It must contain the same password. Use `sslmode=disable` because the database is only reachable on the private Compose network. |

The files are mode 644 because the container runs as user 65532 and Docker
mounts each file with its host permissions. The directory is mode 700, so
other users on the host cannot reach the files. `openssl rand -hex` is used
for the password so that it needs no escaping inside the URL.

Verify:

```sh
ls -l secrets
```

Expected result: three files, `database_url`, `postgres_password` and
`secret_key`, each with a non-zero size.

```text
-rw-r--r--  1 siyam  staff  103 Nov 19 09:56 database_url
-rw-r--r--  1 siyam  staff   49 Nov 19 09:56 postgres_password
-rw-r--r--  1 siyam  staff   45 Nov 19 09:56 secret_key
```

Back up `secrets/secret_key` now, in a place that is separate from your
database backups. If you lose it, the two-factor secrets and the saved
GitLab secret become unreadable. See [the secret key](configuration.md#the-secret-key).

Do not commit the `secrets` directory or the `.env` file to a repository.

## Step 3. Create the configuration file

1. Copy the example file:

   ```sh
   cp .env.example .env
   ```

2. Open `.env` and set `SCANOPSIS_PUBLIC_URL` to the address users will type
   in the browser, without a path. The address may include a port. For a trial
   on your own machine, use `http://localhost:8080`. For a real deployment, use
   the HTTPS address of your reverse proxy, for example
   `https://scanopsis.example.com`, or `https://scanopsis.example.com:8443` if
   the proxy listens on another port. Scanopsis must be served at the root of
   its own host name: a sub-path such as `https://example.com/scanopsis/` is
   not supported.

   The URL decides the cookie settings, so you must open Scanopsis in the
   browser with exactly this host name. A mismatch makes sign-in fail.

3. Set `SCANOPSIS_IMAGE` to the image tag from step 1, for example
   `SCANOPSIS_IMAGE=scanopsis:vX.Y.Z`. Compose runs exactly this image, and the
   [upgrade](versions.md#upgrade-to-a-newer-version) later changes only this
   line.

Verify that Compose accepts the files:

```sh
docker compose config --quiet && echo config-ok
```

Expected result:

```text
config-ok
```

If Compose prints `env file ... not found`, you did not create `.env`. This
check does not look at the files in `secrets`. A missing secret file shows up
in the next step.

## Step 4. Start Scanopsis

```sh
docker compose up -d
```

Compose starts the database first and waits until it is healthy, then starts
Scanopsis. Expected result:

```text
 Container scanopsis-db-1 Healthy
 Container scanopsis-scanopsis-1 Starting
 Container scanopsis-scanopsis-1 Started
```

If Compose ends with `invalid mount config for type "bind": bind source path
does not exist`, the path in the message is a secret file that you did not
create. Create it as in step 2 and run `docker compose up -d` again.

## Step 5. Check that it is healthy

1. Wait about 30 seconds, then list the containers:

   ```sh
   docker compose ps
   ```

   Expected result: both containers are `Up` and show `(healthy)`. The image
   health check needs up to 20 seconds to start.

   ```text
   NAME                    SERVICE     STATUS                    PORTS
   scanopsis-db-1          db          Up 34 seconds (healthy)   5432/tcp
   scanopsis-scanopsis-1   scanopsis   Up 28 seconds (healthy)   127.0.0.1:8080->8080/tcp
   ```

2. Check that the process is alive:

   ```sh
   curl -s http://127.0.0.1:8080/healthz
   ```

   Expected result:

   ```text
   {"status":"ok"}
   ```

3. Check that it can reach the database:

   ```sh
   curl -s http://127.0.0.1:8080/readyz
   ```

   Expected result:

   ```text
   {"status":"ready"}
   ```

If this step fails:

| Symptom | Likely cause and fix |
|---|---|
| `scanopsis-scanopsis-1` is `Restarting` | Run `docker compose logs scanopsis`. The server prints `configuration error:` followed by one line per problem, for example `SCANOPSIS_PUBLIC_URL is required`. Fix `.env` and run `docker compose up -d` again. |
| The log says `SCANOPSIS_SECRET_KEY is not valid` | `secrets/secret_key` is not a base64 encoded 32-byte key. Create it again with the command in step 2. |
| `curl` cannot connect | The container is not running, or another program uses port 8080 on the host. Run `docker compose ps`. |
| `/readyz` returns `503` | The database is not reachable. Check `docker compose logs db` and that the password in `secrets/database_url` matches `secrets/postgres_password`. The database user gets its password only when the volume is created. If you changed `secrets/postgres_password` after the first start, run `docker compose down -v` to delete the database volume and start again. Do this only before the setup, because it deletes all data. |

## Step 6. Get the setup token

While no administrator exists, Scanopsis creates a one-time setup token at
every start and writes it to its log. Only someone with access to the log can
create the first administrator.

```sh
docker compose logs scanopsis | grep setup_token
```

Expected result: one line with the setup URL and the token.

```text
scanopsis-1  | {"time":"2026-11-19T03:57:06.39732871Z","level":"WARN","msg":"no administrator exists yet: open Scanopsis in a browser to finish the setup","setup_url":"http://localhost:8080/setup","setup_token":"-wwBGkQ1w6f51nnvXGekSLporF2mi9Yd5DbrgV36kME"}
```

Copy the value of `setup_token`. If the container restarted, use the last
line: only the token from the latest start works.

If the command prints nothing, an administrator already exists and the setup
is done. Go to [step 8](#step-8-sign-in-again) to sign in.

## Step 7. Complete the setup in the browser

1. Open the `setup_url` from the log. You see the page **Set up Scanopsis**.
2. Fill in the form:
   - **Setup token**: the token from step 6.
   - **Username**: for example `root`. This becomes a local account with the
     admin role.
   - **Password** and **Confirm password**: at least 12 characters, not the
     same as the username, and not one character repeated.
3. Select **Create administrator**.

   Expected result: the page **Set up two-factor sign-in** opens. You are
   signed in, but you cannot use the application until you finish the
   two-factor enrolment in the next steps.

4. Open your authenticator app, add an account, and scan the QR code. If you
   cannot scan it, enter the secret shown under the code. Select **Next**.
5. Enter the 6-digit code that the app shows and select **Confirm**. A code
   changes every 30 seconds, so enter it before it changes.

   Expected result: the page **Save your recovery codes** shows 10 codes.

6. Store the recovery codes in a password manager or another safe place. Use
   **Copy all** or **Download as text file**. Each code works once, in place
   of an app code, and they are shown only now.
7. Select **I have saved these codes**, then **Continue**.

   Expected result: you land on the home page and see **Users** and
   **Settings** in the navigation bar.

If this step fails:

| Symptom | Likely cause and fix |
|---|---|
| The browser cannot open the setup URL | The host name in `SCANOPSIS_PUBLIC_URL` does not resolve, or the reverse proxy is not in place. For a trial, set `SCANOPSIS_PUBLIC_URL=http://localhost:8080` in `.env` and run `docker compose up -d`. Then read the new setup URL and token from the log. |
| "The setup token is not correct" | You copied an old token, or part of it. Scanopsis creates a new token at every start. Copy the last `setup_token` from the log. |
| "The password must be at least 12 characters long" or another password message | Choose a password that meets the rules in step 7. |
| "Too many sign-in attempts. Wait N seconds" | The setup form allows 5 attempts at once, then one every 12 seconds, for each client address. Wait and try again. |
| "That code is not correct" while confirming two-factor | The app code expired or the host clock is wrong. Wait for the next code. If it keeps failing, check the time on the host with `date` and on your phone. |
| "Too many wrong codes. Ask an administrator to reset two-factor authentication." | Ten wrong codes in a row locked the account. Another administrator resets its two-factor authentication on the **Users** page, or you run `scanopsis admin reset-2fa local:<username>` on the host. See [operations](operations.md#reset-two-factor-authentication). |
| You closed the browser before saving the recovery codes | Sign in again. Scanopsis asks you to enrol again only if you did not confirm a code. If you did confirm one, you can reset your own two-factor authentication on the **Users** page, or with the command in [operations](operations.md#reset-two-factor-authentication). |

You can also complete the setup without a browser, which is useful for
automation. The setup endpoint is `POST /api/v1/setup` with a JSON body that
has `token`, `username` and `password`. It answers `204` and starts a session.
The browser path above is the supported one.

## Step 8. Sign in again

1. Sign out with the button in the navigation bar.
2. Open the sign-in page, enter your username and password, and select **Sign
   in**.
3. Enter the 6-digit code from the app on the page **Enter your code**.

Expected result: you see the home page again.

If you lost the phone, select **Use a recovery code** and enter one of the
codes you saved. A recovery code works once.

Ten wrong codes in a row, authenticator or recovery codes together, lock
two-factor sign-in for that account. Scanopsis then signs the account out
everywhere and answers "Too many wrong codes. Ask an administrator to reset
two-factor authentication." Even the correct code is refused until an
administrator resets two-factor authentication on the **Users** page or with
`scanopsis admin reset-2fa`. A password reset with `scanopsis admin
reset-password` lifts the lock too. A correct code before the tenth wrong one
sets the count back to zero.

If sign-in fails, see [recovering access](operations.md#recovering-access-when-locked-out).

## Next steps

1. Put a reverse proxy with TLS in front of `127.0.0.1:8080` and set
   `SCANOPSIS_PUBLIC_URL` to its HTTPS address. See
   [reverse proxy and TLS](deployment.md#reverse-proxy-and-tls) and the
   [host settings](deployment.md#host-settings) (restart after a reboot, log
   rotation, time zone, limits).
2. Let your colleagues sign in with GitLab. See the
   [GitLab sign-in guide](integrations/gitlab-sso.md).
3. Learn what every setting does in the
   [configuration reference](configuration.md).
4. Set up backups and learn the administration commands in
   [operations](operations.md).
5. Once services exist, choose the branch each dashboard shows. See
   [choose the dashboard branch](operations.md#choose-the-dashboard-branch-of-a-service).
