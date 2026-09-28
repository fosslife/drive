# Design — multi-user-v2

## Context

See `proposal.md` → Why. What exists today and constrains the design:

- `internal/auth.Store` owns accounts: `Create`, `List`, `Active`, `Authenticate`, `SetDisabled`,
  `Delete`, plus tokens. There is no password change, no role change, no quota.
- `internal/server` already has `requireAdmin` and four `/api/admin/users` routes, none of them reachable
  from the interface. `Server` is constructed once in `cmd/drive/main.go` and once in the test harness.
- Sessions are `alexedwards/scs` over the index. A session holds one value, `user_id`; identity and
  privileges are read from the index on every request, which is why disable and delete already take
  effect on the next request.
- `storage.Root.CheckSpace` guards free space on the data volume. `storage` has no access to the index,
  deliberately — it is the layer that only knows about bytes and paths.
- Uploads are tus and `Upload-Length` is mandatory (`internal/server/upload.go`), so a file's size is
  known before the first byte is stored. The `uploads` table holds that declared size.
- The index is disposable and rebuilt by scanning. Nothing user-visible may exist only there, accounts
  excepted, and that exception is already documented.

## Goals / Non-Goals

Goals beyond the proposal's scope statement:

- Quota accounting that cannot drift, given the index is thrown away and rebuilt.
- One place per rule: the last-administrator rule and the quota rule each hold for the browser and for an
  API token because both route through the same function, not because two handlers agree.
- The interface stays an ordinary client of the API.

Non-goals at design level:

- No maintained usage counter, no triggers, no materialized totals.
- No enforcement of quotas against writes that bypass the application (SSH, rsync). Out of reach by
  construction; see Risks.
- No email, no password-reset link, no invitation flow. There is no address on an account and no mailer in
  a single binary with no configuration.

## Decisions

### Usage is computed from the index, never stored

`SUM(size)` over an account's file rows, plus the declared size of its in-flight uploads:

```sql
SELECT COALESCE(SUM(size), 0), COUNT(*) FROM files
 WHERE user_id = ? AND kind = 'file' AND state IN ('present', 'trashed')
```

Trashed rows count because their bytes are still on the volume. `missing` rows do not, because their bytes
are not.

Alternative considered and rejected: a `users.usage_bytes` counter maintained on every write. It is faster
and it is wrong for this codebase — a counter is authoritative state for user-visible behaviour living in
the disposable half of the system, and every path that frees or consumes bytes becomes a place the counter
can drift. Recomputing costs one indexed aggregate at the moments that matter (an upload starting, an
admin opening a screen), not per chunk.

`ponytail: SUM over one user's rows per upload creation and per admin listing; a maintained counter or a
cached total only if a folder of 100k files makes the aggregate show up in upload latency.`

### In-flight uploads count toward the quota

The check is `existing + pending + this upload's Upload-Length <= quota`, where `pending` is the declared
size of that user's unfinished uploads. Without the pending term, ten concurrent uploads each pass the
check and together blow past the limit. With it, a quota cannot be exceeded by racing, and the worst case
is a user being refused for space their abandoned uploads still reserve — which the existing upload
retention sweep releases.

### Quota is enforced where the size is known, not in `storage`

The check goes in the upload path in `internal/server` (upload creation) and in `internal/files` for any
future operation that adds bytes. It does not go into `storage.Root`: giving the bytes-and-paths layer a
database handle to answer "whose root is this and what is their allowance" inverts the layering for one
caller's benefit.

Consequence to keep true: the two refusals stay distinguishable. The reserve refusal keeps coming from
`storage` ("not enough free space on the volume"); the quota refusal comes from the layer above and names
the account's limit. The spec's "two limits are told apart" scenario is the test.

Not enforced against: thumbnail writes (derived data the system chose to create), restore from trash (the
bytes are already counted), permanent delete and folder creation.

### `PATCH /api/admin/users/{username}` replaces `POST .../disabled`

One endpoint takes `disabled` and `quota_bytes`, each optional.
Password reset stays separate at `POST /api/admin/users/{username}/password`, because a secret in a
general-purpose patch body is a secret that ends up in a log line someone added for debugging.

Rejected: keeping the old route beside the new one. Nothing calls it, so the only thing it would buy is two
ways to disable an account and a second place to enforce the last-administrator rule.

### Sessions are invalidated by an epoch, not by deleting rows

`users.sessions_valid_from` holds a unix timestamp. `Login` records the login time in the session
alongside `user_id`; `authenticate` rejects a session whose recorded login time is older than the account's
`sessions_valid_from`. A password change or reset sets it to now.

Rejected: deleting that user's rows from the scs session table. scs encodes the session payload itself;
finding "rows belonging to user 4" means decoding a third party's storage format, and a library upgrade
then silently stops logging anyone out. The epoch is two columns of arithmetic and it cannot silently fail.

API tokens are deliberately unaffected — a token is revoked by revoking it, and a password change that
killed an account's automation would be a surprise nobody asked for.

### There is no role management, so there is almost no rule to enforce

An instance has one administrator, created by first-run setup. Nothing else writes `is_admin`: no promote,
no demote, no hand-over, no `is_admin` field on account creation or on the patch endpoint.

The first cut of this design had two rules instead — refuse to remove the last enabled administrator, and
refuse to act on your own account — and the first of them turned out to be unreachable over HTTP. Whoever
is calling is an enabled administrator, so a *different* enabled administrator as the target means two
exist and the guard never fires; the only way the target could be the last one is if it is the caller,
which the second rule already refused. A correlated subquery on every destructive statement, to enforce an
invariant that nothing could violate.

Removing role management entirely makes the invariant structural rather than enforced: with no way to
grant or revoke the status, no sequence of permitted operations can leave an instance without its
administrator. One rule survives, and it is the one that catches the actual accident — you cannot delete
or disable your own account. It is an `if` on the username, not SQL.

What this gives up, deliberately: a second administrator, and handing the drive to someone else without
handing over the password. Both are outside a product whose users are an operator and the family they gave
accounts to.

Rejected: keeping the last-administrator guard as defence in depth for a future caller that administers
without an acting user. There is no such caller, and an unreachable guard is a thing to maintain and
misread, not a safety net.

### Administrator status does not grant file access

Stated in the auth delta and enforced by construction: every path is resolved through the requesting
user's own `storage.Root`, and nothing in this change adds an administrative file route. The one new test
worth having asserts an administrator gets the same refusal as anyone else for another account's path.

### Instance state comes from one struct passed to `server.New`

`server.New` grows one parameter — an `Instance` value carrying version, data directory, process start
time, and the scanner's `Trigger` — rather than four more positional arguments. Two call sites, one of
them the test harness. Free and total space come from the same syscall `storage` already uses for the
reserve; last-scan facts come from `scan.Status`, extended with when the last scan started, how long it
took, and its error.

### Screens: one admin screen, one account card

- `/accounts` — admin only, a fifth series in the nav, rendered only when `/api/me` says
  `is_admin`. Accounts table (role, state, created, usage against quota, file count) with the lifecycle
  actions, the instance panel, and the rescan button.
- `/account` — every user, reached from the username in the masthead rather than the series nav, because
  the reader's own card is not one of the collection's series. Holds the password form and the account's
  own usage against its quota.
- New file `web/src/admin.jsx`. `panels.jsx` is already 400 lines of three unrelated screens.

Server-side authorization is the real gate; hiding the tab is presentation.

## Risks / Trade-offs

- **Quotas do not bind writes made outside the application** (SSH, rsync, a file manager on the host) →
  Accept and document. The filesystem is the source of truth; an application-level allowance can only
  refuse the application's own writes, and the reconciler indexes whatever it finds rather than deleting it.
  The operator's protection against a filled volume remains the free-space reserve.
- **Usage is the sum of apparent file sizes, not blocks consumed** → A sparse file or a small-file-heavy
  tree occupies more or less on disk than the figure shown. Accept; the instance panel shows real volume
  space beside it, which is the number that matters when the disk fills.
- **A deleted account's bytes stay on disk and leave every usage figure** → That is the existing,
  deliberate behaviour (nothing but permanent delete frees a byte). The instance panel's free space is
  where the operator sees it; `docs/backup.md` is where the cleanup is a manual, considered act.
- **The aggregate query grows with the file count** → See the `ponytail:` note above; one query per upload
  creation, not per chunk.
- **Abandoned uploads reserve quota until the retention sweep** → Bounded by `DRIVE_UPLOAD_RETENTION`
  (24h default) and visible: the refusal names the pending reservation, so it is explainable rather than
  mysterious.
- **An administrator who forgets their password has no way back in** → First-run setup only reopens when
  the instance has no accounts at all, so an instance with family members on it and a locked-out operator
  can currently only be repaired by editing `index.db` by hand, which this project's rules put out of
  bounds as an operation. This is true today and is not made worse by dropping hand-over: hand-over needs
  you to still be signed in, so it never helped the case that actually happens. The fix is to reopen the
  setup token when an instance has no enabled administrator; it is in `BACKLOG.md` rather than here,
  because it changes what a printed setup token can do and deserves its own decision.

## Migration Plan

1. One appended migration adds `users.quota_bytes INTEGER NOT NULL DEFAULT 0` (0 means unlimited) and
   `users.sessions_valid_from INTEGER NOT NULL DEFAULT 0`. No shipped migration is edited.
2. Defaults are chosen so that an existing instance is unchanged: no account has a quota, and every
   existing session keeps working (`0` is older than any login time).
3. `DRIVE_DEFAULT_QUOTA_BYTES` defaults to `0`, so an operator who sets nothing gets today's behaviour.
4. Rollback: an older binary refuses to start against a newer index (`index.VersionError`), so rolling
   back a release means restoring `index.db` from a backup, or deleting it and re-creating accounts — the
   procedure `docs/backup.md` already documents. Nothing in this change touches a byte of user data, so
   the rollback risk is logins, never files.

## Open Questions

- Whether the account card should show a usage bar to users with no quota at all. It has real information
  (how much you are storing) and no limit to show it against. Decided at the point the screen is built;
  changes no requirement.
