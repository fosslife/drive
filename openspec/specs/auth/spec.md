# auth Specification

## Purpose
Defines who can reach the drive and what they can reach: user accounts with isolated storage roots, browser sessions for people, and API tokens for programs, all authenticating against the same API.

## Requirements

### Requirement: One API for all clients

The system SHALL expose a single API. The bundled web interface SHALL be an ordinary client of that API with no private or privileged endpoints available to it, and every operation available to the web interface SHALL be available to an authenticated programmatic client.

#### Scenario: Programmatic client has parity with the web UI

- **WHEN** a client authenticates with an API token and performs any operation the web interface offers
- **THEN** the operation succeeds with the same behavior and permission checks as it would from the browser

#### Scenario: No unauthenticated endpoints

- **WHEN** an API request is made without valid credentials
- **THEN** the request is rejected, except for endpoints explicitly defined as public such as share link access and first-run setup

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

### Requirement: Password authentication

The system SHALL authenticate users by password, storing only a salted hash produced by a memory-hard hashing function. The system MUST NOT ship with any default or well-known credentials.

The system SHALL rate-limit failed authentication attempts.

#### Scenario: Successful login

- **WHEN** a user submits a correct username and password
- **THEN** a session is established

#### Scenario: Failed login

- **WHEN** a user submits an incorrect password
- **THEN** authentication fails with a message that does not reveal whether the account exists

#### Scenario: Repeated failures are throttled

- **WHEN** repeated failed attempts are made against an account or from one source
- **THEN** further attempts are delayed or refused for a period

#### Scenario: Passwords are never stored recoverably

- **WHEN** the index database is inspected
- **THEN** no plaintext or reversibly encrypted password is present

### Requirement: Browser sessions

The system SHALL maintain browser sessions using cookies that are not accessible to scripts, are restricted to secure transport, and are constrained against cross-site submission. Sessions SHALL expire and SHALL be revocable by logging out.

Session state SHALL be held server-side; the cookie SHALL carry only an opaque identifier and MUST NOT carry user identity or privileges.

#### Scenario: Session cookie attributes

- **WHEN** a session is established over HTTPS
- **THEN** the session cookie is set with HttpOnly, Secure, and SameSite restrictions

#### Scenario: Cookie carries no authority

- **WHEN** a session cookie is inspected
- **THEN** it contains an opaque identifier only, and altering it does not change the identity or privileges of the requester

#### Scenario: Logout revokes the session

- **WHEN** a user logs out
- **THEN** the session is invalidated server-side and can no longer authenticate requests

### Requirement: Session identifier is renewed on privilege change

The system SHALL issue a new session identifier whenever a session's authenticated identity or privilege level changes, including at login. A session identifier that existed before authentication MUST NOT remain valid after it.

#### Scenario: Identifier changes at login

- **WHEN** a visitor holds a pre-authentication session and then logs in successfully
- **THEN** a different session identifier is issued

#### Scenario: Fixated identifier is rejected

- **WHEN** a session identifier obtained before a user authenticated is presented after that user logs in
- **THEN** it does not authenticate as that user

#### Scenario: Cross-site state change is rejected

- **WHEN** a state-changing request arrives with a valid session cookie but without proof it originated from the application
- **THEN** the request is rejected

#### Scenario: Cross-origin request is refused

- **WHEN** a state-changing request carries an origin indicating it was initiated by another site
- **THEN** the request is rejected before any file is read or written

### Requirement: API tokens

The system SHALL allow a user to create named API tokens scoped to their own account, to list them, and to revoke them individually. A token's secret SHALL be displayed only at creation time and stored only as a hash.

#### Scenario: Create and use a token

- **WHEN** a user creates an API token and uses it to authenticate a request
- **THEN** the request is authorized as that user with that user's permissions

#### Scenario: Token secret is shown once

- **WHEN** a user views their list of API tokens after creating one
- **THEN** the token's name, creation time, and last use are shown but the secret is not

#### Scenario: Revoked token stops working

- **WHEN** a user revokes an API token
- **THEN** subsequent requests using that token are rejected

#### Scenario: Token cannot exceed its owner's access

- **WHEN** a request authenticated by an API token targets a path outside its owner's storage root
- **THEN** the request is rejected

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
