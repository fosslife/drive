## 1. Project skeleton

- [x] 1.1 Initialise the Go module and a `cmd/drive` entrypoint; verify `go build ./...` produces a runnable binary that prints its version and exits
- [x] 1.2 Add configuration loading with a working default for every value, overridable by environment variable and requiring no config file; verify the binary starts with a completely empty environment and reports the chosen data directory and port
- [x] 1.3 Reject invalid configuration at startup with a message naming the offending value and what was expected; verify the process exits non-zero and never reaches a partially configured state
- [x] 1.4 Add the HTTP server with a health endpoint and startup reporting of version, listening address, transport encryption status, and data directory; verify the health endpoint responds and discloses no configuration secrets

## 2. Index and migrations

- [x] 2.1 Wire up `modernc.org/sqlite` in WAL mode with a single writer; verify a smoke test opens the index, writes, and reads back
- [x] 2.2 Implement forward-only migration machinery that runs automatically at startup and records the applied schema version; verify a test starts against an empty directory and reaches the current version with no manual step
- [x] 2.3 Refuse to start when the index schema is newer than the binary understands, reporting the version mismatch; verify a test writes a future version and asserts startup fails without modifying the index
- [x] 2.4 Abort startup and report which migration failed when one cannot be applied; verify a test with a deliberately failing migration asserts no user file is touched
- [x] 2.5 Write schema v1 including files, folders, users, sessions, API tokens, shares, and uploads, with a stable never-reused file identifier as the primary key; verify a test asserts identifiers are monotonic and not recycled after deletion

## 3. Storage core

- [x] 3.1 Implement path resolution confining every request to the requesting user's storage root, rejecting relative segments, absolute paths, encoded separators, and symlinks escaping the root; verify a table-driven test covering each traversal form asserts rejection
- [x] 3.2 Create the per-root `.drive/{tmp,trash,thumbs}` layout on first use; verify the directories exist after a user is created
- [x] 3.3 Implement the atomic write pipeline: write to `.drive/tmp`, fsync the file, rename into place, fsync the parent directory; verify a test asserts the destination path does not exist until the rename completes
- [x] 3.4 Compute and record a content checksum on every write and on first index of an external file; verify a test asserts the stored checksum matches an independently computed one
- [x] 3.5 Add an integrity verification operation that recomputes checksums and reports mismatches without deleting, quarantining, or modifying anything; verify a test corrupts a file on disk and asserts it is reported and left untouched
- [x] 3.6 Refuse space-consuming operations when free space on the data volume is below a configurable reserve, while leaving browse, download, and delete working; verify a test against a constrained volume asserts the upload is refused with a space error and leaves no partial file or index inconsistency

## 4. Reconciler

- [x] 4.1 Implement a storage-root scan with a `(size, mtime)` fast path that rehashes only changed files and skips `.drive`; verify a test asserts unchanged files are not rehashed on a second scan
- [x] 4.2 Index externally added files so they become browsable with correct name, size, mtime, and checksum; verify a test copies 500 files in directly, scans, and asserts all 500 are indexed
- [x] 4.3 Mark vanished files as missing and ensure the reconciler has no delete path at all; verify a test removes a file on disk, scans, and asserts the row is marked missing and no other file was deleted or modified
- [x] 4.4 Abort a scan whole and report the error when a storage root is unreadable, leaving the index unchanged; verify a test makes a root unreadable and asserts zero index writes
- [x] 4.5 Detect external moves by unique `(checksum, size)` match between a file that vanished and one that appeared in the same scan, preserving identity, falling back to a new identity when ambiguous; verify a test moves a file externally and asserts the identifier is preserved, plus a second test with byte-identical duplicates asserts a new identity rather than a wrong match
- [x] 4.6 Rebuild the entire index from the filesystem when it is missing at startup, and move a corrupt index aside and start a fresh one rather than refusing to start; verify a test deletes the index, restarts, and asserts all files are browsable with correct paths once the account is re-created, and a second test with a corrupt index asserts it is moved aside, rebuilt, and no user file is touched
- [x] 4.7 Run the scan at startup, periodically, and on demand; verify an on-demand trigger picks up a file added seconds earlier
- [x] 4.8 Run scans in the background so they never block startup or request serving, exposing scan state and progress; verify a test asserts requests are served throughout a rebuild of a large root and that responses indicate indexing is incomplete

## 5. Authentication

- [x] 5.1 Implement user accounts with isolated storage roots named `users/<username>` and restricted to `[a-z0-9._-]`, with administrator management of create, disable, and delete; verify a test asserts deleting one account leaves another account's files intact, that an unsafe username is refused, and that re-creating an account with an existing username reattaches it to that storage root
- [x] 5.2 Implement Argon2id password hashing and login with responses that do not reveal whether an account exists; verify a test asserts identical responses for unknown user and wrong password, and that no plaintext password appears in the index
- [x] 5.3 Rate-limit failed authentication attempts per account and per source; verify a test asserts attempts are refused or delayed after a threshold
- [x] 5.4 Implement server-side sessions using `alexedwards/scs`, delivered in an `HttpOnly; Secure; SameSite=Lax` cookie carrying only an opaque identifier, with expiry and logout revocation; verify a test asserts the cookie attributes, that it carries no identity or privileges, and that a session cannot authenticate after logout
- [x] 5.5 Renew the session identifier on login and on any privilege change via `RenewToken()`; verify a test asserts a pre-authentication identifier differs from the post-login one and no longer authenticates as that user
- [x] 5.6 Reject state-changing requests that carry a valid session cookie without proof of same-origin submission, by validating `Origin` and `Sec-Fetch-Site`; verify a test asserts a forged cross-site request is refused before any file is read or written
- [x] 5.7 Implement API tokens with create, list, and individual revoke, storing only a hash and looking tokens up by that hash so verification is an indexed equality test; verify a test asserts the secret is absent from the list response and that a revoked token stops authenticating
- [x] 5.8 Enforce that a token never exceeds its owner's access; verify a test asserts a token request for a path outside its owner's root is rejected
- [x] 5.9 Enforce authentication on all endpoints except share access and first-run setup; verify a test enumerates registered routes and asserts each is either authenticated or on the explicit public list

## 6. First-run setup

- [x] 6.1 Generate a one-time setup token on first start when no account exists and print the setup URL to process output; verify a test asserts no credentials grant access before setup
- [x] 6.2 Implement the browser setup flow creating the first administrator, invalidating the token and closing the flow on completion; verify a test asserts the token and the setup route both stop working afterwards
- [x] 6.3 Refuse setup when the token is absent or wrong, and keep setup available with a valid token across restarts before completion; verify tests cover both cases

## 7. File operations

- [x] 7.1 Implement folder listing returning name, type, size, and modification time, paginated or streamed; verify a test with 100,000 entries asserts the first page returns without loading the whole folder into memory
- [x] 7.2 Return a not-found error with no partial listing for a nonexistent folder; verify a test asserts the error shape
- [x] 7.3 Implement download with byte-range support returning partial content; verify a test asserts a full download is byte-identical and a ranged request returns only the requested bytes
- [x] 7.4 Implement folder creation, rename, and move within a storage root, preserving contents and identifiers; verify tests assert identity is unchanged after rename and after move
- [x] 7.5 Reject a move that would overwrite an existing destination entry unless replacement was explicitly requested, leaving neither side partial; verify a test asserts both paths are unchanged after rejection
- [x] 7.6 Reject moving a folder into its own descendant; verify a test asserts the request fails and nothing changed on disk
- [x] 7.7 Add ETag preconditions on modifying requests for last-write-wins concurrency; verify a test asserts a stale precondition is rejected
- [x] 7.8 Serve stored files with `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff`, and a restrictive `Content-Security-Policy`, rendering inline only for an allowlist of inert raster image types that excludes SVG and PDF; verify tests assert an uploaded HTML file and a script-bearing SVG are delivered as downloads and do not execute in the application origin, and that a JPEG renders inline
- [x] 7.9 Implement folder and multi-selection download streaming an uncompressed zip produced on the fly; verify a test asserts folder structure is preserved, that a folder larger than available memory streams without buffering or staging a copy on disk, and that trashed and unauthorised items are excluded

## 8. Upload

- [x] 8.1 Implement tus 1.0 create, offset, and append handlers over the atomic write pipeline, streaming to disk without buffering whole files; verify a test uploads a large file and asserts peak process memory does not scale with file size
- [x] 8.2 Resume an interrupted upload from the reported offset and finalise into place; verify a test interrupts mid-upload, resumes, and asserts the final checksum matches the source
- [x] 8.3 Leave no file at the destination and no modification to an existing file when an upload is aborted; verify a test asserts the destination is untouched
- [x] 8.4 Handle name collisions by storing under a non-colliding name unless replacement was explicitly requested; verify a test asserts the existing file is never left partially overwritten
- [x] 8.5 Move previous content to trash before a replacement becomes visible at a path, so no operation but permanent deletion destroys content; verify tests assert the replaced content is restorable from trash, that two sequential overwrites without preconditions leave both prior versions accounted for, and that a failure partway leaves the path holding one complete version
- [x] 8.6 Reclaim incomplete uploads not resumed within the retention period; verify a test asserts stale temp data is removed and no user-visible file is affected

## 9. Trash

- [x] 9.1 Move deleted files and folders into `.drive/trash` recording their original path, excluding them from listings and search; verify a test asserts a deleted file is absent from its folder, present in trash, and still readable
- [x] 9.2 Delete a folder and its contents into trash as one unit restorable together; verify a test asserts restoring the folder restores every descendant
- [x] 9.3 Restore an item to its original path, recreating a missing parent or reporting a clear fallback location; verify tests cover both the normal case and the deleted-parent case, asserting the file is never lost
- [x] 9.4 Implement permanent deletion on demand and automatic expiry after a configurable retention period defaulting to 30 days, with a never-expire setting; verify tests assert expiry removes the file and frees space, and that never-expire retains it
- [x] 9.5 Confirm permanent deletion is the only code path that destroys user file content; verify by grepping for file removal and truncating-write calls and asserting each is reachable only from permanent delete, overwrite-to-trash, or temp cleanup

## 10. Search

- [x] 10.1 Implement case-insensitive partial filename search scoped to the requesting user, excluding trashed items, returning full paths; verify tests assert `tax` matches `taxes.pdf`, that another user's files never appear, and that trashed items are excluded
- [x] 10.2 Bound search results to a page and indicate when more exist; verify a test with more matches than the page size asserts the result count is capped and the response signals truncation

## 11. Sharing

- [x] 11.1 Create share links for a file or folder in the owner's root, identified by a high-entropy token that does not encode path, name, or identifier; verify a test asserts tokens are unguessable and reveal nothing about the target
- [x] 11.2 Serve shared files and folder listings to unauthenticated visitors, read-only and confined to the shared subtree; verify tests assert every modifying operation is refused and that requests above the shared folder are rejected without disclosing the parent path
- [x] 11.3 Add optional password protection with hashed storage and rate-limited attempts; verify a test asserts no content or listing is returned before the correct password is supplied
- [x] 11.4 Add optional expiry that refuses access after the expiry time with no administrator action; verify a test asserts access is refused after expiry and that a link without expiry stays valid
- [x] 11.5 Implement listing, inspecting, and immediately effective revocation of a user's share links; verify a test asserts a revoked link is refused on the next request
- [x] 11.6 Stop serving a link whose target is trashed and report it as such to the owner, while keeping links working across rename and move; verify tests cover trashed, renamed, and moved targets
- [x] 11.7 Reject share creation for a path outside the creator's storage root; verify a test asserts rejection

## 12. Previews

- [x] 12.1 Generate thumbnails for JPEG, PNG, GIF, and WebP preserving aspect ratio, using stdlib and `golang.org/x/image` with no external media toolchain; verify a test asserts thumbnails are produced on a machine with no additional media software installed
- [x] 12.2 Apply EXIF orientation so rotated photographs display upright; verify a test asserts a rotated source produces a correctly oriented thumbnail
- [x] 12.3 Report no-thumbnail-available for unsupported types without breaking listing or download; verify a test asserts a video file is still listed, downloadable, and shareable
- [x] 12.4 Generate thumbnails off the listing path via a bounded worker pool so listings never wait; verify a test asserts a 1,000-image folder with no thumbnails returns its listing immediately
- [x] 12.5 Cache thumbnails, invalidate on source change, and record failures so they are not retried on every request; verify tests assert a repeat request is served from cache, that a changed source yields a new thumbnail, and that a corrupt image is not reprocessed repeatedly
- [x] 12.6 Treat the thumbnail cache as derived data regenerable after deletion; verify a test deletes the whole cache and asserts no user file is affected and thumbnails regenerate
- [x] 12.7 Enforce that thumbnail access follows the underlying file's access rules including share links and revocation; verify tests cover another user, a valid share link, and a revoked share link
- [x] 12.8 Serve full-size images for in-place viewing under the same access rules and the inline allowlist from task 7.8; verify a test asserts an allowlisted image is served inline and a non-allowlisted type is not

## 13. Frontend

- [x] 13.1 Set up React with Vite building to static assets embedded via `embed.FS`; verify the built binary serves the UI with no adjacent asset directory
- [x] 13.2 Implement login, logout, and the first-run setup screen; verify an operator can complete setup and log in end to end
- [x] 13.3 Implement the virtualised file browser with folder navigation, rename, move, create folder, and delete; verify scrolling stays smooth at 100,000 rows
- [x] 13.4 Implement drag-and-drop resumable upload with per-file progress and resume after an interrupted connection; verify a large upload survives a deliberate network interruption
- [x] 13.5 Render thumbnails asynchronously so they never gate a render; verify the list scrolls smoothly while thumbnails are still loading
- [x] 13.6 Implement trash browsing with restore and permanent delete; verify a deleted file can be restored from the UI
- [x] 13.7 Implement filename search with result paths; verify searching a partial term returns matching files
- [x] 13.8 Implement share link creation with optional password and expiry, plus listing and revocation; verify a created link opens in a logged-out browser and stops working after revocation
- [x] 13.9 Implement API token management showing the secret once; verify the secret is absent when revisiting the list
- [x] 13.10 Implement multi-selection in the file browser with bulk delete, move, and download; verify selecting fifty items and deleting them moves all fifty to trash in one action
- [x] 13.11 Implement full-size image viewing in place with next and previous navigation within the folder; verify clicking an image displays it and arrow navigation moves between images
- [x] 13.12 Surface scan progress and the incomplete-index state in the UI; verify browsing during a rebuild shows indexed results and indicates indexing is still running
- [x] 13.13 Surface actionable errors for insufficient disk space, stale ETag preconditions, and failed uploads; verify each produces a message naming the cause rather than a generic failure

## 14. Transport and distribution

- [x] 14.1 Integrate CertMagic for automatic ACME issuance and renewal when a public hostname is configured; verify against a staging ACME endpoint that a certificate is obtained and served
- [x] 14.2 Serve plaintext with no certificate of any kind when no hostname is configured; verify a drive started with nothing set answers over HTTP and generates nothing
- [x] 14.3 Warn at every start when plaintext is served on an address other than loopback, naming what is exposed and both remedies; verify loopback is quiet and a network-reachable listener warns
- [x] 14.4 Produce a container image running the same binary with a mounted data directory; verify `docker run` with a volume and published port yields a reachable instance storing data in the mount
- [x] 14.5 Cross-compile for linux amd64 and arm64; verify each artifact starts on its target architecture
- [x] 14.6 Document and script the backup and restore procedure, stating plainly that accounts, API tokens, and share links live only in the index and are lost if it is excluded; verify a backup restored into a fresh data directory yields all users, files, shares, and settings intact, and that restoring files alone yields a working instance after index rebuild and account re-creation

## 15. End-to-end verification

- [ ] 15.1 Confirm upload streaming by watching resident memory during a 4 GB upload; verify RSS stays flat rather than growing with file size
- [ ] 15.2 Confirm write durability with `strace -f -e trace=fsync,fdatasync,rename` during an upload; verify fsync on the temp file precedes the rename and that the parent directory is fsynced after it
- [ ] 15.3 Confirm the reconciler never deletes by running a full external-change cycle of add, move, and remove; verify the count of files deleted by the reconciler is zero
- [ ] 15.4 Confirm index disposability by deleting the index on a populated instance, restarting, and re-creating the account with its original username; verify every file is browsable, downloadable, and searchable afterwards
- [ ] 15.5 Confirm resumable upload survives a real reverse proxy by testing against nginx, Caddy, and Cloudflare defaults; verify an interrupted large upload resumes to a correct checksum through each
- [ ] 15.6 Confirm tenant isolation with a cross-user attempt matrix covering listing, download, thumbnail, share creation, and token use; verify every cross-user request is rejected
- [ ] 15.7 Confirm no operation other than permanent deletion destroys content by running an overwrite, a move onto an existing name, and an interrupted replace; verify the prior content is recoverable from trash in every case
- [ ] 15.8 Confirm stored content cannot execute in the application origin by uploading HTML, SVG, and a polyglot file and opening each directly and through a share link; verify no script executes and the session cookie is not reachable
