## Purpose

Defines how file bytes are persisted and how the system stays truthful about them: the filesystem holds the files, the index is a rebuildable cache, and no failure short of disk loss may destroy or corrupt user data.

## ADDED Requirements

### Requirement: Filesystem is the source of truth

The system SHALL store every user file as an ordinary file on disk at a path that mirrors the path shown to the user. The system SHALL NOT store file content in a database, an object store, or any content-addressed or chunked format.

No user-visible file state SHALL exist only in the index. Deleting the index MUST NOT lose files, folder structure, or file content.

Account records are the documented exception: they exist only in the index, so deleting it loses the ability to log in. A storage root SHALL be named for its owner's username so that re-creating an account with the same username reattaches that account to its existing files.

#### Scenario: Uploaded file is an ordinary file on disk

- **WHEN** a user uploads `taxes.pdf` into folder `documents`
- **THEN** a readable file exists on disk under that user's storage root at `documents/taxes.pdf`
- **AND** its bytes are byte-for-byte identical to the uploaded file

#### Scenario: Application is removed entirely

- **WHEN** the application binary and the index database are deleted
- **THEN** every user file remains on disk at its original path with its original name and contents

### Requirement: Writes are atomic

The system SHALL write file content to a temporary location, flush it to durable storage, and only then move it into its final path. A file MUST NOT become visible at its final path until its content is complete and durable.

#### Scenario: Upload is interrupted

- **WHEN** an upload is aborted before all bytes are received
- **THEN** no file appears at the destination path
- **AND** any existing file at that path is left unmodified

#### Scenario: Process is killed during a write

- **WHEN** the process is terminated while writing file content
- **THEN** no partially written file exists at any user-visible path
- **AND** incomplete temporary data is not listed to the user

#### Scenario: Power loss after a completed upload

- **WHEN** an upload reports success and the machine immediately loses power
- **THEN** after restart the file exists at its final path with complete contents

### Requirement: Content is checksummed on ingest

The system SHALL compute and record a checksum of every file's contents when the file is written or first indexed, and SHALL provide an operation that recomputes checksums and reports files whose contents no longer match.

#### Scenario: Checksum recorded on upload

- **WHEN** a file is uploaded successfully
- **THEN** a checksum of its contents is recorded in the index

#### Scenario: Corruption is detected

- **WHEN** a file's bytes are altered on disk outside the application and an integrity check is run
- **THEN** the system reports that file as failing verification
- **AND** does not delete, quarantine, or modify it

### Requirement: Index is rebuildable from the filesystem

The system SHALL be able to reconstruct its index entirely by scanning the storage roots. If the index is missing or unreadable at startup, the system SHALL rebuild it rather than refusing to start or reporting data loss.

#### Scenario: Index deleted

- **WHEN** the index database is deleted and the system is started
- **THEN** the system rebuilds the index by scanning storage
- **AND** once an account is re-created with its original username, all of that account's files and folders are browsable with their correct paths and sizes

#### Scenario: Index is corrupt

- **WHEN** the index database exists but cannot be opened or migrated
- **THEN** the system moves it aside, starts a new one, and rebuilds by scanning storage
- **AND** no user file is modified or deleted

### Requirement: Scanning does not block service

Index scanning and rebuilding SHALL run without blocking startup or the serving of requests. While a scan is in progress the system SHALL serve what it has already indexed and SHALL report that a scan is running and how far it has progressed.

#### Scenario: Rebuild of a large storage root

- **WHEN** the index is rebuilt for a storage root containing hundreds of thousands of files
- **THEN** the system accepts requests throughout the rebuild
- **AND** reports scan progress rather than appearing unresponsive

#### Scenario: Partial results during a scan

- **WHEN** a user browses or searches while a scan is still running
- **THEN** already-indexed results are returned
- **AND** the response indicates that indexing is incomplete

### Requirement: Writes are refused before the volume fills

The system SHALL refuse operations that consume storage when free space on the data volume is below a configurable reserve, and SHALL report the reason. The reserve exists to prevent the system from reaching a state where writes fail partway or the index cannot be written.

#### Scenario: Upload refused near capacity

- **WHEN** an upload would take free space below the configured reserve
- **THEN** the upload is refused with an error identifying insufficient space
- **AND** no partial file and no index inconsistency results

#### Scenario: Reads continue near capacity

- **WHEN** free space is below the reserve
- **THEN** browsing, downloading, and deleting continue to work so that space can be reclaimed

### Requirement: Reconciliation never deletes user data

Reconciling the index against the filesystem SHALL only add, update, or mark index entries. It MUST NOT delete, move, truncate, or overwrite any file on disk, and MUST NOT propagate an observed absence as a deletion to any other system.

#### Scenario: File missing from disk

- **WHEN** a rescan finds an indexed file no longer present on disk
- **THEN** the index entry is marked missing and reported
- **AND** no file on disk is deleted or modified as a result

#### Scenario: Storage root temporarily unavailable

- **WHEN** a rescan runs while a storage root is unmounted or unreadable
- **THEN** the system reports the error and aborts the scan
- **AND** makes no change to the index or to any file

### Requirement: Externally added files are adopted

Files and folders placed into a storage root by means outside the application SHALL be indexed and become browsable, downloadable, and searchable after a rescan, with no manual repair step.

#### Scenario: Files copied in over SSH

- **WHEN** 500 files are copied into a user's storage root using an external tool and a rescan is run
- **THEN** all 500 files are listed with correct names, sizes, and modification times
- **AND** each has a recorded checksum

### Requirement: Files have stable identifiers

Every file and folder SHALL have an identifier that is assigned once and remains unchanged when the file is renamed or moved through the application. Identifiers MUST NOT be reused after a file is deleted.

#### Scenario: Rename preserves identity

- **WHEN** a user renames a file through the application
- **THEN** the file's identifier is unchanged

#### Scenario: Move preserves identity

- **WHEN** a user moves a file to a different folder through the application
- **THEN** the file's identifier is unchanged

#### Scenario: Identifiers are not recycled

- **WHEN** a file is permanently deleted and a new file is created afterwards
- **THEN** the new file receives an identifier that was never previously used

### Requirement: Paths are confined to the user's storage root

The system SHALL resolve every requested path within the requesting user's storage root and SHALL reject any request that resolves outside it, including requests using relative path segments, absolute paths, encoded separators, or symbolic links.

#### Scenario: Traversal attempt is rejected

- **WHEN** a request targets a path containing `../` sequences that resolve above the user's storage root
- **THEN** the request is rejected with an error
- **AND** no file outside the storage root is read, written, or listed

#### Scenario: Symlink escaping the root is not followed

- **WHEN** a symbolic link inside a storage root points to a location outside that root
- **THEN** the system does not serve or traverse the link target
