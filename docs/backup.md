# Backing up and restoring

Everything an instance is made of lives in one directory — the one `DRIVE_DATA_DIR` points at,
by default `~/.local/share/drive`:

```
index.db                 file metadata, and the only copy of accounts, API tokens and share links
certs/                   what the ACME client stored, if a hostname is configured
users/<username>/        every file, at its real path
users/<username>/.drive/ trash (keep it), thumbnails and half-finished uploads (derived, skip them)
```

Copy that directory and you have copied the instance. The script does exactly that, with the
two details that are easy to get wrong handled:

```sh
scripts/backup.sh backup  ~/.local/share/drive /mnt/backup/drive
scripts/backup.sh restore /mnt/backup/drive   /srv/drive-new
```

## What the index holds and nothing else does

**Accounts, password hashes, API tokens, and share links exist only in `index.db`.** They are not
in the filesystem, on purpose: the alternative is writing password hashes into user storage, which
puts the one secret worth protecting in a second place. A backup that excludes the index restores
every byte of every file and not one login.

File metadata in the index is a different matter — it is rebuildable by scanning, which is what
makes the restore-files-only path below work at all.

## Backup

```sh
scripts/backup.sh backup <data-dir> <backup-dir>
```

- `users/` is rsynced, so the second backup only moves what changed. `.drive/trash` comes with it:
  a file someone deleted yesterday and wants back tomorrow is still a file.
- `.drive/tmp` is skipped. It is upload data nobody has ever seen.
- `index.db` is snapshotted with `sqlite3 .backup` when `sqlite3` is installed, which is consistent
  against a running drive. Without it the file is copied and the script says so: **stop the drive
  first in that case**, or the copied index may be torn. Nothing is lost either way — a torn index
  gets replaced and rebuilt — but the accounts in it would be.

The drive can keep running during a backup. Files are written atomically (write to a temp file,
fsync, rename), so rsync never sees a half-written file, only a file that is or is not there yet.

## Restore

```sh
scripts/backup.sh restore <backup-dir> <data-dir>
```

The target must be empty; restoring over a live data directory is not something a script should
do quietly. Start the drive against the restored directory and it comes up as it was: same users,
same files, same shares, same settings.

## Restoring the files alone

If the index is gone — excluded from the backup, or the disk it was on died — the files are still
a complete drive:

1. Restore `users/` into a fresh data directory.
2. Start the drive. It creates an empty index and prints a first-run setup URL.
3. Create each account **with the username its folder is named for**: the storage root is
   `users/ada`, so the account has to be `ada` to be handed that directory back.
4. The reconciler indexes what is there. Files become browsable as the scan progresses; a large
   root takes minutes and the drive serves what it has already indexed meanwhile.

What comes back: every file, at its original path, with its content intact and no repair step
applied to it. What does not: API tokens and share links, which have to be created again, and the
trash, whose contents are still on disk under `users/<name>/.drive/trash/<id>/` but are invisible
to the drive — the reconciler skips `.drive`, and the index that knew where each of those files
came from is the one that was lost. Moving them back out by hand is a normal file operation, and
the next scan picks them up.
