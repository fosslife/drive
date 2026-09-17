#!/bin/sh
# Builds the release artifacts: the interface, then one static binary per
# supported architecture. No cgo anywhere, which is why cross-compiling is a
# loop rather than a build farm.
#
#   scripts/release.sh            # dist/drive-linux-amd64, dist/drive-linux-arm64
#   VERSION=v1.2.3 scripts/release.sh
set -eu

cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
OUT=${OUT:-dist}

# `go build` does not build the interface, and a binary without it serves the
# API and a note saying so. SKIP_WEB exists for checks that only care about the
# binary itself.
if [ "${SKIP_WEB:-}" = "" ]; then
	npm --prefix web ci
	npm --prefix web run build
fi

mkdir -p "$OUT"
for arch in amd64 arm64; do
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
		-trimpath -ldflags "-s -w -X main.Version=$VERSION" \
		-o "$OUT/drive-linux-$arch" ./cmd/drive
	echo "$OUT/drive-linux-$arch $VERSION"
done
