# Docker Compose deployment

This directory holds the Compose file for Scanopsis and its PostgreSQL
database.

- `compose.yaml`: the `db` and `scanopsis` services.
- `.env.example`: copy it to `.env` and edit it.

## Defaults in `compose.yaml`

| Setting | Default | How to change it |
|---|---|---|
| Published address and port | `127.0.0.1:8080` | Set `SCANOPSIS_BIND` and `SCANOPSIS_PORT` in `.env`. The container always listens on 8080. |
| Stop grace period | 20 seconds | The application finishes running requests within 15 seconds. |
| Container logs | `json-file`, 5 files of 10 MB for each service | Edit the `x-logging` block at the top. |
| Process limit | 100 for `scanopsis`, 200 for `db` | `pids_limit`. |
| Memory | 512 MB limit for `scanopsis`, `GOMEMLIMIT` 400 MiB | `mem_limit` and `GOMEMLIMIT`. |
| Root file system | read-only for both services | The database writes to its volume and to two `tmpfs` mounts. |

Both services also drop all capabilities (the database keeps the five that its
entrypoint needs), set `no-new-privileges`, and run on a network that has no
route to the outside (`backend`). Only `scanopsis` is also on `frontend`.

Scanopsis publishes no container image. Build the image from a version branch
with `make image` in the repository root, see
[versions and branches](../../docs/versions.md#deploy-a-version).

Follow the [getting started guide](../../docs/getting-started.md) to create
the secrets, start the stack and finish the setup. See the
[configuration reference](../../docs/configuration.md) for every setting.
