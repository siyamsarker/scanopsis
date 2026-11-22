# Docker Compose deployment

This directory holds the Compose file for Scanopsis and its PostgreSQL
database.

- `compose.yaml`: the `db` and `scanopsis` services.
- `.env.example`: copy it to `.env` and edit it.

Scanopsis publishes no container image. Build the image from a version branch
with `make image` in the repository root, see
[versions and branches](../../docs/versions.md#deploy-a-version).

Follow the [getting started guide](../../docs/getting-started.md) to create
the secrets, start the stack and finish the setup. See the
[configuration reference](../../docs/configuration.md) for every setting.
