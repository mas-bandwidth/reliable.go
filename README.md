[![CI](https://github.com/mas-bandwidth/reliable.go/actions/workflows/ci.yml/badge.svg)](https://github.com/mas-bandwidth/reliable.go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/mas-bandwidth/reliable.go.svg)](https://pkg.go.dev/github.com/mas-bandwidth/reliable.go)
[![Go Report Card](https://goreportcard.com/badge/github.com/mas-bandwidth/reliable.go)](https://goreportcard.com/report/github.com/mas-bandwidth/reliable.go)

# Introduction

**reliable.go** is a simple packet acknowledgement system for UDP-based protocols, written in Go.

It's useful in situations where you need to know which UDP packets you sent were received by the other side.

It's a faithful port of the C library [reliable](https://github.com/mas-bandwidth/reliable) (v1.3.4) to modern, idiomatic Go.

It has the following features:

1. Acknowledgement when packets are received
2. Packet fragmentation and reassembly
3. RTT, jitter and packet loss estimates
4. Duplicate packets are detected and dropped

The wire format is identical to the C library, so Go and C endpoints interoperate. This is enforced by tests: see [Wire compatibility](#wire-compatibility).

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
func transmitPacket(id uint64, sequence uint16, packetData []byte) {
    // send packet using your own udp socket
}
```

And a function to process received packets:

```go
func processPacket(id uint64, sequence uint16, packetData []byte) bool {
    // read the packet here and process its contents, return false if the packet should not be acked
    return true
}
```

To pass state to the callbacks, use a closure or a method value. The id is the `Config.ID` of the endpoint that fired the callback, so callbacks shared between endpoints can tell them apart — for example, one transmit function bound to one socket serving every client slot on a server.

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
3. The `void * context` parameter on the callbacks is gone — closures and method values are how state reaches callbacks in Go. The endpoint id remains.
4. `ReceivePacket` treats an empty packet as invalid instead of asserting, since network input is untrusted. `SendPacket` panics on an empty packet, which is a programmer error.
5. `reliable_init`/`reliable_term` are gone — there is no library state to initialize.
6. Stats are float64 instead of float.

Sending and receiving packets does not allocate (fragment reassembly allocates one buffer per fragmented packet, just like the C library). Run `go test -bench .` to check on your hardware.

# Wire compatibility

Binary compatibility with the C library is locked in by a golden transcript test that runs as part of `go test`, on every platform and every pull request:

1. [interop/transcript.c](interop/transcript.c) runs a deterministic scenario through the C library — 300 frames of bidirectional traffic with regular and fragmented packets, deterministic packet loss and duplication — and prints every transmitted packet as hex, every ack, and the endpoint counters.
2. The output, generated from the C library pinned at the commit in [interop/regenerate.sh](interop/regenerate.sh), is committed as `testdata/c_transcript.txt.gz`.
3. `TestWireCompatibility` ([wire_compat_test.go](wire_compat_test.go)) runs the identical scenario through the Go port and requires byte-for-byte identical output.
4. A CI job rebuilds the golden from the pinned C sources on every run, so the golden itself cannot drift from the C library.

If you change anything that touches the wire format, this test fails and points at the first diverging line.

# Author

The author of this library is [Glenn Fiedler](https://www.linkedin.com/in/glenn-fiedler-11b735302/).

Open source libraries by the same author include: [reliable](https://github.com/mas-bandwidth/reliable), [netcode](https://github.com/mas-bandwidth/netcode), [serialize](https://github.com/mas-bandwidth/serialize), and [yojimbo](https://github.com/mas-bandwidth/yojimbo)

If you find this software useful, [please consider sponsoring it](https://github.com/sponsors/mas-bandwidth). Thanks!

# License

[MBSL](LICENSE).

## Crediting

This library is licensed under the [Más Bandwidth Source License (MBSL)](LICENSE),
which is BSD 3-Clause plus one clause: products that incorporate it must include
this credit in their product credits, or in their documentation:

> **Más Bandwidth LLC**
> reliable.go by Glenn Fiedler

Free to use, source open, credit required. Fair credit keeps open source honest.
