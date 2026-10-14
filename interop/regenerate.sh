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
# Regenerated 2026-10-14 from C PR 68 (merge dfd5541c): the fix removes the early
# received-packets advance in the fragment path and refuses stale fragments there,
# so receiver acks in this lossy scenario move with the fix and the wire bytes of
# the transcript legitimately change. C is normative and the golden follows it; the
# Go port reproduces the post-fix C transcript byte for byte.
# C reliable release 1.4.5 is at commit e4e70927d1656ffcd822309fc2aa7af004c58f6a.
# Regeneration from 1.4.5 is byte-identical to 1.4.4. The pin below is PR 68
# (dfd5541c), a direct descendant of the 1.4.5 release (e4e70927) on C main which
# implements the fragment window advance fix. Moving the pin back to 1.4.5 (e4e70927)
# would rewind the fragment-window wire fix that this port implements and redden
# TestWireCompatibility, so the 1.4.5 release appears in VersionFull and README,
# while the pin remains on the post-1.4.5 fix commit.
RELIABLE_C_COMMIT=dfd5541c2ec82e912e67420c07361a3c48bfaa68 # reliable PR 68 (fragment window advance fix, descends from 1.4.5 e4e70927)

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
