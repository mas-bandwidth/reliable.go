#!/usr/bin/env bash
#
# Regenerates testdata/c_transcript.txt.gz, the golden wire transcript, from
# the C reliable library pinned at the commit below. TestWireCompatibility
# verifies on every test run that the Go port reproduces it byte for byte.
#
# usage: interop/regenerate.sh [output.gz]

set -euo pipefail

# Bumped 2026-09-13 from c5be93c (1.4.0) to the C 1.4.4 release, per the rule in
# CLAUDE.md: when C cuts a release, bump the pin and regenerate. The regenerated
# transcript is BYTE-IDENTICAL again -- the wire did not move across 1.4.1 through
# 1.4.4, whose changes were strict floating point, the export surface, 64-bit
# fragment arithmetic with refused configurations, and an allocation-failure guard.
# So this changes what a green TestWireCompatibility MEANS, not what it checks: the
# check now proves compatibility with the current C release rather than with one
# four patch releases behind. A pin has no staleness signal of its own.
RELIABLE_C_COMMIT=d4b6fe0882d2d752d57984f70e7a29037982984f # reliable 1.4.4

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
