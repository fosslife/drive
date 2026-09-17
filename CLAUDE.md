# drive

Self-hosted drive. **The filesystem is the source of truth; SQLite is a disposable index.**
Any design that makes the index authoritative for user-visible state is wrong, however fast it is.

Full plan: `openspec/changes/bootstrap-drive-v1/` (proposal, design, specs, tasks).
Rejected ideas and their reasons: `BACKLOG.md`. Read `design.md` before making an architectural call.

## Workflow

- Work is driven by `openspec/changes/bootstrap-drive-v1/tasks.md`, **one group at a time**, not all 99 at once.
  Implement a group, run the tests, stop for review and a commit, then move to the next.
- `/opsx:apply bootstrap-drive-v1 group N` continues the work. Tick the checkbox as each task lands.
- A task's "verify …" clause is the acceptance criterion. It ships as a test in the same slice, not later.

## Toolchain

Go lives at `~/.local/go/bin` (not on the default PATH, not a system package):

```sh
export PATH=$HOME/.local/go/bin:$PATH
go build ./... && go vet ./... && go test ./...
go build -ldflags "-X main.Version=v0.1.0" -o /tmp/drive ./cmd/drive
```

Podman is available; there is no Docker. `Containerfile` and `compose.yml` land with task 14.4.

## Layout

```
cmd/drive/          entrypoint, startup, graceful shutdown
internal/config/    env-only configuration, every value defaulted
internal/index/     SQLite open + forward-only migrations + schema
internal/storage/   one Root per user: atomic writes, checksums, integrity, space guard, Walk
internal/scan/      the reconciler: filesystem → index, add/update/mark-missing only
internal/auth/      accounts, Argon2id passwords, scs sessions, API tokens, failed-login limiter
internal/files/     browse, create, rename, move, trash: index reads, filesystem writes
internal/share/     public links: create, resolve, confine to one subtree
internal/thumb/     thumbnails: decode, orient, scale, cache as derived data
internal/server/    HTTP surface
```

Environment: `DRIVE_DATA_DIR`, `DRIVE_ADDR`, `DRIVE_MIN_FREE_BYTES` (plain byte count, default 1 GiB),
`DRIVE_SCAN_INTERVAL` (Go duration, default 15m), `DRIVE_UPLOAD_RETENTION` (Go duration, default 24h),
`DRIVE_TRASH_RETENTION` (Go duration, default 720h; `0` means never expire).

Data directory (default `$XDG_DATA_HOME/drive`, else `~/.local/share/drive`, else `/var/lib/drive`):

```
index.db                  file metadata is rebuildable by scanning; accounts are not
users/<username>/         storage root, user files at their real paths
  .drive/{tmp,trash,thumbs}
```

## Rules that are not negotiable

- **Nothing but permanent delete destroys user bytes.** Overwrites and deletes go through trash first.
- **The reconciler has no delete path.** A vanished file marks its row missing; an unreadable root aborts the whole scan.
- **Writes are atomic**: temp file → fsync file → rename → **fsync the parent directory**. The last step is the one people forget.
- **File identity is assigned once and never reused** (`INTEGER PRIMARY KEY AUTOINCREMENT`).
- **Every config value has a working default.** No config file, ever. Env overrides only, `DRIVE_*`.
- **Stored files are served as attachments** with `nosniff` and a restrictive CSP. Inline only for inert raster images — never SVG, never PDF.
- **Single binary, single process.** No second service, no Node runtime, no cgo (hence `modernc.org/sqlite`).

## Decisions made while building, not in the spec

- Config is environment-only; no flags. `version` is the one subcommand.
- Files and folders share one `files` table with a `kind` column, so identity is unique across both and a
  share or upload references a row without a type tag. Path is stored as `(dir, name)`, unique among
  non-trashed rows — a trashed row keeps its original path as the restore target.
- Schema version is `PRAGMA user_version`; migrations are `[]string` in `internal/index/migrations.go`,
  applied one transaction each. **Never edit a shipped migration, append.**
- `sessions` already matches the schema `alexedwards/scs/sqlite3store` expects.
- `/healthz` returns readiness and nothing else — no version, no paths. It is unauthenticated.
- The index connection pool is capped at one connection: that is the "single writer".
- Confinement is `os.Root` (Go 1.24+), not a hand-written path resolver: it blocks `..` and escaping
  symlinks at the syscall level, free of TOCTOU. `relPath` adds `fs.ValidPath` and the `.drive` ban on
  top for early, legible errors. **Never build a real path string and open it directly.**
- `Write` renames over whatever is at the destination, symlink included, and never follows it.
- `storage.Verify` takes the expected checksums as a map; the index feeds it (group 4 wires this up).
- `dir` is `""` for a top-level entry, not `"."`. `scan.split`/`scan.join` are the only place that knows it.
- `mtime` is stored in Unix **seconds**. The scan fast path is `(kind, size, mtime)`; a rewrite to the same
  size within the same second is not rehashed, and `storage.Verify` is the backstop for that.
- The scan walks read-only first and writes afterwards in batches of 500, so a walk that fails partway
  writes nothing at all and a long rebuild becomes browsable as it goes.
- `storage.Write` does not create parent directories (that is task 7.4), so tests that need one `os.Mkdir` it.
- **Accounts live only in the index, deliberately.** Losing `index.db` loses logins; it does not lose a
  byte of user data. Storage roots are therefore `users/<username>/`, not `users/<id>/`, so re-creating an
  account with the same username reattaches it to its files. Usernames are `[a-z0-9._-]` and immutable in
  v1. **Do not write password hashes into a storage root to make this recoverable** — decided against.
- No Postgres, now or later. Filename search is a `LIKE` scan, not FTS; a second engine buys nothing and
  costs the single-binary install.
- `users.storage_root` is relative to the data directory (`users/ada`), never absolute: the data
  directory moves between a host and a container, the roots inside it do not.
- The scanner is one goroutine started with `go scanner.Run(ctx)` after the listener is open. One root
  failing is logged and skipped, not fatal: an unmounted disk for one account must not stall the others.
- `GET /api/scan` reports scan progress and `indexing`, authenticated. There is deliberately no HTTP
  route that triggers a scan; `Scanner.Trigger()` is the on-demand entry point.
- Routes are a `[]route` in `server.routes()` with a `public` flag, and `Handler()` wraps every
  non-public one in `requireAuth`. **Adding a route unauthenticated takes a deliberate `true`**, and a
  test enumerates the slice against a hardcoded public list. Public today: `/healthz`, `POST
  /api/login`, and `GET|POST /api/setup`; share access (11.x) joins it.
- First-run setup keeps only the token's **hash** in `settings`, and `OpenSetup()` mints a **fresh token
  on every start** while no account exists. A restart therefore invalidates the previously printed URL —
  deliberate, so the live token is always the one in the newest output and nothing is stored in the
  clear. `CompleteSetup` consumes it with a conditional `DELETE ... WHERE key = ? AND value = ?`, which
  is the whole concurrency control, and puts it back if account creation fails so a typo is not a lockout.
- `main.setupURL` prints `http://localhost:<port>/setup?token=…` for a wildcard listener: a wildcard
  answers on every address and so names none. `/setup` is a frontend route that lands with task 13.2.
- One `requireAuth` resolves either credential. A bearer token authenticates *as* its owner with no
  scope of its own, so "a token never exceeds its owner's access" is a property of `Store.Root`, not a
  check anyone can forget. There is no path-taking endpoint yet, so 5.8 is tested at that seam.
- CSRF: `sameOrigin` runs outside authentication and refuses any unsafe method that shows neither
  `Sec-Fetch-Site: same-origin|none` nor an `Origin` matching `Host`. **A bearer token is exempt** —
  browsers never attach one, and requiring an `Origin` from curl would break every API client.
- Argon2id at OWASP's low-memory setting (m=19 MiB, t=2, p=1); parameters live in the PHC string so
  raising them later leaves stored hashes verifiable. API token secrets are 256 random bits hashed with
  plain SHA-256 — there is nothing to brute-force, and Argon2id per API request would be absurd.
- A failed login costs one Argon2id verification even for an unknown username (`equaliseTiming`), or the
  response time answers "does this account exist?" whatever the body says.
- The failed-login limiter is in-memory, keyed `user:<name>` and `ip:<addr>`, 5 failures per 15 minutes.
  `clientIP` reads `RemoteAddr` only; **no `X-Forwarded-For`** until there is a trusted-proxy setting.
- Session cookie `Secure` follows whether the listener is encrypted. Hardcoding `true` would mean no
  browser ever sends it over the plaintext listener, i.e. every login silently failing.
- `Store.Delete` removes the account row (files, tokens, shares cascade) and **never touches the storage
  root**. The directory left behind is what makes re-creating the username a recovery.
- `auth.ErrNotFound` is the one "no such row" for accounts and tokens alike. Every authenticated request
  re-reads the account (`Store.Active`), so disable and delete land on the next request, not next login.
- **Listings read the index; anything that changes something writes the filesystem first.** That order is
  not negotiable: if the process dies between the two, the reconciler agrees with the disk. A listing is
  keyset-paginated on `name` (`?cursor=`), never `OFFSET`, so page 500 costs what page 1 does.
- Schema v2 rebuilt `files_listing` as `(user_id, dir, state, name)`. Without `name` in the index SQLite
  sorts the whole folder into a temp b-tree, which is the "first page loads the folder" failure 7.1 bans.
- `index.SplitPath`/`index.JoinPath` are the only place that knows `dir` is `""` for a top-level entry.
- ETag is `"<id>-<mtime>-<size>"`, not the checksum: a per-request rehash to tag a file precisely is not
  worth it, and the same-size-same-second rewrite is what `storage.Verify` already exists to catch.
  `If-Match` on a modifying request opts into a precondition; without it, last write wins.
- `storage.Rename` **refuses an occupied destination**. Replacing is the caller's explicit decision and
  goes through `files.Trash` first — trash is created here in group 7 because `replace: true` cannot be
  correct without it; group 9 adds browsing, restore, and expiry on top of the same layout.
- Subtree SQL uses `substr(dir, 1, ?)` with **rune counts, not byte counts** — SQLite's `substr` on TEXT
  counts characters, so a byte offset loses the subtree of any non-ASCII folder name.
- Serving content: extension **allowlist** (`.jpg .jpeg .png .gif .webp`) gets its real type and
  `inline`; everything else gets `application/octet-stream` and `attachment`, plus `nosniff` and
  `default-src 'none'; sandbox` on every response. The dangerous set is open-ended (SVG carries script,
  PDF carries script, the next one is not invented yet), so the narrow list is the only safe direction.
  `?download` forces an attachment for an allowlisted type.
- Archives are `zip.Store` written straight to the `ResponseWriter` — no temp file, no buffer, no
  `Content-Length`. A failure partway abandons the stream unclosed: a truncated zip fails to open, which
  beats a complete-looking archive quietly missing files.
- tus 1.0 is implemented directly in `server/upload.go` + `files/upload.go`: `POST /api/uploads` (create,
  `Upload-Length` + `Upload-Metadata` carrying `filename`/`dir`/`replace`), `HEAD` (offset), `PATCH`
  (append), `DELETE` (abort), `OPTIONS` (capabilities). All authenticated, all one API.
- **The temp file's size is the upload offset, not the `offset_bytes` column.** A column can disagree
  with the disk after a crash, and telling a client "I have n bytes" when the last few were never
  fsynced is how a resumed upload finishes corrupt. The column is a denormalised copy for the sweep.
- The finished upload's checksum is computed by reading the published file back — one extra sequential
  pass, marked `ponytail:`. `crypto/sha256` can marshal its state between chunks if that ever costs more
  than the per-chunk fsync already does.
- `storage.tempPath` refuses any name this package did not hand out. The temp name round-trips through
  the index on every resume, and a path that could point elsewhere turns an upload into write-anywhere.
- A collision stores `report (2).pdf` unless `replace` was asked for; `replace` sends the previous
  content to trash first, so two sequential overwrites leave both prior versions under their own ids.
  A replacement gets a **new** identity — the trashed row keeps the old one, because it is the
  restorable version.
- `DRIVE_UPLOAD_RETENTION` (default 24h) bounds abandoned upload data. `files.Reclaim` runs on its own
  ticker in `main`, **not** inside the reconciler, which has no delete path and must keep it that way.
- Trash content lives at `.drive/trash/<id>` — the entry itself moved under its own identifier, not a
  directory holding it. A folder goes in one rename, contents and all, and restore is the rename back.
- `files.trash_root` (schema v4) is the id of the entry that was actually deleted, carried by it and by
  every row under it. It is what makes the trash list a folder once instead of once per file, and what
  restore and purge take as a unit. A trashed row keeps its original `(dir, name)` as the restore target.
- Restore **never overwrites and never fails for want of a path**: a deleted parent is recreated, and an
  occupied path sends the item to `notes (2).txt` with the response reporting where it actually landed.
- `files.Purge` is the only function in the codebase that destroys user content, and
  `internal/files/destruction_test.go` is the enforcement: it parses every non-test file and fails on any
  `Remove`/`RemoveAll`/`Truncate`/`os.Create`/`O_TRUNC` outside a four-entry allowlist, plus any
  `DELETE FROM files` outside `Purge`. **Adding one means adding an entry with a reason.**
- `DRIVE_TRASH_RETENTION` defaults to 30 days; `0` is never-expire, the one setting where zero means
  "do nothing" rather than "do it immediately". `files.ExpireTrash` runs on its own ticker in `main`,
  next to the upload sweep and nowhere near the reconciler.
- Search escapes `%`, `_` and `\` in the term and passes `ESCAPE '\'`: unescaped, searching `%` returns
  every file a user has. It signals `truncated` from asking for `limit+1` rows rather than paging —
  a filename search that needs page two needs a better word, not a cursor.
- A share points at a **file id, not a path**, so rename and move keep it working for nothing, a trashed
  target answers 410 and shows as `"state":"trashed"` in the owner's listing, and purging the target
  takes the link with it by foreign-key cascade. Restoring the target revives the same link.
- Share tokens are `auth.NewSecret`/`auth.HashSecret` — the same 256-bit secret as an API token, and
  **only the hash is stored**. The URL therefore exists exactly once, in the creation response: a link
  that was not copied has to be created again. The listing shows the target, never the token.
- A share password is Argon2id (a human chose it, unlike a 256-bit token) and the proof of it lives in
  the **scs session**, not in the URL: a password in a query string ends up in logs and history.
  `unlockShare` calls `RenewToken` before `Put`, for the same fixation reason login does.
- Share listings return paths **relative to the shared folder** (`Access.Relative`). Returning
  `projects/alpha/notes.txt` for a share of `projects/alpha` would disclose the parent the spec
  says must stay hidden. Everything outside the subtree is one answer, 404, naming nothing.
- The confinement is `files.Lookup` by `(user_id, dir, name)`: stored paths are canonical, so a
  non-canonical `rel` cannot match any row. `fs.ValidPath` and `Access.contains` are assertions on
  top of that, not the mechanism.
- Thumbnails are 256px JPEGs at `.drive/thumbs/<id>.jpg`, written through the same atomic pipeline.
  **Listings never touch them**: the client asks `GET /api/thumb/{path...}` per row, and generation is
  bounded by a counting semaphore (`NumCPU/2`) on the requesting goroutine, so a cancelled request
  stops costing immediately. `thumbs` (schema v5) holds `version` (the source ETag) and `state`;
  a changed source is a miss, and a `failed` row is what stops a corrupt JPEG being decoded forever.
- The cache is derived data: deleting `.drive/thumbs` wholesale is supported, and a row saying `ready`
  with no file on disk regenerates. **Purging a file discards its thumbnail** — a permanent delete that
  leaves a recognisable picture behind has not deleted anything.
- EXIF orientation is read by hand in `internal/thumb/exif.go` (JPEG APP1 → TIFF IFD0 → tag 0x0112),
  bounded to the first 64 KiB and total: anything it does not understand is orientation 1. An EXIF
  library is a big dependency with a parser-bug history for a 2-byte answer. Take one if thumbnails
  ever need the date, the camera, or the GPS position.
- `golang.org/x/image` is for **WebP decoding and CatmullRom scaling** only. There is no WebP encoder in
  Go, so the WebP test fixture is 46 checked-in base64 bytes.
- Full-size in-place viewing (12.8) is `GET /api/download/{path...}` without `?download`: 7.8's inline
  allowlist already does exactly this, so there is no second endpoint. Thumbnails are served `inline`
  because they are our own re-encoded pixels, not stored bytes.
- Memory-ceiling tests assert on `MemStats.TotalAlloc`, never `HeapAlloc`: total allocation is monotonic,
  so it does not depend on when the collector happened to run. `HeapAlloc` flaked under `-race`.
- `strace` is not installed on this machine — task 15.2 needs it.

## Style

- Lazy senior dev: stdlib first, no abstraction with one implementation, shortest diff that is actually correct.
- Comments explain *why*, never *what*. A deliberate shortcut gets a `ponytail:` comment naming its ceiling
  and upgrade path; `/ponytail-debt` harvests them.
