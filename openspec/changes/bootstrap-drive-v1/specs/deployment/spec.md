## Purpose

Getting the drive running must be one step, and keeping it running must never require hand-editing a config file, a database row, or a migration script. This is the capability that separates this project from the alternatives.

## ADDED Requirements

### Requirement: Single self-contained artifact

The system SHALL be distributed as a single executable file that contains the web interface and requires no separate application server, database server, cache server, or static asset directory alongside it. It SHALL additionally be distributed as a container image that runs the same executable.

#### Scenario: Run from a single file

- **WHEN** the executable is placed on a machine with no other project files and started
- **THEN** it serves the full web interface and API

#### Scenario: No external services required

- **WHEN** the system is started on a machine with no database server, cache server, or reverse proxy present
- **THEN** it starts and operates normally

#### Scenario: Container start

- **WHEN** the container image is run with a data directory mounted and a port published
- **THEN** the system is reachable and stores its data in the mounted directory

### Requirement: Starts with no configuration

Every configuration value SHALL have a working default. The system SHALL start successfully with no configuration file and no environment variables set. Configuration SHALL be overridable by environment variable, and a configuration file SHALL never be required.

#### Scenario: Start with no configuration at all

- **WHEN** the executable is started with no arguments, no environment variables, and no configuration file
- **THEN** it starts, chooses a default data directory and port, and reports both

#### Scenario: Environment override

- **WHEN** a configuration value is supplied by environment variable
- **THEN** it takes effect and overrides the default

#### Scenario: Invalid configuration

- **WHEN** a supplied configuration value is invalid
- **THEN** the system refuses to start and reports which value is wrong and what was expected
- **AND** does not start in a partially configured state

### Requirement: First-run setup without default credentials

On first start the system SHALL have no usable account. It SHALL create the first administrator through a browser-based setup flow authorised by a one-time token that the system generates and prints to its own output. The token SHALL become invalid once setup completes.

#### Scenario: First start

- **WHEN** the system is started for the first time
- **THEN** it prints a setup URL containing a one-time token
- **AND** no account exists and no credentials grant access

#### Scenario: Completing setup

- **WHEN** an operator opens the setup URL and creates the first administrator account
- **THEN** that account can log in
- **AND** the setup token no longer works
- **AND** the setup flow is no longer reachable

#### Scenario: Setup attempted without the token

- **WHEN** the setup URL is opened without a valid token
- **THEN** setup is refused

#### Scenario: Restart before setup completes

- **WHEN** the system is restarted before the first administrator is created
- **THEN** setup is still available and a valid token is reported

### Requirement: Encrypted transport by default

The system SHALL serve over HTTPS without requiring the operator to configure a reverse proxy. Where a public hostname is configured, it SHALL obtain and renew a certificate automatically. Otherwise it SHALL generate a self-signed certificate and report its fingerprint so the operator can verify it.

Plaintext HTTP SHALL be used only when explicitly enabled by the operator, and the system SHALL warn when it is.

#### Scenario: Public hostname configured

- **WHEN** a public hostname is configured and the host is reachable
- **THEN** the system obtains a certificate automatically and serves HTTPS
- **AND** renews the certificate before expiry without operator action

#### Scenario: No hostname configured

- **WHEN** the system starts with no public hostname
- **THEN** it serves HTTPS using a self-signed certificate
- **AND** prints the certificate fingerprint

#### Scenario: Plaintext explicitly enabled

- **WHEN** the operator explicitly enables plaintext HTTP
- **THEN** the system serves over HTTP and logs a warning that traffic is unencrypted

### Requirement: Automatic schema migrations

The system SHALL apply any required index schema migrations automatically at startup. Upgrading SHALL never require the operator to run a migration command, edit the index, or perform any manual step.

If a migration cannot be applied, the system SHALL refuse to start and report the reason rather than operating against an inconsistent index.

#### Scenario: Upgrade to a newer version

- **WHEN** a newer version of the executable is started against an existing data directory
- **THEN** required migrations are applied automatically and the system starts normally

#### Scenario: Migration fails

- **WHEN** a migration cannot be applied
- **THEN** the system refuses to start and reports which migration failed and why
- **AND** no user file is modified or deleted

#### Scenario: Downgrade is refused

- **WHEN** an older version is started against a data directory written by a newer version
- **THEN** it refuses to start and reports the version mismatch rather than modifying the index

### Requirement: Backup and restore

All state required to reconstruct a running instance SHALL live within a single data directory. The system SHALL provide a documented procedure to produce a consistent backup and to restore from one.

Because the filesystem is the source of truth, restoring only the file contents SHALL yield a working instance once accounts are re-created, without any repair step applied to the files themselves. The documented procedure SHALL state that accounts, API tokens, and share links live only in the index and are lost if it is not included in the backup.

#### Scenario: Back up and restore

- **WHEN** an operator follows the documented backup procedure and restores that backup into a new data directory
- **THEN** the system starts with all users, files, shares, and settings intact

#### Scenario: Restore files only

- **WHEN** only the stored files are restored and the index is absent
- **THEN** the system rebuilds the index and, once an account is created with the username its storage root is named for, that account's files are browsable and downloadable

### Requirement: Operational visibility

The system SHALL report its startup state, listening address, data directory, and version in its output, and SHALL expose a health endpoint indicating whether it is ready to serve requests.

#### Scenario: Startup reporting

- **WHEN** the system starts
- **THEN** it reports its version, listening address, whether transport is encrypted, and the data directory in use

#### Scenario: Health check

- **WHEN** a health endpoint is queried
- **THEN** it reports whether the system is ready to serve requests
- **AND** does not disclose user data or configuration secrets
