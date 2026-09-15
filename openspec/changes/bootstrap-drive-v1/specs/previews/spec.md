## Purpose

Makes a folder of photos recognisable at a glance by serving small image thumbnails, without ever letting thumbnail work slow down or block browsing.

## ADDED Requirements

### Requirement: Image thumbnails

The system SHALL generate and serve reduced-size thumbnails for common raster image formats. Thumbnails SHALL preserve aspect ratio and SHALL respect image orientation metadata so that rotated photographs are not displayed sideways.

#### Scenario: Thumbnail for a photograph

- **WHEN** a client requests a thumbnail for a JPEG, PNG, GIF, or WebP file
- **THEN** the system returns a reduced-size image preserving the original aspect ratio

#### Scenario: Rotated photograph

- **WHEN** an image carries orientation metadata indicating rotation
- **THEN** the returned thumbnail is displayed in the correct orientation

#### Scenario: Unsupported file type

- **WHEN** a thumbnail is requested for a file type with no thumbnail support, such as a video or archive
- **THEN** the system reports that no thumbnail is available
- **AND** does not return an error that prevents the file from being listed or downloaded

### Requirement: Thumbnails never block listing

Directory listings SHALL be returned without waiting for any thumbnail to be generated. Thumbnail generation SHALL happen separately from listing, and a pending, failed, or unsupported thumbnail MUST NOT delay or fail a listing response.

#### Scenario: Browsing a folder of new images

- **WHEN** a user opens a folder of 1,000 images for which no thumbnails have yet been generated
- **THEN** the listing is returned immediately with all entries
- **AND** thumbnails arrive afterwards as they become available

#### Scenario: Thumbnail generation fails

- **WHEN** an image is corrupt and its thumbnail cannot be generated
- **THEN** the file is still listed, downloadable, and shareable
- **AND** the failure is not retried indefinitely on every request

### Requirement: Thumbnails are cached and derived

Generated thumbnails SHALL be cached so that repeat requests do not regenerate them. Thumbnails SHALL be treated as derived data: deleting the entire thumbnail cache MUST NOT lose any user data, and the system SHALL regenerate thumbnails on demand afterwards.

#### Scenario: Repeat request is served from cache

- **WHEN** a thumbnail is requested a second time and the source file is unchanged
- **THEN** it is served from cache without regeneration

#### Scenario: Source file changes

- **WHEN** a file's contents change and its thumbnail is requested
- **THEN** the returned thumbnail reflects the new contents

#### Scenario: Thumbnail cache is deleted

- **WHEN** the entire thumbnail cache is deleted
- **THEN** no user file is affected
- **AND** thumbnails are regenerated on subsequent requests

### Requirement: View an image at full size

The system SHALL allow a user to open an image and view it at full size without downloading it to disk first, and to move to the next and previous image in the same folder.

#### Scenario: Open an image

- **WHEN** a user opens an image from a folder listing
- **THEN** the full-size image is displayed in place

#### Scenario: Move between images

- **WHEN** a user is viewing an image and advances to the next one
- **THEN** the next image in the same folder is displayed

### Requirement: Thumbnails follow the access rules of the file

A thumbnail SHALL be retrievable only by a requester who is permitted to read the underlying file, whether through their own account or through a share link granting access to it.

#### Scenario: Another user requests a thumbnail

- **WHEN** a user requests a thumbnail for a file belonging to a different user
- **THEN** the request is rejected

#### Scenario: Thumbnail through a share link

- **WHEN** a visitor holding a valid share link for a folder requests a thumbnail for an image inside it
- **THEN** the thumbnail is returned

#### Scenario: Thumbnail after revocation

- **WHEN** a share link is revoked and a visitor requests a thumbnail for a file it covered
- **THEN** the request is rejected

### Requirement: No mandatory external media dependency

Thumbnail generation MUST NOT require an external media toolchain to be installed for the system to start or to serve image thumbnails.

#### Scenario: Clean installation

- **WHEN** the system is started on a machine with no additional media software installed
- **THEN** image thumbnails are generated and served normally
