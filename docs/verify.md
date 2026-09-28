# Verifying what you downloaded

Every release is signed at the moment it is built, so you can check that a binary or an image really
came out of this repository's `release` workflow and not from somewhere else. Nothing here is
required to run the drive — skip it if you build from source, since then the answer is already yes.

You need the [GitHub CLI](https://cli.github.com) (`gh`), logged in or not, and nothing else.

## A binary

```sh
gh attestation verify ./drive-linux-amd64 --repo fosslife/drive
```

It exits 0 and prints a `✓ Verification succeeded!` summary when the file really came from this
repository's workflow, and exits non-zero with the reason when it did not. Nothing is printed when
the output is piped rather than shown in a terminal, so in a script check the exit status.

The `SHA256SUMS` file in each release is the simpler, weaker check: it proves the file is the one the
release page lists, which is enough to catch a truncated download and nothing else. The attestation
is what proves where that file came from.

## An image

```sh
gh attestation verify oci://ghcr.io/fosslife/drive:v0.1.1 --repo fosslife/drive
```

That pulls the manifest, finds the signed statement attached to its digest, and checks it names this
repository and this workflow. It reads the manifest from the registry, so a private package needs a
`docker login ghcr.io` first — the error if you skip it is `remote registry authorization failed`.

You can also ask GitHub directly, which needs no registry access at all, given a digest:

```sh
gh api repos/fosslife/drive/attestations/sha256:<digest>
```

The image also carries its own build record and a bill of materials — every package that went into
it — which do not need `gh`:

```sh
docker buildx imagetools inspect ghcr.io/fosslife/drive:v0.1.1 --format '{{json .SBOM}}'
docker buildx imagetools inspect ghcr.io/fosslife/drive:v0.1.1 --format '{{json .Provenance}}'
```

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

A successful run still leaves two annotations, and both are noise: the action asks after an
`artifact-metadata:write` permission and then fails to "create a storage record" because that record
describes workflow artifacts, which this release does not produce — it attaches files to the release
instead. The attestations themselves are created, signed, logged in Rekor, and verifiable.
