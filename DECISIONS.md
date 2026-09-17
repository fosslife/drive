# Decisions made while building v1

Why things are the way they are, where the reason is not obvious from the code and did not come
from the spec. Nobody needs to read this file to work on the project — `CLAUDE.md` holds the rules
that matter, and the code carries its own `why` comments. This is here so a question that was
already settled during v1 does not get re-litigated during v1.

**Delete this file once v1 ships.** After that the codebase is the context, and a stale record of
how it got here is worse than no record.

## Index and schema

- Schema version is `PRAGMA user_version`; migrations are a `[]string` in
  `internal/index/migrations.go`, applied one transaction each.
- Files and folders share one `files` table with a `kind` column, so identity is unique across both
  and a share or upload references a row without a type tag. Path is stored as `(dir, name)`,
  unique among non-trashed rows — a trashed row keeps its original path as the restore target.
- `dir` is `""` for a top-level entry, not `"."`. `index.SplitPath`/`index.JoinPath` are the only
  place that knows that.
- `mtime` is stored in Unix **seconds**.
- The index connection pool is capped at one connection: that is the "single writer".
- `sessions` already matches the schema `alexedwards/scs/sqlite3store` expects.
- Schema v2 rebuilt `files_listing` as `(user_id, dir, state, name)`. Without `name` in the index
  SQLite sorts the whole folder into a temp b-tree, which is the "first page loads the folder"
  failure 7.1 bans.
- Subtree SQL uses `substr(dir, 1, ?)` with **rune counts, not byte counts** — SQLite's `substr` on
  TEXT counts characters, so a byte offset loses the subtree of any non-ASCII folder name.

## Storage and paths

- Confinement is `os.Root` (Go 1.24+), not a hand-written path resolver: it blocks `..` and escaping
  symlinks at the syscall level, free of TOCTOU. `relPath` adds `fs.ValidPath` and the `.drive` ban
  on top for early, legible errors.
- `Write` renames over whatever is at the destination, symlink included, and never follows it.
- `storage.Rename` refuses an occupied destination. Replacing is the caller's explicit decision and
  goes through `files.Trash` first.
- `storage.Verify` takes the expected checksums as a map; the index feeds it.
- `users.storage_root` is relative to the data directory (`users/ada`), never absolute: the data
  directory moves between a host and a container, the roots inside it do not.

## Reconciler

- The scan fast path is `(kind, size, mtime)`; a rewrite to the same size within the same second is
  not rehashed, and `storage.Verify` is the backstop for that.
- The scan walks read-only first and writes afterwards in batches of 500, so a walk that fails
  partway writes nothing at all and a long rebuild becomes browsable as it goes.
- The scanner is one goroutine started with `go scanner.Run(ctx)` after the listener is open. One
  root failing is logged and skipped, not fatal: an unmounted disk for one account must not stall
  the others.
- `GET /api/scan` reports progress and `indexing`, authenticated. There is deliberately no HTTP
  route that triggers a scan; `Scanner.Trigger()` is the on-demand entry point.

## Accounts, sessions, tokens

- **Accounts live only in the index, deliberately.** Losing `index.db` loses logins; it does not
  lose a byte of user data. Storage roots are `users/<username>/`, not `users/<id>/`, so re-creating
  an account with the same username reattaches it to its files. Usernames are `[a-z0-9._-]` and
  immutable in v1. Writing password hashes into a storage root to make this recoverable was
  considered and rejected.
- `Store.Delete` removes the account row (files, tokens, shares cascade) and never touches the
  storage root. The directory left behind is what makes re-creating the username a recovery.
- `auth.ErrNotFound` is the one "no such row" for accounts and tokens alike. Every authenticated
  request re-reads the account (`Store.Active`), so disable and delete land on the next request.
- One `requireAuth` resolves either credential. A bearer token authenticates *as* its owner with no
  scope of its own, so "a token never exceeds its owner's access" is a property of `Store.Root`.
- Argon2id at OWASP's low-memory setting (m=19 MiB, t=2, p=1); parameters live in the PHC string so
  raising them later leaves stored hashes verifiable. Token secrets are 256 random bits hashed with
  plain SHA-256 — there is nothing to brute-force, and Argon2id per API request would be absurd.
- A failed login costs one Argon2id verification even for an unknown username (`equaliseTiming`), or
  the response time answers "does this account exist?" whatever the body says.
- The failed-login limiter is in-memory, keyed `user:<name>` and `ip:<addr>`, 5 failures per 15
  minutes. `clientIP` reads `RemoteAddr` only; **no `X-Forwarded-For`** until there is a
  trusted-proxy setting.
- Session cookie `Secure` follows whether the listener is encrypted. Hardcoding `true` would mean no
  browser ever sends it over the plaintext listener, i.e. every login silently failing.
- CSRF: `sameOrigin` runs outside authentication and refuses any unsafe method that shows neither
  `Sec-Fetch-Site: same-origin|none` nor an `Origin` matching `Host`. A bearer token is exempt —
  browsers never attach one, and requiring an `Origin` from curl would break every API client.

## First-run setup

- Only the token's **hash** is kept in `settings`, and `OpenSetup()` mints a fresh token on every
  start while no account exists. A restart invalidates the previously printed URL — deliberate, so
  the live token is always the one in the newest output and nothing is stored in the clear.
- `CompleteSetup` consumes it with a conditional `DELETE ... WHERE key = ? AND value = ?`, which is
  the whole concurrency control, and puts it back if account creation fails so a typo is not a
  lockout.
- `main.setupURL` prints `http://localhost:<port>/setup?token=…` for a wildcard listener: a wildcard
  answers on every address and so names none.

## Files and HTTP

- A listing is keyset-paginated on `name` (`?cursor=`), never `OFFSET`, so page 500 costs what page
  1 does.
- ETag is `"<id>-<mtime>-<size>"`, not the checksum: a per-request rehash to tag a file precisely is
  not worth it, and the same-size-same-second rewrite is what `storage.Verify` exists to catch.
  `If-Match` on a modifying request opts into a precondition; without it, last write wins.
- Serving content: extension allowlist (`.jpg .jpeg .png .gif .webp`) gets its real type and
  `inline`; everything else gets `application/octet-stream` and `attachment`. The dangerous set is
  open-ended (SVG carries script, PDF carries script, the next one is not invented yet), so the
  narrow list is the only safe direction. `?download` forces an attachment for an allowlisted type.
- Archives are `zip.Store` written straight to the `ResponseWriter` — no temp file, no buffer, no
  `Content-Length`. A failure partway abandons the stream unclosed: a truncated zip fails to open,
  which beats a complete-looking archive quietly missing files.
- `/healthz` returns readiness and nothing else — no version, no paths. It is unauthenticated.
- Config is environment-only; no flags. `version` is the one subcommand.

## Upload

- tus 1.0 is implemented directly in `server/upload.go` + `files/upload.go`: `POST /api/uploads`
  (create, `Upload-Length` + `Upload-Metadata` carrying `filename`/`dir`/`replace`), `HEAD`
  (offset), `PATCH` (append), `DELETE` (abort), `OPTIONS` (capabilities).
- **The temp file's size is the upload offset, not the `offset_bytes` column.** A column can
  disagree with the disk after a crash, and telling a client "I have n bytes" when the last few were
  never fsynced is how a resumed upload finishes corrupt. The column is a denormalised copy for the
  reclaim sweep.
- The finished upload's checksum is computed by reading the published file back — one extra
  sequential pass, marked `ponytail:`.
- `storage.tempPath` refuses any name this package did not hand out. The temp name round-trips
  through the index on every resume, and a path that could point elsewhere turns an upload into
  write-anywhere.
- A collision stores `report (2).pdf` unless `replace` was asked for; `replace` sends the previous
  content to trash first, so two sequential overwrites leave both prior versions under their own
  ids. A replacement gets a **new** identity — the trashed row keeps the old one, because it is the
  restorable version.
- `DRIVE_UPLOAD_RETENTION` (default 24h) bounds abandoned upload data. `files.Reclaim` runs on its
  own ticker in `main`, not inside the reconciler, which has no delete path and must keep it so.

## Trash

- Trash content lives at `.drive/trash/<id>` — the entry itself moved under its own identifier, not
  a directory holding it. A folder goes in one rename, contents and all, and restore is the rename
  back.
- `files.trash_root` (schema v4) is the id of the entry that was actually deleted, carried by it and
  by every row under it. It is what makes the trash list a folder once instead of once per file, and
  what restore and purge take as a unit.
- Restore never overwrites and never fails for want of a path: a deleted parent is recreated, and an
  occupied path sends the item to `notes (2).txt` with the response reporting where it landed.
- `DRIVE_TRASH_RETENTION` defaults to 30 days; `0` is never-expire, the one setting where zero means
  "do nothing" rather than "do it immediately". `files.ExpireTrash` runs on its own ticker in
  `main`, next to the upload sweep and nowhere near the reconciler.

## Search

- Filename search is a `LIKE` scan, not FTS: a second engine buys a word index for something that is
  not a word search — `tax` has to match `2025-taxes.pdf` — and costs the single-binary install.
- The term's `%`, `_` and `\` are escaped and the query passes `ESCAPE '\'`: unescaped, searching
  `%` returns every file a user has.
- Truncation is signalled by asking for `limit+1` rows rather than paging. A filename search that
  needs page two needs a better word, not a cursor.
- Case-insensitivity is SQLite's, which is ASCII-only: `Ä` does not match `ä`. Fix by storing a
  folded copy of the name if anyone needs it.

## Sharing

- A share points at a **file id, not a path**, so rename and move keep it working for nothing, a
  trashed target answers 410 and shows as `"state":"trashed"` in the owner's listing, and purging
  the target takes the link with it by foreign-key cascade. Restoring the target revives the link.
- Share tokens are `auth.NewSecret`/`auth.HashSecret` — the same 256-bit secret as an API token, and
  only the hash is stored. The URL exists exactly once, in the creation response: a link that was
  not copied has to be created again. The listing shows the target, never the token.
- A share password is Argon2id (a human chose it, unlike a 256-bit token) and the proof of it lives
  in the scs session, not in the URL: a password in a query string ends up in logs and history.
  `unlockShare` calls `RenewToken` before `Put`, for the same fixation reason login does.
- Share listings return paths relative to the shared folder (`Access.Relative`). Returning
  `projects/alpha/notes.txt` for a share of `projects/alpha` would disclose the parent the spec says
  must stay hidden. Everything outside the subtree is one answer, 404, naming nothing.
- The confinement is `files.Lookup` by `(user_id, dir, name)`: stored paths are canonical, so a
  non-canonical `rel` cannot match any row. `fs.ValidPath` and `Access.contains` are assertions on
  top of that, not the mechanism.

## Thumbnails

- 256px JPEGs at `.drive/thumbs/<id>.jpg`, written through the same atomic pipeline. Listings never
  touch them: the client asks `GET /api/thumb/{path...}` per row, and generation is bounded by a
  counting semaphore (`NumCPU/2`) on the requesting goroutine, so a cancelled request stops costing
  immediately.
- `thumbs` (schema v5) holds `version` (the source ETag) and `state`; a changed source is a miss,
  and a `failed` row is what stops a corrupt JPEG being decoded on every request.
- The cache is derived data: deleting `.drive/thumbs` wholesale is supported, and a row saying
  `ready` with no file on disk regenerates. Purging a file discards its thumbnail — a permanent
  delete that leaves a recognisable picture behind has not deleted anything.
- EXIF orientation is read by hand in `internal/thumb/exif.go` (JPEG APP1 → TIFF IFD0 → tag 0x0112),
  bounded to the first 64 KiB and total: anything it does not understand is orientation 1. An EXIF
  library is a large dependency with a parser-bug history for a 2-byte answer. Take one if
  thumbnails ever need the date, the camera, or the GPS position.
- `golang.org/x/image` is for WebP decoding and CatmullRom scaling only. There is no WebP encoder in
  Go, so the WebP test fixture is 46 checked-in base64 bytes.
- Full-size in-place viewing is `GET /api/download/{path...}` without `?download`: the inline
  allowlist already does exactly this, so there is no second endpoint. Thumbnails are served
  `inline` because they are our own re-encoded pixels, not stored bytes.

## Transport and distribution

- `DRIVE_HOSTNAME` is the switch for everything about certificates: set it and CertMagic gets one
  from `DRIVE_ACME_DIRECTORY`, unset it and the drive serves plaintext. There is no "enable HTTPS"
  setting because a public name is the only thing ACME needs to know, and no self-signed fallback
  because that ships a browser warning to somebody who is still deciding whether to trust us with
  their files. The deployments without a public name — behind Caddy, on Tailscale, on a laptop for
  ten minutes — all have a better answer than a certificate we signed for ourselves.
- Plaintext on anything but loopback warns at every start, naming what is readable and both ways
  out. The bind address is the signal: `127.0.0.1:8080` is someone trying the drive out and needs
  no lecture, `:8080` is a network that can read session cookies.
- Setting a hostname also moves the default address to `:443`. A certificate for a public name is
  only useful on the port the public connects to, and `DRIVE_ADDR` still overrides it.
- Obtaining the certificate blocks startup. An instance that cannot get a certificate for the name
  it was told to serve should say so in the log rather than answer every handshake with an error.
- CertMagic's own zap logger is left alone. Routing it into `slog` costs a second logging dependency
  to make ACME errors match the house format, and they are already legible.
- The ACME test runs Pebble, Let's Encrypt's test CA, inside the test process: account, order,
  HTTP-01 challenge, CSR and issuance are all real, against a root nobody trusts. Staging Let's
  Encrypt cannot validate a machine with no public name, so it would have been a skipped test
  everywhere. `acmeTrustedRoots` and `acmeHTTPPort` in `internal/transport` exist for it — two
  unexported variables rather than two configuration values nobody should set.
- The image is `scratch` plus the binary and the CA roots. The roots are the one thing the binary
  cannot carry: without them `DRIVE_HOSTNAME` cannot verify Let's Encrypt. No user is declared, so
  rootless Podman maps the container's root to the invoking user and the mounted data directory is
  writable without a chown.
- `scripts/backup.sh` uses `rsync` and `sqlite3 .backup`, and says plainly when `sqlite3` is missing
  that the drive should be stopped first. The interesting part of the procedure is not the copying,
  it is that the index holds the accounts and nothing else does — `docs/backup.md` leads with that.

## Frontend

- `web/` is one directory holding the Vite project, its build output, and the `embed.go` that
  compiles that output in. `go:embed` cannot reach above its own package, so `dist/` has to live
  beside the Go file; keeping the sources there too means everything about the interface is in one
  place.
- `web/dist/.gitkeep` is committed and re-copied from `web/public/` by every build, because
  `go:embed` fails the build outright on an empty directory. That is what lets `go build ./...`
  work on a machine with no Node toolchain; `web.Built()` reports the difference at runtime and the
  Go UI tests skip rather than pass quietly.
- Plain JavaScript and JSX, no TypeScript. The API shapes are small and each is used in one place;
  a compiler, a tsconfig and a type package are more moving parts than they earn here. Add it if
  the interface grows a second consumer of the same shapes.
- No router, no state library, no component library, no virtual-list package. `useRoute` is fifteen
  lines over `history.pushState` and `popstate`; `windowOf` is arithmetic over a fixed row height.
  `navigate()` is the only way to change the URL — a bare `replaceState` changes the address bar
  without telling the router, which is how the setup screen once refused to leave itself.
- `useListing` numbers its requests instead of serialising them. Dropping a refresh because an
  older one is in flight loses it permanently and leaves the folder showing something untrue,
  which is what fifty uploads finishing at once produces.
- The tus client is written out rather than taken from `tus-js-client`: the part that matters is
  resuming from the offset the *server* reports after a drop, and that is the part a library hides.
  A refusal (507, 409) is never retried; only a broken connection is.
- Thumbnails are `<img loading="lazy">`. The native attribute is the whole asynchronous story, and
  `onError` falling back to an icon is how "no preview for this type" reaches the screen.
- Renaming, moving and naming a new folder use `prompt()`. It is native, accessible and zero code.
  A folder picker is the upgrade when typing a deep path becomes the complaint.
- `GET /` serves the interface and is public; `GET /api/` answers a JSON 404. Both are in the
  reviewed public list. Serving HTML for a mistyped API path sends whoever wrote that client
  looking in the wrong place.

## Testing

- Memory-ceiling tests assert on `MemStats.TotalAlloc`, never `HeapAlloc`: total allocation is
  monotonic, so it does not depend on when the collector happened to run. `HeapAlloc` flaked under
  `-race`.
- `storage.Write` does not create parent directories, so tests that need one `os.Mkdir` it.
- The interface has two test layers and no framework. `web/src/*.test.js` is `node --test` over the
  pure modules — the window arithmetic, the error wording, the tus resume — with zero dependencies.
  `web/e2e/` drives a real binary in a real Chrome via `puppeteer-core`, which downloads no browser
  and uses whichever Chrome the machine has. The section 13 acceptance criteria are browser-level
  claims; checking them against a fake would not be checking them.
- e2e tests that count `.row` elements must account for virtualisation: only a screenful exists in
  the DOM. The fifty-item selection test sets a viewport tall enough to hold fifty rows.
