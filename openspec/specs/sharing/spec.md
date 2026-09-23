# sharing Specification

## Purpose
Lets a user hand a file or folder to someone who has no account on the instance, via a link, without weakening the isolation between accounts.

## Requirements

### Requirement: Create a public share link

A user SHALL be able to create a share link for any file or folder within their own storage root. The link SHALL be identified by a token with sufficient entropy that it cannot be guessed or enumerated, and the token MUST NOT encode the file's path, name, or identifier in a recoverable form.

#### Scenario: Share a file

- **WHEN** a user creates a share link for a file they own
- **THEN** the system returns a URL containing an unguessable token
- **AND** an unauthenticated visitor opening that URL can view and download that file

#### Scenario: Share a folder

- **WHEN** a user creates a share link for a folder
- **THEN** an unauthenticated visitor can browse that folder and its descendants and download files within it

#### Scenario: Cannot share what you do not own

- **WHEN** a user attempts to create a share link for a path outside their storage root
- **THEN** the request is rejected

### Requirement: Share links are read-only

A share link SHALL grant read access only. A visitor holding a share link MUST NOT be able to upload, rename, move, delete, or otherwise modify anything, and MUST NOT be able to reach any path outside the shared item.

#### Scenario: Write attempt through a share link

- **WHEN** a visitor holding a share link attempts any modifying operation
- **THEN** the request is rejected

#### Scenario: Escaping the shared subtree

- **WHEN** a visitor holding a share link for a folder requests a path above or outside that folder
- **THEN** the request is rejected and no information about the parent path is disclosed

### Requirement: Optional password protection

A user SHALL be able to protect a share link with a password. Where a password is set, the shared content MUST NOT be retrievable without it, and the password SHALL be stored only as a hash.

#### Scenario: Password required

- **WHEN** a visitor opens a password-protected share link
- **THEN** they are prompted for the password and no file content or listing is returned until it is supplied correctly

#### Scenario: Wrong password

- **WHEN** a visitor submits an incorrect share password
- **THEN** access is refused
- **AND** repeated attempts are rate-limited

### Requirement: Optional expiry

A user SHALL be able to set an expiry time on a share link. An expired link SHALL stop granting access without any administrator action.

#### Scenario: Link expires

- **WHEN** a visitor opens a share link after its expiry time has passed
- **THEN** access is refused and the shared content is not disclosed

#### Scenario: No expiry set

- **WHEN** a share link is created without an expiry
- **THEN** it remains valid until revoked

### Requirement: Manage and revoke share links

A user SHALL be able to list the share links they have created, see what each one points to and whether it is password-protected or expiring, and revoke any of them. Revocation SHALL take effect immediately.

#### Scenario: Revoke a link

- **WHEN** a user revokes a share link
- **THEN** subsequent visits to that URL are refused

#### Scenario: Shared item is deleted

- **WHEN** the file a share link points to is moved to trash
- **THEN** the link stops serving content
- **AND** the link is reported to its owner as pointing at a trashed item

#### Scenario: Shared item is renamed or moved

- **WHEN** the owner renames or moves a shared file within their storage root
- **THEN** the existing share link continues to serve the same file
