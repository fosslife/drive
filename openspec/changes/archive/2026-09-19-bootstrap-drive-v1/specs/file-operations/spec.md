## Purpose

Defines what a user can actually do with their files: browse, upload, download, organise, delete safely, and find things again. This is the surface the drive is judged on.

## ADDED Requirements

### Requirement: Browse the file tree

The system SHALL list the contents of any folder within the requesting user's storage root, returning for each entry its name, whether it is a file or folder, its size, and its modification time.

Listing SHALL be paginated or streamed so that response time does not degrade materially with folder size.

#### Scenario: List a folder

- **WHEN** a user requests the contents of a folder they own
- **THEN** the system returns every direct child with its name, type, size, and modification time

#### Scenario: Large folder remains responsive

- **WHEN** a user opens a folder containing 100,000 entries
- **THEN** the first page of results is returned without loading the entire folder into memory

#### Scenario: Folder does not exist

- **WHEN** a user requests a folder that does not exist
- **THEN** the system returns a not-found error and no partial listing

### Requirement: Upload files

The system SHALL accept file uploads into any folder within the user's storage root. Upload content SHALL be streamed to disk; the system MUST NOT buffer an entire file in memory.

#### Scenario: Upload succeeds

- **WHEN** a user uploads a file to a folder they own
- **THEN** the file is stored and appears in that folder's listing with the correct size

#### Scenario: Large upload does not exhaust memory

- **WHEN** a user uploads a 4 GB file
- **THEN** the server's resident memory does not grow in proportion to the file size

#### Scenario: Upload to a path that already exists

- **WHEN** a user uploads a file whose name already exists in the destination folder
- **THEN** the system either stores it under a non-colliding name or replaces the existing file only when replacement was explicitly requested
- **AND** the existing file is never left in a partially overwritten state

### Requirement: Overwriting preserves the replaced content

Replacing the contents of an existing file SHALL move the previous content to trash before the new content becomes visible at that path. No operation other than permanent deletion may render previous file content unrecoverable.

#### Scenario: Overwrite is recoverable

- **WHEN** a user replaces an existing file with new content
- **THEN** the new content is served at that path
- **AND** the previous content is in trash and can be restored

#### Scenario: Concurrent overwrite does not destroy data

- **WHEN** two clients replace the same file in sequence without preconditions
- **THEN** the later write wins at that path
- **AND** the content it replaced is recoverable from trash

#### Scenario: Overwrite is atomic with its trash step

- **WHEN** an overwrite fails partway through
- **THEN** the path holds either the complete previous content or the complete new content
- **AND** no content is lost

### Requirement: Uploads are resumable

An interrupted upload SHALL be resumable from the last successfully received offset without re-sending previously transferred bytes. The system SHALL discard incomplete upload data that has not been resumed within a configured retention period.

#### Scenario: Connection drops mid-upload

- **WHEN** a connection fails after 3 GB of a 4 GB upload has been received and the client resumes
- **THEN** the client is told the received offset
- **AND** transfer continues from that offset
- **AND** the completed file's checksum matches the source file

#### Scenario: Abandoned upload is reclaimed

- **WHEN** an incomplete upload is not resumed within the retention period
- **THEN** its temporary data is removed
- **AND** no user-visible file is affected

### Requirement: Download files

The system SHALL serve file content for download, supporting byte-range requests so that transfers can be resumed and media can be seeked.

#### Scenario: Download a file

- **WHEN** a user downloads a file they own
- **THEN** the received bytes are identical to the stored file

#### Scenario: Resume a download

- **WHEN** a client requests a byte range of a file
- **THEN** the system returns only that range with a partial-content response

### Requirement: User content cannot execute in the application's origin

Stored file content SHALL be served in a way that prevents it from running as active content in the application's security context. The system SHALL serve user content as a download by default, SHALL instruct clients not to infer a content type other than the one declared, and SHALL apply a policy that prevents scripts in served content from executing.

Inline rendering SHALL be permitted only for an explicit allowlist of types known to be inert when rendered.

#### Scenario: Uploaded HTML does not execute

- **WHEN** a user uploads an HTML file containing a script and then opens its download URL
- **THEN** the file is delivered as a download rather than rendered
- **AND** the script does not execute in the application's origin

#### Scenario: Uploaded SVG does not execute

- **WHEN** an SVG containing a script is stored and requested
- **THEN** the script does not execute in the application's origin

#### Scenario: Content type is not re-sniffed

- **WHEN** any stored file is served
- **THEN** the response instructs the client not to infer a different content type from the bytes

#### Scenario: Images render inline

- **WHEN** a user opens an image that is on the inline allowlist
- **THEN** it is displayed in the browser rather than downloaded

### Requirement: Download folders and multiple files

The system SHALL allow a user to download a folder, or a selection of several items, as a single archive. The archive SHALL be streamed as it is produced, without staging a complete copy on disk first.

#### Scenario: Download a folder

- **WHEN** a user downloads a folder containing nested subfolders and files
- **THEN** a single archive is returned preserving the folder structure and file contents

#### Scenario: Download a selection

- **WHEN** a user selects several files and folders and downloads them together
- **THEN** a single archive containing exactly those items is returned

#### Scenario: Large folder does not stage to disk

- **WHEN** a user downloads a folder substantially larger than available memory
- **THEN** the archive streams to the client
- **AND** the server neither buffers the archive in memory nor writes a complete copy to disk

#### Scenario: Archive respects access rules

- **WHEN** an archive download is requested
- **THEN** it contains only items the requester is permitted to read
- **AND** trashed items are excluded

### Requirement: Create, rename, and move

The system SHALL allow users to create folders, rename files and folders, and move files and folders to another location within their storage root.

#### Scenario: Create a folder

- **WHEN** a user creates a folder
- **THEN** the folder exists on disk and appears in the parent's listing

#### Scenario: Rename a file

- **WHEN** a user renames a file
- **THEN** the file appears under the new name
- **AND** its contents and identifier are unchanged

#### Scenario: Move a folder with contents

- **WHEN** a user moves a folder containing files into another folder
- **THEN** the folder and all its contents appear at the new location
- **AND** no file is lost or duplicated

#### Scenario: Move onto an existing name

- **WHEN** a move would overwrite an existing entry at the destination
- **THEN** the system rejects the move unless replacement was explicitly requested
- **AND** neither source nor destination is left in a partial state

#### Scenario: Move a folder into itself

- **WHEN** a user attempts to move a folder into one of its own descendants
- **THEN** the system rejects the request and makes no change

### Requirement: Delete moves to trash

Deleting a file or folder SHALL move it to the user's trash rather than destroying it. Trashed items SHALL retain their original path so they can be restored to it, and SHALL NOT appear in normal folder listings or search results.

#### Scenario: Delete a file

- **WHEN** a user deletes a file
- **THEN** the file no longer appears in its folder listing
- **AND** it appears in the user's trash
- **AND** its contents are still recoverable

#### Scenario: Delete a folder

- **WHEN** a user deletes a folder containing files
- **THEN** the folder and its contents move to trash together
- **AND** restoring the folder restores all of its contents

### Requirement: Restore from trash

The system SHALL allow a user to restore a trashed item to its original location, and SHALL handle the case where that location no longer exists.

#### Scenario: Restore a file

- **WHEN** a user restores a file from trash
- **THEN** the file reappears at its original path with its original contents
- **AND** it no longer appears in trash

#### Scenario: Original folder no longer exists

- **WHEN** a user restores a file whose original parent folder has been deleted
- **THEN** the system recreates the parent path or restores the file to a clearly reported fallback location
- **AND** the file is not lost

### Requirement: Trash retention and permanent deletion

The system SHALL permanently delete trashed items after a configurable retention period, and SHALL allow a user to permanently delete a trashed item or empty their trash on demand. Permanent deletion SHALL be the only operation that destroys user file content.

#### Scenario: Retention period elapses

- **WHEN** an item has been in trash longer than the retention period
- **THEN** the system permanently deletes it and frees the space

#### Scenario: Explicit permanent delete

- **WHEN** a user permanently deletes an item from trash
- **THEN** the file is removed from disk and no longer appears anywhere

#### Scenario: Retention disabled

- **WHEN** retention is configured to never expire
- **THEN** trashed items are retained until a user permanently deletes them

### Requirement: Search by filename

The system SHALL let a user search their files and folders by name, matching partial and case-insensitive input, and SHALL return results scoped to that user's storage root only.

#### Scenario: Partial match

- **WHEN** a user searches for `tax`
- **THEN** results include files such as `taxes.pdf` and `tax-2025.xlsx` with their full paths

#### Scenario: Results are scoped to the user

- **WHEN** a user searches for a term that matches another user's files
- **THEN** no other user's files appear in the results

#### Scenario: Trashed items are excluded

- **WHEN** a user searches for a term matching a trashed file
- **THEN** the trashed file does not appear in normal search results

#### Scenario: Result count is bounded

- **WHEN** a search matches more items than a single response should carry
- **THEN** a bounded page of results is returned along with an indication that more exist
