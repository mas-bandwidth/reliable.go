#!/usr/bin/env bash
#
# Regenerates testdata/c_transcript.txt.gz, the golden wire transcript, from
# the C reliable library pinned at the commit below. TestWireCompatibility
# verifies on every test run that the Go port reproduces it byte for byte.
#
# usage: interop/regenerate.sh [output.gz]

set -euo pipefail

# Bumped 2026-11-09 from d4b6fe0 (1.4.4) to the C 1.4.5 release, per the rule in
# CLAUDE.md: when C cuts a release, bump the pin and regenerate. This changes what
# a green TestWireCompatibility MEANS, not what it checks: the check now proves
# compatibility with the current C release rather than with one release behind.
# A pin has no staleness signal of its own.
RELIABLE_C_COMMIT=3d06ae1ead42ec1532ee93be43d80f1e31cfba95 # reliable 1.4.5

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
