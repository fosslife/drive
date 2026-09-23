# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The operator and the handful of people they hand an account to — family and
friends, non-technical, who did not read the documentation and will not. The
operator knows the tree by heart; nobody else does. A second audience never
signs in at all: the stranger who receives a share link and sees exactly one
screen.

## Product Purpose

A self-hosted drive: one binary, one process, the operator's own disk. It
exists so that files live somewhere the operator controls, and so that handing
a file to someone else does not route through a company. Success is a drive
that is boring — it holds things, finds them, and gives them back.

## Positioning

The filesystem is the source of truth and SQLite is a disposable index. Losing
the database loses logins, never a byte: storage roots are `users/<username>/`
at real paths, so re-creating an account reattaches it to its files. Nothing
but permanent delete destroys user bytes; overwrites and deletes go through
trash first. A competitor with an object store and a metadata database cannot
truthfully make either claim.

## Operating Context

Desktop-first: the real work is a big screen, multi-select, drag-and-drop,
keyboard. The phone is for grabbing a file or checking a share and must work
without being the design centre. Three moments carry the product: finding a
file, getting files in, and looking at photographs. Folders may hold 100,000
entries; the listing is paged and virtualised, and a background reconciler may
be mid-scan while someone browses, so a listing is sometimes an honestly
partial answer.

## Capabilities and Constraints

Six authenticated screens (browse/search, trash, share links, API tokens,
sign-in, first-run setup) plus one public share page. Browse, search by
filename across the whole drive, upload with resumable per-file progress,
create folders, rename, move, multi-select, download (single file or a zip
archive), trash with restore and permanent delete, public share links with
optional password and expiry, API tokens shown exactly once, thumbnails for
raster images with an in-place viewer.

Constraints that bind the interface:

- Single binary. The interface is React + Vite built to `web/dist` and embedded
  via `embed.FS`; `go build` alone does not run it, and the binary serves a
  plain note at `/` when it was not built.
- No runtime dependency may be added that needs a Node process in production.
- Stored files are served as attachments with `nosniff` and a restrictive CSP.
  Inline rendering is allowed only for inert raster images — never SVG, never
  PDF. Any preview design must respect this.
- Configuration is environment-only, every value defaulted. There is no
  settings screen to design because there are no user-facing settings.
- Share tokens and API token secrets are returned exactly once and stored only
  as hashes. A screen that shows one has no second chance to show it.

## Brand Commitments

The name is lowercase `drive`. No logo, no colour, no typeface has been
committed; the current look is a first-iteration placeholder with no
authority.

## Evidence on Hand

Real, in-repo: a working API, a virtualised listing, resumable uploads, a
thumbnail cache, end-to-end tests against the real binary in real Chrome. No
customers, no benchmarks published, no pricing, no hosted service. None of
those may be invented on any surface.

## Product Principles

- **The filesystem is the truth.** The interface may never imply the index
  knows something the disk does not; a partial listing says so.
- **Nothing here destroys bytes by accident.** Destruction is one screen, one
  vocabulary, and always confirmed.
- **The uninitiated must succeed unattended.** A relative who was handed an
  account gets no onboarding call.
- **Density is a feature, not a symptom.** This is a tool for finding things
  among many things; whitespace that costs rows costs the product.
- **One binary, one stylesheet.** Weight added to the interface is weight in
  every release forever.

## Accessibility & Inclusion

No formal standard was established. Keyboard operation of the listing and
visible focus are product requirements because the primary operator works
by keyboard; contrast must survive a laptop screen in daylight.
