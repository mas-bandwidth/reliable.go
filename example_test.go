package reliable_test

import (
	"fmt"

	reliable "github.com/mas-bandwidth/reliable.go"
)

// Example connects two endpoints back to back and exchanges a packet in each
// direction, so the client learns that its packet was received.
func Example() {
	var client, server *reliable.Endpoint

	// in a real application the transmit callback sends the packet over your
	// own udp socket, and you call ReceivePacket for each packet you receive

	clientConfig := reliable.DefaultConfig()
	clientConfig.Name = "client"
	clientConfig.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		server.ReceivePacket(packetData)
	}
	clientConfig.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		return true
	}

	serverConfig := reliable.DefaultConfig()
	serverConfig.Name = "server"
	serverConfig.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		client.ReceivePacket(packetData)
	}
	serverConfig.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		fmt.Printf("server received packet %d: %q\n", sequence, packetData)
		return true
	}

	time := 0.0

	var err error
	client, err = reliable.NewEndpoint(&clientConfig, time)
	if err != nil {
		panic(err)
	}
	server, err = reliable.NewEndpoint(&serverConfig, time)
	if err != nil {
		panic(err)
	}

	// the client sends a packet to the server, then the server sends one back
	// whose header acks it

	client.SendPacket([]byte("hello"))
	server.SendPacket([]byte("world"))

	for _, ack := range client.Acks() {
		fmt.Printf("server acked packet %d\n", ack)
	}
	client.ClearAcks()

	// Output:
	// server received packet 0: "hello"
	// server acked packet 0
}
