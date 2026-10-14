package reliable

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// cConfigSpecs returns the configs fed to both the C reference and this port:
// each of the three sequence buffers at 0,1,2,3,100,255,256,257,16383,16384,
// 16385,32768,65535,65536,65537 with every other field at its default, plus the
// three header-plus-packet edge configs. Each spec is a comma-separated list of
// `field=value` settings over the default config.
func cConfigSpecs() []string {
	specs := []string{}

	values := []int{0, 1, 2, 3, 100, 255, 256, 257, 16383, 16384, 16385, 32768, 65535, 65536, 65537}
	for _, field := range []string{
		"sent_packets_buffer_size",
		"received_packets_buffer_size",
		"fragment_reassembly_buffer_size",
	} {
		for _, value := range values {
			specs = append(specs, fmt.Sprintf("%s=%d", field, value))
		}
	}

	maxInt := int(^uint32(0) >> 1)
	edge := maxInt - MaxPacketHeaderBytes - FragmentHeaderBytes
	specs = append(specs,
		fmt.Sprintf("packet_header_size=28,max_packet_size=%d", edge-28),
		fmt.Sprintf("packet_header_size=28,max_packet_size=%d", edge-27),
		fmt.Sprintf("packet_header_size=9,max_packet_size=%d", maxInt-MaxPacketHeaderBytes-FragmentHeaderBytes-9),
	)

	return specs
}

// goConfigSpecDescription renders a spec the way the C oracle does: every ','
// and '=' becomes a space, so a config reads as a list of field value pairs.
func goConfigSpecDescription(spec string) string {
	return strings.NewReplacer(",", " ", "=", " ").Replace(spec)
}

// goConfigFromSpec builds a default port config with the spec's settings
// applied.
func goConfigFromSpec(t *testing.T, spec string) Config {
	t.Helper()

	config := DefaultConfig()
	config.Name = "under-test"
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }

	for _, setting := range strings.Split(spec, ",") {
		name, valueText, ok := strings.Cut(setting, "=")
		if !ok {
			t.Fatalf("malformed config setting %q", setting)
		}
		value, err := strconv.Atoi(valueText)
		if err != nil {
			t.Fatalf("malformed config value %q: %v", valueText, err)
		}

		switch name {
		case "sent_packets_buffer_size":
			config.SentPacketsBufferSize = value
		case "received_packets_buffer_size":
			config.ReceivedPacketsBufferSize = value
		case "fragment_reassembly_buffer_size":
			config.FragmentReassemblyBufferSize = value
		case "packet_header_size":
			config.PacketHeaderSize = value
		case "max_packet_size":
			config.MaxPacketSize = value
		default:
			t.Fatalf("unknown config field %q", name)
		}
	}

	return config
}

// goConfigCreated reports whether this port creates an endpoint for the config.
// A panic from an out-of-range allocation is treated the way the C reference
// treats a failed endpoint create: the config is not usable.
func goConfigCreated(config Config) (created bool) {
	defer func() {
		if recover() != nil {
			created = false
		}
	}()

	endpoint, err := NewEndpoint(&config, 100.0)
	return err == nil && endpoint != nil
}

// goConfigAck sends one one-byte packet from an endpoint built from the config
// to a default peer and reports the sent bytes the peer acknowledged, or zero
// when no ack came back.
func goConfigAck(config Config) int {
	var sender, receiver *Endpoint

	senderConfig := config
	senderConfig.ID = 0
	senderConfig.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		if receiver != nil {
			receiver.ReceivePacket(packetData)
		}
	}

	receiverConfig := DefaultConfig()
	receiverConfig.Name = "peer"
	receiverConfig.ID = 1
	receiverConfig.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		if sender != nil {
			sender.ReceivePacket(packetData)
		}
	}
	receiverConfig.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }

	acked := false
	func() {
		defer func() {
			if recover() != nil {
				acked = false
			}
		}()

		var err error
		sender, err = NewEndpoint(&senderConfig, 100.0)
		if err != nil {
			return
		}
		receiver, err = NewEndpoint(&receiverConfig, 100.0)
		if err != nil {
			return
		}

		sender.SendPacket([]byte{0x00})
		receiver.SendPacket([]byte{0x00})

		acked = sender.Counters()[CounterNumPacketsAcked] > 0
	}()

	if acked {
		return config.PacketHeaderSize + 1
	}
	return 0
}

// runGoConfigTranscript feeds the config specs through the port and returns one
// or two lines per spec in the same form as the C oracle.
func runGoConfigTranscript(t *testing.T, specs []string) string {
	t.Helper()

	var b strings.Builder
	for _, spec := range specs {
		description := goConfigSpecDescription(spec)
		config := goConfigFromSpec(t, spec)

		if !goConfigCreated(config) {
			fmt.Fprintf(&b, "%s refused\n", description)
			continue
		}

		fmt.Fprintf(&b, "%s created\n", description)
		fmt.Fprintf(&b, "ack %d\n", goConfigAck(config))
	}
	return b.String()
}

// runCOracleConfigTranscript runs the C oracle in config mode with the same
// specs and returns its stdout transcript.
func runCOracleConfigTranscript(t *testing.T, oracleBin string, specs []string) string {
	t.Helper()

	cmd := exec.Command(oracleBin, append([]string{"config"}, specs...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("could not run the C oracle in config mode: %v", err)
	}
	return string(out)
}

// TestCDifferentialConfig feeds the same configs through the C reference and
// this port and compares the accept/refuse verdict per config (and the ack of a
// one-byte send for each created config) byte for byte. Gated on RELIABLE_C_DIR
// so the unit gate stays hermetic:
//
//	RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .
func TestCDifferentialConfig(t *testing.T) {
	cDir := os.Getenv("RELIABLE_C_DIR")
	if cDir == "" {
		t.Skip("RELIABLE_C_DIR is not set; skipping the config differential test against the C reference. Set it to a checkout of mas-bandwidth/reliable holding the config fixes and run: RELIABLE_C_DIR=<checkout> go test -count=1 -run TestCDifferential .")
	}

	if _, err := exec.LookPath("cc"); err != nil {
		t.Fatalf("a C compiler is required for the C reference leg: %v", err)
	}

	oracleBin := buildCOracle(t, cDir)

	specs := cConfigSpecs()
	goTranscript := runGoConfigTranscript(t, specs)
	cTranscript := runCOracleConfigTranscript(t, oracleBin, specs)

	if goTranscript == cTranscript {
		return
	}

	t.Logf("C reference transcript:\n%s", cTranscript)
	t.Logf("Go port transcript:\n%s", goTranscript)

	goLines := strings.Split(goTranscript, "\n")
	cLines := strings.Split(cTranscript, "\n")
	for i := 0; i < len(goLines) && i < len(cLines); i++ {
		if goLines[i] != cLines[i] {
			t.Fatalf("config differential diverges from the C reference at line %d:\n  c: %s\ngo: %s", i+1, cLines[i], goLines[i])
		}
	}
	t.Fatalf("config differential line count differs from the C reference: c %d lines, go %d lines", len(cLines), len(goLines))
}
