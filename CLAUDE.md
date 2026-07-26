<!-- HOT:BEGIN -->
## HOT — read before reasoning about this repo

WHAT: the official Go port of the C reliable library (mas-bandwidth/reliable, which is
NORMATIVE). Module path ends `.go`; the package is `reliable`. Not the C repo, not
reliable.rs.

INVARIANT: wire compatibility with the C reference. C is normative — where the bytes
disagree, this port is wrong until the C side is proven wrong.

THE WIRE CHECK IS PINNED, AND THAT CUTS BOTH WAYS
`interop/regenerate.sh` clones C reliable at a PINNED commit (`RELIABLE_C_COMMIT`),
builds `interop/transcript.c` against it, and `TestWireCompatibility` byte-compares the
result to `testdata/c_transcript.txt.gz`.

- Pinning means an upstream C commit CANNOT redden this repo spuriously. That is the
  opposite of netcode.go, which reads C main unpinned and can go red with no change here.
- But a pin has no staleness signal. On 2026-07-26 it sat at 1.3.4 while C main was 21
  commits and one minor release ahead — and in that window the wire format was formally
  specified in STANDARD.md (+165 lines) and the header size bounds were corrected. The
  check was green throughout and would have stayed green even if the wire HAD moved.
  Verified by regenerating against C main: byte-identical, so the port was fine. That was
  luck confirmed, not a guarantee earned.
- **So: when C cuts a release, bump the pin and regenerate.** A green wire check only
  means "compatible with whatever this pin points at."

DECISIONS
- The golden transcript is committed (`testdata/c_transcript.txt.gz`) so the test runs
  without a C toolchain. Regenerating requires `cc` and network access to clone C.
- CI is lint (gofmt, vet, staticcheck, govulncheck), wire-compatibility, and `go test
  -race` across a matrix. The wire job regenerates from C rather than trusting the
  committed golden, so a tampered golden fails there even though `go test` passes.
<!-- HOT:END -->

# CLAUDE.md

Pure Go port of the C reliable library — packet fragmentation, reassembly and acks over
an unreliable transport. See the HOT block above before changing anything.
