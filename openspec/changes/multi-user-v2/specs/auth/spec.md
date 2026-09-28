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

### Requirement: Administrator status is assigned once, by first-run setup

An instance SHALL have exactly the administrator that first-run setup created. The system SHALL NOT offer
any way to grant administrator status to an account or to revoke it from one: not through the API, not
through the interface, not through configuration.

An administrator SHALL NOT delete or disable their own account. That refusal SHALL come from the API, not
only from the interface, and it is the only rule needed to keep an instance administrable — with no way to
grant or revoke the status, no sequence of permitted operations can leave an instance without the
administrator it was set up with.

#### Scenario: No endpoint grants administrator status

- **WHEN** any client asks for an account to be made an administrator, by any request the system accepts
- **THEN** no account's administrator status changes

#### Scenario: An administrator cannot remove themselves

- **WHEN** an administrator deletes or disables their own account
- **THEN** the operation is refused with a message naming the reason, and the account is unchanged

#### Scenario: Ordinary accounts are administered freely

- **WHEN** an administrator deletes or disables any account other than their own
- **THEN** the operation succeeds

## MODIFIED Requirements

### Requirement: User accounts with isolated storage roots

Each user SHALL have an account and a storage root that is not reachable by any other non-administrative user. A user MUST NOT be able to read, write, list, or search outside their own storage root.

Administrator status SHALL confer the ability to administer accounts, quotas, and the instance. It SHALL NOT confer the ability to read, write, list, or search inside another user's storage root: there is no administrative path to another account's file content.

#### Scenario: User cannot reach another user's files

- **WHEN** a user requests a path belonging to another user
- **THEN** the request is rejected and no information about the other user's files is disclosed

#### Scenario: Administrator manages accounts

- **WHEN** an administrator creates, disables, enables, deletes, resets the password of, or sets a quota on a user account
- **THEN** the change takes effect on that user's subsequent requests
- **AND** deleting an account does not delete another user's files

#### Scenario: Administrator cannot browse another account

- **WHEN** an administrator requests a file listing, download, search, or thumbnail for a path in another user's storage root
- **THEN** the request is rejected exactly as it would be for a non-administrative user
