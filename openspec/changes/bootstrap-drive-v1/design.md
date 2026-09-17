## Context

Greenfield. See `proposal.md` for motivation and the settled architecture; see `BACKLOG.md` for what was rejected and why.

The binding constraint on every decision below: **the filesystem is the source of truth and SQLite is a disposable index.** Any design that requires the index to be authoritative for user-visible state is wrong, regardless of how much faster it is.

## Goals / Non-Goals

**Goals**

- No failure short of disk loss destroys or corrupts a file.
- One artifact, one process, zero required configuration.
- A future sync client is possible without a data migration.

**Non-Goals (design level)**

- No horizontal scaling. One process, one machine, local disk. Multi-node is not a deferred feature, it is a different product.
- No pluggable storage backends. There is one storage driver and it is the local filesystem. An interface with one implementation is dead weight.
- No abstraction over SQLite. It is the index forever; if it is ever replaced the rebuild-from-disk path makes that a non-event.

## Decisions

### Storage layout

```
<data-dir>/
  index.db                  disposable for file metadata, see "What the index owns alone"
  users/<username>/         storage root, user files at real paths
    .drive/
      tmp/                  in-flight uploads and pre-rename temp files
      trash/<file-id>/      soft-deleted, original path in index
      thumbs/               derived cache
```

`.drive` is skipped by the reconciler and excluded from listings and search. Everything a user owns still lives under one directory, so backup remains "copy the folder" and trash is included in it.

The root is named for the username, not the numeric account id, so that the folder is self-describing without the index. Usernames are therefore restricted to `[a-z0-9._-]` and cannot be changed in v1; renaming a user would mean moving their storage root, which is a migration, not a settings change.

*Alternative considered:* a single global trash outside the user roots. Rejected — it breaks per-user backup and makes account deletion a cross-directory operation.

### What the index owns alone

Account records — username, password hash, admin flag, API tokens, shares — exist only in the index. Losing `index.db` therefore loses the ability to log in, even though every byte of every file is still on disk at its real path.

This is accepted rather than fixed. The alternative is writing a copy of each account, password hash included, into its own storage root so the index can be reconstructed from the filesystem alone; that duplicates the one secret worth protecting into a second place for a failure the operator is already responsible for preventing. A different database engine does not change this — the accounts live wherever the database lives.

What makes it survivable is the layout above: after index loss, an operator who creates an account with the same username gets the same storage root back, and the reconciler re-adopts every file in it. The backup procedure (task 14.6) is "copy `<data-dir>`", which covers the index by construction.

*Alternative considered:* `users/<user-id>/`. Rejected for exactly this reason — a recreated account would be issued a new id and would not find its own files.

### Atomic write pipeline

Write to `.drive/tmp/`, `fsync` the file, `rename` into place, then **`fsync` the parent directory**.

The last step is the one that is usually missed and it is the one that matters: on Linux a rename is atomic but not durable until the containing directory is synced. Without it, "upload succeeded" followed by power loss can leave the file absent despite the fsync on its contents.

*Trade-off:* an fsync per upload costs real latency on spinning disks. Accepted — priority 1 is reliability, and correctness here is not negotiable.

### Disk space guard

Writes are refused when free space on the data volume falls below a reserved threshold, with an error that names the condition.

There are no quotas in v1, so a single user can fill the volume. A full disk is not graceful degradation: uploads fail mid-write, trash cannot absorb an overwrite, and SQLite can fail in ways that need manual recovery. Reserving headroom converts a reliability incident into a comprehensible error message. This is a guard, not a quota system — it protects the instance, not fairness between users.

### File identity

Identity is the index row's primary key, assigned once and never reused. In-app rename and move just update the path column, so identity is trivially preserved.

The hard case is an external `mv`, which looks like a delete plus a create. Layered resolution, cheapest first:

| Signal | Used for | Fails on |
|---|---|---|
| Path unchanged | the common case | — |
| `(checksum, size)` unique match between a file that vanished and one that appeared in the same scan | external move/rename | two byte-identical files |
| Otherwise | new identity | — |

*Alternatives considered:* inode numbers — rejected, they do not survive a backup restore or a copy, which is precisely when you most need identity to hold. Extended attributes — rejected for v1, real filesystem-support and tool-stripping caveats for a benefit no v1 feature consumes; revisit when the sync client is built.

`ponytail: checksum heuristic for external-move detection, ambiguous only for byte-identical duplicates; upgrade to xattr-backed ids if the sync client needs certainty.`

### Reconciler

Startup scan, periodic scan, and on-demand trigger. Fast path compares `(size, mtime)` and only rehashes when they differ.

One invariant, enforced as a hard rule rather than a policy: **the reconciler has no delete path.** A vanished file marks its row missing. If a storage root is unreadable the scan aborts whole rather than concluding everything was deleted. This is the failure mode that has wiped out users of other sync products and it is prevented structurally, not by care.

Scans run in the background and never block startup or request serving. A full rebuild of a large root takes minutes, and a server that appears hung for that long reads as broken. The system serves what it has indexed so far and reports scan progress.

### Resumable upload

Implement tus 1.0 wire semantics (`POST` create, `HEAD` offset, `PATCH` append) directly — roughly three handlers over the temp-file pipeline already required for atomic writes.

*Alternative considered:* the `tusd` library. Rejected — it brings its own storage abstraction and hook system that duplicates and fights the temp-file layout above. *Alternative considered:* a bespoke offset protocol. Rejected — same implementation cost, but following tus means existing clients and tooling work for free.

### SQLite driver: `modernc.org/sqlite`

Pure Go, no cgo. Cross-compiling for ARM NAS and Raspberry Pi targets was a stated reason for choosing Go; cgo would give that back. `mattn/go-sqlite3` is faster, but the index is small and read-mostly and the workload is dominated by disk I/O, so the speed is not worth reintroducing a C toolchain to every release build.

WAL mode, one writer.

### Filename search: `LIKE`, not FTS5

FTS5 tokenises on word boundaries and does not do substring matching, so searching `tax` would not find `taxes.pdf` without workarounds. A `LIKE '%term%'` scan over a filename column is both simpler and more correct for this query shape, and is milliseconds at 100k rows.

`ponytail: full-column LIKE scan; switch to SQLite's trigram tokenizer (3.34+), which is built to accelerate substring LIKE, if it degrades past ~1M files.`

### Serving user content safely

Stored files are served with `Content-Disposition: attachment` by default, `X-Content-Type-Options: nosniff`, and a restrictive `Content-Security-Policy`. Inline rendering is allowed only for an allowlist of inert types — raster images.

This is the stored-XSS hole that has produced CVEs in every major self-hosted drive. An uploaded `.html` or `.svg` served inline from the application's own origin executes with full access to the session cookie, so uploading a file becomes account takeover for anyone who opens it — including through a share link, where the attacker chooses the victim.

SVG is on the deny list despite being an image: it is a document format that can carry script. PDF is likewise not inline-rendered, since PDF supports JavaScript.

*Alternative considered:* serving user content from a separate origin, which is the strongest form of this defence. Deferred — it requires a second hostname and certificate, which fights the zero-configuration goal. The header-based defence is sufficient when the allowlist stays narrow.

### Bulk download

Folder and multi-file downloads stream a zip archive generated on the fly, with no temporary file and no staging directory.

This is not a nice-to-have. Without it, getting a folder out of the drive means clicking every file individually — the kind of gap that makes people call a drive broken, and a direct contradiction of priority 2.

Zip rather than tar, for native extraction on Windows and macOS. Stored rather than deflated: the payload is usually already-compressed media, so compression would burn CPU for nothing and prevent the response from streaming.

### HTTPS: CertMagic

Automatic ACME issuance and renewal, plus self-signed fallback, from the library that backs Caddy. Writing an ACME client is not a thing to do by hand.

### Auth primitives

Argon2id via `golang.org/x/crypto/argon2`; randomness from `crypto/rand`.

Sessions use `alexedwards/scs` rather than a hand-rolled token table. The reason is narrow and specific: **session fixation**. A new session ID must be issued at the moment of login, or an attacker who plants a known session ID in the victim's browser beforehand remains authenticated as them afterwards. `scs` exposes this as `RenewToken()`. It also handles concurrent session access and cookie semantics correctly.

*Alternative considered:* rolling sessions by hand, roughly fifty lines. Rejected — the happy path is trivial and the bug lives in the part you don't think to write. A well-maintained library is the safer default for anything on the authentication path.

*Alternative considered:* an external identity provider (Keycloak, Ory Kratos, Authelia). Rejected on architecture rather than effort: each is a separate process, which breaks the single-binary property outright.

CSRF is handled by `SameSite=Lax` plus validation of `Origin` / `Sec-Fetch-Site` on state-changing requests, rather than a synchroniser-token library. This is an OWASP-accepted defence, reads a header rather than inventing a token flow, and avoids threading tokens through an SPA.

API tokens are opaque random secrets stored only as a hash and looked up by that hash, so verification is an indexed equality test rather than a timing-sensitive comparison. No expiry, explicit revoke.

### Thumbnails

`image/jpeg`, `image/png`, `image/gif` from stdlib plus `golang.org/x/image` for WebP and scaling. EXIF orientation from a small dedicated decoder. No ffmpeg, ever, per priority 3.

Generated lazily off the listing path by a bounded worker pool. A failed generation is recorded so it is not retried on every request.

### Concurrency and overwrite safety

Last-write-wins with ETag preconditions on modifying requests. No locking, no operational transform, no conflict resolution UI.

Last-write-wins is only acceptable because **an overwrite moves the previous content to trash before the new content lands.** Without that, a concurrent overwrite silently destroys bytes with no recovery path, which contradicts priority 1 outright — and an ETag precondition is no protection, since a caller can simply omit it. The trash machinery already exists for deletes; overwrite reuses it.

This covers the far more common non-concurrent case too: a user who uploads the wrong version over a good one has the retention window to get it back.

*Alternative considered:* file locking. Rejected — it requires a lock-breaking UI for abandoned locks and solves a coordination problem this audience does not have.

### Frontend

React + Vite, built to static assets, embedded with `embed.FS`. Virtualised file list so 100k rows scroll smoothly; thumbnails fill in asynchronously and never gate a render.

Next.js is excluded — it requires a Node runtime in the deployment and destroys the single-binary property.

## Risks / Trade-offs

- **fsync-per-upload hurts on slow disks** → Accepted deliberately. Batch only if measurement shows it matters, and never at the cost of the durability guarantee.
- **Checksum-based move detection is ambiguous for byte-identical files** → Falls back to a new identity, which is merely inefficient, never wrong. No data is lost either way.
- **Full rescan is O(files) and slow on large roots** → `(size, mtime)` fast path avoids rehashing; full verification is a separate explicit operation, not part of routine scans.
- **Reverse proxies terminate long uploads** → tus resumption is the mitigation, but proxy defaults must be tested rather than assumed.
- **Single process, single machine** → Stated non-goal. The audience is homelabs.
- **Pure-Go SQLite is slower than cgo** → Index is small and read-mostly; swap the driver if it ever shows up in a profile.
- **No quotas, so one user can fill the volume** → The disk space guard reserves headroom and refuses writes before the volume is full, turning a corruption risk into an error message. Real quotas are in `BACKLOG.md`.
- **Overwrite-to-trash doubles the space a replace consumes until retention expires** → Accepted; the disk guard and retention bound it. Losing bytes is worse than holding them for thirty days.
- **Trash is not versioning** → It keeps exactly one prior copy per path, which is displaced by the next overwrite. Real version history is in `BACKLOG.md`; this only closes the silent-data-loss hole.

## Migration Plan

Greenfield: nothing to migrate, rollback is deleting the directory.

Two things must be right from the first commit because retrofitting them is expensive:

1. **Stable file IDs in the first schema.** Adding them later means a data migration and breaks any client that cached identity.
2. **Migration machinery before the second schema change.** Version the schema at v1 on day one, apply forward-only migrations at startup, refuse to start on a newer schema than the binary understands.

## Open Questions

- Thumbnail cache eviction — size cap, LRU, or unbounded. Safe to defer: thumbnails are derived data, deleting the whole cache is always safe, and the policy changes no spec or task.
- Default trash retention period. 30 days is the assumption until someone objects.
- Which proxy defaults actually break long uploads (nginx, Caddy, Cloudflare). Empirical, answered during testing, changes no interface.
