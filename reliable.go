/*
Package reliable is a simple packet acknowledgement system for UDP-based protocols.

It's useful in situations where you need to know which UDP packets you sent
were received by the other side.

It has the following features:

 1. Acknowledgement when packets are received
 2. Packet fragmentation and reassembly
 3. RTT, jitter and packet loss estimates
 4. Duplicate packets are detected and dropped

This is a faithful port of the C library https://github.com/mas-bandwidth/reliable
to Go.

reliable is designed to operate with your own network socket library. Create an
Endpoint on each side of a connection, send packets with Endpoint.SendPacket,
and pass every packet received from your socket to Endpoint.ReceivePacket.

Endpoints are not thread safe: use one endpoint per goroutine, or protect each
endpoint with your own lock. The log level and printf handler are global to the
process.
*/
package reliable

import (
	"fmt"
	"sync/atomic"
)

// Version of the C reliable library this package is ported from.
const (
	VersionFull  = "1.3.4"
	VersionMajor = 1
	VersionMinor = 3
	VersionPatch = 4
)

// Endpoint counters. Index the array returned by Endpoint.Counters with these.
const (
	CounterNumPacketsSent = iota
	CounterNumPacketsReceived
	CounterNumPacketsAcked
	CounterNumPacketsStale
	CounterNumPacketsInvalid
	CounterNumPacketsTooLargeToSend
	CounterNumPacketsTooLargeToReceive
	CounterNumFragmentsSent
	CounterNumFragmentsReceived
	CounterNumFragmentsInvalid
	CounterNumPacketsDuplicate
	NumCounters
)

const (
	// MaxPacketHeaderBytes is the largest possible packet header written in front of a payload.
	MaxPacketHeaderBytes = 9

	// FragmentHeaderBytes is the size of the header written in front of each fragment.
	FragmentHeaderBytes = 5
)

// LogLevel controls how much log output the library generates.
type LogLevel int32

const (
	LogLevelNone LogLevel = iota
	LogLevelError
	LogLevelInfo
	LogLevelDebug
)

var logLevel atomic.Int32

// SetLogLevel sets the log level (process-wide). LogLevelNone by default.
func SetLogLevel(level LogLevel) {
	logLevel.Store(int32(level))
}

var printfFunction func(format string, args ...any) = func(format string, args ...any) {
	fmt.Printf(format, args...)
}

// SetPrintfFunction overrides where log output goes (process-wide). The default
// prints to standard output. Set this before creating any endpoints.
func SetPrintfFunction(function func(format string, args ...any)) {
	if function == nil {
		panic("reliable: printf function must not be nil")
	}
	printfFunction = function
}

func logPrintf(level LogLevel, format string, args ...any) {
	if int32(level) > logLevel.Load() {
		return
	}
	printfFunction(format, args...)
}

// debugLogging reports whether debug log output is enabled. Hot paths check
// this before calling logPrintf so that building the argument list costs
// nothing when logging is off.
func debugLogging() bool {
	return logLevel.Load() >= int32(LogLevelDebug)
}

// sequenceGreaterThan compares 16 bit sequence numbers, handling wrap around.
func sequenceGreaterThan(s1, s2 uint16) bool {
	return ((s1 > s2) && (s1-s2 <= 32768)) ||
		((s1 < s2) && (s2-s1 > 32768))
}

func sequenceLessThan(s1, s2 uint16) bool {
	return sequenceGreaterThan(s2, s1)
}
