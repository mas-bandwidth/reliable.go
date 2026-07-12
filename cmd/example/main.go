// This is a port of example.c from https://github.com/mas-bandwidth/reliable
//
// It connects two endpoints back to back with 90% simulated packet loss, and
// prints out acks as they arrive.
package main

import (
	"fmt"
	"math/rand"
	"os"

	reliable "github.com/mas-bandwidth/reliable.go"
)

type connection struct {
	endpoint *reliable.Endpoint
}

var client connection
var server connection

func transmitPacket(context any, id uint64, sequence uint16, packetData []byte) {
	// simulate 90% packet loss

	if rand.Intn(10) != 0 {
		return
	}

	// send the packet directly to the other endpoint (normally this would be done via sockets...)

	if context == &client {
		server.endpoint.ReceivePacket(packetData)
	} else {
		client.endpoint.ReceivePacket(packetData)
	}
}

func processPacket(context any, id uint64, sequence uint16, packetData []byte) bool {
	// read the packet here and process its contents, return false if the packet should not be acked

	return true
}

func main() {
	fmt.Printf("\nreliable example\n\n")

	time := 0.0

	// configure the endpoints

	config := reliable.DefaultConfig()

	config.MaxPacketSize = 32 * 1024               // maximum packet size that may be sent in bytes
	config.FragmentAbove = 1200                    // fragment and reassemble packets above this size
	config.MaxFragments = 32                       // maximum number of fragments per-packet
	config.FragmentSize = 1024                     // the size of each fragment sent
	config.TransmitPacketFunction = transmitPacket // set the callback function to transmit packets
	config.ProcessPacketFunction = processPacket   // set the callback function to process packets

	// create client connection

	config.Context = &client
	config.Name = "client"
	var err error
	client.endpoint, err = reliable.NewEndpoint(&config, time)
	if err != nil {
		fmt.Printf("error: could not create client endpoint: %v\n", err)
		os.Exit(1)
	}

	// create server connection

	config.Context = &server
	config.Name = "server"
	server.endpoint, err = reliable.NewEndpoint(&config, time)
	if err != nil {
		fmt.Printf("error: could not create server endpoint: %v\n", err)
		os.Exit(1)
	}

	// send packets and print out acks

	packet := make([]byte, 8)

	for i := 0; i < 1000; i++ {
		clientPacketSequence := client.endpoint.NextPacketSequence()

		client.endpoint.SendPacket(packet)
		server.endpoint.SendPacket(packet)

		client.endpoint.Update(time)
		server.endpoint.Update(time)

		fmt.Printf("%d: client sent packet %d\n", i, clientPacketSequence)

		for _, ack := range client.endpoint.Acks() {
			fmt.Printf(" --> server acked packet %d\n", ack)
		}

		client.endpoint.ClearAcks()
		server.endpoint.ClearAcks()

		time += 0.01
	}

	fmt.Printf("\nSuccess\n\n")
}
