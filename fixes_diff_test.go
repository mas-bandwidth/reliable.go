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

// cDifferentialScenario is one fragment exchange fed to both sides: optional
// preamble packets (used to advance the received window), the fragment(s) under
// test, and the genuine packet(s) that follow.
type cDifferentialScenario struct {
	label            string
	fragmentSequence uint16
	preamble         [][]byte
	fragment         [][]byte
	genuine          [][]byte
}

// cDifferentialHeader builds a regular packet at the given sequence with no
// acks, the same bytes on both sides.
func cDifferentialHeader(sequence uint16, payload []byte) []byte {
	var packetHeader [MaxPacketHeaderBytes]byte
	packetHeaderBytes := writePacketHeader(packetHeader[:], sequence, 0xFFFF, 0)
	packet := make([]byte, packetHeaderBytes+len(payload))
	copy(packet, packetHeader[:packetHeaderBytes])
	copy(packet[packetHeaderBytes:], payload)
	return packet
}

// cDifferentialLoneLastFragment builds a lone last fragment (id 1 of 2) of a
// packet at the given sequence, with payloadBytes payload bytes.
func cDifferentialLoneLastFragment(sequence uint16, payloadBytes int) []byte {
	fragment := []byte{1, byte(sequence), byte(sequence >> 8), 1, 1}
	for i := 0; i < payloadBytes; i++ {
		fragment = append(fragment, byte(i+1))
	}
	return fragment
}

// cDifferentialInOrderFragments captures the fragments the port emits for a
// 1.5 x fragment size packet at sequence 0. Feeding these back in order is the
// in-order fragmented delivery pin (case C).
func cDifferentialInOrderFragments() [][]byte {
	config := DefaultConfig()
	config.Name = "sender"
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }

	var fragments [][]byte
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		fragments = append(fragments, append([]byte(nil), packetData...))
	}

	endpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		panic(err)
	}

	packet := make([]byte, config.FragmentSize+config.FragmentSize/2)
	endpoint.SendPacket(packet)
	return fragments
}

// buildCDifferentialScenarios returns the deterministic inputs fed to both
// sides: C's cases A, B and C, then every fragment sequence in the sweep fed to
// a cold receiver and to a receiver already advanced to 1000, each as a lone
// last fragment with zero and with one payload byte.
func buildCDifferentialScenarios() []cDifferentialScenario {
	var scenarios []cDifferentialScenario

	scenarios = append(scenarios, cDifferentialScenario{
		label:            "A",
		fragmentSequence: 512,
		fragment:         [][]byte{cDifferentialLoneLastFragment(512, 0)},
		genuine:          [][]byte{cDifferentialHeader(0, []byte("ok"))},
	})
	scenarios = append(scenarios, cDifferentialScenario{
		label:            "B",
		fragmentSequence: 32512,
		fragment:         [][]byte{cDifferentialLoneLastFragment(32512, 0)},
		genuine:          [][]byte{cDifferentialHeader(0, []byte("ok"))},
	})

	inOrderFragments := cDifferentialInOrderFragments()
	scenarios = append(scenarios, cDifferentialScenario{
		label:            "C",
		fragmentSequence: 0,
		fragment:         inOrderFragments[:1],
		genuine:          inOrderFragments[1:],
	})

	sequences := []uint16{0, 1, 255, 256, 511, 512, 513, 32511, 32512, 32513, 32768, 65280, 65535}
	for _, sequence := range sequences {
		for _, payloadBytes := range []int{0, 1} {
			for _, advanced := range []bool{false, true} {
				scenario := cDifferentialScenario{
					label:            fmt.Sprintf("sweep-%d-%d", sequence, payloadBytes),
					fragmentSequence: sequence,
					fragment:         [][]byte{cDifferentialLoneLastFragment(sequence, payloadBytes)},
					genuine:          [][]byte{cDifferentialHeader(0, []byte("ok"))},
				}
				if advanced {
					scenario.label += "-adv1000"
					scenario.preamble = [][]byte{cDifferentialHeader(1000, []byte("adv"))}
				}
				scenarios = append(scenarios, scenario)
			}
		}
	}

	return scenarios
}

// runGoCDifferentialScenario feeds one scenario through a fresh Endpoint
// (injected clock, in-memory transmit) and returns its transcript line.
func runGoCDifferentialScenario(scenario cDifferentialScenario) string {
	config := DefaultConfig()
	config.Name = "oracle"
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}

	delivered := false
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		delivered = true
		return true
	}

	endpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		panic(err)
	}

	for _, packet := range scenario.preamble {
		delivered = false
		endpoint.ReceivePacket(packet)
	}

	fragmentsInvalidBefore := endpoint.Counters()[CounterNumFragmentsInvalid]
	for _, packet := range scenario.fragment {
		endpoint.ReceivePacket(packet)
	}
	fragmentsInvalidAfter := endpoint.Counters()[CounterNumFragmentsInvalid]

	delivered = false
	for _, packet := range scenario.genuine {
		endpoint.ReceivePacket(packet)
	}

	disposition := "accepted"
	if fragmentsInvalidAfter != fragmentsInvalidBefore {
		disposition = "refused"
	}

	deliveredFlag := 0
	if delivered {
		deliveredFlag = 1
	}

	counters := endpoint.Counters()
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d %s %d", scenario.label, scenario.fragmentSequence, disposition, deliveredFlag)
	for _, counter := range counters {
		fmt.Fprintf(&b, " %d", counter)
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

	cmd := exec.Command("cc", "-O2", "-ffp-contract=off", "-I"+cDir, "-o", oracleBin, src, "-lm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("could not build the C oracle: %v\n%s", err, out)
	}
	return oracleBin
}

// runCOracleScenario runs the C oracle for one scenario, piping the identical
// packet bytes as length-prefixed records on stdin, and returns its line.
func runCOracleScenario(t *testing.T, oracleBin string, scenario cDifferentialScenario) string {
	t.Helper()

	var buf bytes.Buffer
	var length [4]byte
	writePacket := func(packet []byte) {
		binary.LittleEndian.PutUint32(length[:], uint32(len(packet)))
		buf.Write(length[:])
		buf.Write(packet)
	}
	for _, packet := range scenario.preamble {
		writePacket(packet)
	}
	for _, packet := range scenario.fragment {
		writePacket(packet)
	}
	for _, packet := range scenario.genuine {
		writePacket(packet)
	}

	cmd := exec.Command(oracleBin,
		scenario.label,
		fmt.Sprintf("%d", scenario.fragmentSequence),
		fmt.Sprintf("%d", len(scenario.preamble)),
		fmt.Sprintf("%d", len(scenario.fragment)),
	)
	cmd.Stdin = bytes.NewReader(buf.Bytes())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("could not run the C oracle for %s: %v\n%s", scenario.label, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// TestCDifferential feeds the same inputs through the C reference and this port
// and compares the transcript byte for byte. It is gated on RELIABLE_C_DIR so
// the unit gate stays hermetic:
//
//	RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .
func TestCDifferential(t *testing.T) {
	cDir := os.Getenv("RELIABLE_C_DIR")
	if cDir == "" {
		t.Skip("RELIABLE_C_DIR is not set; skipping the differential test against the C reference. Set it to a checkout of mas-bandwidth/reliable holding the fix (branch sprint/reliable-audit-fixes at or after dfd5541c) and run: RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .")
	}

	if _, err := exec.LookPath("cc"); err != nil {
		t.Fatalf("a C compiler is required for the C reference leg: %v", err)
	}

	oracleBin := buildCOracle(t, cDir)

	scenarios := buildCDifferentialScenarios()

	firstDifference := -1
	for i, scenario := range scenarios {
		goLine := runGoCDifferentialScenario(scenario)
		cLine := runCOracleScenario(t, oracleBin, scenario)
		if goLine != cLine {
			firstDifference = i
			t.Logf("first differing line %d:\n  c: %s\ngo: %s", i+1, cLine, goLine)
			break
		}
	}

	if firstDifference >= 0 {
		t.Fatalf("differential transcript diverges from the C reference at line %d", firstDifference+1)
	}
}
