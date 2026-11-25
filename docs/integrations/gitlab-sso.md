# Sign in with GitLab

Let people sign in to Scanopsis with their GitLab account. This guide connects
Scanopsis to your GitLab as an OpenID Connect client, so Scanopsis never sees a
GitLab password.

**Audience:** Operators · **Back to:** [README](../../README.md) · [All guides](../../README.md#documentation)

---

## Before you start

You need:

- A running Scanopsis with the setup finished. See the [getting started
  guide](../getting-started.md).
- `SCANOPSIS_PUBLIC_URL` set to the address people open in the browser, for
  example `https://scanopsis.example.com`. GitLab sends people back to this
  address.
- A network path from Scanopsis to your GitLab, for example
  `https://gitlab.example.com`. Scanopsis calls GitLab itself to read its OpenID
  Connect configuration, to exchange the sign-in code and to read the user's
  groups.
- A GitLab administrator, or the Owner of a group, to create the application.

If your GitLab uses a certificate from an internal certificate authority, set
`SCANOPSIS_CA_FILE` first, see [configuration](../configuration.md). Scanopsis
has no option to skip certificate checks.

Local accounts keep working next to GitLab sign-in. Keep at least one
administrator as a local account, so you can still sign in when GitLab is down.

## Step 1. Create the application in GitLab

Menu names differ a little between GitLab versions. If you cannot find one,
check the GitLab documentation for your version.

1. In GitLab, open **Admin area > Applications** and select **Add new
   application**. To own the application in a group instead, open the group,
   then **Settings > Applications**.
2. Fill in the form:
   - **Name**: `Scanopsis`.
   - **Redirect URI**: your public URL plus `/api/v1/auth/gitlab/callback`, for
     example `https://scanopsis.example.com/api/v1/auth/gitlab/callback`.
     Scheme, host and port must match `SCANOPSIS_PUBLIC_URL` exactly.
   - **Confidential**: selected. Scanopsis is a server application and keeps the
     secret safe.
   - **Scopes**: `openid`, `profile` and `email`, and nothing else.
3. Save the application.
4. Copy the **Application ID** and the **Secret**. GitLab may show the secret
   only once. If you lose it, renew the secret in GitLab.

The application now appears in the list with the three scopes.

## Step 2. Enter the application in Scanopsis

1. Sign in to Scanopsis as an administrator and open **Settings**.
2. In the section **GitLab**, fill in:
   - **GitLab URL** (`gitlab.url`): the base URL of your GitLab, for example
     `https://gitlab.example.com`, without a path.
   - **Application ID** (`gitlab.client_id`): the application ID.
   - **Application secret** (`gitlab.client_secret`): the secret. Scanopsis stores it encrypted and never
     shows it again.
   - **Groups that may sign in** (`gitlab.allowed_groups`): the groups whose
     members may sign in, one per line. Read [Choose who may sign in](#choose-who-may-sign-in) first. If you
     leave it empty, every user of your GitLab can sign in.
3. Select **Save changes**. The page shows "Settings saved" and how many
   changes apply now, for example "3 changes apply now." No restart is needed.

If saving fails, the page names the field:

- "Required when other GitLab sign-in settings are set." The URL, the
  application ID and the secret must all be filled in.
- "Group path ... must not start or end with a slash." or "Group path ... must
  not contain spaces." A group needs its plain full path, such as
  `security/appsec`.

Instead of the Settings page, you can seed these values with
`SCANOPSIS_GITLAB_URL`, `SCANOPSIS_GITLAB_CLIENT_ID`,
`SCANOPSIS_GITLAB_CLIENT_SECRET` and `SCANOPSIS_GITLAB_ALLOWED_GROUPS`
(comma-separated). A seed is saved at every start for a setting that has no
saved value, so it applies only while the setting was never saved on the
Settings page. See [configuration](../configuration.md).

## Step 3. Check that it works

1. Ask Scanopsis which sign-in methods it offers:

   ```sh
   curl -s https://scanopsis.example.com/api/v1/auth/methods
   ```

   It prints:

   ```json
   {"gitlab":true,"local":true}
   ```

   If `gitlab` is `false`, the URL, the application ID or the secret is missing.
   Check the Settings page.

2. Open a private browser window and go to your Scanopsis address. The sign-in
   page shows **Sign in with GitLab**.
3. Select it. GitLab asks you to sign in and to authorize Scanopsis.
4. After you authorize, you come back to Scanopsis on the page **Set up
   two-factor sign-in**. Scan the QR code with an authenticator app, enter the
   code and save your recovery codes, as in the [getting started
   guide](../getting-started.md).

You reach the home page. On the **Users** page, an administrator now sees your
account with the sign-in method **GitLab** and the role **Viewer**.

If something fails, see [Troubleshooting](#troubleshooting).

## What happens at sign-in

- The first time someone signs in with GitLab, Scanopsis creates an account with
  the role **Viewer**. The account is tied to the numeric GitLab user ID and to
  your GitLab URL. The user name comes from GitLab.
- Every user enrols two-factor authentication in Scanopsis, GitLab users too.
  Scanopsis does not rely on the two-factor authentication of GitLab. Until a
  user finishes the enrolment, the application answers "Set up two-factor
  authentication to continue."
- To make someone an administrator, open **Users** and choose **Admin** in the
  role list next to them, or **Make admin** in their actions menu.
- A user who loses their phone needs an administrator to select **Reset
  two-factor** in their actions menu on the **Users** page, or to run the `reset-2fa` command
  described in [operations](../operations.md).
- Ten wrong two-factor codes in a row lock the second factor, and Scanopsis
  signs the user out everywhere. The user sees "Too many wrong codes. Ask an
  administrator to reset two-factor authentication." Even the right code is
  refused until an administrator resets it.

## Choose who may sign in

The setting `gitlab.allowed_groups` limits sign-in to members of some GitLab
groups. The rules:

- **Empty list:** every GitLab user can sign in. Do not leave it empty on a
  GitLab that is open to the public, such as `gitlab.com`.
- **Full paths:** write each group by its full path, as in the group's URL:
  `platform` for a top-level group, `security/appsec` for a subgroup. No leading
  or trailing `/`, no spaces.
- **Subgroups count:** a user may sign in if they belong to at least one listed
  group. An entry matches the group and every group below it. `platform` matches
  `platform` and `platform/payments`. `security/appsec` matches
  `security/appsec` and its subgroups, but not `security`.
- **Inherited membership counts:** Scanopsis reads the groups from the GitLab
  userinfo endpoint, which lists the groups a user belongs to directly or
  through a parent group.
- **Case matters:** GitLab ignores case in paths, but reports each path in the
  case it was created with, and Scanopsis compares that text. Copy the path from
  GitLab: the entry `Platform` does not match the group `platform`.
- **Checked at sign-in only:** changing the list does not end existing sessions.
  To make someone pass the check again now, select **Disable user** and then
  **Enable user** in their actions menu on the **Users** page. Disabling signs the user out everywhere.
- **No fallback:** if GitLab cannot return the groups, sign-in fails with
  `login_failed`. It never lets the user in by default.

## Troubleshooting

When a sign-in fails, Scanopsis sends the browser back to the sign-in page with
an error code, and the page shows a message. Scanopsis also writes one log line
with the reason and the technical error. With the Docker Compose deployment:

```sh
docker compose logs scanopsis | grep "sign-in failed"
```

A line looks like this:

```json
{"time":"2026-11-22T17:15:07.403465+06:00","level":"WARN","msg":"GitLab sign-in failed","reason":"group_not_allowed","error":"user is in none of the allowed groups"}
```

### Error codes on the sign-in page

| Code | Message on the page | Cause | Fix |
| --- | --- | --- | --- |
| `access_denied` | You cancelled the sign-in on GitLab. | The user selected **Deny** on the GitLab authorization page, or GitLab refused the request. | Sign in again and select **Authorize**. If the user did not deny, check in GitLab that the application still exists. |
| `group_not_allowed` | Your GitLab account is not in a group that may use Scanopsis. Ask an administrator to add your group. | The user is in none of the allowed groups. | Check in GitLab that the user is a member of a listed group, directly or through a parent group. Check that the entry is the full path, with the same upper and lower case. Then ask the user to sign in again. |
| `account_disabled` | Your account is disabled. Ask an administrator to enable it. | An administrator disabled the account in Scanopsis. | Select **Enable user** in the user's actions menu on the **Users** page, if they should have access. |
| `login_expired` | The sign-in took too long or was started in another tab. Try again. | The browser did not send back the sign-in cookie, more than 10 minutes passed, or the user started a second sign-in in another tab. | Start again from the Scanopsis sign-in page. Open Scanopsis with exactly the host name in `SCANOPSIS_PUBLIC_URL`: another name, such as an IP address or a short name, loses the cookie. Check that the reverse proxy passes `Set-Cookie` headers. |
| `login_failed` | Sign-in with GitLab failed. Try again, and contact an administrator if it keeps failing. | Any other failure, for example a wrong application secret, GitLab unreachable during the exchange, an untrusted certificate or a wrong clock. | Read the `error` field of the log line and find it in the next table. |

### Other problems

| Symptom | Cause | Fix |
| --- | --- | --- |
| The browser shows `{"error":{"code":"gitlab_unavailable","message":"GitLab could not be reached. Try again in a few seconds, or sign in as the local administrator."}}` | Scanopsis could not read `<GitLab URL>/.well-known/openid-configuration`. The log line `GitLab discovery failed` gives the reason, such as `no such host`, `connection refused` or a certificate error. | Check `gitlab.url` in Settings, and DNS, routing and firewalls between Scanopsis and GitLab. After a failure Scanopsis waits 10 seconds before it tries again, so wait a moment. Local sign-in still works. |
| GitLab shows an error about the redirect URI before the sign-in. | The redirect URI of the GitLab application is not exactly `<SCANOPSIS_PUBLIC_URL>/api/v1/auth/gitlab/callback`. | Edit the application in GitLab. If you changed `SCANOPSIS_PUBLIC_URL`, change the redirect URI too. |
| `login_failed`, and the log says `token exchange: oauth2: "invalid_client"` | GitLab rejected the application ID or the secret. | Enter both again in Settings. Renew the secret in GitLab if you no longer have it. |
| `login_failed`, and the log contains `x509: certificate signed by unknown authority`, also in `GitLab discovery failed` | Scanopsis does not trust the certificate authority of your GitLab. | Give Scanopsis the CA certificate with `SCANOPSIS_CA_FILE`, see [configuration](../configuration.md). |
| `login_failed`, and the log says `ID token:` followed by a message such as `token is expired` | The clocks of Scanopsis and GitLab differ too much. The ID token lives only a short time. | Synchronise both hosts with a time service such as chrony or systemd-timesyncd. Compare `date -u` on both. |
| `login_failed`, and the log says `userinfo:` | Allowed groups are set, and Scanopsis could not read the groups from GitLab. | Check that GitLab is reachable and that the application has the scopes `openid`, `profile` and `email`. The rest of the `error` field has GitLab's answer. |
| No **Sign in with GitLab** button. | GitLab sign-in is not fully configured. | Set the URL, the application ID and the secret. `curl -s <public URL>/api/v1/auth/methods` must show `"gitlab":true`. |
| A new user sees "Set up two-factor authentication to continue." | This is expected: every user enrols. | Ask the user to finish the enrolment. |

---

[Back to the README](../../README.md) · [All guides](../../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
