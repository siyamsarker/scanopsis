# Getting started

This guide takes you from nothing to a running Scanopsis with one administrator who signs in with two-factor authentication. It uses Docker Compose on one host and takes about 15 minutes.

**Audience:** Operators · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## What you need

- A Linux or macOS host with Docker Engine and the Compose plugin
  (`docker compose`). These steps were run with Docker 29.9.3 and Compose
  5.7.0.
- Git, GNU Make and OpenSSL on the host where you build the image.
- An authenticator app on your phone that supports time-based one-time
  passwords (TOTP), for example Aegis, 2FAS or Google Authenticator.
- For production: a DNS name such as `scanopsis.example.com` and a reverse
  proxy that terminates TLS. You can add the proxy after this guide.

Scanopsis speaks plain HTTP. Compose publishes it on `127.0.0.1:8080` only, so
nothing outside the host can reach it until you add a reverse proxy. Once the
image is built, Scanopsis needs no outbound access except to your GitLab and,
if you use it, your SonarQube.

## Step 1. Build the image

Scanopsis publishes no container image, so you build one from a version
branch. Always deploy a version branch, never `main`: `main` is development
code. Replace `X.Y.Z` with the newest version, for example `0.2.0` (see
[find the newest version](versions.md#find-the-newest-version)).

1. Clone the version branch:

   ```sh
   git clone --branch release/X.Y.Z --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   ```

2. Build the image with a tag that names the version:

   ```sh
   make image IMAGE=scanopsis:vX.Y.Z
   ```

   The first build downloads dependencies and takes a few minutes. The last
   lines look like this:

   ```text
   #24 unpacking to docker.io/library/scanopsis:vX.Y.Z 0.0s done
   #24 DONE 0.3s
   ```

3. Check that the image exists:

   ```sh
   docker image ls scanopsis:vX.Y.Z
   ```

   You see one line with the repository `scanopsis` and the tag `vX.Y.Z`.

If the build fails with `VERSION and COMMIT may only contain ...`, the version
string has unusual characters. Pass a plain one, for example
`make image IMAGE=scanopsis:v1.0.0 VERSION=1.0.0`. If it fails while
downloading, check the network access and proxy settings of Docker on the
host.

## Step 2. Create the secrets

Scanopsis reads its database URL and its encryption key from files, which
Compose mounts as Docker secrets. Run these commands in `deploy/compose`:

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

| File | What it holds |
| --- | --- |
| `secrets/secret_key` | A random 32-byte key, base64 encoded. Scanopsis uses it to encrypt two-factor secrets and the secrets you enter on the Settings page. |
| `secrets/postgres_password` | The password of the database user `scanopsis`. |
| `secrets/database_url` | The connection string, with the same password. `sslmode=disable` is fine here, because the database is only reachable on the private Compose network. |

The files are mode 644 because the container runs as user 65532 and Docker
mounts each file with its host permissions. The directory is mode 700, so
other users on the host cannot reach the files. The password is hex, so it
needs no escaping inside the URL.

Check:

```sh
ls -l secrets
```

You see three files with a size above zero:

```text
-rw-r--r--  1 you  staff  103 Nov 22 11:11 database_url
-rw-r--r--  1 you  staff   49 Nov 22 11:11 postgres_password
-rw-r--r--  1 you  staff   45 Nov 22 11:11 secret_key
```

Back up `secrets/secret_key` now, somewhere apart from your database
backups. Without it, the two-factor secrets and the saved GitLab and
SonarQube secrets cannot be read. See [the secret key](configuration.md#the-secret-key).

Never commit the `secrets` directory or the `.env` file.

## Step 3. Create the configuration file

1. Copy the example file:

   ```sh
   cp .env.example .env
   ```

2. In `.env`, set `SCANOPSIS_PUBLIC_URL` to the address users type in the
   browser, without a path. For a trial on your own machine, use
   `http://localhost:8080`. For a real deployment, use the HTTPS address of
   your reverse proxy, for example `https://scanopsis.example.com` (a port such
   as `:8443` is fine). Scanopsis must run at the root of its own host name;
   a sub-path such as `https://example.com/scanopsis/` does not work.

   The cookies are set for this address, so always open Scanopsis with exactly
   this host name. Another name makes sign-in fail.

3. Set `SCANOPSIS_IMAGE` to the image tag from step 1, for example
   `SCANOPSIS_IMAGE=scanopsis:vX.Y.Z`. Compose runs exactly this image, and an
   [upgrade](versions.md#upgrade-to-a-newer-version) later changes only this
   line.

Check that Compose accepts the files:

```sh
docker compose config --quiet && echo config-ok
```

You see `config-ok`. If Compose says
`required variable SCANOPSIS_IMAGE is missing a value`, `.env` is missing or
has no `SCANOPSIS_IMAGE` line. This check does not look into `secrets`; a
missing secret file shows up in the next step.

## Step 4. Start Scanopsis

```sh
docker compose up -d
```

Compose starts the database, waits until it is healthy, then starts
Scanopsis:

```text
 Container scanopsis-db-1 Healthy
 Container scanopsis-scanopsis-1 Starting
 Container scanopsis-scanopsis-1 Started
```

If Compose ends with `invalid mount config for type "bind": bind source path
does not exist`, the path in the message is a secret file you did not create.
Create it as in step 2 and run `docker compose up -d` again.

## Step 5. Check that it is healthy

1. Wait about 30 seconds, then list the containers:

   ```sh
   docker compose ps
   ```

   Both containers are `Up` and `(healthy)`. The image health check takes up
   to 20 seconds to start.

   ```text
   NAME                    SERVICE     STATUS                    PORTS
   scanopsis-db-1          db          Up 34 seconds (healthy)   5432/tcp
   scanopsis-scanopsis-1   scanopsis   Up 28 seconds (healthy)   127.0.0.1:8080->8080/tcp
   ```

2. Check that the process is alive:

   ```sh
   curl -s http://127.0.0.1:8080/healthz
   ```

   You get `{"status":"ok"}`.

3. Check that it can reach the database:

   ```sh
   curl -s http://127.0.0.1:8080/readyz
   ```

   You get `{"status":"ready"}`.

If this step fails:

| Symptom | Cause and fix |
| --- | --- |
| `scanopsis-scanopsis-1` is `Restarting` | Run `docker compose logs scanopsis`. The server prints `configuration error:` and one line per problem, for example `SCANOPSIS_PUBLIC_URL is required, for example https://scanopsis.example.com`. Fix `.env` and run `docker compose up -d` again. |
| The log says `SCANOPSIS_SECRET_KEY is not valid` | `secrets/secret_key` is not a base64 encoded 32-byte key. Create it again as in step 2. |
| `curl` cannot connect | The container is not running, or another program uses port 8080 on the host. Run `docker compose ps`. |
| `/readyz` answers `503` | The database is not reachable. Check `docker compose logs db`, and check that the password in `secrets/database_url` matches `secrets/postgres_password`. The database user gets its password only when the volume is first created. If you changed the password after the first start and have not finished the setup yet, run `docker compose down -v` to delete the database volume and start again. This deletes all data. |

## Step 6. Get the setup token

While no administrator exists, Scanopsis writes a one-time setup token to its
log at every start. Only someone who can read the log can create the first
administrator.

```sh
docker compose logs scanopsis | grep setup_token
```

You see one line with the setup URL and the token:

```text
scanopsis-1  | {"time":"2026-11-22T11:11:28.675727763Z","level":"WARN","msg":"no administrator exists yet: open Scanopsis in a browser to finish the setup","setup_url":"http://localhost:8080/setup","setup_token":"zkMcwZId2nNg2LvQdmhBJYosolvDMUAFJ3AjUYADDcw"}
```

Copy the value of `setup_token`. If the container restarted, use the last
line: only the token from the latest start works.

If the command prints nothing, an administrator already exists. Go to
[step 8](#step-8-sign-in-again).

## Step 7. Complete the setup in the browser

1. Open the `setup_url` from the log. You see **Set up Scanopsis**.
2. Fill in the form:
   - **Setup token**: the token from step 6.
   - **Username**: for example `root`. This becomes a local account with the
     admin role.
   - **Password** and **Confirm password**: at least 12 characters, not the
     same as the username, and not one character repeated.
3. Select **Create administrator**. The page **Set up two-factor sign-in**
   opens. You are signed in, but you cannot use Scanopsis until you finish
   the next steps.
4. In your authenticator app, add an account and scan the QR code. If you
   cannot scan it, type the secret shown under the code. Select **Next**.
5. Enter the 6-digit code from the app and select **Confirm**. A code changes
   every 30 seconds, so be quick. The page **Save your recovery codes** shows
   10 codes.
6. Store the recovery codes in a password manager or another safe place. Use
   **Copy all** or **Download as text file**. Each code works once, in place of
   an app code, and you see them only now.
7. Select **I have saved these codes**, then **Continue**. You land on the home
   page and see **Users** and **Settings** in the navigation bar.

If this step fails:

| Symptom | Cause and fix |
| --- | --- |
| The browser cannot open the setup URL | The host name in `SCANOPSIS_PUBLIC_URL` does not resolve, or the reverse proxy is not in place yet. For a trial, set `SCANOPSIS_PUBLIC_URL=http://localhost:8080` in `.env`, run `docker compose up -d`, and read the new setup URL and token from the log. |
| "The setup token is not correct. Copy it again from the server log." | You copied an old token, or part of it. Scanopsis makes a new token at every start. Copy the last `setup_token` from the log. |
| "The password must be at least 12 characters long." or another password message | Choose a password that meets the rules in item 2. |
| "Too many sign-in attempts. Wait N seconds and try again." | The setup form allows 5 attempts at once, then one every 12 seconds, per client address. Wait and try again. |
| "That code is not correct. Wait for a new one in the app and try again." | The code expired, or the clock on the host or the phone is wrong. Wait for the next code. If it keeps failing, compare the time on the host (`date`) with your phone. |
| "Too many wrong codes. Ask an administrator to reset two-factor authentication." | Ten wrong codes in a row locked the account. Run `scanopsis admin reset-2fa local:<username>` on the host, as the [operations guide](operations.md) shows under "Reset two-factor authentication". |
| You closed the browser before saving the recovery codes | Sign in again. If you had not confirmed a code yet, Scanopsis starts the enrolment again. If you had, reset your own two-factor authentication on the **Users** page or with the command above, then enrol again. |

You can also finish the setup without a browser, for automation:
`POST /api/v1/setup` with a JSON body that has `token`, `username` and
`password` answers `204` and starts a session. The browser path is the
supported one.

## Step 8. Sign in again

1. Sign out with the button in the navigation bar.
2. On the sign-in page, enter your username and password and select
   **Sign in**.
3. On **Enter your code**, enter the 6-digit code from the app.

You are back on the home page.

Lost your phone? Select **Use a recovery code** and enter one of the codes you
saved. Each works once.

Ten wrong codes in a row (app and recovery codes together) lock two-factor
sign-in for the account. Scanopsis signs the account out everywhere and
refuses even the correct code until an administrator resets two-factor
authentication on the **Users** page or with `scanopsis admin reset-2fa`.
A password reset with `scanopsis admin reset-password` lifts the lock too.
A correct code before the tenth wrong one sets the count back to zero.

If you are locked out, the [operations guide](operations.md) explains how to
recover access.

## Next steps

1. Put a reverse proxy with TLS in front of `127.0.0.1:8080` and set
   `SCANOPSIS_PUBLIC_URL` to its HTTPS address. The
   [deployment guide](deployment.md) has proxy examples and host settings
   (restart after a reboot, log rotation, time zone, limits).
2. Let pipelines upload their Trivy reports:
   [upload Trivy reports from GitLab CI](integrations/gitlab-ci.md).
3. Connect SonarQube: [SonarQube guide](integrations/sonarqube.md).
4. Let your colleagues sign in with GitLab:
   [GitLab sign-in guide](integrations/gitlab-sso.md).
5. Set up backups and learn the administration commands in the
   [operations guide](operations.md). Once services exist, choose the branch
   each dashboard shows there too.
6. Look up any setting in the [configuration reference](configuration.md).

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
