package reliable

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cDifferentialInputs returns the deterministic inputs fed, in order, to both
// the C reference (interop/fixes_oracle.c) and this port: a zero length packet
// with a nonzero pointer, a one-byte packet {0}, a one-byte packet {1}, a
// packet of exactly max_packet_size + the header bytes (the largest a receiver
// still reads), one byte more than that (refused as too large), and a valid
// packet produced by a sender.
func cDifferentialInputs(t *testing.T) [][]byte {
	t.Helper()

	config := DefaultConfig()
	threshold := config.MaxPacketSize + MaxPacketHeaderBytes + FragmentHeaderBytes

	return [][]byte{
		{},                          // 1 zero length
		{0x00},                      // 2 one-byte {0}
		{0x01},                      // 3 one-byte {1}
		make([]byte, threshold),     // 4 exactly max_packet_size + 14 bytes of zeros
		make([]byte, threshold+1),   // 5 one byte more than that
		cDifferentialValidPacket(t), // 6 valid packet from a sender
	}
}

// cDifferentialValidPacket sends a packet from a default sender and returns the
// bytes it transmitted, so both sides receive the same valid packet.
func cDifferentialValidPacket(t *testing.T) []byte {
	t.Helper()

	config := DefaultConfig()
	config.Name = "sender"

	var validPacket []byte
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		if sequence == 0 && validPacket == nil {
			validPacket = append([]byte(nil), packetData...)
		}
	}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }

	endpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		t.Fatalf("could not create sender endpoint: %v", err)
	}

	endpoint.SendPacket([]byte("hello"))

	if validPacket == nil {
		t.Fatal("the sender did not transmit a packet")
	}
	return validPacket
}

// runGoCDifferential feeds the inputs through a fresh Endpoint (injected clock
// and in-memory transmit) and returns one line per input in the same form as
// the C oracle:
//
//	<input> <refused|accepted> <process callback ran 0|1> <the eleven counters>
func runGoCDifferential(inputs [][]byte) string {
	config := DefaultConfig()
	config.Name = "receiver"
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}

	callbackRan := false
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		callbackRan = true
		return true
	}

	endpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		panic(err)
	}

	var b strings.Builder
	for i, in := range inputs {
		callbackRan = false
		endpoint.ReceivePacket(in)

		ran := 0
		accepted := "refused"
		if callbackRan {
			ran = 1
			accepted = "accepted"
		}

		fmt.Fprintf(&b, "%d %s %d", i+1, accepted, ran)

		counters := endpoint.Counters()
		for j := range NumCounters {
			fmt.Fprintf(&b, " %d", counters[j])
		}
		b.WriteString("\n")
	}

	return b.String()
}

// buildCOracle compiles interop/fixes_oracle.c (which includes reliable.c) from
// the checkout named by RELIABLE_C_DIR and returns the path to the binary.
func buildCOracle(t *testing.T, cDir string) string {
	t.Helper()

	if _, err := os.Stat(filepath.Join(cDir, "reliable.c")); err != nil {
		t.Fatalf("RELIABLE_C_DIR %q has no reliable.c: %v", cDir, err)
	}

	dir, err := os.MkdirTemp("", "reliable-fixes-oracle")
	if err != nil {
		t.Fatalf("could not make temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	oracleBin := filepath.Join(dir, "oracle")
	src := filepath.Join("interop", "fixes_oracle.c")

	cmd := exec.Command("cc", "-O2", "-ffp-contract=off", "-I"+cDir, "-o", oracleBin, src, "-lm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("could not build the C oracle: %v\n%s", err, out)
	}
	return oracleBin
}

// runCOracleTranscript pipes the inputs to the oracle as length-prefixed
// packets and returns its stdout transcript.
func runCOracleTranscript(t *testing.T, oracleBin string, inputs [][]byte) string {
	t.Helper()

	var buf bytes.Buffer
	var lenBuf [4]byte
	for _, in := range inputs {
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(in)))
		buf.Write(lenBuf[:])
		buf.Write(in)
	}

	cmd := exec.Command(oracleBin)
	cmd.Stdin = bytes.NewReader(buf.Bytes())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("could not run the C oracle: %v", err)
	}
	return string(out)
}

// TestCDifferential feeds the same inputs through the C reference and this port
// and compares the transcripts byte for byte. This port already refuses and
// counts empty packets the way C does, so the transcript is expected to match
// at BASE. It is gated on RELIABLE_C_DIR so the unit gate stays hermetic:
//
//	RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .
func TestCDifferential(t *testing.T) {
	cDir := os.Getenv("RELIABLE_C_DIR")
	if cDir == "" {
		t.Skip("RELIABLE_C_DIR is not set; skipping the differential test against the C reference. Set it to a checkout of mas-bandwidth/reliable holding the fix (branch sprint/reliable-audit-fixes at or after e515523c) and run: RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .")
	}

	if _, err := exec.LookPath("cc"); err != nil {
		t.Fatalf("a C compiler is required for the C reference leg: %v", err)
	}

	oracleBin := buildCOracle(t, cDir)

	inputs := cDifferentialInputs(t)
	goTranscript := runGoCDifferential(inputs)
	cTranscript := runCOracleTranscript(t, oracleBin, inputs)

	if goTranscript == cTranscript {
		return
	}

	t.Logf("C reference transcript:\n%s", cTranscript)
	t.Logf("Go port transcript:\n%s", goTranscript)

	goLines := strings.Split(goTranscript, "\n")
	cLines := strings.Split(cTranscript, "\n")
	for i := 0; i < len(goLines) && i < len(cLines); i++ {
		if goLines[i] != cLines[i] {
			t.Fatalf("differential transcript diverges from the C reference at line %d:\n  c: %s\ngo: %s", i+1, cLines[i], goLines[i])
		}
	}
	t.Fatalf("differential transcript line count differs from the C reference: c %d lines, go %d lines", len(cLines), len(goLines))
}
