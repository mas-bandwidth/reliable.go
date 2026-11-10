package reliable

import (
	"testing"
)

// fuzzRecvConfig returns the config used by the receive-packet fuzz target and
// its seed validation test.
func fuzzRecvConfig() Config {
	config := DefaultConfig()
	config.Name = "fuzz"
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }
	return config
}

// fuzzReceiveSeeds captures real wire datagrams from SendPacket: one regular
// packet and every fragment of one fragmented packet.
func fuzzReceiveSeeds(t testing.TB) (regular []byte, fragments [][]byte) {
	t.Helper()

	config := fuzzRecvConfig()

	var datagrams [][]byte
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		datagrams = append(datagrams, append([]byte(nil), packetData...))
	}

	sender, err := NewEndpoint(&config, 100.0)
	if err != nil {
		t.Fatalf("could not create seed sender: %v", err)
	}

	sender.SendPacket(make([]byte, config.FragmentAbove))
	sender.SendPacket(make([]byte, config.FragmentAbove+1))

	if len(datagrams) < 2 {
		t.Fatalf("expected a regular datagram and at least one fragment, got %d", len(datagrams))
	}

	return datagrams[0], datagrams[1:]
}

// FuzzReceivePacket feeds arbitrary data into Endpoint.ReceivePacket. This is
// the Go-native equivalent of fuzz.c: the endpoint must never panic no matter
// what arrives from the network.
func FuzzReceivePacket(f *testing.F) {
	regular, fragments := fuzzReceiveSeeds(f)
	f.Add(regular)
	for _, fragment := range fragments {
		f.Add(fragment)
	}

	f.Add([]byte{0})
	f.Add([]byte{1})

	config := fuzzRecvConfig()

	f.Fuzz(func(t *testing.T, data []byte) {
		endpoint, err := NewEndpoint(&config, 100.0)
		if err != nil {
			t.Fatalf("could not create endpoint: %v", err)
		}

		endpoint.ReceivePacket(data)
		endpoint.Update(100.01)
		endpoint.ClearAcks()
	})
}

// TestFuzzReceivePacketSeeds checks that the datagrams seeded into
// FuzzReceivePacket are real wire packets accepted by a fresh endpoint.
func TestFuzzReceivePacketSeeds(t *testing.T) {
	regular, fragments := fuzzReceiveSeeds(t)

	config := fuzzRecvConfig()

	regularEndpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		t.Fatalf("could not create endpoint: %v", err)
	}
	regularEndpoint.ReceivePacket(regular)

	regularCounters := regularEndpoint.Counters()
	if regularCounters[CounterNumPacketsInvalid] != 0 {
		t.Fatalf("regular seed was rejected as invalid")
	}
	if regularCounters[CounterNumPacketsReceived] != 1 {
		t.Fatalf("expected 1 packet received from regular seed, got %d", regularCounters[CounterNumPacketsReceived])
	}

	fragmentEndpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		t.Fatalf("could not create endpoint: %v", err)
	}
	for _, fragment := range fragments {
		fragmentEndpoint.ReceivePacket(fragment)
	}

	fragmentCounters := fragmentEndpoint.Counters()
	if fragmentCounters[CounterNumFragmentsInvalid] != 0 {
		t.Fatalf("expected 0 invalid fragments, got %d", fragmentCounters[CounterNumFragmentsInvalid])
	}
	if fragmentCounters[CounterNumFragmentsReceived] != uint64(len(fragments)) {
		t.Fatalf("expected %d fragments received, got %d", len(fragments), fragmentCounters[CounterNumFragmentsReceived])
	}
	if fragmentCounters[CounterNumPacketsReceived] != 1 {
		t.Fatalf("expected reassembled packet to be received, got %d", fragmentCounters[CounterNumPacketsReceived])
	}
}
