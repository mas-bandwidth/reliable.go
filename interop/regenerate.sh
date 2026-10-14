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
# Regenerated 2026-10-14 from the C fix branch sprint/reliable-audit-fixes: PR 68
# (merge dfd5541c) removes the early received-packets advance in the fragment path
# and refuses stale fragments there, so receiver acks in this lossy scenario move
# with the fix and the wire bytes of the transcript legitimately change. C is
# normative and the golden follows it; the Go port reproduces the post-fix C
# transcript byte for byte. The transcript at the branch tip is byte-identical to
# the transcript at dfd5541c itself.
RELIABLE_C_COMMIT=c8995dcbcc252d018a641a1a1bb52b1c1c3c2519 # reliable sprint/reliable-audit-fixes at or after dfd5541c (PR 68)

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
