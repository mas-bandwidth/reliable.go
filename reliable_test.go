package reliable

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// The tests in this file are ported from the test suite in reliable.c.

const testSequenceBufferSize = 256

type testSequenceData struct {
	sequence uint16
}

func TestSequenceBuffer(t *testing.T) {
	sequenceBuffer := newSequenceBuffer[testSequenceData](testSequenceBufferSize)

	if sequenceBuffer.sequence != 0 {
		t.Fatalf("expected sequence 0, got %d", sequenceBuffer.sequence)
	}
	if sequenceBuffer.numEntries != testSequenceBufferSize {
		t.Fatalf("expected %d entries, got %d", testSequenceBufferSize, sequenceBuffer.numEntries)
	}

	for i := 0; i < testSequenceBufferSize; i++ {
		if sequenceBuffer.find(uint16(i)) != nil {
			t.Fatalf("expected no entry for sequence %d", i)
		}
	}

	for i := 0; i <= testSequenceBufferSize*4; i++ {
		entry := sequenceBuffer.insert(uint16(i))
		if entry == nil {
			t.Fatalf("insert failed for sequence %d", i)
		}
		entry.sequence = uint16(i)
		if int(sequenceBuffer.sequence) != i+1 {
			t.Fatalf("expected buffer sequence %d, got %d", i+1, sequenceBuffer.sequence)
		}
	}

	for i := 0; i <= testSequenceBufferSize; i++ {
		entry := sequenceBuffer.insert(uint16(i))
		if entry != nil {
			t.Fatalf("expected insert to fail for stale sequence %d", i)
		}
	}

	index := testSequenceBufferSize * 4
	for i := 0; i < testSequenceBufferSize; i++ {
		entry := sequenceBuffer.find(uint16(index))
		if entry == nil {
			t.Fatalf("expected entry for sequence %d", index)
		}
		if entry.sequence != uint16(index) {
			t.Fatalf("expected entry sequence %d, got %d", index, entry.sequence)
		}
		index--
	}

	sequenceBuffer.reset()

	if sequenceBuffer.sequence != 0 {
		t.Fatalf("expected sequence 0 after reset, got %d", sequenceBuffer.sequence)
	}
	if sequenceBuffer.numEntries != testSequenceBufferSize {
		t.Fatalf("expected %d entries after reset, got %d", testSequenceBufferSize, sequenceBuffer.numEntries)
	}

	for i := 0; i < testSequenceBufferSize; i++ {
		if sequenceBuffer.find(uint16(i)) != nil {
			t.Fatalf("expected no entry for sequence %d after reset", i)
		}
	}
}

func TestGenerateAckBits(t *testing.T) {
	sequenceBuffer := newSequenceBuffer[testSequenceData](testSequenceBufferSize)

	ack, ackBits := sequenceBuffer.generateAckBits()
	if ack != 0xFFFF {
		t.Fatalf("expected ack 0xFFFF, got %#x", ack)
	}
	if ackBits != 0 {
		t.Fatalf("expected ack bits 0, got %#x", ackBits)
	}

	for i := 0; i <= testSequenceBufferSize; i++ {
		sequenceBuffer.insert(uint16(i))
	}

	ack, ackBits = sequenceBuffer.generateAckBits()
	if ack != testSequenceBufferSize {
		t.Fatalf("expected ack %d, got %d", testSequenceBufferSize, ack)
	}
	if ackBits != 0xFFFFFFFF {
		t.Fatalf("expected ack bits 0xFFFFFFFF, got %#x", ackBits)
	}

	sequenceBuffer.reset()

	inputAcks := []uint16{1, 5, 9, 11}
	for _, inputAck := range inputAcks {
		sequenceBuffer.insert(inputAck)
	}

	ack, ackBits = sequenceBuffer.generateAckBits()

	if ack != 11 {
		t.Fatalf("expected ack 11, got %d", ack)
	}
	expectedAckBits := uint32(1 | (1 << (11 - 9)) | (1 << (11 - 5)) | (1 << (11 - 1)))
	if ackBits != expectedAckBits {
		t.Fatalf("expected ack bits %#x, got %#x", expectedAckBits, ackBits)
	}
}

func TestPacketHeader(t *testing.T) {
	var packetData [MaxPacketHeaderBytes]byte

	checkHeader := func(writeSequence, writeAck uint16, writeAckBits uint32, expectedBytes int) {
		t.Helper()

		bytesWritten := writePacketHeader(packetData[:], writeSequence, writeAck, writeAckBits)

		if bytesWritten != expectedBytes {
			t.Fatalf("expected %d bytes written, got %d", expectedBytes, bytesWritten)
		}

		readSequence, readAck, readAckBits, bytesRead := readPacketHeader("test_packet_header", packetData[:bytesWritten])

		if bytesRead != bytesWritten {
			t.Fatalf("expected %d bytes read, got %d", bytesWritten, bytesRead)
		}
		if readSequence != writeSequence {
			t.Fatalf("expected sequence %d, got %d", writeSequence, readSequence)
		}
		if readAck != writeAck {
			t.Fatalf("expected ack %d, got %d", writeAck, readAck)
		}
		if readAckBits != writeAckBits {
			t.Fatalf("expected ack bits %#x, got %#x", writeAckBits, readAckBits)
		}
	}

	// worst case, sequence and ack are far apart, no packets acked.

	checkHeader(10000, 100, 0, MaxPacketHeaderBytes)

	// boundary: sequence difference of exactly 255 encodes the ack as a one
	// byte offset, 256 as a full two byte ack

	checkHeader(355, 100, 0, 1+2+1+4)
	checkHeader(356, 100, 0, 1+2+2+4)

	// rare case. sequence and ack are far apart, significant # of acks are missing

	checkHeader(10000, 100, 0xFEFEFFFE, 1+2+2+3)

	// common case under packet loss. sequence and ack are close together, some acks are missing

	checkHeader(200, 100, 0xFFFEFFFF, 1+2+1+1)

	// ideal case. no packet loss.

	checkHeader(200, 100, 0xFFFFFFFF, 1+2+1)
}

type testContext struct {
	drop         bool
	allowPackets int
	sender       *Endpoint
	receiver     *Endpoint
}

func newTestContext() *testContext {
	return &testContext{allowPackets: -1}
}

func (ctx *testContext) transmitPacket(id uint64, sequence uint16, packetData []byte) {
	if ctx.drop {
		return
	}

	if ctx.allowPackets >= 0 {
		if ctx.allowPackets == 0 {
			return
		}
		ctx.allowPackets--
	}

	if id == 0 {
		ctx.receiver.ReceivePacket(packetData)
	} else if id == 1 {
		ctx.sender.ReceivePacket(packetData)
	}
}

func testProcessPacketFunction(id uint64, sequence uint16, packetData []byte) bool {
	return true
}

func endpointConfigForValidation() Config {
	config := DefaultConfig()
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }
	return config
}

func TestNewEndpointFragmentConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		modify  func(*Config)
	}{
		{
			name:    "fragment threshold above maximum",
			wantErr: "fragment above must not exceed max packet size",
			modify: func(config *Config) {
				config.FragmentAbove = config.MaxPacketSize + 1
			},
		},
		{
			name:    "fragment capacity one short",
			wantErr: "max fragments times fragment size must cover max packet size",
			modify: func(config *Config) {
				config.MaxFragments = 4
				config.FragmentSize = 1024
				config.MaxPacketSize = 4*1024 + 1
				config.FragmentAbove = 1024
			},
		},
		{
			name:    "fragment buffer length overflow",
			wantErr: "max fragments times fragment size does not fit in a packet length",
			modify: func(config *Config) {
				maxInt := int(^uint(0) >> 1)
				config.MaxFragments = 256
				config.FragmentSize = (maxInt-MaxPacketHeaderBytes)/config.MaxFragments + 1
				config.MaxPacketSize = 1
				config.FragmentAbove = 1
			},
		},
		{
			name:    "receive length overflow",
			wantErr: "max packet size is too large for the receive length check",
			modify: func(config *Config) {
				maxInt := int(^uint(0) >> 1)
				config.MaxFragments = 1
				config.FragmentSize = maxInt - MaxPacketHeaderBytes
				config.MaxPacketSize = maxInt - MaxPacketHeaderBytes - FragmentHeaderBytes + 1
				config.FragmentAbove = 1
			},
		},
		{
			name:    "packet header length overflow",
			wantErr: "packet header size plus max packet size does not fit a packet length",
			modify: func(config *Config) {
				maxInt := int(^uint(0) >> 1)
				config.MaxFragments = 1
				config.FragmentSize = maxInt - MaxPacketHeaderBytes - FragmentHeaderBytes
				config.MaxPacketSize = maxInt - MaxPacketHeaderBytes - FragmentHeaderBytes
				config.FragmentAbove = 1
			},
		},
		{
			name:    "negative packet header size",
			wantErr: "packet header size must not be negative",
			modify: func(config *Config) {
				config.PacketHeaderSize = -1
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := endpointConfigForValidation()
			test.modify(&config)
			endpoint, err := NewEndpoint(&config, 0)
			if err == nil {
				if endpoint != nil {
					t.Fatal("expected invalid config to be refused")
				}
				t.Fatal("expected invalid config error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected error containing %q, got %q", test.wantErr, err)
			}
		})
	}

	t.Run("exact fragment capacity is valid", func(t *testing.T) {
		config := endpointConfigForValidation()
		config.MaxFragments = 4
		config.FragmentSize = 1024
		config.MaxPacketSize = 4 * 1024
		config.FragmentAbove = 1024
		endpoint, err := NewEndpoint(&config, 0)
		if err != nil {
			t.Fatalf("exactly covering config was refused: %v", err)
		}
		if endpoint == nil {
			t.Fatal("expected endpoint for exactly covering config")
		}
	})

	t.Run("fragment threshold equal to maximum is valid", func(t *testing.T) {
		config := endpointConfigForValidation()
		config.MaxPacketSize = 1024
		config.FragmentAbove = config.MaxPacketSize
		config.MaxFragments = 1
		config.FragmentSize = config.MaxPacketSize
		endpoint, err := NewEndpoint(&config, 0)
		if err != nil {
			t.Fatalf("equal threshold config was refused: %v", err)
		}
		if endpoint == nil {
			t.Fatal("expected endpoint for equal threshold config")
		}
	})
}

func TestNewEndpointBufferSizeValidation(t *testing.T) {
	bufferSizes := []struct {
		name   string
		modify func(*Config, int)
	}{
		{
			name: "sent packets",
			modify: func(config *Config, size int) {
				config.SentPacketsBufferSize = size
			},
		},
		{
			name: "received packets",
			modify: func(config *Config, size int) {
				config.ReceivedPacketsBufferSize = size
			},
		},
		{
			name: "fragment reassembly",
			modify: func(config *Config, size int) {
				config.FragmentReassemblyBufferSize = size
			},
		},
	}

	for _, bufferSize := range bufferSizes {
		for _, invalidSize := range []int{16385, 32768, 65536} {
			t.Run(bufferSize.name+" rejects "+strconv.Itoa(invalidSize), func(t *testing.T) {
				config := endpointConfigForValidation()
				bufferSize.modify(&config, invalidSize)
				endpoint, err := NewEndpoint(&config, 0)
				if err == nil {
					if endpoint != nil {
						t.Fatal("expected oversized config to be refused")
					}
					t.Fatal("expected oversized config error")
				}
			})
		}

		t.Run(bufferSize.name+" accepts 16384", func(t *testing.T) {
			config := endpointConfigForValidation()
			bufferSize.modify(&config, 16384)
			endpoint, err := NewEndpoint(&config, 0)
			if err != nil {
				t.Fatalf("maximum buffer size was refused: %v", err)
			}
			if endpoint == nil {
				t.Fatal("expected endpoint for maximum buffer size")
			}
		})
	}
}

func TestNewEndpointSmoothingFactorRangeValidation(t *testing.T) {
	factors := []struct {
		name   string
		modify func(*Config, float64)
	}{
		{
			name: "rtt smoothing factor",
			modify: func(config *Config, value float64) {
				config.RTTSmoothingFactor = value
			},
		},
		{
			name: "packet loss smoothing factor",
			modify: func(config *Config, value float64) {
				config.PacketLossSmoothingFactor = value
			},
		},
		{
			name: "bandwidth smoothing factor",
			modify: func(config *Config, value float64) {
				config.BandwidthSmoothingFactor = value
			},
		},
	}

	invalidValues := []struct {
		name  string
		value float64
	}{
		{name: "negative", value: -0.1},
		{name: "above one", value: 1.1},
		{name: "not a number", value: math.NaN()},
	}

	for _, factor := range factors {
		for _, invalid := range invalidValues {
			t.Run(factor.name+" rejects "+invalid.name, func(t *testing.T) {
				config := endpointConfigForValidation()
				factor.modify(&config, invalid.value)
				endpoint, err := NewEndpoint(&config, 0)
				if err == nil {
					if endpoint != nil {
						t.Fatal("expected out of range smoothing factor to be refused")
					}
					t.Fatal("expected smoothing factor range error")
				}
			})
		}

		for _, validValue := range []float64{0.0, 1.0} {
			t.Run(factor.name+" accepts "+strconv.FormatFloat(validValue, 'g', -1, 64), func(t *testing.T) {
				config := endpointConfigForValidation()
				factor.modify(&config, validValue)
				endpoint, err := NewEndpoint(&config, 0)
				if err != nil {
					t.Fatalf("valid smoothing factor %v was refused: %v", validValue, err)
				}
				if endpoint == nil {
					t.Fatalf("expected endpoint for valid smoothing factor %v", validValue)
				}
			})
		}
	}
}

// newTestEndpointPair creates a connected sender/receiver pair with the given
// config modifications applied to both endpoints.
func newTestEndpointPair(t *testing.T, context *testContext, time float64, modifyConfig func(senderConfig, receiverConfig *Config)) {
	t.Helper()

	senderConfig := DefaultConfig()
	receiverConfig := DefaultConfig()

	senderConfig.Name = "sender"
	senderConfig.ID = 0
	senderConfig.TransmitPacketFunction = context.transmitPacket
	senderConfig.ProcessPacketFunction = testProcessPacketFunction

	receiverConfig.Name = "receiver"
	receiverConfig.ID = 1
	receiverConfig.TransmitPacketFunction = context.transmitPacket
	receiverConfig.ProcessPacketFunction = testProcessPacketFunction

	if modifyConfig != nil {
		modifyConfig(&senderConfig, &receiverConfig)
	}

	var err error
	context.sender, err = NewEndpoint(&senderConfig, time)
	if err != nil {
		t.Fatalf("could not create sender endpoint: %v", err)
	}
	context.receiver, err = NewEndpoint(&receiverConfig, time)
	if err != nil {
		t.Fatalf("could not create receiver endpoint: %v", err)
	}
}

const testAcksNumIterations = 256

func TestAcks(t *testing.T) {
	time := 100.0

	context := newTestContext()

	newTestEndpointPair(t, context, time, nil)

	deltaTime := 0.01

	for i := 0; i < testAcksNumIterations; i++ {
		dummyPacket := make([]byte, 8)

		context.sender.SendPacket(dummyPacket)
		context.receiver.SendPacket(dummyPacket)

		context.sender.Update(time)
		context.receiver.Update(time)

		time += deltaTime
	}

	var senderAckedPacket [testAcksNumIterations]bool
	senderAcks := context.sender.Acks()
	for _, ack := range senderAcks {
		if ack < testAcksNumIterations {
			senderAckedPacket[ack] = true
		}
	}
	for i := 0; i < testAcksNumIterations-32; i++ {
		if !senderAckedPacket[i] {
			t.Fatalf("expected sender packet %d to be acked", i)
		}
	}

	var receiverAckedPacket [testAcksNumIterations]bool
	receiverAcks := context.receiver.Acks()
	for _, ack := range receiverAcks {
		if ack < testAcksNumIterations {
			receiverAckedPacket[ack] = true
		}
	}
	for i := 0; i < testAcksNumIterations-32; i++ {
		if !receiverAckedPacket[i] {
			t.Fatalf("expected receiver packet %d to be acked", i)
		}
	}
}

func TestAcksPacketLoss(t *testing.T) {
	time := 100.0

	context := newTestContext()

	newTestEndpointPair(t, context, time, nil)

	deltaTime := 0.1

	for i := 0; i < testAcksNumIterations; i++ {
		dummyPacket := make([]byte, 8)

		context.drop = (i % 2) != 0

		context.sender.SendPacket(dummyPacket)
		context.receiver.SendPacket(dummyPacket)

		context.sender.Update(time)
		context.receiver.Update(time)

		time += deltaTime
	}

	var senderAckedPacket [testAcksNumIterations]bool
	senderAcks := context.sender.Acks()
	for _, ack := range senderAcks {
		if ack < testAcksNumIterations {
			senderAckedPacket[ack] = true
		}
	}
	for i := 0; i < testAcksNumIterations-32; i++ {
		expected := (i+1)%2 != 0
		if senderAckedPacket[i] != expected {
			t.Fatalf("expected sender packet %d acked to be %v", i, expected)
		}
	}

	var receiverAckedPacket [testAcksNumIterations]bool
	receiverAcks := context.receiver.Acks()
	for _, ack := range receiverAcks {
		if ack < testAcksNumIterations {
			receiverAckedPacket[ack] = true
		}
	}
	for i := 0; i < testAcksNumIterations-32; i++ {
		expected := (i+1)%2 != 0
		if receiverAckedPacket[i] != expected {
			t.Fatalf("expected receiver packet %d acked to be %v", i, expected)
		}
	}
}

func TestSendPacketEmpty(t *testing.T) {
	config := endpointConfigForValidation()
	endpoint, err := NewEndpoint(&config, 0)
	if err != nil {
		t.Fatalf("could not create endpoint: %v", err)
	}

	for _, packetData := range [][]byte{nil, {}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("expected SendPacket with empty packet data to panic")
				}
			}()
			endpoint.SendPacket(packetData)
		}()
	}
}

func TestSendPacketTooLarge(t *testing.T) {
	config := endpointConfigForValidation()

	transmitted := 0
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {
		transmitted++
	}

	endpoint, err := NewEndpoint(&config, 0)
	if err != nil {
		t.Fatalf("could not create endpoint: %v", err)
	}

	endpoint.SendPacket(make([]byte, config.MaxPacketSize+1))

	if transmitted != 0 {
		t.Fatalf("expected no packets transmitted, got %d", transmitted)
	}
	counters := endpoint.Counters()
	if counters[CounterNumPacketsTooLargeToSend] != 1 {
		t.Fatalf("expected 1 packet too large to send, got %d", counters[CounterNumPacketsTooLargeToSend])
	}
	if counters[CounterNumPacketsSent] != 0 {
		t.Fatalf("expected 0 packets sent, got %d", counters[CounterNumPacketsSent])
	}
	if endpoint.NextPacketSequence() != 0 {
		t.Fatalf("expected sequence 0 after oversized send, got %d", endpoint.NextPacketSequence())
	}
}

func TestReceivePacketEmpty(t *testing.T) {
	config := endpointConfigForValidation()

	processed := 0
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		processed++
		return true
	}

	endpoint, err := NewEndpoint(&config, 0)
	if err != nil {
		t.Fatalf("could not create endpoint: %v", err)
	}

	for _, packetData := range [][]byte{nil, {}} {
		endpoint.ReceivePacket(packetData)
	}

	if processed != 0 {
		t.Fatalf("expected no packets processed, got %d", processed)
	}
	counters := endpoint.Counters()
	if counters[CounterNumPacketsInvalid] != 2 {
		t.Fatalf("expected 2 invalid packets, got %d", counters[CounterNumPacketsInvalid])
	}
	if counters[CounterNumPacketsReceived] != 0 {
		t.Fatalf("expected 0 packets received, got %d", counters[CounterNumPacketsReceived])
	}
}

func TestReceivePacketTooLarge(t *testing.T) {
	t.Run("packet exceeds maximum receive size", func(t *testing.T) {
		config := endpointConfigForValidation()

		processed := 0
		config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
			processed++
			return true
		}

		endpoint, err := NewEndpoint(&config, 0)
		if err != nil {
			t.Fatalf("could not create endpoint: %v", err)
		}

		endpoint.ReceivePacket(make([]byte, config.MaxPacketSize+MaxPacketHeaderBytes+FragmentHeaderBytes+1))

		if processed != 0 {
			t.Fatalf("expected no packets processed, got %d", processed)
		}
		counters := endpoint.Counters()
		if counters[CounterNumPacketsTooLargeToReceive] != 1 {
			t.Fatalf("expected 1 packet too large to receive, got %d", counters[CounterNumPacketsTooLargeToReceive])
		}
		if counters[CounterNumPacketsReceived] != 0 {
			t.Fatalf("expected 0 packets received, got %d", counters[CounterNumPacketsReceived])
		}
	})

	t.Run("payload exceeds maximum packet size", func(t *testing.T) {
		config := endpointConfigForValidation()
		config.MaxPacketSize = 64
		config.FragmentAbove = config.MaxPacketSize

		processed := 0
		config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
			processed++
			return true
		}

		endpoint, err := NewEndpoint(&config, 0)
		if err != nil {
			t.Fatalf("could not create endpoint: %v", err)
		}

		packetData := make([]byte, MaxPacketHeaderBytes+config.MaxPacketSize+1)
		headerBytes := writePacketHeader(packetData, 0, 0xFFFF, 0)
		payloadBytes := config.MaxPacketSize + 1

		endpoint.ReceivePacket(packetData[:headerBytes+payloadBytes])

		if processed != 0 {
			t.Fatalf("expected no packets processed, got %d", processed)
		}
		counters := endpoint.Counters()
		if counters[CounterNumPacketsTooLargeToReceive] != 1 {
			t.Fatalf("expected 1 packet too large to receive, got %d", counters[CounterNumPacketsTooLargeToReceive])
		}
		if counters[CounterNumPacketsReceived] != 1 {
			t.Fatalf("expected 1 packet received, got %d", counters[CounterNumPacketsReceived])
		}
	})
}

func TestLoggingPacketExchange(t *testing.T) {
	var logs []string

	SetPrintfFunction(func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	t.Cleanup(func() {
		SetLogLevel(LogLevelNone)
		SetPrintfFunction(func(format string, args ...any) {
			fmt.Printf(format, args...)
		})
	})

	for _, level := range []LogLevel{LogLevelDebug, LogLevelError} {
		t.Run(fmt.Sprintf("level %d", level), func(t *testing.T) {
			logs = logs[:0]
			SetLogLevel(level)

			time := 100.0

			context := newTestContext()

			newTestEndpointPair(t, context, time, nil)

			context.sender.SendPacket(make([]byte, 8))
			context.receiver.SendPacket(make([]byte, 8))

			context.sender.Update(time)
			context.receiver.Update(time)

			context.sender.SendPacket(make([]byte, context.sender.config.MaxPacketSize+1))

			if len(logs) == 0 {
				t.Fatalf("expected log output with %v enabled", level)
			}
		})
	}
}

func makeTestRegularPacket(config *Config, sequence uint16, payload []byte) []byte {
	packetData := make([]byte, MaxPacketHeaderBytes+len(payload))
	headerBytes := writePacketHeader(packetData, sequence, 0xFFFF, 0)
	copy(packetData[headerBytes:], payload)
	return packetData[:headerBytes+len(payload)]
}

func makeTestFragmentPacket(config *Config, sequence uint16, fragmentID, numFragments int) []byte {
	packetData := make([]byte, FragmentHeaderBytes+MaxPacketHeaderBytes+config.FragmentSize)

	packetData[0] = 1
	packetData[1] = byte(sequence)
	packetData[2] = byte(sequence >> 8)
	packetData[3] = byte(fragmentID)
	packetData[4] = byte(numFragments - 1)

	p := FragmentHeaderBytes
	if fragmentID == 0 {
		p += writePacketHeader(packetData[p:], sequence, 0xFFFF, 0)
	}

	p += config.FragmentSize

	return packetData[:p]
}

func TestNetworkEventLogLevels(t *testing.T) {
	scenarios := []struct {
		name  string
		setup func(t *testing.T) func()
		want  string
	}{
		{
			name: "oversized payload",
			setup: func(t *testing.T) func() {
				config := endpointConfigForValidation()
				config.Name = "receiver"
				config.MaxPacketSize = 64
				config.FragmentAbove = config.MaxPacketSize
				endpoint, err := NewEndpoint(&config, 100)
				if err != nil {
					t.Fatalf("could not create endpoint: %v", err)
				}
				packet := makeTestRegularPacket(&config, 0, make([]byte, config.MaxPacketSize+1))
				return func() { endpoint.ReceivePacket(packet) }
			},
			want: "packet too large to receive. packet is at",
		},
		{
			name: "callback rejection",
			setup: func(t *testing.T) func() {
				config := endpointConfigForValidation()
				config.Name = "receiver"
				config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
					return false
				}
				endpoint, err := NewEndpoint(&config, 100)
				if err != nil {
					t.Fatalf("could not create endpoint: %v", err)
				}
				packet := makeTestRegularPacket(&config, 0, make([]byte, 8))
				return func() { endpoint.ReceivePacket(packet) }
			},
			want: "process packet failed",
		},
		{
			name: "stale fragment",
			setup: func(t *testing.T) func() {
				config := endpointConfigForValidation()
				config.Name = "receiver"
				config.FragmentReassemblyBufferSize = 2
				endpoint, err := NewEndpoint(&config, 100)
				if err != nil {
					t.Fatalf("could not create endpoint: %v", err)
				}
				for sequence := uint16(0); sequence < 3; sequence++ {
					endpoint.ReceivePacket(makeTestFragmentPacket(&config, sequence, 0, 2))
				}
				packet := makeTestFragmentPacket(&config, 0, 0, 2)
				return func() { endpoint.ReceivePacket(packet) }
			},
			want: "could not insert in reassembly buffer (stale)",
		},
		{
			name: "fragment count mismatch",
			setup: func(t *testing.T) func() {
				config := endpointConfigForValidation()
				config.Name = "receiver"
				endpoint, err := NewEndpoint(&config, 100)
				if err != nil {
					t.Fatalf("could not create endpoint: %v", err)
				}
				endpoint.ReceivePacket(makeTestFragmentPacket(&config, 0, 0, 2))
				packet := makeTestFragmentPacket(&config, 0, 0, 3)
				return func() { endpoint.ReceivePacket(packet) }
			},
			want: "fragment count mismatch",
		},
		{
			name: "duplicate fragment",
			setup: func(t *testing.T) func() {
				config := endpointConfigForValidation()
				config.Name = "receiver"
				endpoint, err := NewEndpoint(&config, 100)
				if err != nil {
					t.Fatalf("could not create endpoint: %v", err)
				}
				packet := makeTestFragmentPacket(&config, 0, 0, 2)
				endpoint.ReceivePacket(packet)
				return func() { endpoint.ReceivePacket(packet) }
			},
			want: "fragment already received",
		},
	}

	var logs []string

	SetPrintfFunction(func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	t.Cleanup(func() {
		SetLogLevel(LogLevelNone)
		SetPrintfFunction(func(format string, args ...any) {
			fmt.Printf(format, args...)
		})
	})

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			SetLogLevel(LogLevelNone)
			trigger := scenario.setup(t)
			logs = logs[:0]

			SetLogLevel(LogLevelError)
			trigger()
			for _, line := range logs {
				if strings.Contains(line, scenario.want) {
					t.Fatalf("expected %q at debug level only, got error log: %q", scenario.want, line)
				}
			}

			SetLogLevel(LogLevelNone)
			trigger = scenario.setup(t)
			logs = logs[:0]

			SetLogLevel(LogLevelDebug)
			trigger()
			found := false
			for _, line := range logs {
				if strings.Contains(line, scenario.want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected %q with debug logging enabled, got %v", scenario.want, logs)
			}
		})
	}
}

func TestReceivePacketDoesNotAllocateWhenLoggingDisabled(t *testing.T) {
	SetLogLevel(LogLevelNone)
	t.Cleanup(func() {
		SetLogLevel(LogLevelNone)
	})

	t.Run("duplicate fragment", func(t *testing.T) {
		config := endpointConfigForValidation()
		config.Name = "receiver"
		endpoint, err := NewEndpoint(&config, 100)
		if err != nil {
			t.Fatalf("could not create endpoint: %v", err)
		}

		packet := makeTestFragmentPacket(&config, 0, 0, 2)
		endpoint.ReceivePacket(packet)
		if endpoint.Counters()[CounterNumFragmentsReceived] != 1 {
			t.Fatal("expected fragment to be stored before measuring")
		}

		allocs := testing.AllocsPerRun(100, func() {
			endpoint.ReceivePacket(packet)
		})
		if allocs != 0 {
			t.Fatalf("expected 0 allocations without logging, got %v", allocs)
		}
	})

	t.Run("rejected packet", func(t *testing.T) {
		config := endpointConfigForValidation()
		config.Name = "receiver"
		config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
			return false
		}
		endpoint, err := NewEndpoint(&config, 100)
		if err != nil {
			t.Fatalf("could not create endpoint: %v", err)
		}

		packet := makeTestRegularPacket(&config, 0, make([]byte, 8))

		allocs := testing.AllocsPerRun(100, func() {
			endpoint.ReceivePacket(packet)
		})
		if allocs != 0 {
			t.Fatalf("expected 0 allocations without logging, got %v", allocs)
		}
	})
}

const testDuplicatePacketsNumIterations = 16

func TestDuplicatePackets(t *testing.T) {
	time := 100.0

	context := newTestContext()

	numProcessed := 0

	senderConfig := DefaultConfig()
	receiverConfig := DefaultConfig()

	// deliver each packet to the receiver twice, simulating duplication on the network

	duplicateTransmit := func(id uint64, sequence uint16, packetData []byte) {
		if id == 0 {
			context.receiver.ReceivePacket(packetData)
			context.receiver.ReceivePacket(packetData)
		}
	}

	senderConfig.Name = "sender"
	senderConfig.ID = 0
	senderConfig.TransmitPacketFunction = duplicateTransmit
	senderConfig.ProcessPacketFunction = testProcessPacketFunction

	receiverConfig.Name = "receiver"
	receiverConfig.ID = 1
	receiverConfig.TransmitPacketFunction = duplicateTransmit
	receiverConfig.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		numProcessed++
		return true
	}

	var err error
	context.sender, err = NewEndpoint(&senderConfig, time)
	if err != nil {
		t.Fatalf("could not create sender endpoint: %v", err)
	}
	context.receiver, err = NewEndpoint(&receiverConfig, time)
	if err != nil {
		t.Fatalf("could not create receiver endpoint: %v", err)
	}

	for i := 0; i < testDuplicatePacketsNumIterations; i++ {
		dummyPacket := make([]byte, 8)
		context.sender.SendPacket(dummyPacket)
	}

	if numProcessed != testDuplicatePacketsNumIterations {
		t.Fatalf("expected %d packets processed, got %d", testDuplicatePacketsNumIterations, numProcessed)
	}

	receiverCounters := context.receiver.Counters()

	if receiverCounters[CounterNumPacketsReceived] != 2*testDuplicatePacketsNumIterations {
		t.Fatalf("expected %d packets received, got %d", 2*testDuplicatePacketsNumIterations, receiverCounters[CounterNumPacketsReceived])
	}
	if receiverCounters[CounterNumPacketsDuplicate] != testDuplicatePacketsNumIterations {
		t.Fatalf("expected %d duplicate packets, got %d", testDuplicatePacketsNumIterations, receiverCounters[CounterNumPacketsDuplicate])
	}

	// duplicate fragments arriving after their packet was delivered must not restart reassembly

	fragmentedSequence := context.sender.NextPacketSequence()

	largePacket := make([]byte, 2048)
	context.sender.SendPacket(largePacket)

	if numProcessed != testDuplicatePacketsNumIterations+1 {
		t.Fatalf("expected %d packets processed, got %d", testDuplicatePacketsNumIterations+1, numProcessed)
	}
	if context.receiver.fragmentReassembly.exists(fragmentedSequence) {
		t.Fatalf("expected no reassembly in progress for packet %d", fragmentedSequence)
	}
}

const testStalePacketsNumIterations = 300

func TestStalePackets(t *testing.T) {
	time := 100.0

	context := newTestContext()

	var firstPacket []byte
	numProcessed := 0

	senderConfig := DefaultConfig()
	receiverConfig := DefaultConfig()

	transmit := func(id uint64, sequence uint16, packetData []byte) {
		if id == 0 {
			if sequence == 0 && firstPacket == nil {
				firstPacket = append([]byte(nil), packetData...)
			}
			context.receiver.ReceivePacket(packetData)
		}
	}

	senderConfig.Name = "sender"
	senderConfig.ID = 0
	senderConfig.TransmitPacketFunction = transmit
	senderConfig.ProcessPacketFunction = testProcessPacketFunction

	receiverConfig.Name = "receiver"
	receiverConfig.ID = 1
	receiverConfig.TransmitPacketFunction = transmit
	receiverConfig.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		numProcessed++
		return true
	}

	var err error
	context.sender, err = NewEndpoint(&senderConfig, time)
	if err != nil {
		t.Fatalf("could not create sender endpoint: %v", err)
	}
	context.receiver, err = NewEndpoint(&receiverConfig, time)
	if err != nil {
		t.Fatalf("could not create receiver endpoint: %v", err)
	}

	// send enough packets that sequence 0 falls out of the receive window (256 entries)

	for i := 0; i < testStalePacketsNumIterations; i++ {
		dummyPacket := make([]byte, 8)
		context.sender.SendPacket(dummyPacket)
	}

	if numProcessed != testStalePacketsNumIterations {
		t.Fatalf("expected %d packets processed, got %d", testStalePacketsNumIterations, numProcessed)
	}
	if firstPacket == nil {
		t.Fatal("expected first packet to be captured")
	}

	// replaying the first packet must be rejected as stale, not processed

	context.receiver.ReceivePacket(firstPacket)

	if numProcessed != testStalePacketsNumIterations {
		t.Fatalf("expected %d packets processed after stale replay, got %d", testStalePacketsNumIterations, numProcessed)
	}

	receiverCounters := context.receiver.Counters()

	if receiverCounters[CounterNumPacketsStale] != 1 {
		t.Fatalf("expected 1 stale packet, got %d", receiverCounters[CounterNumPacketsStale])
	}
}

const (
	testAckBufferOverflowNumPackets = 32
	testAckBufferOverflowBufferSize = 16
)

func TestAckBufferOverflow(t *testing.T) {
	time := 100.0

	context := newTestContext()

	// undersized ack buffer on the sender, so a single received packet acking 32 sent packets overflows it

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.AckBufferSize = testAckBufferOverflowBufferSize
	})

	for i := 0; i < testAckBufferOverflowNumPackets; i++ {
		dummyPacket := make([]byte, 8)
		context.sender.SendPacket(dummyPacket)
	}

	// one packet back from the receiver acks all 32, but only 16 fit in the ack buffer. the rest are dropped

	{
		dummyPacket := make([]byte, 8)
		context.receiver.SendPacket(dummyPacket)
	}

	if len(context.sender.Acks()) != testAckBufferOverflowBufferSize {
		t.Fatalf("expected %d acks, got %d", testAckBufferOverflowBufferSize, len(context.sender.Acks()))
	}

	senderCounters := context.sender.Counters()
	if senderCounters[CounterNumPacketsAcked] != testAckBufferOverflowBufferSize {
		t.Fatalf("expected %d packets acked, got %d", testAckBufferOverflowBufferSize, senderCounters[CounterNumPacketsAcked])
	}

	// once the caller clears acks, the dropped acks are reported on the next packet that covers them

	context.sender.ClearAcks()

	{
		dummyPacket := make([]byte, 8)
		context.receiver.SendPacket(dummyPacket)
	}

	if len(context.sender.Acks()) != testAckBufferOverflowNumPackets-testAckBufferOverflowBufferSize {
		t.Fatalf("expected %d acks, got %d", testAckBufferOverflowNumPackets-testAckBufferOverflowBufferSize, len(context.sender.Acks()))
	}

	senderCounters = context.sender.Counters()
	if senderCounters[CounterNumPacketsAcked] != testAckBufferOverflowNumPackets {
		t.Fatalf("expected %d packets acked, got %d", testAckBufferOverflowNumPackets, senderCounters[CounterNumPacketsAcked])
	}
}

const testMaxPacketBytes = 4 * 1024

func generatePacketDataWithSize(sequence uint16, packetData []byte, packetBytes int) []byte {
	packetData[0] = byte(sequence & 0xFF)
	packetData[1] = byte((sequence >> 8) & 0xFF)
	for i := 2; i < packetBytes; i++ {
		packetData[i] = byte((i + int(sequence)) % 256)
	}
	return packetData[:packetBytes]
}

func generatePacketData(sequence uint16, packetData []byte) []byte {
	packetBytes := (int(sequence)*1023)%(testMaxPacketBytes-2) + 2
	return generatePacketDataWithSize(sequence, packetData, packetBytes)
}

func validatePacketData(t *testing.T, packetData []byte) {
	t.Helper()
	packetBytes := len(packetData)
	sequence := uint16(packetData[0]) | uint16(packetData[1])<<8
	if packetBytes != (int(sequence)*1023)%(testMaxPacketBytes-2)+2 {
		t.Fatalf("packet %d has unexpected size %d", sequence, packetBytes)
	}
	for i := 2; i < packetBytes; i++ {
		if packetData[i] != byte((i+int(sequence))%256) {
			t.Fatalf("packet %d has corrupt data at index %d", sequence, i)
		}
	}
}

const (
	testPacketsNumIterations   = 16
	testPacketsNumPerIteration = 2
)

func TestPackets(t *testing.T) {
	time := 100.0

	context := newTestContext()

	delivered := 0

	processPacketValidate := func(id uint64, sequence uint16, packetData []byte) bool {
		delivered++
		validatePacketData(t, packetData)
		return true
	}

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.FragmentAbove = 500
		receiverConfig.FragmentAbove = 500
		senderConfig.ProcessPacketFunction = processPacketValidate
		receiverConfig.ProcessPacketFunction = processPacketValidate
	})

	deltaTime := 0.1

	for i := 0; i < testPacketsNumIterations; i++ {
		{
			var packetData [testMaxPacketBytes]byte
			sequence := context.sender.NextPacketSequence()
			packet := generatePacketData(sequence, packetData[:])
			context.sender.SendPacket(packet)
		}

		{
			var packetData [testMaxPacketBytes]byte
			sequence := context.sender.NextPacketSequence()
			packet := generatePacketData(sequence, packetData[:])
			context.sender.SendPacket(packet)
		}

		context.sender.Update(time)
		context.receiver.Update(time)

		context.sender.ClearAcks()
		context.receiver.ClearAcks()

		time += deltaTime
	}

	expectedDelivered := testPacketsNumIterations * testPacketsNumPerIteration
	if delivered != expectedDelivered {
		t.Fatalf("expected %d packets delivered, got %d", expectedDelivered, delivered)
	}
}

func generatePacketDataLarge(packetData []byte) []byte {
	dataBytes := testMaxPacketBytes - 2
	packetData[0] = byte(dataBytes & 0xFF)
	packetData[1] = byte((dataBytes >> 8) & 0xFF)
	for i := 2; i < dataBytes; i++ {
		packetData[i] = byte(i % 256)
	}
	return packetData[:dataBytes+2]
}

func TestLargePackets(t *testing.T) {
	time := 100.0

	context := newTestContext()

	processPacketValidateLarge := func(id uint64, sequence uint16, packetData []byte) bool {
		packetBytes := len(packetData)
		dataBytes := int(packetData[0]) | int(packetData[1])<<8
		if packetBytes != dataBytes+2 {
			t.Fatalf("expected packet size %d, got %d", dataBytes+2, packetBytes)
		}
		for i := 2; i < dataBytes; i++ {
			if packetData[i] != byte(i%256) {
				t.Fatalf("large packet has corrupt data at index %d", i)
			}
		}
		return true
	}

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.MaxPacketSize = testMaxPacketBytes
		receiverConfig.MaxPacketSize = testMaxPacketBytes
		senderConfig.FragmentAbove = testMaxPacketBytes
		receiverConfig.FragmentAbove = testMaxPacketBytes
		senderConfig.ProcessPacketFunction = processPacketValidateLarge
		receiverConfig.ProcessPacketFunction = processPacketValidateLarge
	})

	{
		var packetData [testMaxPacketBytes]byte
		packet := generatePacketDataLarge(packetData[:])
		if len(packet) != testMaxPacketBytes {
			t.Fatalf("expected large packet to be %d bytes, got %d", testMaxPacketBytes, len(packet))
		}
		context.sender.SendPacket(packet)
	}

	context.sender.Update(time)
	context.receiver.Update(time)

	context.sender.ClearAcks()
	context.receiver.ClearAcks()

	receiverCounters := context.receiver.Counters()
	if receiverCounters[CounterNumPacketsTooLargeToReceive] != 0 {
		t.Fatalf("expected 0 packets too large to receive, got %d", receiverCounters[CounterNumPacketsTooLargeToReceive])
	}
	if receiverCounters[CounterNumPacketsReceived] != 1 {
		t.Fatalf("expected 1 packet received, got %d", receiverCounters[CounterNumPacketsReceived])
	}
}

func TestSequenceBufferRollover(t *testing.T) {
	time := 100.0

	context := newTestContext()

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.FragmentAbove = 500
		receiverConfig.FragmentAbove = 500
	})

	numPacketsSent := 0
	for i := 0; i <= 32767; i++ {
		packetData := make([]byte, 16)
		context.sender.NextPacketSequence()
		context.sender.SendPacket(packetData)
		numPacketsSent++
	}

	packetData := make([]byte, testMaxPacketBytes)
	context.sender.NextPacketSequence()
	context.sender.SendPacket(packetData)
	numPacketsSent++

	receiverCounters := context.receiver.Counters()

	if receiverCounters[CounterNumPacketsReceived] != uint64(numPacketsSent) {
		t.Fatalf("expected %d packets received, got %d", numPacketsSent, receiverCounters[CounterNumPacketsReceived])
	}
	if receiverCounters[CounterNumFragmentsInvalid] != 0 {
		t.Fatalf("expected 0 invalid fragments, got %d", receiverCounters[CounterNumFragmentsInvalid])
	}
}

func TestFragmentCleanup(t *testing.T) {
	time := 100.0

	context := newTestContext()

	var senderFragmentSize int

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		receiverConfig.FragmentReassemblyBufferSize = 4
		senderFragmentSize = senderConfig.FragmentSize
	})

	deltaTime := 0.1

	packetSizes := []int{
		senderFragmentSize + senderFragmentSize/2,
		10,
		10,
		10,
		10,
	}

	// make sure we're sending more than FragmentReassemblyBufferSize packets, so the buffer wraps around.
	if len(packetSizes) <= 4 {
		t.Fatal("must send more packets than the reassembly buffer size")
	}

	for i := 0; i < len(packetSizes); i++ {
		// only allow one packet per transmit, so that our fragmented packets are only partially delivered.
		context.allowPackets = 1
		{
			var packetData [testMaxPacketBytes]byte
			sequence := context.sender.NextPacketSequence()
			packet := generatePacketDataWithSize(sequence, packetData[:], packetSizes[i])
			context.sender.SendPacket(packet)
		}

		context.sender.Update(time)
		context.receiver.Update(time)

		context.sender.ClearAcks()
		context.receiver.ClearAcks()

		time += deltaTime
	}

	// the partial reassembly of packet 0 must have been cleaned up when the reassembly buffer wrapped around

	if context.receiver.fragmentReassembly.exists(0) {
		t.Fatal("expected partial reassembly of packet 0 to be cleaned up")
	}
	for i := 0; i < context.receiver.fragmentReassembly.numEntries; i++ {
		if entry := context.receiver.fragmentReassembly.atIndex(i); entry != nil && entry.packetData != nil {
			t.Fatalf("expected no reassembly buffers to be held, found one at index %d", i)
		}
	}
}

func TestStoreFragmentDataBounds(t *testing.T) {
	context := newTestContext()
	newTestEndpointPair(t, context, 100.0, nil)

	endpoint := context.receiver
	fragmentSize := endpoint.config.FragmentSize
	numFragments := 2

	reassemblyData := &fragmentReassemblyData{
		numFragmentsTotal: numFragments,
		packetData:        make([]byte, MaxPacketHeaderBytes+numFragments*fragmentSize),
	}

	fragmentID := numFragments
	fragmentData := make([]byte, fragmentSize)

	if endpoint.storeFragmentData(reassemblyData, 0, 0, 0, fragmentID, fragmentSize, fragmentData) {
		t.Fatal("expected out-of-bounds fragment store to fail")
	}

	if reassemblyData.numFragmentsReceived != 0 {
		t.Fatalf("expected no fragments marked received, got %d", reassemblyData.numFragmentsReceived)
	}
	if reassemblyData.fragmentReceived[fragmentID] {
		t.Fatalf("expected fragment %d not to be marked received", fragmentID)
	}
}

func TestEndpointReset(t *testing.T) {
	time := 100.0

	context := newTestContext()

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.FragmentAbove = 500
		receiverConfig.FragmentAbove = 500
	})

	// exchange packets both ways so acks and counters accumulate

	for i := 0; i < 8; i++ {
		dummyPacket := make([]byte, 8)

		context.sender.SendPacket(dummyPacket)
		context.receiver.SendPacket(dummyPacket)

		context.sender.Update(time)
		context.receiver.Update(time)

		time += 0.01
	}

	if len(context.sender.Acks()) == 0 {
		t.Fatal("expected sender to have acks")
	}
	if context.sender.Counters()[CounterNumPacketsSent] == 0 {
		t.Fatal("expected sender to have sent packets")
	}

	// leave a fragment reassembly in progress on the receiver by delivering only the first fragment of a large packet

	context.allowPackets = 1
	{
		largePacket := make([]byte, 1500)
		context.sender.SendPacket(largePacket)
	}
	context.allowPackets = -1

	context.sender.Reset()
	context.receiver.Reset()

	if context.sender.NextPacketSequence() != 0 {
		t.Fatalf("expected sender sequence 0 after reset, got %d", context.sender.NextPacketSequence())
	}
	if context.receiver.NextPacketSequence() != 0 {
		t.Fatalf("expected receiver sequence 0 after reset, got %d", context.receiver.NextPacketSequence())
	}

	if len(context.sender.Acks()) != 0 {
		t.Fatalf("expected no acks after reset, got %d", len(context.sender.Acks()))
	}

	senderCounters := context.sender.Counters()
	receiverCounters := context.receiver.Counters()
	for i := 0; i < NumCounters; i++ {
		if senderCounters[i] != 0 {
			t.Fatalf("expected sender counter %d to be 0 after reset, got %d", i, senderCounters[i])
		}
		if receiverCounters[i] != 0 {
			t.Fatalf("expected receiver counter %d to be 0 after reset, got %d", i, receiverCounters[i])
		}
	}

	// reset must have dropped the in-progress reassembly buffer

	for i := 0; i < context.receiver.fragmentReassembly.numEntries; i++ {
		if context.receiver.fragmentReassembly.entries[i].packetData != nil {
			t.Fatalf("expected reassembly buffer at index %d to be dropped after reset", i)
		}
	}

	// the endpoints must work normally after reset

	for i := 0; i < 8; i++ {
		dummyPacket := make([]byte, 8)

		context.sender.SendPacket(dummyPacket)
		context.receiver.SendPacket(dummyPacket)

		context.sender.Update(time)
		context.receiver.Update(time)

		time += 0.01
	}

	if len(context.sender.Acks()) == 0 {
		t.Fatal("expected sender to have acks after reset")
	}
	if context.receiver.Counters()[CounterNumPacketsReceived] == 0 {
		t.Fatal("expected receiver to have received packets after reset")
	}
}

func requireStatClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	const tolerance = 1e-6
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s: expected %.6f, got %.6f", name, want, got)
	}
}

func TestStatsInitialValues(t *testing.T) {
	time := 100.0

	context := newTestContext()

	newTestEndpointPair(t, context, time, nil)

	stats := []struct {
		name string
		got  float64
	}{
		{"RTT", context.sender.RTT()},
		{"RTTMin", context.sender.RTTMin()},
		{"RTTMax", context.sender.RTTMax()},
		{"RTTAvg", context.sender.RTTAvg()},
		{"JitterAvgVsMinRTT", context.sender.JitterAvgVsMinRTT()},
		{"JitterMaxVsMinRTT", context.sender.JitterMaxVsMinRTT()},
		{"JitterStddevVsAvgRTT", context.sender.JitterStddevVsAvgRTT()},
		{"PacketLoss", context.sender.PacketLoss()},
	}

	for _, stat := range stats {
		requireStatClose(t, stat.name, stat.got, 0.0)
	}

	sentBandwidthKbps, receivedBandwidthKbps, ackedBandwidthKbps := context.sender.Bandwidth()
	requireStatClose(t, "SentBandwidthKBPS", sentBandwidthKbps, 0.0)
	requireStatClose(t, "ReceivedBandwidthKBPS", receivedBandwidthKbps, 0.0)
	requireStatClose(t, "AckedBandwidthKBPS", ackedBandwidthKbps, 0.0)
}

func TestRTTAndJitterStats(t *testing.T) {
	time := 100.0

	context := newTestContext()

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.RTTHistorySize = 8
		senderConfig.RTTSmoothingFactor = 0.5
	})

	// send one packet per sample and ack it after a known delay, so the rtt
	// history buffer contains exactly 20, 40, 60 and 80 milliseconds.

	delays := []float64{20.0, 40.0, 60.0, 80.0}
	for _, delay := range delays {
		context.sender.Update(time)
		context.sender.SendPacket(make([]byte, 8))

		ackTime := time + delay/1000.0

		context.sender.Update(ackTime)
		context.receiver.Update(ackTime)
		context.receiver.SendPacket(make([]byte, 8))

		context.sender.ClearAcks()
		context.receiver.ClearAcks()

		time = ackTime
	}

	context.sender.Update(time)

	// 20, then 20+(40-20)*0.5, 30+(60-30)*0.5, 45+(80-45)*0.5.

	requireStatClose(t, "RTT", context.sender.RTT(), 62.5)
	requireStatClose(t, "RTTMin", context.sender.RTTMin(), 20.0)
	requireStatClose(t, "RTTMax", context.sender.RTTMax(), 80.0)
	requireStatClose(t, "RTTAvg", context.sender.RTTAvg(), 50.0)
	requireStatClose(t, "JitterAvgVsMinRTT", context.sender.JitterAvgVsMinRTT(), 30.0)
	requireStatClose(t, "JitterMaxVsMinRTT", context.sender.JitterMaxVsMinRTT(), 60.0)
	requireStatClose(t, "JitterStddevVsAvgRTT", context.sender.JitterStddevVsAvgRTT(), math.Sqrt(500.0))
}

func TestRTTMinLarge(t *testing.T) {
	for _, sample := range []float64{9000.0, 10000.0, 12000.0} {
		t.Run(fmt.Sprintf("%.0fms", sample), func(t *testing.T) {
			time := 100.0

			context := newTestContext()

			newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
				senderConfig.RTTHistorySize = 8
			})

			context.sender.Update(time)
			context.sender.SendPacket(make([]byte, 8))

			ackTime := time + sample/1000.0

			context.sender.Update(ackTime)
			context.receiver.Update(ackTime)
			context.receiver.SendPacket(make([]byte, 8))

			context.sender.ClearAcks()
			context.receiver.ClearAcks()

			context.sender.Update(ackTime)

			requireStatClose(t, "RTTMin", context.sender.RTTMin(), sample)
			requireStatClose(t, "RTTMax", context.sender.RTTMax(), sample)
			requireStatClose(t, "RTTAvg", context.sender.RTTAvg(), sample)
			requireStatClose(t, "JitterAvgVsMinRTT", context.sender.JitterAvgVsMinRTT(), 0.0)
		})
	}
}

func TestPacketLossStats(t *testing.T) {
	time := 100.0

	context := newTestContext()

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.SentPacketsBufferSize = 16
		senderConfig.PacketLossSmoothingFactor = 1.0
	})

	// send 16 packets, dropping every fourth. the sampled window is the first
	// half of the sent buffer, sequences 0..7, which drops packets 0 and 4:
	// two lost out of eight sent is 25%.

	for i := 0; i < 16; i++ {
		context.drop = i%4 == 0

		context.sender.Update(time)
		context.receiver.Update(time)
		context.sender.SendPacket(make([]byte, 8))

		time += 0.01
	}

	context.drop = false
	context.receiver.Update(time)
	context.receiver.SendPacket(make([]byte, 8))
	context.sender.Update(time)

	requireStatClose(t, "PacketLoss", context.sender.PacketLoss(), 25.0)
}

func TestBandwidthStats(t *testing.T) {
	time := 100.0

	context := newTestContext()

	packetHeaderSize := 0

	newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
		senderConfig.SentPacketsBufferSize = 16
		receiverConfig.ReceivedPacketsBufferSize = 16
		senderConfig.BandwidthSmoothingFactor = 1.0
		receiverConfig.BandwidthSmoothingFactor = 1.0
		packetHeaderSize = senderConfig.PacketHeaderSize
	})

	const (
		payloadBytes = 8
		// the receiver sends nothing until the end, so every sender packet
		// header is a 1 byte prefix, 2 byte sequence, 1 byte ack offset and
		// 4 bytes of ack bits.
		senderWireHeaderBytes = 8
	)

	sentPacketBytes := packetHeaderSize + payloadBytes
	receivedPacketBytes := packetHeaderSize + senderWireHeaderBytes + payloadBytes

	// send 16 packets at 10ms intervals, dropping packets 0 and 5. the
	// sampled window is sequences 0..7, so sent bandwidth covers all eight
	// packets over 70ms, while received and acked bandwidth cover the six
	// delivered packets 1, 2, 3, 4, 6 and 7 over 60ms.

	for i := 0; i < 16; i++ {
		context.drop = i == 0 || i == 5

		context.sender.Update(time)
		context.receiver.Update(time)
		context.sender.SendPacket(make([]byte, payloadBytes))

		time += 0.01
	}

	context.drop = false
	context.receiver.Update(time)
	context.receiver.SendPacket(make([]byte, payloadBytes))
	context.sender.Update(time)

	sentBandwidthKbps, _, ackedBandwidthKbps := context.sender.Bandwidth()
	_, receivedBandwidthKbps, _ := context.receiver.Bandwidth()

	expectedSentBandwidthKbps := float64(8*sentPacketBytes) * 8.0 / 1000.0 / 0.07
	expectedReceivedBandwidthKbps := float64(6*receivedPacketBytes) * 8.0 / 1000.0 / 0.06
	expectedAckedBandwidthKbps := float64(6*sentPacketBytes) * 8.0 / 1000.0 / 0.06

	requireStatClose(t, "SentBandwidthKBPS", sentBandwidthKbps, expectedSentBandwidthKbps)
	requireStatClose(t, "ReceivedBandwidthKBPS", receivedBandwidthKbps, expectedReceivedBandwidthKbps)
	requireStatClose(t, "AckedBandwidthKBPS", ackedBandwidthKbps, expectedAckedBandwidthKbps)
}

func TestBandwidthLargeSampleTotals(t *testing.T) {
	const packetBytes uint32 = 1<<30 - 1
	context := newTestContext()
	newTestEndpointPair(t, context, 1000.0, func(senderConfig, receiverConfig *Config) {
		senderConfig.SentPacketsBufferSize = 8
		senderConfig.ReceivedPacketsBufferSize = 8
		senderConfig.BandwidthSmoothingFactor = 1.0
	})
	endpoint := context.sender
	// Populate only packet metadata: each length fits a signed 32-bit value,
	// while the four sampled lengths together require a wider accumulator.
	for i := 0; i < 8; i++ {
		*endpoint.sentPackets.insert(uint16(i)) = sentPacketData{
			time: 1000.0 + float64(i), acked: true, packetBytes: packetBytes,
		}
		*endpoint.receivedPackets.insert(uint16(i)) = receivedPacketData{
			time: 1000.0 + float64(i), packetBytes: packetBytes,
		}
	}
	endpoint.Update(1008.0)
	sent, received, acked := endpoint.Bandwidth()
	want := float64(int64(packetBytes)*4) / 3.0 * 8.0 / 1000.0
	for _, stat := range []struct {
		name string
		got  float64
	}{
		{"sent", sent}, {"received", received}, {"acked", acked},
	} {
		t.Run(stat.name, func(t *testing.T) {
			requireStatClose(t, stat.name, stat.got, want)
		})
	}
}

func TestSequenceBufferAcrossWrap(t *testing.T) {
	for _, size := range []int{3, 255} {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			buffer := newSequenceBuffer[testSequenceData](size)

			if buffer.numEntries < size || 65536%buffer.numEntries != 0 {
				t.Fatalf("expected entry count to divide 65536, got %d for size %d", buffer.numEntries, size)
			}

			for sequence := 0; sequence <= 65535; sequence++ {
				entry := buffer.insert(uint16(sequence))
				if entry == nil {
					t.Fatalf("insert failed for sequence %d", sequence)
				}
				entry.sequence = uint16(sequence)
			}

			entry := buffer.insert(0)
			if entry == nil {
				t.Fatal("insert failed for sequence 0")
			}
			entry.sequence = 0

			kept := buffer.find(65535)
			if kept == nil || kept.sequence != 65535 {
				t.Fatal("expected sequence 65535 to stay in the buffer")
			}
			if buffer.find(0) == nil {
				t.Fatal("expected sequence 0 to stay in the buffer")
			}

			ack, ackBits := buffer.generateAckBits()
			if ack != 0 {
				t.Fatalf("expected ack 0, got %d", ack)
			}
			if ackBits&1 == 0 {
				t.Fatalf("expected sequence 0 to be acked, ack bits %#x", ackBits)
			}
			if ackBits&2 == 0 {
				t.Fatalf("expected sequence 65535 to be acked, ack bits %#x", ackBits)
			}

			replayed := buffer.insert(65535)
			if replayed == nil || replayed.sequence != 65535 {
				t.Fatal("expected replay of sequence 65535 to keep its entry")
			}
			if zero := buffer.find(0); zero == nil || zero.sequence != 0 {
				t.Fatal("expected sequence 0 to stay in the buffer after replay")
			}
		})
	}
}

func testPacketsAcrossSequenceWrap(t *testing.T, bufferSize int) {
	t.Helper()

	time := 100.0

	var sender *Endpoint
	var receiver *Endpoint
	var packet65535 []byte
	deliveries := 0

	transmit := func(id uint64, sequence uint16, packetData []byte) {
		if id == 0 {
			if sequence == 65535 && packet65535 == nil {
				packet65535 = append([]byte(nil), packetData...)
			}
			receiver.ReceivePacket(packetData)
			return
		}
		sender.ReceivePacket(packetData)
	}
	process := func(id uint64, sequence uint16, packetData []byte) bool {
		if id == 1 && sequence == 65535 {
			deliveries++
		}
		return true
	}

	senderConfig := DefaultConfig()
	receiverConfig := DefaultConfig()
	senderConfig.Name = "sender"
	senderConfig.ID = 0
	senderConfig.TransmitPacketFunction = transmit
	senderConfig.ProcessPacketFunction = process
	senderConfig.SentPacketsBufferSize = bufferSize
	senderConfig.ReceivedPacketsBufferSize = bufferSize
	receiverConfig.Name = "receiver"
	receiverConfig.ID = 1
	receiverConfig.TransmitPacketFunction = transmit
	receiverConfig.ProcessPacketFunction = process
	receiverConfig.SentPacketsBufferSize = bufferSize
	receiverConfig.ReceivedPacketsBufferSize = bufferSize

	var err error
	sender, err = NewEndpoint(&senderConfig, time)
	if err != nil {
		t.Fatalf("could not create sender endpoint: %v", err)
	}
	receiver, err = NewEndpoint(&receiverConfig, time)
	if err != nil {
		t.Fatalf("could not create receiver endpoint: %v", err)
	}

	packet := make([]byte, 8)
	for sender.NextPacketSequence() != 65535 {
		sender.SendPacket(packet)
	}
	sender.SendPacket(packet)
	if sender.NextPacketSequence() != 0 {
		t.Fatalf("expected sender sequence 0, got %d", sender.NextPacketSequence())
	}
	if deliveries != 1 {
		t.Fatalf("expected sequence 65535 to be delivered once, got %d", deliveries)
	}

	sender.SendPacket(packet)
	if deliveries != 1 {
		t.Fatalf("expected sequence 65535 to be delivered once, got %d", deliveries)
	}
	if packet65535 == nil {
		t.Fatal("expected packet 65535 to be captured")
	}

	receiver.ReceivePacket(packet65535)
	if deliveries != 1 {
		t.Fatalf("expected sequence 65535 to be delivered once, got %d", deliveries)
	}

	receiver.SendPacket(packet)

	acked := false
	for _, ack := range sender.Acks() {
		if ack == 65535 {
			acked = true
			break
		}
	}
	if !acked {
		t.Fatal("expected ack for sequence 65535")
	}
}

func TestPacketsAcrossSequenceWrap(t *testing.T) {
	t.Run("3", func(t *testing.T) {
		testPacketsAcrossSequenceWrap(t, 3)
	})

	t.Run("255", func(t *testing.T) {
		testPacketsAcrossSequenceWrap(t, 255)
	})
}
