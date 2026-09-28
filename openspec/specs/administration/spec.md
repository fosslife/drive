# administration Specification

## Purpose
Defines the operator's surface over a running instance: the lifecycle of the accounts on it, the storage
allowance each one has, and the state of the instance itself — so that running a drive for other people
never requires a terminal, a database client, or a restart.

## Requirements

### Requirement: Administrative surface is reachable from the interface

Every administrative operation the system offers SHALL be available to an administrator through the
bundled interface, and SHALL be available to an administrator's API token on the same endpoints. No
administrative operation SHALL require a terminal, a database client, a configuration file, or a restart.

The administrative surface SHALL be reached only by an account marked as an administrator, and SHALL NOT
be visible to any other account.

#### Scenario: Administrator reaches account management in the interface

- **WHEN** an administrator signs in to the bundled interface
- **THEN** an accounts screen is offered from which every account operation can be performed

#### Scenario: Non-administrator is refused

- **WHEN** a signed-in non-administrative user requests an administrative endpoint or the accounts screen
- **THEN** the request is refused and no account, quota, or instance information is disclosed

#### Scenario: Administrator's token administers

- **WHEN** a request authenticated by an API token belonging to an administrator performs an
  administrative operation
- **THEN** it succeeds, and the same operation with a non-administrator's token is refused

### Requirement: Account inventory

The system SHALL report, to an administrator, every account on the instance with its username, whether it
is an administrator, whether it is disabled, when it was created, its storage quota if it has one, the
number of bytes it currently occupies, and the number of files it holds.

Reported usage SHALL include everything the account occupies on the data volume, trashed files included,
because trashed bytes have not been freed.

#### Scenario: Inventory lists every account with its usage

- **WHEN** an administrator requests the account inventory
- **THEN** every account is listed with its role, state, creation time, quota, byte usage, and file count

#### Scenario: Usage counts trash

- **WHEN** a user trashes a file and an administrator then reads that account's usage
- **THEN** the trashed file's bytes are still counted
- **AND** they stop being counted once the file is permanently deleted

### Requirement: Account lifecycle

An administrator SHALL be able to create an account with an initial password, disable an account, enable a
disabled account, delete an account, and set a new password for an account without knowing the old one.

Administrator status SHALL NOT be among the things an administrator can change. It is assigned by
first-run setup and by nothing else, so an instance has the administrator it started with; see the `auth`
capability.

Creating an account SHALL create its storage root. An account created with a username that was used before
SHALL reattach to the existing storage root and its files, which is the documented recovery path after the
index is lost.

Deleting an account SHALL NOT delete a byte of file content: the storage root is left in place.

#### Scenario: Administrator creates an account that can sign in

- **WHEN** an administrator creates an account with a username and password
- **THEN** that account can sign in and browse an empty drive of its own

#### Scenario: Disabling takes effect immediately

- **WHEN** an administrator disables an account that has an active session
- **THEN** that session's next request is refused, and that account cannot sign in again until it is enabled

#### Scenario: Deleting an account keeps its files

- **WHEN** an administrator deletes an account and then re-creates it with the same username
- **THEN** the re-created account sees its previous files after a rescan

#### Scenario: An administrator cannot remove themselves

- **WHEN** an administrator deletes or disables their own account
- **THEN** the operation is refused with a message naming the reason, and the account is unchanged

### Requirement: Per-account storage quota administration

An administrator SHALL be able to set, change, and remove a storage quota for any account, expressed in
bytes. An account with no quota SHALL be unlimited. New accounts SHALL take their quota from a configured
default whose own default is unlimited.

Setting a quota below what an account already occupies SHALL be accepted and SHALL NOT delete, refuse
access to, or hide any existing file; it takes effect on the next write.

#### Scenario: Quota is set and reported

- **WHEN** an administrator sets a quota on an account
- **THEN** that quota is reported in the account inventory and to that account itself

#### Scenario: Quota below current usage is accepted

- **WHEN** an administrator sets a quota smaller than the account's current usage
- **THEN** the quota is stored, every existing file remains readable and downloadable, and the next write
  by that account is refused

#### Scenario: Removing a quota restores unlimited

- **WHEN** an administrator removes an account's quota
- **THEN** that account's writes are limited only by the free-space reserve

### Requirement: Instance state is visible to the operator

The system SHALL report, to an administrator, the version it is running, how long it has been running, the
data directory it is using, the free and total space on the data volume, when the last index scan started,
how long it took, how many entries it saw, and the error it failed with if it failed.

#### Scenario: Operator reads instance state

- **WHEN** an administrator requests instance state
- **THEN** version, uptime, data directory, free and total space, and last scan outcome are reported

#### Scenario: A failed scan is reported, not hidden

- **WHEN** the last scan aborted with an error
- **THEN** that error is reported as part of instance state until a later scan succeeds

### Requirement: Rescan on demand

An administrator SHALL be able to start an index scan without restarting the instance. Requesting a scan
while one is running SHALL NOT start a second one, and SHALL NOT be reported as an error.

#### Scenario: Administrator triggers a scan

- **WHEN** an administrator requests a rescan after adding files to a storage root outside the application
- **THEN** a scan starts, its progress is reported, and the files become browsable when it finishes

#### Scenario: Requesting a scan during a scan is harmless

- **WHEN** an administrator requests a rescan while a scan is already running
- **THEN** the request is accepted, no second scan runs concurrently, and the running scan is not disturbed
