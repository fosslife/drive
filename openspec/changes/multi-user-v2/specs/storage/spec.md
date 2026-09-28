## ADDED Requirements

### Requirement: Per-user storage quota

The system SHALL support an optional per-account byte allowance. Where an account has one, the system SHALL
refuse any operation that would increase the bytes that account occupies beyond it, and SHALL report that
the account's quota refused the write, distinguishably from the free-space reserve refusing it. An account
without an allowance SHALL be limited only by the reserve.

Bytes occupied SHALL be counted as the account's stored files plus its trashed files, because trash has not
been freed. Derived data the system generates for itself, such as thumbnails, SHALL NOT be counted and
SHALL NOT be refused by a quota.

Being over quota SHALL NOT restrict anything but writes. Listing, searching, downloading, viewing,
trashing, restoring from trash, and permanently deleting SHALL keep working, so that the way out of a full
account is always available.

A quota SHALL be a limit on new bytes only. It MUST NOT cause any existing file to be deleted, hidden, or
made unreadable, whatever its value.

#### Scenario: Upload beyond the quota is refused

- **WHEN** an account with a quota uploads a file whose size would take it past that quota
- **THEN** the upload is refused before any bytes are stored, and the message names the quota as the reason

#### Scenario: Two limits are told apart

- **WHEN** a write is refused because the data volume is below its free-space reserve
- **THEN** the message names the reserve, not the account's quota
- **AND** when a write is refused by the quota, the message names the quota

#### Scenario: A full account can still empty itself

- **WHEN** an account is at or over its quota
- **THEN** it can still list, search, download, trash, restore, and permanently delete
- **AND** permanently deleting a file lowers its usage and lets the next write through

#### Scenario: Unlimited by default

- **WHEN** an account has no quota
- **THEN** its writes are refused only by the free-space reserve, exactly as before this change
