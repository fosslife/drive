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
internal/server/    HTTP surface
```

Environment: `DRIVE_DATA_DIR`, `DRIVE_ADDR`, `DRIVE_MIN_FREE_BYTES` (plain byte count, default 1 GiB).

Data directory (default `$XDG_DATA_HOME/drive`, else `~/.local/share/drive`, else `/var/lib/drive`):

```
index.db                  disposable, rebuildable by scanning
users/<user-id>/          storage root, user files at their real paths
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
- **Accounts do not survive index loss yet.** `users` lives only in the index, so task 4.6's "delete the
  index and restart" leaves the files on disk but nobody to own them. Needs a per-root account record
  (`users/<id>/.drive/user.json`) written by 5.1 and adopted at startup — decide before 4.6 or 5.1.
- `strace` is not installed on this machine — task 15.2 needs it.

## Style

- Lazy senior dev: stdlib first, no abstraction with one implementation, shortest diff that is actually correct.
- Comments explain *why*, never *what*. A deliberate shortcut gets a `ponytail:` comment naming its ceiling
  and upgrade path; `/ponytail-debt` harvests them.
