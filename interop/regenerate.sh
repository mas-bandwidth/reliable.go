#!/usr/bin/env bash
#
# Regenerates testdata/c_transcript.txt.gz, the golden wire transcript, from
# the C reliable library pinned at the commit below. TestWireCompatibility
# verifies on every test run that the Go port reproduces it byte for byte.
#
# usage: interop/regenerate.sh [output.gz]

set -euo pipefail

# Bumped 2026-07-26 from e00e11f (1.3.4). The regenerated transcript is BYTE-IDENTICAL
# either way -- the wire did not move across those 21 commits -- so this changes what a
# green TestWireCompatibility MEANS, not what it checks. At 1.3.4 the check proved
# compatibility with a version one minor release behind, and would have stayed green
# even if the wire HAD moved, because a pin has no staleness signal of its own.
RELIABLE_C_COMMIT=c5be93c40e3951508a3dc05e23ab2ddd4fab676d # reliable 1.4.0

here="$(cd "$(dirname "$0")" && pwd)"
out="${1:-$here/../testdata/c_transcript.txt.gz}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

git clone --quiet https://github.com/mas-bandwidth/reliable "$tmp/reliable"
git -C "$tmp/reliable" checkout --quiet "$RELIABLE_C_COMMIT"

cc -O2 -I"$tmp/reliable" -o "$tmp/transcript" "$here/transcript.c" "$tmp/reliable/reliable.c" -lm

mkdir -p "$(dirname "$out")"
"$tmp/transcript" | gzip -n -9 > "$out"

echo "wrote $out (C reliable @ $RELIABLE_C_COMMIT)"
