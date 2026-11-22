# Security policy

This page tells you how to report a security problem in Scanopsis privately, and what happens next.

**Audience:** Everyone · **Back to:** [README](README.md) · [All guides](README.md#documentation)

---

## Report a vulnerability

Please do not report security problems in public issues, pull requests or
discussions. Report them privately instead:

- **Preferred:** use GitHub's private vulnerability reporting. Open the
  [new advisory form](https://github.com/siyamsarker/scanopsis/security/advisories/new),
  or select **Report a vulnerability** on the repository's **Security** tab.
- **Or** send an e-mail to [siyam.ts@gmail.com](mailto:siyam.ts@gmail.com).

Please include:

- The affected version (for example `v0.2.0`) or commit.
- The steps to reproduce the problem.
- The impact you expect: what an attacker could see or change.

## What happens next

The maintainer aims to confirm your report within 5 working days. This is a
best effort: the time to a fix depends on the problem. You hear about the
progress, and you are credited in the advisory if you wish.

## Supported versions

Only the newest version is supported, today **0.2.0** (the branch
`release/0.2.0`). Version branches never change, so a security fix lands on
`main` and ships as a new version. Upgrade to get it, as described in
[versions and upgrades](docs/versions.md#upgrade-to-a-newer-version).

## Hardening your installation

The [security guide](docs/security.md) explains the threat model, the
controls Scanopsis has, and a checklist for a safe deployment.

---

[Back to the README](README.md) · [All guides](README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
