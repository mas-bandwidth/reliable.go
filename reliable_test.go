package reliable

import (
	"fmt"
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

// newEndpointOrError creates an endpoint, turning a panic (for example an
// out-of-range make on a config that should have been refused) into an error so
// a validation test fails cleanly instead of crashing the test binary.
func newEndpointOrError(config *Config) (endpoint *Endpoint, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			endpoint = nil
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return NewEndpoint(config, 0)
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
			name:    "packet header plus max packet size overflow",
			wantErr: "packet header size plus max packet size does not fit a packet length",
			modify: func(config *Config) {
				maxInt := int(^uint(0) >> 1)
				config.MaxFragments = 1
				config.FragmentSize = maxInt - MaxPacketHeaderBytes - FragmentHeaderBytes
				config.MaxPacketSize = maxInt - MaxPacketHeaderBytes - FragmentHeaderBytes - 1
				config.FragmentAbove = 1
				config.PacketHeaderSize = MaxPacketHeaderBytes + FragmentHeaderBytes + 1
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
			endpoint, err := newEndpointOrError(&config)
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

	for _, field := range []struct {
		name string
		set  func(*Config, int)
		word string
	}{
		{"sent packets buffer size", func(config *Config, value int) { config.SentPacketsBufferSize = value }, "sent packets buffer size"},
		{"received packets buffer size", func(config *Config, value int) { config.ReceivedPacketsBufferSize = value }, "received packets buffer size"},
		{"fragment reassembly buffer size", func(config *Config, value int) { config.FragmentReassemblyBufferSize = value }, "fragment reassembly buffer size"},
	} {
		for _, value := range []int{100, 32768, 65535, 65536} {
			t.Run(fmt.Sprintf("%s %d is refused", field.name, value), func(t *testing.T) {
				config := endpointConfigForValidation()
				field.set(&config, value)
				endpoint, err := newEndpointOrError(&config)
				if err == nil {
					if endpoint != nil {
						t.Fatalf("expected %s %d to be refused", field.name, value)
					}
					t.Fatalf("expected %s %d to return an error", field.name, value)
				}
				wantErr := field.word + " must be a power of two no larger than 16384"
				if !strings.Contains(err.Error(), wantErr) {
					t.Fatalf("expected error containing %q, got %q", wantErr, err)
				}
			})
		}
	}

	for _, field := range []struct {
		name string
		set  func(*Config, int)
	}{
		{"sent packets buffer size", func(config *Config, value int) { config.SentPacketsBufferSize = value }},
		{"received packets buffer size", func(config *Config, value int) { config.ReceivedPacketsBufferSize = value }},
		{"fragment reassembly buffer size", func(config *Config, value int) { config.FragmentReassemblyBufferSize = value }},
	} {
		for _, value := range []int{256, 16384} {
			t.Run(fmt.Sprintf("%s %d is acked", field.name, value), func(t *testing.T) {
				context := newTestContext()
				newTestEndpointPair(t, context, 100.0, func(senderConfig, receiverConfig *Config) {
					field.set(senderConfig, value)
					field.set(receiverConfig, value)
				})

				context.sender.SendPacket([]byte{0x00})
				context.receiver.SendPacket([]byte{0x00})

				if len(context.sender.Acks()) == 0 {
					t.Fatalf("expected a one-byte send with %s %d to be acked", field.name, value)
				}
			})
		}
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

// TestSequenceBufferSizesMustBePowersOfTwo pins the rule C applies to the three
// sequence buffers: each size must be a power of two in [1,16384]. A size that
// is not makes sequence 0 stale on a cold buffer, and a size that does not
// divide 65536 aliases after the sequence wraps.
func TestSequenceBufferSizesMustBePowersOfTwo(t *testing.T) {
	fields := []struct {
		name string
		set  func(*Config, int)
	}{
		{"sent packets buffer size", func(config *Config, value int) { config.SentPacketsBufferSize = value }},
		{"received packets buffer size", func(config *Config, value int) { config.ReceivedPacketsBufferSize = value }},
		{"fragment reassembly buffer size", func(config *Config, value int) { config.FragmentReassemblyBufferSize = value }},
	}

	sizes := []int{0, 1, 2, 3, 100, 255, 256, 257, 16383, 16384, 16385, 32768, 65535, 65536, 65537}

	for _, field := range fields {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s %d", field.name, size), func(t *testing.T) {
				config := endpointConfigForValidation()
				field.set(&config, size)
				endpoint, err := newEndpointOrError(&config)

				valid := size > 0 && size <= 16384 && size&(size-1) == 0
				if valid {
					if err != nil {
						t.Fatalf("expected %s %d to be accepted, got %v", field.name, size, err)
					}
					if endpoint == nil {
						t.Fatalf("expected an endpoint for %s %d", field.name, size)
					}
					return
				}

				if err == nil {
					t.Fatalf("expected %s %d to be refused", field.name, size)
				}
				wantErr := field.name + " must be a power of two no larger than 16384"
				if !strings.Contains(err.Error(), wantErr) {
					t.Fatalf("expected error containing %q, got %q", wantErr, err)
				}
			})
		}
	}

	t.Run("ack buffer and rtt history stay on positivity", func(t *testing.T) {
		config := endpointConfigForValidation()
		config.AckBufferSize = 100
		config.RTTHistorySize = 100
		endpoint, err := NewEndpoint(&config, 0)
		if err != nil {
			t.Fatalf("a non-power-of-two ack buffer or rtt history was refused: %v", err)
		}
		if endpoint == nil {
			t.Fatal("expected an endpoint for a non-power-of-two ack buffer and rtt history")
		}
	})
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
	for i := 0; i < testAcksNumIterations/2; i++ {
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
	for i := 0; i < testAcksNumIterations/2; i++ {
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
	for i := 0; i < testAcksNumIterations/2; i++ {
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
	for i := 0; i < testAcksNumIterations/2; i++ {
		expected := (i+1)%2 != 0
		if receiverAckedPacket[i] != expected {
			t.Fatalf("expected receiver packet %d acked to be %v", i, expected)
		}
	}
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

// TestReceiveEmptyPacketIsCountedInvalid pins the behaviour the C reference
// gained in 190bce5b: a zero length packet is refused and counted invalid
// before any byte is read, and the process callback does not run. A nil slice
// has length zero and is covered by the same check.
func TestReceiveEmptyPacketIsCountedInvalid(t *testing.T) {
	time := 100.0

	processed := 0

	config := DefaultConfig()
	config.Name = "receiver"

	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
		processed++
		return true
	}

	endpoint, err := NewEndpoint(&config, time)
	if err != nil {
		t.Fatalf("could not create endpoint: %v", err)
	}

	for i, packetData := range [][]byte{nil, {}} {
		before := endpoint.Counters()[CounterNumPacketsInvalid]

		endpoint.ReceivePacket(packetData)

		after := endpoint.Counters()[CounterNumPacketsInvalid]
		if after != before+1 {
			t.Fatalf("input %d: expected the invalid counter to rise by one, rose by %d", i, after-before)
		}
	}

	if processed != 0 {
		t.Fatalf("expected the process callback not to run, ran %d times", processed)
	}
}

// TestUnfinishedFragmentDoesNotStalePackets pins that a lone fragment does not
// move the received-packets window before its packet is reassembled and
// accepted. A fragment that fails the stale test is refused and counted
// instead. The cases mirror the C reference test for the same fix:
//
//	A: a last fragment inside the window (sequence 512) leaves a genuine
//	   sequence 0 deliverable and counts no stale packets.
//	B: a last fragment outside the window (sequence 32512) is refused before
//	   any reassembly entry exists, and sequence 0 is still delivered.
//	C: an in-order fragmented packet (1.5 x fragment size) at sequence 0 still
//	   reassembles from two fragments and delivers at full size.
func TestUnfinishedFragmentDoesNotStalePackets(t *testing.T) {
	time := 100.0

	// a lone last fragment of a 2-fragment packet: prefix 1, sequence little
	// endian, fragment id 1, count-1 1, then payloadBytes payload bytes.
	fragmentAt := func(sequence uint16, payloadBytes int) []byte {
		fragment := []byte{1, byte(sequence), byte(sequence >> 8), 1, 1}
		for i := 0; i < payloadBytes; i++ {
			fragment = append(fragment, byte(i+1))
		}
		return fragment
	}

	runFragmentCase := func(t *testing.T, fragmentSequence uint16, payloadBytes int) (*testContext, bool, uint16) {
		t.Helper()

		context := newTestContext()

		var delivered bool
		var deliveredSequence uint16

		newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
			receiverConfig.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
				if !delivered {
					delivered = true
					deliveredSequence = sequence
				}
				return true
			}
		})

		// the lone fragment must not move the received window before its packet is accepted

		context.receiver.ReceivePacket(fragmentAt(fragmentSequence, payloadBytes))

		// a genuine packet at sequence 0 from the sender must still be delivered

		context.sender.SendPacket([]byte("data"))

		return context, delivered, deliveredSequence
	}

	t.Run("case A: fragment inside the window leaves sequence 0 deliverable", func(t *testing.T) {
		context, delivered, deliveredSequence := runFragmentCase(t, 512, 0)

		if !delivered || deliveredSequence != 0 {
			t.Fatalf("expected genuine sequence 0 to be delivered, got delivered=%v sequence=%d", delivered, deliveredSequence)
		}

		counters := context.receiver.Counters()
		if counters[CounterNumPacketsStale] != 0 {
			t.Fatalf("expected 0 stale packets, got %d", counters[CounterNumPacketsStale])
		}
		if counters[CounterNumFragmentsInvalid] != 0 {
			t.Fatalf("expected 0 invalid fragments, got %d", counters[CounterNumFragmentsInvalid])
		}
	})

	t.Run("case B: fragment outside the window is refused before reassembly", func(t *testing.T) {
		context, delivered, deliveredSequence := runFragmentCase(t, 32512, 0)

		if context.receiver.fragmentReassembly.exists(32512) {
			t.Fatal("expected no reassembly entry for refused fragment 32512")
		}
		if !delivered || deliveredSequence != 0 {
			t.Fatalf("expected genuine sequence 0 to be delivered, got delivered=%v sequence=%d", delivered, deliveredSequence)
		}

		counters := context.receiver.Counters()
		if counters[CounterNumPacketsStale] != 0 {
			t.Fatalf("expected 0 stale packets, got %d", counters[CounterNumPacketsStale])
		}
		if counters[CounterNumFragmentsInvalid] != 1 {
			t.Fatalf("expected 1 invalid fragment, got %d", counters[CounterNumFragmentsInvalid])
		}
	})

	t.Run("case C: in-order fragmented packet still delivers at full size", func(t *testing.T) {
		context := newTestContext()

		deliveredBytes := -1

		newTestEndpointPair(t, context, time, func(senderConfig, receiverConfig *Config) {
			receiverConfig.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool {
				deliveredBytes = len(packetData)
				return true
			}
		})

		fragmentSize := context.sender.config.FragmentSize
		packet := make([]byte, fragmentSize+fragmentSize/2)

		context.sender.SendPacket(packet)

		if deliveredBytes != len(packet) {
			t.Fatalf("expected fragmented packet to deliver %d bytes, got %d", len(packet), deliveredBytes)
		}
	})
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

func TestPackets(t *testing.T) {
	time := 100.0

	context := newTestContext()

	processPacketValidate := func(id uint64, sequence uint16, packetData []byte) bool {
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

	for i := 0; i < 16; i++ {
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

// TestUnfinishedReassemblyRespectsByteBudget pins that a first fragment whose
// buffer would push unfinished reassemblies past FragmentReassemblyByteBudget
// is refused and counted, so five bytes on the wire cannot hold a full
// reassembly buffer per slot. It mirrors the C reference test
// test_unfinished_reassembly_respects_byte_budget: 64 last fragments for
// sequences 0..63, each claiming 16 fragments, with the budget at 64 KiB.
func TestUnfinishedReassemblyRespectsByteBudget(t *testing.T) {
	time := 100.0

	config := DefaultConfig()
	config.Name = "receiver"
	config.FragmentReassemblyByteBudget = 64 * 1024
	config.TransmitPacketFunction = func(id uint64, sequence uint16, packetData []byte) {}
	config.ProcessPacketFunction = func(id uint64, sequence uint16, packetData []byte) bool { return true }

	endpoint, err := NewEndpoint(&config, time)
	if err != nil {
		t.Fatalf("could not create endpoint: %v", err)
	}

	budget := config.FragmentReassemblyByteBudget

	// one slot is the room a first fragment allocates for a full packet,
	// the 8 spare bytes the C reference reserves included.

	slotBytes := MaxPacketHeaderBytes + config.MaxFragments*config.FragmentSize + 8

	heldBytes := func() int {
		total := 0
		for i := 0; i < endpoint.fragmentReassembly.numEntries; i++ {
			if entry := endpoint.fragmentReassembly.atIndex(i); entry != nil {
				total += len(entry.packetData)
			}
		}
		return total
	}

	for sequence := 0; sequence < 64; sequence++ {
		fragment := make([]byte, FragmentHeaderBytes+config.FragmentSize)
		fragment[0] = 1
		fragment[1] = byte(sequence)
		fragment[2] = byte(sequence >> 8)
		fragment[3] = byte(config.MaxFragments - 1)
		fragment[4] = byte(config.MaxFragments - 1)
		for i := FragmentHeaderBytes; i < len(fragment); i++ {
			fragment[i] = 0x5A
		}

		endpoint.ReceivePacket(fragment)

		if endpoint.reassemblyBytes > budget+slotBytes {
			t.Fatalf("sequence %d: endpoint reassembly bytes %d exceed the budget plus one slot %d", sequence, endpoint.reassemblyBytes, budget+slotBytes)
		}
		held := heldBytes()
		if held != endpoint.reassemblyBytes {
			t.Fatalf("sequence %d: endpoint reassembly bytes %d do not match the sum of held buffers %d", sequence, endpoint.reassemblyBytes, held)
		}
		if held > budget+slotBytes {
			t.Fatalf("sequence %d: held reassembly bytes %d exceed the budget plus one slot %d", sequence, held, budget+slotBytes)
		}
	}

	if endpoint.reassemblyBytes == 0 {
		t.Fatal("expected at least one unfinished reassembly to be held within the budget")
	}
	if endpoint.reassemblyBytes > budget {
		t.Fatalf("expected reassembly bytes within the budget %d, got %d", budget, endpoint.reassemblyBytes)
	}

	counters := endpoint.Counters()
	if counters[CounterNumFragmentsInvalid] == 0 {
		t.Fatal("expected some first fragments to be refused by the byte budget")
	}

	endpoint.Reset()

	if endpoint.reassemblyBytes != 0 {
		t.Fatalf("expected reassembly bytes 0 after reset, got %d", endpoint.reassemblyBytes)
	}
	if held := heldBytes(); held != 0 {
		t.Fatalf("expected no held reassembly buffers after reset, got %d bytes", held)
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
