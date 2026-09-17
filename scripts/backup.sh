#!/bin/sh
# Backup and restore for a drive data directory. See docs/backup.md.
#
#   scripts/backup.sh backup  <data-dir> <backup-dir>
#   scripts/backup.sh restore <backup-dir> <data-dir>
#
# The whole state of an instance is one directory, so this script is mostly
# rsync with the two things that are easy to get wrong handled: the index is
# snapshotted rather than copied out from under a running process, and the
# restore refuses to write into a directory that already has something in it.
set -eu

usage() {
	echo "usage: $0 backup <data-dir> <backup-dir>" >&2
	echo "       $0 restore <backup-dir> <data-dir>" >&2
	exit 2
}

need() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "$0: $1 is not installed; this script does nothing a cp -a cannot" >&2
		exit 1
	}
}

[ $# -eq 3 ] || usage
action=$1
src=$2
dst=$3
need rsync

case "$action" in
backup)
	[ -d "$src" ] || {
		echo "$0: no data directory at $src" >&2
		exit 1
	}
	mkdir -p "$dst"

	# Files first and on their own: they are the source of truth, they are
	# almost all of the bytes, and rsync makes the second backup cheap.
	# .drive/trash comes along, .drive/tmp does not — it is half-finished
	# uploads nobody has ever seen.
	if [ -d "$src/users" ]; then
		rsync -a --delete --exclude '.drive/tmp/' "$src/users/" "$dst/users/"
	fi
	if [ -d "$src/certs" ]; then
		rsync -a --delete "$src/certs/" "$dst/certs/"
	fi

	# The index is a live SQLite database. Copying the file while the drive is
	# writing to it can capture a torn state, so ask SQLite for a snapshot when
	# it is available and say so plainly when it is not.
	if [ -f "$src/index.db" ]; then
		if command -v sqlite3 >/dev/null 2>&1; then
			rm -f "$dst/index.db"
			sqlite3 "$src/index.db" ".backup '$dst/index.db'"
		else
			echo "$0: sqlite3 not installed, copying the index file directly." >&2
			echo "$0: stop the drive first, or the copied index may be inconsistent." >&2
			cp "$src/index.db" "$dst/index.db"
		fi
	fi

	echo "backed up $src to $dst"
	echo "the index holds the accounts, API tokens, and share links, and nothing else does:"
	echo "a backup without $dst/index.db restores the files and loses every login."
	;;
restore)
	[ -d "$src" ] || {
		echo "$0: no backup at $src" >&2
		exit 1
	}
	if [ -d "$dst" ] && [ -n "$(ls -A "$dst" 2>/dev/null)" ]; then
		echo "$0: $dst is not empty; restore into a fresh directory and move it into place" >&2
		exit 1
	fi
	mkdir -p "$dst"
	rsync -a "$src/" "$dst/"
	echo "restored $src to $dst"
	if [ ! -f "$dst/index.db" ]; then
		echo "no index in the backup: the drive will rebuild one by scanning, and"
		echo "each account has to be created again with the username its folder in"
		echo "$dst/users/ is named for, which reattaches it to those files."
	fi
	;;
*) usage ;;
esac
