## Why

Self-hosted file storage today forces a bad trade: the tools that are easy to run (Filebrowser, dufs) are thin file browsers with no real multi-user, sharing, or durability story, while the tools that are real drives (Nextcloud, Seafile) are operationally heavy and conceptually alien. Seafile in particular stores files as content-addressed blocks, so your data is a bag of hashes that cannot be listed, rsynced, or restored without the application — which is both the reason it is fast and the reason it is hard to trust and hard to use.

This change bootstraps a self-hosted drive built on the opposite premise: **the filesystem is the source of truth and the database is a disposable index**. If the application is deleted, abandoned, or corrupted, the files are still just files in a folder.

## What Changes

This is a greenfield project. Everything below is new.

**Priority order, in this order, for every decision in this change:**

1. **Reliable** — never lose a file. Durability and integrity outrank confidentiality.
2. **Easy to use and deploy** — no novel concepts, no multi-component install.
3. **Not bloated** — a focused drive, not an app platform. Features and performance are welcome; scope creep is not.
4. **Safe by default** — HTTPS, real auth, hardened share links, small attack surface.

**Settled architectural decisions:**

- Files are stored as **plain files on disk** at their real paths. SQLite is an index that can be deleted and rebuilt from the filesystem at any time. No value may live only in the database.
- All writes are **atomic**: write to a temp file, fsync, then rename. A crash or aborted upload can never produce a partially written file at a real path.
- Every file is **checksummed on ingest** so corruption can be detected later.
- **Reconciliation never deletes.** A rescan that finds a file missing from disk marks it missing; it never propagates a deletion anywhere.
- Files placed on disk by other means (`rsync`, `scp`, cron) are picked up by a rescan and indexed. This is a first-class supported workflow, not a repair tool.
- There is **exactly one API**. The web UI is its first client, not a privileged one. Browser sessions and programmatic API tokens authenticate to the same routes.
- Single static binary with the built frontend embedded. One artifact to ship, one process to run.

**In scope for v1:**

- Browse a file tree; create, rename, move, and delete folders and files
- Resumable uploads that stream to disk without buffering whole files in memory
- Downloads, including streaming large files
- Soft delete to trash with a retention period, and restore from trash
- Filename search
- Multiple users, each with an isolated storage root
- Browser sessions and API tokens against the same API
- Public share links with optional password and optional expiry
- Image thumbnails, generated lazily and never blocking the file list
- First-run browser setup with a one-time token; no default credentials, no mandatory config file
- Automatic schema migrations on boot

**Explicitly out of scope for v1** (see `BACKLOG.md` for the deferred list and rationale):

- Desktop sync client — deferred, but v1 must not foreclose it
- WebDAV — the filesystem is directly reachable over SSH/rsync/SFTP, so a second, worse door is not worth the cost
- End-to-end encryption and encryption at rest — incompatible with filesystem-as-truth, and the threat model does not justify it
- File versioning beyond trash
- Video thumbnails — would require an ffmpeg dependency that contradicts priority 3
- Full-text content search, groups, quotas, external storage backends

**Permanent non-goals.** This is a drive. It will not grow calendars, contacts, chat, an office suite, or a plugin platform.

## Capabilities

### New Capabilities

- `storage`: Filesystem as source of truth, atomic write pipeline, ingest checksums, stable file identity, the SQLite index as a rebuildable cache, and non-destructive reconciliation of external filesystem changes.
- `file-operations`: Browsing, resumable upload, download, rename, move, folder creation, soft delete to trash, restore, permanent delete, and filename search.
- `auth`: User accounts, per-user isolated storage roots, password hashing and login, browser sessions, and API tokens for programmatic clients.
- `sharing`: Public share links for files and folders with optional password protection and optional expiry.
- `previews`: Lazily generated image thumbnails served without blocking directory listing.
- `deployment`: Single-binary distribution, first-run setup flow, zero-required-config defaults with environment overrides, HTTPS handling, automatic schema migrations, and a documented backup and restore procedure.

### Modified Capabilities

None. This is the first change in the project.

## Impact

- **Creates the entire codebase.** No existing code is affected.
- **Backend:** Go, standard library `net/http`, SQLite for the index, frontend embedded via `embed.FS`. Chosen because the problem is I/O plumbing, cross-compilation for ARM targets is trivial, the prior art in this domain is overwhelmingly Go, and generated diffs are cheap to review.
- **Frontend:** React built with Vite as a static SPA. Next.js is explicitly rejected: it would require a Node runtime in the deployment and destroy the single-binary property.
- **Distribution:** one binary and one Docker image.
- **Constraints carried forward from this change:** stable file IDs must exist in the data model from the first migration or a future sync client becomes impossible without a data migration; every configuration value must have a working default; every schema change must ship as an automatic migration with no manual step.
