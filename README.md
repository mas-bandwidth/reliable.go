[![CI](https://github.com/mas-bandwidth/reliable.go/actions/workflows/ci.yml/badge.svg)](https://github.com/mas-bandwidth/reliable.go/actions/workflows/ci.yml)

# Introduction

**reliable.go** is a simple packet acknowledgement system for UDP-based protocols, written in Go.

It's useful in situations where you need to know which UDP packets you sent were received by the other side.

It's a faithful port of the C library [reliable](https://github.com/mas-bandwidth/reliable) (v1.3.4) to modern, idiomatic Go.

It has the following features:

1. Acknowledgement when packets are received
2. Packet fragmentation and reassembly
3. RTT, jitter and packet loss estimates
4. Duplicate packets are detected and dropped

The wire format is identical to the C library, so Go and C endpoints interoperate.

# Usage

```
go get github.com/mas-bandwidth/reliable.go
```

reliable.go is designed to operate with your own network socket library.

First, create an endpoint on each side of the connection:

```go
import reliable "github.com/mas-bandwidth/reliable.go"

config := reliable.DefaultConfig()

config.MaxPacketSize = 32 * 1024
config.FragmentAbove = 1200
config.MaxFragments = 32
config.FragmentSize = 1024
config.TransmitPacketFunction = transmitPacket
config.ProcessPacketFunction = processPacket

endpoint, err := reliable.NewEndpoint(&config, time)
if err != nil {
    log.Fatalf("error: could not create endpoint: %v", err)
}
```

For example, in a client/server setup you would have one endpoint on each client, and n endpoints on the server, one for each client slot.

Next, create a function to transmit packets:

```go
func transmitPacket(context any, id uint64, sequence uint16, packetData []byte) {
    // send packet using your own udp socket
}
```

And a function to process received packets:

```go
func processPacket(context any, id uint64, sequence uint16, packetData []byte) bool {
    // read the packet here and process its contents, return false if the packet should not be acked
    return true
}
```

For each packet you receive from your udp socket, call this on the endpoint that should receive it:

```go
endpoint.ReceivePacket(packetData)
```

Now you can send packets through the endpoint:

```go
endpoint.SendPacket(packetData)
```

And get acks like this:

```go
for _, ack := range endpoint.Acks() {
    fmt.Printf("acked packet %d\n", ack)
}
```

Once you process all acks, clear them:

```go
endpoint.ClearAcks()
```

Before you send a packet, you can ask reliable what sequence number the sent packet will have:

```go
sequence := endpoint.NextPacketSequence()
```

This way you can map acked sequence numbers to the contents of packets you sent, for example, resending unacked messages until a packet that included that message was acked.

Make sure to update each endpoint once per-frame. This keeps track of network stats like latency, jitter, packet loss and bandwidth:

```go
endpoint.Update(time)
```

You can then grab stats from the endpoint:

```go
fmt.Printf("rtt = %.1fms | jitter = %.1fms | packet loss = %.1f%%\n",
    endpoint.RTTMin(),
    endpoint.JitterAvgVsMinRTT(),
    endpoint.PacketLoss())
```

See [cmd/example](cmd/example/main.go) for a complete program, and [cmd/soak](cmd/soak/main.go) for a soak test you can run with `go run ./cmd/soak --quiet 10000`.

# Caveats

reliable.go is a packet acknowledgement system, not a full messaging layer. Keep the following in mind:

1. Acks accumulate until you call `endpoint.ClearAcks`, so make sure you clear acks once you have processed them each frame. If the ack buffer fills up, additional acks are dropped and an error is logged.

2. Endpoints are not thread safe. Use one endpoint per-goroutine, or protect each endpoint with your own lock. The log level and printf handler are global to the process.

# Differences from the C library

The port keeps the structure, behavior and wire format of the C library, with the following adaptations to Go:

1. `reliable_endpoint_create` is `reliable.NewEndpoint` and returns an error for invalid configs instead of asserting.
2. There are no custom allocator hooks — the Go garbage collector manages memory, and `reliable_endpoint_free_packet`/`reliable_endpoint_destroy` have no equivalent.
3. `ReceivePacket` treats an empty packet as invalid instead of asserting, since network input is untrusted. `SendPacket` panics on an empty packet, which is a programmer error.
4. `reliable_init`/`reliable_term` are gone — there is no library state to initialize.
5. Stats are float64 instead of float.

# Author

The author of this library is [Glenn Fiedler](https://www.linkedin.com/in/glenn-fiedler-11b735302/).

Open source libraries by the same author include: [reliable](https://github.com/mas-bandwidth/reliable), [netcode](https://github.com/mas-bandwidth/netcode), [serialize](https://github.com/mas-bandwidth/serialize), and [yojimbo](https://github.com/mas-bandwidth/yojimbo)

If you find this software useful, [please consider sponsoring it](https://github.com/sponsors/mas-bandwidth). Thanks!

# License

[BSD 3-Clause license](https://opensource.org/licenses/BSD-3-Clause).
