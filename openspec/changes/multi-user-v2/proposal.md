## Why

v1 can hold several accounts but gives the operator no way to run them. The admin API exists
(`GET/POST /api/admin/users`, disable, delete) and nothing in the interface reaches it, so handing a
family member an account means curling the API from a terminal. Once the account exists there is no
way for that person to change their own password, no way for the operator to reset it, no way to see
who is using what, and nothing stops one account filling the disk for everyone else.

The daily complaint is the admin surface, not the file surface: the drive is usable and unadministrable.

## What Changes

- An **Accounts screen**, admin-only, as a fifth series in the interface: every account with its role,
  state, storage used and file count; create, disable, enable, delete; reset a password.
- **Self-service password change** for every user, current password required. There is none today.
- **One administrator, assigned once.** First-run setup creates it and nothing else ever writes the flag:
  there is no promote, no demote, no hand-over, and therefore no rule anyone has to enforce to keep an
  instance administrable. The one refusal that remains is that an administrator cannot delete or disable
  their own account, refused by the API rather than hidden by the interface.
- **Credential changes end sessions.** Changing or resetting a password invalidates that account's
  existing browser sessions; disable and delete already take effect on the next request.
- **Per-user quotas.** An optional byte allowance per account, settable by an administrator, with a
  default for new accounts from `DRIVE_DEFAULT_QUOTA_BYTES` (`0`, the default, means unlimited).
  Usage counts what the account occupies on disk, trash included. Over quota, writes are refused with a
  message that names the limit; reads, listing, download, restore and permanent delete keep working.
- **Instance overview** on the same screen: version, uptime, data directory, free and total space on the
  data volume, when the last scan ran, how long it took, and its error if it failed.
- **Trigger a rescan** from the interface, admin-only, over the scanner's existing `Trigger`.
- API tokens continue to carry exactly their owner's authority: a token belonging to an administrator can
  administer, one belonging to anyone else cannot.

Not in this change, deliberately: shared folders between accounts, groups, per-path permissions. Those
need a centralized permission check to replace root isolation, and that is its own change on top of this
one. No sync client, no change feed — nothing consumes one yet.

## Capabilities

### New Capabilities

- `administration`: the operator's surface over an instance — account lifecycle (create, disable, delete,
  role, password reset), quota administration, instance state (space, scan, version), and triggering a
  rescan. Who may reach it and how the caller is authenticated stay in `auth`.

### Modified Capabilities

- `auth`: adds self-service password change, administrator password reset, session invalidation on a
  credential change, and the rule that administrator status is assigned by first-run setup and by nothing
  else. The existing "Administrator manages accounts" scenario grows the cases it has to survive.
- `storage`: adds a per-user quota alongside the existing free-space reserve — two separate limits with
  two separate reasons, both refusing the write and saying which one refused it.

## Impact

- `internal/auth`: `Store` gains password change/reset, quota read/write, a usage query, and a refusal to
  act on the caller's own account. New migration: `quota_bytes` and a session-epoch column on `users`.
- `internal/index`: one appended migration. Nothing existing is edited.
- `internal/server`: `/api/me/password`, `/api/admin/users/{username}` (quota, disabled),
  `/api/admin/users/{username}/password`, `/api/admin/instance`, `/api/admin/scan`; `/api/me` and the
  admin user list grow usage fields. Every new route is authenticated; the route table test's hardcoded
  public list does not change.
- **BREAKING**: `POST /api/admin/users/{username}/disabled` is replaced by
  `PATCH /api/admin/users/{username}`, which sets quota and disabled state through one endpoint.
  Nothing ships that calls the old route — no screen reaches it and no documentation names it — so it is
  replaced rather than kept as a second way to do one thing.
- **BREAKING**: `POST /api/admin/users` no longer takes `is_admin`. Administrator status comes from
  first-run setup and from nothing else.
- `internal/storage` / upload path: quota checked where `CheckSpace` already guards free space, so both
  limits are refused in one place.
- `internal/config`: `DRIVE_DEFAULT_QUOTA_BYTES`, defaulted to unlimited.
- `web/`: a fifth tab and screen, a password form, quota display for the signed-in user; `web/e2e` gains
  coverage of the admin screen. The interface is an ordinary client — no private endpoints.
- Docs: `README.md` and `CLAUDE.md` environment list; `docs/backup.md` unchanged (quotas are index state,
  rebuilt from the same scan).
- No new dependency. Behaviour for existing data is unchanged: no quota means unlimited, so an instance
  that sets none behaves exactly as it does today.
