# Security policy

## Reporting a vulnerability

Please do not report security vulnerabilities in public issues, pull requests or discussions.

Report them privately using one of the following methods.

- **Preferred:** use GitHub private vulnerability reporting. Open the [Security advisories page](https://github.com/siyamsarker/scanopsis/security/advisories/new), or select **Report a vulnerability** under the repository's **Security** tab.
- **Alternative:** send an e-mail to [siyam.ts@gmail.com](mailto:siyam.ts@gmail.com).

Please include the following information in your report:

- The affected version or commit.
- Steps to reproduce the issue.
- The impact you expect, including what an attacker could access or change.

## Response

The maintainer aims to acknowledge reports within 5 working days. This is a best-effort commitment, and timelines for a fix depend on its complexity. The maintainer will keep you informed of progress and will credit you in the advisory if you wish.

## Supported versions

Only the newest version branch (`release/X.Y.Z`, see [versions and branches](docs/versions.md)) is supported. Version branches never change, so a security fix is made on `main` and published as a new version. No version has been published yet; until the first one exists, only `main` receives fixes, and it is development code.
