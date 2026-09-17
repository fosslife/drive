# drive

Self-hosted drive. **The filesystem is the source of truth; SQLite is a disposable index.**
Any design that makes the index authoritative for user-visible state is wrong, however fast it is.

Full plan: `openspec/changes/bootstrap-drive-v1/` (proposal, design, specs, tasks).
Read `design.md` before making an architectural call.
Kept out of v1 on purpose, to look at later: `BACKLOG.md`. Decisions taken while building: `DECISIONS.md`.

## Workflow

- Work is driven by `openspec/changes/bootstrap-drive-v1/tasks.md`, **one group at a time**, not all 99 at once.
  Implement a group, run the tests, stop for review and a commit, then move to the next.
- `/opsx:apply bootstrap-drive-v1 group N` continues the work. Tick the checkbox as each task lands.
- A task's "verify …" clause is the acceptance criterion. It ships as a test in the same slice, not later.
- **This file is not a build log.** A decision worth recording goes in `DECISIONS.md` or, better, in a
  `why` comment at the code it explains. Add a line here only when its absence would let one of the
  rules below be broken.

## Toolchain

Go lives at `~/.local/go/bin` (not on the default PATH, not a system package):

```sh
export PATH=$HOME/.local/go/bin:$PATH
go build ./... && go vet ./... && go test ./...
go build -ldflags "-X main.Version=v0.1.0" -o /tmp/drive ./cmd/drive
```

The interface is built separately and embedded. **`go build` does not run it**, so a release build
is two steps and the second one is easy to forget:

```sh
npm --prefix web ci && npm --prefix web run build   # → web/dist, embedded by web/embed.go
npm --prefix web test                               # pure logic, node --test, no dependencies
npm --prefix web run test:e2e                       # real binary + real Chrome, ~25s
```

Without that build the binary still serves the whole API and answers `/` with a note saying so;
`web.Built()` is the check, and the Go UI tests skip rather than pass quietly.

Podman is available; there is no Docker. `Containerfile` and `compose.yml` land with task 14.4.
`strace` is not installed on this machine — task 15.2 needs it.

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
web/                React + Vite interface, its dist/, and the embed.FS that compiles it in
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
  `files.Purge` is the only function allowed to free a byte, and `internal/files/destruction_test.go`
  enforces it: any `Remove`/`RemoveAll`/`Truncate`/`os.Create`/`O_TRUNC` outside its allowlist fails the
  build, as does any `DELETE FROM files` outside `Purge`. **Adding one means adding an entry with a reason.**
- **Listings read the index; anything that changes something writes the filesystem first**, index second.
  If the process dies between the two, the reconciler agrees with the disk. No other order is correct.
- **The reconciler has no delete path.** A vanished file marks its row missing; an unreadable root aborts the whole scan.
- **Writes are atomic**: temp file → fsync file → rename → **fsync the parent directory**. The last step is the one people forget.
- **File identity is assigned once and never reused** (`INTEGER PRIMARY KEY AUTOINCREMENT`).
- **Never edit a shipped migration, append one.** Schema version is `PRAGMA user_version`.
- **Never build a real path string and open it directly.** Every file operation goes through `os.Root`,
  which blocks `..` and escaping symlinks at the syscall level.
- **Accounts live only in the index, deliberately.** Losing `index.db` loses logins, never a byte of user
  data; storage roots are `users/<username>/` so re-creating the account reattaches it to its files.
- **A route is authenticated unless someone typed `true`.** `server.routes()` carries a `public` flag and
  a test enumerates the slice against a hardcoded list. Making a route public is a reviewed decision.
- **Stored files are served as attachments** with `nosniff` and a restrictive CSP. Inline only for inert raster images — never SVG, never PDF.
- **Every config value has a working default.** No config file, ever. Env overrides only, `DRIVE_*`.
- **Single binary, single process.** No second service, no second database engine, no Node runtime, no cgo
  (hence `modernc.org/sqlite`).

## Style

- Lazy senior dev: stdlib first, no abstraction with one implementation, shortest diff that is actually correct.
- Comments explain *why*, never *what*. A deliberate shortcut gets a `ponytail:` comment naming its ceiling
  and upgrade path; `/ponytail-debt` harvests them.
