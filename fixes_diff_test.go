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

// cDifferentialCounterNames names the eleven counters in reliable.h order. The
// C oracle (interop/fixes_oracle.c) prints the same names; keep them in sync.
var cDifferentialCounterNames = [...]string{
	"num_packets_sent",
	"num_packets_received",
	"num_packets_acked",
	"num_packets_stale",
	"num_packets_invalid",
	"num_packets_too_large_to_send",
	"num_packets_too_large_to_receive",
	"num_fragments_sent",
	"num_fragments_received",
	"num_fragments_invalid",
	"num_packets_duplicate",
}

// buildCDifferentialInputs returns the deterministic inputs fed to both sides:
// valid traffic, a duplicate, an acking packet, a non-completing far-ahead
// fragment, and one hostile input per checklist item 5 (zero-length, oversize,
// truncated header, mismatched fragment count).
func buildCDifferentialInputs() [][]byte {
	var inputs [][]byte

	header := func(seq uint16, ack uint16, ackBits uint32, payload []byte) []byte {
		var ph [MaxPacketHeaderBytes]byte
		phb := writePacketHeader(ph[:], seq, ack, ackBits)
		p := make([]byte, phb+len(payload))
		copy(p, ph[:phb])
		copy(p[phb:], payload)
		return p
	}

	// valid regular packets (no acks yet: ack 0xFFFF, ack_bits 0)
	inputs = append(inputs, header(0, 0xFFFF, 0, []byte("hello"))) // 1
	inputs = append(inputs, header(0, 0xFFFF, 0, []byte("hello"))) // 2 duplicate
	inputs = append(inputs, header(1, 0xFFFF, 0, []byte("world"))) // 3
	// acking packet: acks sent sequences 0 and 1 (ack_bits 0b11)
	inputs = append(inputs, header(2, 1, 0x3, []byte("ack"))) // 4

	// far-ahead fragment 0 of a 2-fragment packet at sequence 300; never
	// completes, so it must not move the received window
	inputs = append(inputs, buildCDifferentialFragment0(300, 2)) // 5

	// a genuine packet the fragment must not shadow
	inputs = append(inputs, header(3, 0xFFFF, 0, []byte("data"))) // 6

	// hostile inputs (checklist item 5)
	inputs = append(inputs, []byte{})                                                         // 7 zero-length
	inputs = append(inputs, make([]byte, 16*1024+MaxPacketHeaderBytes+FragmentHeaderBytes+1)) // 8 oversize
	inputs = append(inputs, []byte{0x00})                                                     // 9 truncated header
	inputs = append(inputs, []byte{0x01, 0x00, 0x00, 0x00, 0xFF})                             // 10 mismatched fragment count (256 > 16)

	return inputs
}

// buildCDifferentialFragment0 builds a valid, non-completing fragment 0 of a
// numFragments-fragment packet at the given sequence.
func buildCDifferentialFragment0(seq uint16, numFragments int) []byte {
	cfg := DefaultConfig()

	var ph [MaxPacketHeaderBytes]byte
	phb := writePacketHeader(ph[:], seq, 0xFFFF, 0)

	frag := make([]byte, FragmentHeaderBytes+phb+cfg.FragmentSize)
	frag[0] = 1
	frag[1] = byte(seq)
	frag[2] = byte(seq >> 8)
	frag[3] = 0
	frag[4] = byte(numFragments - 1)
	copy(frag[FragmentHeaderBytes:], ph[:phb])
	for i := FragmentHeaderBytes + phb; i < len(frag); i++ {
		frag[i] = byte(i)
	}
	return frag
}

// runGoCDifferential feeds the inputs through a fresh Endpoint (injected clock
// and in-memory transmit) and returns the transcript, one line per input in
// the same form as the C oracle.
func runGoCDifferential(inputs [][]byte) string {
	cfg := DefaultConfig()
	cfg.Name = "oracle"
	cfg.TransmitPacketFunction = func(id uint64, seq uint16, data []byte) {}

	delivered := false
	var deliveredSeq uint16
	var deliveredBytes int
	cfg.ProcessPacketFunction = func(id uint64, seq uint16, data []byte) bool {
		if !delivered {
			delivered = true
			deliveredSeq = seq
			deliveredBytes = len(data)
		}
		return true
	}

	ep, err := NewEndpoint(&cfg, 100.0)
	if err != nil {
		panic(err)
	}

	// send preamble, identical to the C oracle
	ep.SendPacket([]byte("abc"))
	ep.SendPacket([]byte("def"))

	var b strings.Builder
	previous := ep.Counters()

	for i, in := range inputs {
		delivered = false
		ep.ReceivePacket(in)
		current := ep.Counters()

		counter := "none"
		for j := range NumCounters {
			if current[j] != previous[j] {
				counter = cDifferentialCounterNames[j]
				break
			}
		}

		accepted := "refused"
		if delivered {
			accepted = "accepted"
		}

		fmt.Fprintf(&b, "%d %s %s ", i+1, accepted, counter)
		if delivered {
			fmt.Fprintf(&b, "%d:%d", deliveredSeq, deliveredBytes)
		} else {
			b.WriteString("-")
		}
		for j := range NumCounters {
			fmt.Fprintf(&b, " %d", current[j])
		}
		b.WriteString("\n")

		previous = current
	}

	return b.String()
}

// buildCOracle compiles interop/fixes_oracle.c against the checkout named by
// RELIABLE_C_DIR and returns the path to the binary.
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

	cmd := exec.Command("cc", "-O2", "-ffp-contract=off", "-I"+cDir, "-o", oracleBin, src, filepath.Join(cDir, "reliable.c"), "-lm")
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

// TestCDifferential feeds the same inputs through the C reference and this
// port and compares the transcripts byte for byte. It is gated on
// RELIABLE_C_DIR so the unit gate stays hermetic:
//
//	RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .
func TestCDifferential(t *testing.T) {
	cDir := os.Getenv("RELIABLE_C_DIR")
	if cDir == "" {
		t.Skip("RELIABLE_C_DIR is not set; skipping the differential test against the C reference. Set it to a checkout of mas-bandwidth/reliable holding the fix (branch sprint/reliable-audit-fixes at or after 7862477e) and run: RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .")
	}

	if _, err := exec.LookPath("cc"); err != nil {
		t.Fatalf("a C compiler is required for the C reference leg: %v", err)
	}

	oracleBin := buildCOracle(t, cDir)

	inputs := buildCDifferentialInputs()
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
