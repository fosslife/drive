# Verifying what you downloaded

Every release is signed at the moment it is built, so you can check that a binary or an image really
came out of this repository's `release` workflow and not from somewhere else. Nothing here is
required to run the drive — skip it if you build from source, since then the answer is already yes.

You need the [GitHub CLI](https://cli.github.com) (`gh`), logged in or not, and nothing else.

## An image

```sh
gh attest verify oci://ghcr.io/fosslife/drive:v0.1.1 --repo fosslife/drive
```

That pulls the manifest, finds the signed statement attached to its digest, and checks it names this
repository and this workflow. A pass looks like:

```
Loaded digest sha256:… for oci://ghcr.io/fosslife/drive:v0.1.1
✓ Verification succeeded!
```

The image also carries its own build record and a bill of materials — every package that went into
it — which do not need `gh`:

```sh
docker buildx imagetools inspect ghcr.io/fosslife/drive:v0.1.1 --format '{{json .SBOM}}'
docker buildx imagetools inspect ghcr.io/fosslife/drive:v0.1.1 --format '{{json .Provenance}}'
```

## A binary

```sh
gh attest verify ./drive-linux-amd64 --repo fosslife/drive
```

The `SHA256SUMS` file in each release is the simpler, weaker check: it proves the file is the one the
release page lists, which is enough to catch a truncated download and nothing else. The attestation
is what proves where that file came from.

## What a pass does and does not tell you

It says: this exact file was produced by the `release` workflow in `fosslife/drive`, at a commit you
can read, and has not been altered since. It does not say the code is correct, or that the tag it was
built from contains what you expected — for that, read the tag.

## Publishing side

`.github/workflows/release.yml` does this with two `actions/attest-build-provenance` steps, one for
the binaries and one for the image digest, plus `--provenance`/`--sbom` on the image build. It needs
`id-token: write` and `attestations: write`, which are declared at the top of that file. There are no
keys anywhere and nothing to rotate: the signature is made against the workflow's own short-lived
identity.
