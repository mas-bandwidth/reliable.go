package reliable

import (
	"testing"
)

func newBenchEndpoint(b *testing.B, transmit TransmitPacketFunction) *Endpoint {
	b.Helper()
	config := DefaultConfig()
	config.Name = "bench"
	config.TransmitPacketFunction = transmit
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }
	endpoint, err := NewEndpoint(&config, 100.0)
	if err != nil {
		b.Fatalf("could not create endpoint: %v", err)
	}
	return endpoint
}

func BenchmarkSendPacket(b *testing.B) {
	endpoint := newBenchEndpoint(b, func(id uint64, sequence uint16, packetData []byte) {})

	packet := make([]byte, 8)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		endpoint.SendPacket(packet)
	}
}

func BenchmarkSendPacketFragmented(b *testing.B) {
	endpoint := newBenchEndpoint(b, func(id uint64, sequence uint16, packetData []byte) {})

	packet := make([]byte, 4096) // 4 fragments with the default config

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		endpoint.SendPacket(packet)
	}
}

func BenchmarkSendReceive(b *testing.B) {
	var receiver *Endpoint

	sender := newBenchEndpoint(b, func(id uint64, sequence uint16, packetData []byte) {
		receiver.ReceivePacket(packetData)
	})
	receiver = newBenchEndpoint(b, func(id uint64, sequence uint16, packetData []byte) {})

	packet := make([]byte, 256)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sender.SendPacket(packet)
	}
}
