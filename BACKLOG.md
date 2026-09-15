# Backlog

Everything deliberately kept out of v1, with the reason. This exists so that "not yet"
never gets confused with "forgotten", and so that v1 decisions don't quietly foreclose
things we said we wanted later.

Scoped v1 lives in `openspec/changes/bootstrap-drive-v1/proposal.md`.

---

## Deferred — we want these

### Desktop sync client
The single biggest feature gap versus Dropbox/Drive, and the single biggest engineering
lift. File watchers, conflict resolution, partial transfers, rename detection, and
three sets of OS filesystem quirks. Deferring it is a scheduling decision, not a
rejection.

**What v1 must not break:** files need stable IDs that survive a rename, and there must
be a way for a client to ask "what changed since X" without walking the whole tree. If
those aren't in the schema from the first migration, adding a sync client later means a
data migration.

### File versioning
Keep the last N versions of a file, restore any of them.

v1 partially covers this: an overwrite moves the previous content to trash, so a replaced
file is recoverable for the retention period. That closes the silent-data-loss hole but
is not version history — it keeps exactly one prior copy per path, displaced by the next
overwrite. Real history needs a decision on where old versions physically live given that
the filesystem is the source of truth.

### Video thumbnails
Requires ffmpeg, which is a ~70MB dependency that fights the "not bloated" goal
directly. Revisit as an optional feature that degrades gracefully when ffmpeg is absent,
never as a hard dependency.

### Full-text content search
Searching inside PDFs and documents. Pulls in extraction libraries per format and an
index that must be kept current. Filename search in v1 covers most real use.

### Groups and richer permissions
v1 is users with isolated roots plus public share links. Group membership, shared
folders between users, and per-path ACLs are a natural next step. The permission check
must be centralized from day one so this is an extension rather than a rewrite.

### Quotas
Per-user storage limits. Needed the moment more than one household is on an instance.

v1 has no quotas, only a disk space guard that refuses writes below a reserved threshold.
That protects the instance from a full volume; it does nothing about fairness between
users.

### Serving user content from a separate origin
The strongest defence against stored content executing in the application's security
context. v1 relies on `Content-Disposition: attachment`, `nosniff`, a restrictive CSP, and
a narrow inline allowlist, which is sufficient while that allowlist stays small. A second
hostname would be stronger but requires another certificate and fights zero-configuration
deployment.

### Mobile apps
A PWA covers the phone case in v1. Native apps only make sense after the sync protocol
exists.

### Packaging for strangers
Not a v1 feature, but v1 must not take on debt that forbids it: no manual config file,
no manual migration steps, no "just edit the database" operations. Later this should be
a packaging exercise (ARM builds, a one-command install, an upgrade path), not a rewrite.

---

## Rejected — with reasons

### WebDAV
Considered as a cheap way to get native "mount as a network drive" support on Windows,
macOS, and Linux without writing a sync client.

Rejected because the filesystem is already directly reachable over SSH, rsync, and SFTP
on a machine we control, so WebDAV would be a second and strictly worse door into the
same room. It is also a chatty protocol with no change notification and no delta sync,
Windows' client is notoriously unreliable, and every major consumer drive has dropped it.

Reconsider only if people deploying this ask for it.

### End-to-end encryption
Rejected on threat model. The priority is durability, not confidentiality: a leaked file
is embarrassing, a lost file is unrecoverable. E2EE also kills server-side thumbnails,
search, and previews, and is fundamentally incompatible with storing plain files on disk.

### Encryption at rest
Rejected as largely theatrical for this deployment: the key would live on the same
machine as the data. It also means opaque filenames on disk, which throws away the entire
benefit of filesystem-as-truth. Full-disk encryption at the OS layer is the right place
for this.

### Next.js for the frontend
Rejected because it requires a Node runtime in the deployment, which breaks the
single-binary property. React via Vite gives the same ecosystem with a static build.

---

## Permanent non-goals

This is a drive. Not a platform.

- Calendars, contacts, chat, mail
- An office suite or collaborative document editing
- A plugin or app ecosystem
- Anything whose primary justification is "Nextcloud has it"

---

## Open problems

Not features. Unresolved design questions that need real thought before the code that
touches them gets written.

### Stable file identity under external moves
Files need IDs that survive a rename so a future sync client can say "file 4821 moved"
rather than re-uploading 4GB. That's easy for in-app moves. It's hard when someone runs
`mv` over SSH, because from the outside a move is indistinguishable from a delete plus a
create.

Candidate approaches, none free:

| Approach | Survives | Breaks on |
|---|---|---|
| Inode number | rename, move within a filesystem | copy, restore from backup, crossing filesystems |
| Extended attribute on the file | rename, move, most copies | filesystems without xattr support, tools that strip them |
| Content hash + size heuristic | move, rename | large files (cost), and two identical files |

Likely a layered answer rather than one winner. Worth reading how Syncthing handles it.

### Where trash and future versions physically live
Given plain files on disk, deleted files have to go somewhere real. A hidden directory
per storage root is the obvious answer, but it interacts with quotas, with rescan
(the reconciler must ignore it), and with the "back up the folder" story.

### Reverse proxy interaction on large uploads
Resumable upload has to survive whatever proxy sits in front of it. Needs testing
against nginx, Caddy, and Cloudflare defaults rather than assumptions.
