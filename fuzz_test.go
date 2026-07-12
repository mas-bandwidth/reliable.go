package reliable

import (
	"testing"
)

// FuzzReceivePacket feeds arbitrary data into Endpoint.ReceivePacket. This is
// the Go-native equivalent of fuzz.c: the endpoint must never panic no matter
// what arrives from the network.
func FuzzReceivePacket(f *testing.F) {
	// seed with a valid regular packet and a valid fragment

	{
		var packetData [MaxPacketHeaderBytes + 8]byte
		headerBytes := writePacketHeader(packetData[:], 0, 0xFFFF, 0)
		f.Add(packetData[:headerBytes+8])
	}

	f.Add([]byte{1, 0, 0, 0, 1, 0xDE, 0xAD, 0xBE, 0xEF})
	f.Add([]byte{0})
	f.Add([]byte{1})

	config := DefaultConfig()
	config.Name = "fuzz"
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }

	endpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		f.Fatalf("could not create endpoint: %v", err)
	}

	time := 100.0

	f.Fuzz(func(t *testing.T, data []byte) {
		endpoint.ReceivePacket(data)
		time += 0.01
		endpoint.Update(time)
		endpoint.ClearAcks()
	})
}
