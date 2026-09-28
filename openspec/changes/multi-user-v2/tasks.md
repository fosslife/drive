## 1. Account store: schema, roles, passwords, sessions

- [x] 1.1 Append migration `schemaV6` adding `users.quota_bytes INTEGER NOT NULL DEFAULT 0` and `users.sessions_valid_from INTEGER NOT NULL DEFAULT 0`, editing no shipped migration; verify a test opens a v5 index, migrates, and asserts `PRAGMA user_version` is 6 with every existing account unchanged and unlimited
- [x] 1.2 Add `Store.SetPassword(username, newPassword)` and `Store.ChangePassword(userID, current, new)`, both re-hashing with Argon2id and bumping `sessions_valid_from`; verify a test asserts the new password authenticates, the old one does not, and a wrong `current` is refused without changing the stored hash
- [x] 1.3 Record the login time in the session and reject a session older than its account's `sessions_valid_from`; verify a test authenticates a session, resets that account's password, and asserts the session no longer authenticates while an API token for the same account still does
- [x] 1.4 Refuse `SetDisabled` and `Delete` when the target is the acting administrator's own account, and offer no way at all to grant or revoke administrator status; verify a table-driven test covers delete and disable of your own account, the same operations on somebody else's, and that the store exposes no role-changing method
- [x] 1.5 Add `Store.Usage(userID)` returning bytes and file count from `files` (`kind='file'`, state `present` or `trashed`) plus the declared size of unfinished `uploads`; verify a test asserts a trashed file still counts, a purged one does not, a `missing` row does not, and an in-flight upload reserves its declared size
- [x] 1.6 Add `Store.SetQuota(username, bytes)` and carry `quota_bytes` on `User`, with `Create` taking its initial value from configuration; verify a test asserts a quota below current usage is stored and no file row or byte is touched

## 2. Configuration

- [x] 2.1 Add `DRIVE_DEFAULT_QUOTA_BYTES` defaulting to `0` (unlimited), parsed as a plain byte count and rejected at startup when negative or unparseable; verify a config test covers unset, a set value, and a malformed one naming the offending variable

## 3. Account API

- [x] 3.1 Replace `POST /api/admin/users/{username}/disabled` with `PATCH /api/admin/users/{username}` accepting optional `disabled` and `quota_bytes`, and drop `is_admin` from account creation; verify a test asserts each field applies alone, an empty body changes nothing, the removed route is gone, and a request that tries to set `is_admin` anywhere is refused
- [x] 3.2 Add `POST /api/admin/users/{username}/password` (administrator reset) and `POST /api/me/password` (self-service, current password required, rate-limited on the failed-login limiter); verify tests assert a non-administrator cannot reset another account, a wrong current password is refused and counts towards the limit, and neither endpoint ever returns a password hash
- [x] 3.3 Report usage in the admin inventory: extend `GET /api/admin/users` with `created_at`, `quota_bytes`, `usage_bytes`, and `file_count`; verify a test asserts the figures match what an independent count of the account's files and trash produces
- [x] 3.4 Extend `GET /api/me` with `quota_bytes` and `usage_bytes` for the signed-in account only; verify a test asserts one account cannot read another's figures from it
- [x] 3.5 Translate the store's refusals to status codes — last administrator and self-action to `409`, unknown account to `404`, bad input to `400` — with the reason in the message; verify a test asserts the exact status for each and that the message names why
- [x] 3.6 Assert administrator status grants no file access: verify a test has an administrator request a listing, download, search, and thumbnail for a path in another account's root and asserts the same refusal a non-administrative user receives

## 4. Quota enforcement

- [x] 4.1 Refuse upload creation when `usage + pending + Upload-Length` exceeds the account's quota, before any byte is stored, with a message naming the quota and the pending reservation; verify a test asserts the refusal, that no temp file was created, and that an account with no quota is unaffected
- [x] 4.2 Keep the two refusals distinguishable: the free-space reserve keeps reporting the volume, the quota reports the account; verify a test asserts each refusal's message and that neither wording appears in the other's case
- [x] 4.3 Leave every non-write path working over quota; verify a test puts an account past its quota and asserts list, search, download, thumbnail, trash, restore, and permanent delete all succeed, and that permanent delete lowers usage enough for the next upload to be accepted
- [x] 4.4 Confirm concurrent uploads cannot race past the limit; verify a test starts several uploads at once against a quota that fits only one and asserts exactly one is accepted

## 5. Instance state and rescan

- [ ] 5.1 Extend `scan.Status` with when the last scan started, how long it took, the entries it saw, and the error it failed with, retained until a later scan succeeds; verify a test asserts a failed scan's error is still reported afterwards and is cleared by a successful one
- [ ] 5.2 Pass an `Instance` value (version, data directory, process start time, scanner `Trigger`) to `server.New` and add `GET /api/admin/instance` reporting those plus free and total space on the data volume and the last-scan facts; verify a test asserts every field is present and that a non-administrator is refused
- [ ] 5.3 Add `POST /api/admin/scan` triggering a scan without restarting, idempotent while one is running; verify a test asserts a second request during a scan is accepted, starts no concurrent scan, and does not disturb the running one
- [ ] 5.4 Plumb the build version through from `cmd/drive`; verify a test builds with `-ldflags "-X main.Version=..."` or asserts the server reports the version it was constructed with

## 6. Interface: accounts screen

- [ ] 6.1 Add the accounts screen in a new `web/src/admin.jsx` at `/accounts`, a fifth series in the nav rendered only when `/api/me` reports `is_admin`; verify a node test of the route/visibility logic and an e2e assertion that the tab is absent for a non-administrator
- [ ] 6.2 List every account with role, state, created, usage against quota, and file count, in the interface's existing notes-column idiom; verify an e2e test asserts two accounts appear with their figures
- [ ] 6.3 Wire the lifecycle actions — create, disable, enable, delete, reset password, set and clear quota; verify an e2e test creates an account, signs in as it, and asserts it sees an empty drive of its own
- [ ] 6.4 Surface the API's refusals as the reason, not a generic failure; verify an e2e test attempts to disable the administrator's own account and asserts the stated reason appears on screen and the row is unchanged
- [ ] 6.5 Add the instance panel and the rescan button to the same screen; verify an e2e test asserts version, data directory, and free space are shown and that pressing rescan reports indexing

## 7. Interface: the account's own card

- [ ] 7.1 Add `/account`, reached from the username in the masthead, holding the password form and the account's own usage against its quota; verify an e2e test changes a password, signs out, and signs in with the new one
- [ ] 7.2 Report a wrong current password in place without clearing the form; verify an e2e test asserts the message and that the user stays signed in
- [ ] 7.3 Show the quota refusal on an upload that would exceed it, in the notes column beside the listing; verify an e2e test uploads past a small quota and asserts the message names the limit

## 8. Documentation and close-out

- [ ] 8.1 Document `DRIVE_DEFAULT_QUOTA_BYTES` in `README.md` and the `CLAUDE.md` environment list, and the quota's reach — application writes only, not SSH or rsync — where the storage rules are stated; verify the variable appears in both lists and the caveat is stated once, not twice
- [ ] 8.2 Record the design decisions worth keeping in `DECISIONS.md`: usage computed never stored, pending uploads reserving quota, the session epoch over deleting session rows, quota checked above `storage` rather than inside it, and one administrator assigned once instead of a role system with guards; verify each entry names the alternative it rejected
- [ ] 8.3 Run the full gate — `go build ./... && go vet ./... && go test ./...`, `npm --prefix web run build`, `npm --prefix web test`, `npm --prefix web run test:e2e` — and verify the route table test still enumerates the same public routes it did before this change
