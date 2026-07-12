// This is a port of soak.c from https://github.com/mas-bandwidth/reliable
//
// It exchanges randomly sized packets between two endpoints under 5% simulated
// packet loss, validating the contents of every packet that gets through, and
// runs until interrupted (or for the number of iterations given on the
// command line).
package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strconv"

	reliable "github.com/mas-bandwidth/reliable.go"
)

const maxPacketBytes = 16 * 1024

type testContext struct {
	client *reliable.Endpoint
	server *reliable.Endpoint
}

var globalTime = 100.0

var globalContext testContext

func (ctx *testContext) transmitPacket(id uint64, sequence uint16, packetData []byte) {

	if rand.Intn(101) < 5 {
		return
	}

	if id == 0 {
		ctx.server.ReceivePacket(packetData)
	} else if id == 1 {
		ctx.client.ReceivePacket(packetData)
	}
}

func generatePacketData(sequence uint16, packetData []byte) []byte {
	packetBytes := (int(sequence)*1023)%(maxPacketBytes-2) + 2
	packetData[0] = byte(sequence & 0xFF)
	packetData[1] = byte((sequence >> 8) & 0xFF)
	for i := 2; i < packetBytes; i++ {
		packetData[i] = byte((i + int(sequence)) % 256)
	}
	return packetData[:packetBytes]
}

func checkPacketData(packetData []byte) {
	packetBytes := len(packetData)
	sequence := uint16(packetData[0]) | uint16(packetData[1])<<8
	if packetBytes != (int(sequence)*1023)%(maxPacketBytes-2)+2 {
		fmt.Printf("check failed: packet %d has unexpected size %d\n", sequence, packetBytes)
		os.Exit(1)
	}
	for i := 2; i < packetBytes; i++ {
		if packetData[i] != byte((i+int(sequence))%256) {
			fmt.Printf("check failed: packet %d has corrupt data at index %d\n", sequence, i)
			os.Exit(1)
		}
	}
}

func processPacket(id uint64, sequence uint16, packetData []byte) bool {
	checkPacketData(packetData)
	return true
}

func soakInitialize(quiet bool) {
	fmt.Printf("initializing\n")

	if !quiet {
		reliable.SetLogLevel(reliable.LogLevelDebug)
	}

	clientConfig := reliable.DefaultConfig()
	serverConfig := reliable.DefaultConfig()

	clientConfig.FragmentAbove = 500
	serverConfig.FragmentAbove = 500

	clientConfig.Name = "client"
	clientConfig.ID = 0
	clientConfig.TransmitPacketFunction = globalContext.transmitPacket
	clientConfig.ProcessPacketFunction = processPacket

	serverConfig.Name = "server"
	serverConfig.ID = 1
	serverConfig.TransmitPacketFunction = globalContext.transmitPacket
	serverConfig.ProcessPacketFunction = processPacket

	var err error
	globalContext.client, err = reliable.NewEndpoint(&clientConfig, globalTime)
	if err != nil {
		fmt.Printf("error: could not create client endpoint: %v\n", err)
		os.Exit(1)
	}
	globalContext.server, err = reliable.NewEndpoint(&serverConfig, globalTime)
	if err != nil {
		fmt.Printf("error: could not create server endpoint: %v\n", err)
		os.Exit(1)
	}
}

func soakIteration(time float64) {
	packetData := make([]byte, maxPacketBytes)

	sequence := globalContext.client.NextPacketSequence()
	packet := generatePacketData(sequence, packetData)
	globalContext.client.SendPacket(packet)

	sequence = globalContext.server.NextPacketSequence()
	packet = generatePacketData(sequence, packetData)
	globalContext.server.SendPacket(packet)

	globalContext.client.Update(time)
	globalContext.server.Update(time)

	globalContext.client.ClearAcks()
	globalContext.server.ClearAcks()
}

func main() {
	numIterations := -1
	quiet := false

	for _, arg := range os.Args[1:] {
		if arg == "--quiet" {
			quiet = true
		} else if n, err := strconv.Atoi(arg); err == nil {
			numIterations = n
		}
	}

	soakInitialize(quiet)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	quit := func() bool {
		select {
		case <-interrupt:
			return true
		default:
			return false
		}
	}

	deltaTime := 0.1

	if numIterations > 0 {
		for i := 0; i < numIterations; i++ {
			if quit() {
				break
			}

			soakIteration(globalTime)

			globalTime += deltaTime
		}
	} else {
		for !quit() {
			soakIteration(globalTime)

			globalTime += deltaTime
		}
	}

	fmt.Printf("shutdown\n")
}
