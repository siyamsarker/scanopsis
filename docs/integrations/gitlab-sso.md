# Sign in with GitLab

This guide connects Scanopsis to your GitLab instance, so that people sign in
with their GitLab account. Scanopsis acts as an OpenID Connect client of
GitLab. It never sees GitLab passwords.

Local accounts keep working next to GitLab sign-in. Keep at least the first
administrator as a local account, so that you can still sign in when GitLab is
down.

## Before you start

You need:

- A running Scanopsis with a finished setup. See the
  [getting started guide](../getting-started.md).
- `SCANOPSIS_PUBLIC_URL` set to the address that users open in the browser, for
  example `https://scanopsis.example.com`. GitLab sends users back to this
  address.
- Network access from the Scanopsis container to your GitLab instance, for
  example `https://gitlab.example.com`. Scanopsis contacts GitLab itself to read
  its OpenID Connect configuration, to exchange the sign-in code and to read the
  user's groups.
- A GitLab administrator, or the Owner of a group, who can create an
  application.

If your GitLab uses a certificate from an internal certificate authority, set
up `SCANOPSIS_CA_FILE` first. See
[outgoing TLS connections](../configuration.md#outgoing-tls-connections).
Scanopsis has no option to skip certificate checks.

## Step 1. Create the application in GitLab

The menu names below follow current GitLab versions. They differ slightly
between versions, so check your GitLab documentation if you cannot find them.

1. In GitLab, open **Admin area**, then **Applications**, and select **Add new
   application**. To limit the application to one group instead, open the
   group, then **Settings**, then **Applications**.
2. Fill in the form:
   - **Name**: `Scanopsis`.
   - **Redirect URI**: your public URL plus `/api/v1/auth/gitlab/callback`, for
     example `https://scanopsis.example.com/api/v1/auth/gitlab/callback`. The
     scheme, host and port must match `SCANOPSIS_PUBLIC_URL` exactly.
   - **Confidential**: selected. Scanopsis is a server application and keeps the
     secret.
   - **Scopes**: select `openid`, `profile` and `email`, and nothing else.
3. Save the application.
4. Copy the **Application ID** and the **Secret**. GitLab may show the secret
   only once. If you lose it, create a new secret in GitLab.

Expected result: the application appears in the list with the three scopes.

## Step 2. Enter the application in Scanopsis

1. Sign in to Scanopsis as an administrator and open **Settings**.
2. In the group **GitLab sign-in**, fill in:
   - The base URL of the GitLab instance, for example
     `https://gitlab.example.com`. Do not add a path.
   - The application ID.
   - The application secret. Scanopsis stores it encrypted and never shows it
     again.
   - The allowed groups, one per line. Read [Allowed groups](#allowed-groups)
     first. If you leave the list empty, every GitLab user who can reach your
     GitLab may sign in.
3. Select **Save changes**.

   Expected result: the page shows "Settings saved. Changes apply now." The
   change applies to the next request, without a restart.

If saving fails, the page names the field. "Required when other GitLab settings
are set." means that the URL, the application ID and the secret must all be
filled in. "Group path ... must not start or end with a slash." and "Group path
... must not contain spaces." mean that a group needs a plain full path such as
`security/appsec`.

Instead of the Settings page you can seed the values from the environment for
the first start, with `SCANOPSIS_GITLAB_URL`, `SCANOPSIS_GITLAB_CLIENT_ID`,
`SCANOPSIS_GITLAB_CLIENT_SECRET` and `SCANOPSIS_GITLAB_ALLOWED_GROUPS`. They
are used only if the setting was never saved. See
[two kinds of settings](../configuration.md#two-kinds-of-settings).

## Step 3. Verify

1. Check that the server now offers GitLab sign-in:

   ```sh
   curl -s https://scanopsis.example.com/api/v1/auth/methods
   ```

   Expected result:

   ```text
   {"gitlab":true,"local":true}
   ```

   If `gitlab` is `false`, one of the URL, the application ID or the secret is
   missing. Check the Settings page.

2. Open a private browser window and go to your Scanopsis address. The page
   shows **Sign in with GitLab**.
3. Select it. GitLab asks you to sign in and to authorize Scanopsis.
4. After you authorize, you return to Scanopsis and see the page **Set up
   two-factor sign-in**. Complete the enrolment as described in
   [step 7 of the getting started guide](../getting-started.md#step-7-complete-the-setup-in-the-browser)
   from the QR code onward.

Expected result: you reach the home page. On the **Users** page, an
administrator sees your account with the sign-in method **GitLab** and the
role **Viewer**.

If this fails, see [troubleshooting](#troubleshooting).

## What happens at sign-in

- The first time someone signs in with GitLab, Scanopsis creates an account for
  them with the role **viewer**. The account is identified by the numeric
  GitLab user ID, and the user name comes from GitLab.
- Every user must enrol two-factor authentication in Scanopsis, including users
  who use GitLab sign-in. Scanopsis does not rely on the two-factor
  authentication of GitLab. Until a user finishes the enrolment, the
  application answers with "Set up two-factor authentication to continue."
- Administrators promote users on the **Users** page. Select the role **Admin**
  or **Viewer** next to a user.
- A user who loses the phone needs an administrator to reset their two-factor
  authentication on the **Users** page, or the
  [administration command](../operations.md#reset-two-factor-authentication).

## Allowed groups

The setting `gitlab.allowed_groups` limits sign-in to members of GitLab groups.
These are the exact rules.

- If the list is empty, every GitLab user can sign in. Do not leave it empty on
  a GitLab instance that is open to the public, such as `gitlab.com`.
- Write each group by its **full path**, as it appears in the group's URL: for
  example `platform` for a top-level group and `security/appsec` for a
  subgroup. Do not start or end with `/`, and do not use spaces.
- A user may sign in if they belong to at least one listed group. An entry
  matches the group itself and all groups below it. The entry `platform`
  matches the groups `platform` and `platform/payments`. The entry
  `security/appsec` matches `security/appsec` and its subgroups, but not
  `security`.
- Membership that a user inherits from a parent group counts. Scanopsis reads
  the user's groups from the GitLab userinfo endpoint, and GitLab lists the
  groups that the user belongs to directly or through an ancestor group.
- The comparison is case-sensitive. GitLab ignores case in paths, but it
  reports each path in the case it was created with, and Scanopsis compares
  that text. Copy the path from GitLab. The entry `Platform` does not match the
  group `platform`.
- Scanopsis checks the groups at sign-in only. Changing the list does not end
  sessions that already exist. To make a user sign in again, and so pass the
  check again, reset their two-factor authentication on the **Users** page.
  That signs the user out everywhere.
- If GitLab cannot return the groups, sign-in fails with `login_failed`, and it
  does not fall back to allowing the user.

## Troubleshooting

When a sign-in fails, GitLab sends the browser back to the sign-in page with an
error code, and the page shows a message. Scanopsis also writes one log line
with the reason and the technical error:

```sh
docker compose logs scanopsis | grep "sign-in failed"
```

Example of a line:

```text
{"time":"2026-11-19T03:59:00.012603263Z","level":"WARN","msg":"GitLab sign-in failed","reason":"group_not_allowed","error":"user is in none of the allowed groups"}
```

### Error codes on the sign-in page

| Code | Message on the page | Cause | What to do |
|---|---|---|---|
| `access_denied` | You cancelled the sign-in on GitLab. | The user selected **Deny** on the GitLab authorization page, or GitLab refused the request. | Sign in again and select **Authorize**. If the user did not deny, check in GitLab that the application exists and has not been deleted. |
| `group_not_allowed` | Your GitLab account is not in a group that may use Scanopsis. Ask an administrator to add your group. | The user belongs to none of the allowed groups. | Check in GitLab that the user is a member of a listed group, directly or through a parent group. Check that the entry on the Settings page is the full path with the same upper and lower case. Add the group, then ask the user to sign in again. |
| `login_expired` | The sign-in took too long or was started in another tab. Try again. | The browser did not return the sign-in cookie, or more than 10 minutes passed, or the user started a second sign-in in another tab. | Start again from the Scanopsis sign-in page. Open Scanopsis with exactly the host name in `SCANOPSIS_PUBLIC_URL`. A different name, such as an IP address or a short name, hides the cookie. Check that the reverse proxy passes `Set-Cookie` headers. |
| `login_failed` | Sign-in with GitLab failed. Try again, and contact an administrator if it keeps failing. | Any other failure, for example a wrong application secret, GitLab unreachable during the exchange, an untrusted certificate or a wrong clock. | Read the `error` field in the log line and use the rows below. |

### Other problems

| Symptom | Cause | What to do |
|---|---|---|
| The browser shows `{"error":{"code":"gitlab_unavailable", ...}}` with HTTP status 503 and the text "GitLab could not be reached. Try again in a few seconds, or sign in as the local administrator." | Scanopsis could not read the OpenID Connect configuration from `<GitLab URL>/.well-known/openid-configuration`. The log line `GitLab discovery failed` gives the reason, such as `no such host`, `connection refused` or a certificate error. | Check the GitLab URL on the Settings page. Check DNS, routing and firewalls from the Docker host to GitLab. Scanopsis waits 10 seconds after a failure before it tries again, so wait a moment before you retry. Local sign-in still works. |
| GitLab shows an error page about the redirect URI before sign-in | The redirect URI of the GitLab application is not exactly `<SCANOPSIS_PUBLIC_URL>/api/v1/auth/gitlab/callback`. Scheme, host, port and path must all match. | Edit the application in GitLab. If you changed `SCANOPSIS_PUBLIC_URL`, update the redirect URI too. |
| `login_failed`, log says `token exchange: oauth2: "invalid_client"` | GitLab rejected the application ID or the secret. | Enter the ID and secret again on the Settings page. Create a new secret in GitLab if you do not have the old one. |
| `login_failed`, log contains `x509: certificate signed by unknown authority` (also seen in `GitLab discovery failed`) | GitLab uses a certificate from a certificate authority that the Scanopsis container does not trust. | Provide the CA certificate with `SCANOPSIS_CA_FILE`. See [outgoing TLS connections](../configuration.md#outgoing-tls-connections). |
| `login_failed`, log says `ID token:` followed by a message such as `token is expired` | The clock of the Scanopsis host and the clock of GitLab differ too much. The ID token lives only a short time. | Synchronize both hosts with a time service such as chrony or systemd-timesyncd. Compare `date -u` on both. |
| `login_failed`, log says `userinfo:` | Allowed groups are set, and Scanopsis could not read the groups from GitLab. | Check that GitLab is reachable from the container and that the application has the scopes `openid`, `profile` and `email`. Read the rest of the `error` field for GitLab's answer. |
| No **Sign in with GitLab** button | GitLab sign-in is not fully configured. | Check that the URL, the application ID and the secret are all set. `curl -s <public URL>/api/v1/auth/methods` must show `"gitlab":true`. |
| Everything works for administrators but a user sees "Set up two-factor authentication to continue." | This is expected. Every user must enrol. | Ask the user to finish the enrolment. |
