#!/usr/bin/env bash
#
# Regenerates testdata/c_transcript.txt.gz, the golden wire transcript, from
# the C reliable library pinned at the commit below. TestWireCompatibility
# verifies on every test run that the Go port reproduces it byte for byte.
#
# usage: interop/regenerate.sh [output.gz]

set -euo pipefail

RELIABLE_C_COMMIT=e00e11f587efca418820544cccaf085296155834 # reliable 1.3.4

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
