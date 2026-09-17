package index

// migrations are applied in order, forward only. Each entry is one schema
// version: index 0 takes the index to version 1. Never edit a shipped
// migration, append a new one.
var migrations = []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5}

// Identity is an INTEGER PRIMARY KEY AUTOINCREMENT throughout: SQLite recycles
// plain rowids after the highest row is deleted, AUTOINCREMENT does not.
//
// Files and folders share one table. Identity is then unique across both, so a
// share or an upload points at a row without a type-tagged reference, and the
// external-move heuristic compares one population.
const schemaV1 = `
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    storage_root  TEXT    NOT NULL UNIQUE,
    created_at    INTEGER NOT NULL
);

CREATE TABLE files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dir        TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    kind       TEXT    NOT NULL CHECK (kind IN ('file', 'folder')),
    size       INTEGER NOT NULL DEFAULT 0,
    mtime      INTEGER NOT NULL,
    checksum   TEXT,
    state      TEXT    NOT NULL DEFAULT 'present'
                       CHECK (state IN ('present', 'missing', 'trashed')),
    trashed_at INTEGER
);

-- dir/name stay at the original path while trashed, so restore has a target
-- and an overwrite can put the replaced content in the trash without
-- colliding with its replacement.
CREATE UNIQUE INDEX files_path ON files(user_id, dir, name) WHERE state <> 'trashed';
CREATE INDEX files_listing ON files(user_id, dir, state);
CREATE INDEX files_content ON files(user_id, checksum, size);
CREATE INDEX files_name    ON files(user_id, name);

-- Schema required by alexedwards/scs/sqlite3store.
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions(expiry);

-- Only the hash is stored, and lookup is by hash, so verification is an
-- indexed equality test rather than a scan of comparable secrets.
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
);

CREATE TABLE shares (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    token_hash    TEXT    NOT NULL UNIQUE,
    password_hash TEXT,
    expires_at    INTEGER,
    created_at    INTEGER NOT NULL
);

-- In-flight tus uploads. temp_name is a file under <storage root>/.drive/tmp.
CREATE TABLE uploads (
    id           TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dir          TEXT    NOT NULL,
    name         TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    offset_bytes INTEGER NOT NULL DEFAULT 0,
    temp_name    TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// A listing is one folder ordered by name, paged with a keyset cursor. Without
// name in the index SQLite sorts the whole folder into a temp b-tree first,
// which is exactly the "first page loads the entire folder" failure the spec
// rules out. files_listing is dropped because this index supersedes it.
const schemaV2 = `
DROP INDEX files_listing;
CREATE INDEX files_listing ON files(user_id, dir, state, name);
`

// Whether an upload replaces what is at its destination is decided when the
// upload is created and applied when it finishes, so it has to survive every
// restart in between.
const schemaV3 = `
ALTER TABLE uploads ADD COLUMN replace INTEGER NOT NULL DEFAULT 0;
CREATE INDEX uploads_stale ON uploads(updated_at);
`

// Deleting a folder trashes its whole subtree in one rename, so every row under
// it is trashed too. trash_root is the identifier of the entry that was
// actually deleted, carried by itself and by every descendant: it is what the
// trash listing shows one row for, what restore moves back as a unit, and what
// names the content's location under .drive/trash.
const schemaV4 = `
ALTER TABLE files ADD COLUMN trash_root INTEGER;
UPDATE files SET trash_root = id WHERE state = 'trashed';
CREATE INDEX files_trash ON files(user_id, trash_root, trashed_at);
`

// What is known about a file's thumbnail, which is not the thumbnail itself:
// the picture is a file under .drive/thumbs and can be deleted at any time.
// version is the source version it was made from, so a changed file is a miss;
// a 'failed' row is what stops a corrupt image being decoded on every request.
const schemaV5 = `
CREATE TABLE thumbs (
    file_id    INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    version    TEXT    NOT NULL,
    state      TEXT    NOT NULL CHECK (state IN ('ready', 'failed')),
    updated_at INTEGER NOT NULL
);
`
