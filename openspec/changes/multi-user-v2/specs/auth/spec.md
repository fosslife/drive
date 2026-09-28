## ADDED Requirements

### Requirement: Users change their own password

The system SHALL allow any signed-in user to change their own password by supplying their current password
and a new one. A request with an incorrect current password SHALL be refused and SHALL be rate-limited on
the same terms as a failed login.

A user SHALL NOT be able to change any other account's password, whatever their role — an administrator
changes another account's password by resetting it, which is a separate, administrative operation.

#### Scenario: User changes their own password

- **WHEN** a signed-in user submits their correct current password and a new one
- **THEN** the password is changed and the new one authenticates at the next sign-in

#### Scenario: Wrong current password is refused

- **WHEN** a signed-in user submits an incorrect current password
- **THEN** the change is refused, the stored password is unchanged, and the attempt counts towards the
  failed-attempt limit

#### Scenario: Password change is not a path to another account

- **WHEN** a user attempts to change a password by naming another account
- **THEN** the request is refused

### Requirement: Credential changes end existing sessions

When an account's password is changed by its owner or reset by an administrator, every browser session
belonging to that account that existed before the change SHALL stop authenticating requests. The session
that performed the change MAY remain valid.

#### Scenario: Old session stops working after a reset

- **WHEN** an administrator resets an account's password while that account has an active session elsewhere
- **THEN** the next request from that session is refused and the user must sign in again

#### Scenario: API tokens are unaffected by a password change

- **WHEN** a user changes their password
- **THEN** their API tokens continue to authenticate, because a token is revoked by revoking it

### Requirement: The last administrator cannot be locked out

The system SHALL refuse any operation that would leave the instance with no enabled administrator:
deleting, disabling, or revoking administrator status from the last enabled administrator. The refusal
SHALL state the reason.

An administrator SHALL NOT delete, disable, or revoke administrator status from their own account, so that
losing the surface is always another administrator's decision.

These rules SHALL be enforced by the API, not only by the interface.

#### Scenario: Last administrator is protected

- **WHEN** the only enabled administrator is deleted, disabled, or demoted through any client
- **THEN** the operation is refused with a message naming the reason, and the account is unchanged

#### Scenario: Self-demotion is refused

- **WHEN** an administrator revokes their own administrator status, disables their own account, or deletes it
- **THEN** the operation is refused even when other administrators exist

#### Scenario: Demotion is allowed once another administrator exists

- **WHEN** a second administrator exists and one of them demotes the other
- **THEN** the operation succeeds and the demoted account loses the administrative surface on its next request

## MODIFIED Requirements

### Requirement: User accounts with isolated storage roots

Each user SHALL have an account and a storage root that is not reachable by any other non-administrative user. A user MUST NOT be able to read, write, list, or search outside their own storage root.

Administrator status SHALL confer the ability to administer accounts, quotas, and the instance. It SHALL NOT confer the ability to read, write, list, or search inside another user's storage root: there is no administrative path to another account's file content.

#### Scenario: User cannot reach another user's files

- **WHEN** a user requests a path belonging to another user
- **THEN** the request is rejected and no information about the other user's files is disclosed

#### Scenario: Administrator manages accounts

- **WHEN** an administrator creates, disables, enables, deletes, re-roles, resets the password of, or sets a quota on a user account
- **THEN** the change takes effect on that user's subsequent requests
- **AND** deleting an account does not delete another user's files

#### Scenario: Administrator cannot browse another account

- **WHEN** an administrator requests a file listing, download, search, or thumbnail for a path in another user's storage root
- **THEN** the request is rejected exactly as it would be for a non-administrative user
