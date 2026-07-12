package reliable

import (
	"errors"
	"math"
)

// TransmitPacketFunction is called to send a packet: (context, id, sequence,
// packetData). The packet data is only valid for the duration of the call, so
// copy it if you need to keep it. It must not send packets on the same
// endpoint.
type TransmitPacketFunction func(context any, id uint64, sequence uint16, packetData []byte)

// ProcessPacketFunction is called when a packet is received: (context, id,
// sequence, packetData). Return true to accept and ack the packet, false to
// reject it (rejected packets are not acked and may be processed again if they
// arrive again). The packet data is only valid for the duration of the call,
// so copy it if you need to keep it.
type ProcessPacketFunction func(context any, id uint64, sequence uint16, packetData []byte) bool

// Config configures an Endpoint.
type Config struct {
	Name                         string                 // name of the endpoint. used in log output
	Context                      any                    // passed to the transmit and process packet callbacks
	ID                           uint64                 // id of the endpoint. passed to callbacks so shared callbacks can tell endpoints apart
	MaxPacketSize                int                    // maximum packet size that can be sent or received (bytes)
	FragmentAbove                int                    // packets larger than this many bytes are sent as fragments
	MaxFragments                 int                    // maximum number of fragments per-packet. 256 max. must cover MaxPacketSize / FragmentSize
	FragmentSize                 int                    // size of each fragment (bytes)
	AckBufferSize                int                    // maximum number of acks buffered between calls to Endpoint.ClearAcks
	SentPacketsBufferSize        int                    // number of sent packets tracked for acks, packet loss and bandwidth stats
	ReceivedPacketsBufferSize    int                    // number of received packets tracked. also the window for stale and duplicate packet rejection
	FragmentReassemblyBufferSize int                    // number of packets that can be under reassembly from fragments at the same time
	RTTSmoothingFactor           float64                // exponential smoothing factor for the rtt moving average
	RTTHistorySize               int                    // number of rtt samples kept for min/max/avg rtt and jitter
	PacketLossSmoothingFactor    float64                // exponential smoothing factor for packet loss
	BandwidthSmoothingFactor     float64                // exponential smoothing factor for bandwidth
	PacketHeaderSize             int                    // assumed network header overhead per-packet, used only for bandwidth stats. 28 = IPv4 + UDP
	TransmitPacketFunction       TransmitPacketFunction // called to send a packet
	ProcessPacketFunction        ProcessPacketFunction  // called when a packet is received
}

// DefaultConfig returns a config with sensible defaults for a client/server
// game exchanging packets at 60HZ.
func DefaultConfig() Config {
	return Config{
		Name:                         "endpoint",
		MaxPacketSize:                16 * 1024,
		FragmentAbove:                1024,
		MaxFragments:                 16,
		FragmentSize:                 1024,
		AckBufferSize:                256,
		SentPacketsBufferSize:        256,
		ReceivedPacketsBufferSize:    256,
		FragmentReassemblyBufferSize: 64,
		RTTSmoothingFactor:           0.0025,
		RTTHistorySize:               512,
		PacketLossSmoothingFactor:    0.1,
		BandwidthSmoothingFactor:     0.1,
		PacketHeaderSize:             28, // note: UDP over IPv4 = 20 + 8 bytes, UDP over IPv6 = 40 + 8 bytes
	}
}

type sentPacketData struct {
	time        float64
	acked       bool
	packetBytes uint32
}

type receivedPacketData struct {
	time        float64
	packetBytes uint32
}

type fragmentReassemblyData struct {
	sequence             uint16
	ack                  uint16
	ackBits              uint32
	numFragmentsReceived int
	numFragmentsTotal    int
	packetData           []byte
	packetBytes          int
	packetHeaderBytes    int
	fragmentReceived     [256]bool
}

// fragmentReassemblyCleanup drops the reference to the reassembly packet
// buffer so it can be garbage collected.
func fragmentReassemblyCleanup(reassemblyData *fragmentReassemblyData) {
	*reassemblyData = fragmentReassemblyData{}
}

// Endpoint is one side of a connection: a client has one, a server has one
// per client slot. Endpoints are not thread safe: use one endpoint per
// goroutine or protect each endpoint with your own lock.
type Endpoint struct {
	config                Config
	time                  float64
	rtt                   float64
	rttMin                float64
	rttMax                float64
	rttAvg                float64
	jitterAvgVsMinRTT     float64
	jitterMaxVsMinRTT     float64
	jitterStddevVsAvgRTT  float64
	packetLoss            float64
	sentBandwidthKbps     float64
	receivedBandwidthKbps float64
	ackedBandwidthKbps    float64
	acks                  []uint16
	sequence              uint16
	rttHistoryBuffer      []float64
	transmitBuffer        []byte
	sentPackets           *sequenceBuffer[sentPacketData]
	receivedPackets       *sequenceBuffer[receivedPacketData]
	fragmentReassembly    *sequenceBuffer[fragmentReassemblyData]
	counters              [NumCounters]uint64
}

// NewEndpoint creates an endpoint. The time is your current time in seconds.
func NewEndpoint(config *Config, time float64) (*Endpoint, error) {
	if config == nil {
		return nil, errors.New("config must not be nil")
	}
	if config.MaxPacketSize <= 0 {
		return nil, errors.New("max packet size must be positive")
	}
	if config.FragmentAbove <= 0 {
		return nil, errors.New("fragment above must be positive")
	}
	if config.MaxFragments <= 0 || config.MaxFragments > 256 {
		return nil, errors.New("max fragments must be in [1,256]")
	}
	if config.FragmentSize <= 0 {
		return nil, errors.New("fragment size must be positive")
	}
	if config.AckBufferSize <= 0 {
		return nil, errors.New("ack buffer size must be positive")
	}
	if config.SentPacketsBufferSize <= 0 {
		return nil, errors.New("sent packets buffer size must be positive")
	}
	if config.ReceivedPacketsBufferSize <= 0 {
		return nil, errors.New("received packets buffer size must be positive")
	}
	if config.FragmentReassemblyBufferSize <= 0 {
		return nil, errors.New("fragment reassembly buffer size must be positive")
	}
	if config.RTTHistorySize <= 0 {
		return nil, errors.New("rtt history size must be positive")
	}
	if config.TransmitPacketFunction == nil {
		return nil, errors.New("transmit packet function must not be nil")
	}
	if config.ProcessPacketFunction == nil {
		return nil, errors.New("process packet function must not be nil")
	}

	endpoint := &Endpoint{
		config: *config,
		time:   time,
	}

	endpoint.acks = make([]uint16, 0, config.AckBufferSize)

	endpoint.sentPackets = newSequenceBuffer[sentPacketData](config.SentPacketsBufferSize)
	endpoint.receivedPackets = newSequenceBuffer[receivedPacketData](config.ReceivedPacketsBufferSize)
	endpoint.fragmentReassembly = newSequenceBuffer[fragmentReassemblyData](config.FragmentReassemblyBufferSize)

	endpoint.rttHistoryBuffer = make([]float64, config.RTTHistorySize)
	for i := range endpoint.rttHistoryBuffer {
		endpoint.rttHistoryBuffer[i] = -1.0
	}

	// scratch buffer for outgoing packets, so the send path doesn't allocate. sized for whichever is larger: a regular packet or a fragment

	transmitBufferSize := config.MaxPacketSize + MaxPacketHeaderBytes
	fragmentTransmitBufferSize := FragmentHeaderBytes + MaxPacketHeaderBytes + config.FragmentSize
	if fragmentTransmitBufferSize > transmitBufferSize {
		transmitBufferSize = fragmentTransmitBufferSize
	}

	endpoint.transmitBuffer = make([]byte, transmitBufferSize)

	return endpoint, nil
}

// NextPacketSequence returns the sequence number the next sent packet will
// have. Use it to map acked sequence numbers back to the contents of packets
// you sent.
func (endpoint *Endpoint) NextPacketSequence() uint16 {
	return endpoint.sequence
}

// SendPacket sends a packet. The packet is handed to the transmit packet
// callback, split into fragments first if larger than config.FragmentAbove.
func (endpoint *Endpoint) SendPacket(packetData []byte) {
	packetBytes := len(packetData)

	if packetBytes == 0 {
		panic("reliable: packet data must not be empty")
	}

	if packetBytes > endpoint.config.MaxPacketSize {
		logPrintf(LogLevelError, "[%s] packet too large to send. packet is %d bytes, maximum is %d\n",
			endpoint.config.Name, packetBytes, endpoint.config.MaxPacketSize)
		endpoint.counters[CounterNumPacketsTooLargeToSend]++
		return
	}

	sequence := endpoint.sequence
	endpoint.sequence++

	ack, ackBits := endpoint.receivedPackets.generateAckBits()

	logPrintf(LogLevelDebug, "[%s] sending packet %d\n", endpoint.config.Name, sequence)

	sentPacket := endpoint.sentPackets.insert(sequence)

	sentPacket.time = endpoint.time
	sentPacket.packetBytes = uint32(endpoint.config.PacketHeaderSize + packetBytes)
	sentPacket.acked = false

	if packetBytes <= endpoint.config.FragmentAbove {
		// regular packet

		logPrintf(LogLevelDebug, "[%s] sending packet %d without fragmentation\n", endpoint.config.Name, sequence)

		transmitPacketData := endpoint.transmitBuffer

		packetHeaderBytes := writePacketHeader(transmitPacketData, sequence, ack, ackBits)

		copy(transmitPacketData[packetHeaderBytes:], packetData)

		endpoint.config.TransmitPacketFunction(endpoint.config.Context, endpoint.config.ID, sequence, transmitPacketData[:packetHeaderBytes+packetBytes])
	} else {
		// fragmented packet

		var packetHeader [MaxPacketHeaderBytes]byte

		packetHeaderBytes := writePacketHeader(packetHeader[:], sequence, ack, ackBits)

		numFragments := packetBytes / endpoint.config.FragmentSize
		if packetBytes%endpoint.config.FragmentSize != 0 {
			numFragments++
		}

		logPrintf(LogLevelDebug, "[%s] sending packet %d as %d fragments\n", endpoint.config.Name, sequence, numFragments)

		fragmentPacketData := endpoint.transmitBuffer

		q := 0
		end := packetBytes

		for fragmentID := 0; fragmentID < numFragments; fragmentID++ {
			p := 0

			fragmentPacketData[p] = 1
			p++
			fragmentPacketData[p] = byte(sequence)
			fragmentPacketData[p+1] = byte(sequence >> 8)
			p += 2
			fragmentPacketData[p] = byte(fragmentID)
			p++
			fragmentPacketData[p] = byte(numFragments - 1)
			p++

			if fragmentID == 0 {
				copy(fragmentPacketData[p:], packetHeader[:packetHeaderBytes])
				p += packetHeaderBytes
			}

			bytesToCopy := endpoint.config.FragmentSize
			if q+bytesToCopy > end {
				bytesToCopy = end - q
			}

			copy(fragmentPacketData[p:], packetData[q:q+bytesToCopy])

			p += bytesToCopy
			q += bytesToCopy

			endpoint.config.TransmitPacketFunction(endpoint.config.Context, endpoint.config.ID, sequence, fragmentPacketData[:p])

			endpoint.counters[CounterNumFragmentsSent]++
		}
	}

	endpoint.counters[CounterNumPacketsSent]++
}

// storeFragmentData copies a fragment payload into the reassembly buffer.
// Fragment 0 also carries the packet header, which is re-encoded canonically
// just before the start of the packet payload so the reassembled packet can be
// fed back through ReceivePacket.
func (endpoint *Endpoint) storeFragmentData(reassemblyData *fragmentReassemblyData, sequence uint16, ack uint16, ackBits uint32, fragmentID int, fragmentSize int, fragmentData []byte) {
	fragmentBytes := len(fragmentData)

	if fragmentID == 0 {
		var packetHeader [MaxPacketHeaderBytes]byte

		reassemblyData.packetHeaderBytes = writePacketHeader(packetHeader[:], sequence, ack, ackBits)

		copy(reassemblyData.packetData[MaxPacketHeaderBytes-reassemblyData.packetHeaderBytes:], packetHeader[:reassemblyData.packetHeaderBytes])

		fragmentData = fragmentData[reassemblyData.packetHeaderBytes:]
		fragmentBytes -= reassemblyData.packetHeaderBytes
	}

	if fragmentID == reassemblyData.numFragmentsTotal-1 {
		reassemblyData.packetBytes = (reassemblyData.numFragmentsTotal-1)*fragmentSize + fragmentBytes
	}

	offset := MaxPacketHeaderBytes + fragmentID*fragmentSize
	endOffset := offset + fragmentBytes
	maxSize := MaxPacketHeaderBytes + reassemblyData.numFragmentsTotal*fragmentSize

	if fragmentBytes < 0 || endOffset > maxSize {
		logPrintf(LogLevelDebug, "[reliable] invalid fragment size %d (would write past %d/%d)\n", fragmentBytes, endOffset, maxSize)
		return
	}

	copy(reassemblyData.packetData[offset:], fragmentData[:fragmentBytes])
}

// ReceivePacket processes a packet received from your socket. Valid packets
// are passed to the process packet callback. Stale and duplicate packets are
// dropped.
func (endpoint *Endpoint) ReceivePacket(packetData []byte) {
	packetBytes := len(packetData)

	if packetBytes == 0 {
		logPrintf(LogLevelDebug, "[%s] ignoring empty packet\n", endpoint.config.Name)
		endpoint.counters[CounterNumPacketsInvalid]++
		return
	}

	if packetBytes > endpoint.config.MaxPacketSize+MaxPacketHeaderBytes+FragmentHeaderBytes {
		logPrintf(LogLevelDebug, "[%s] packet too large to receive. packet is at least %d bytes, maximum is %d\n",
			endpoint.config.Name, packetBytes-(MaxPacketHeaderBytes+FragmentHeaderBytes), endpoint.config.MaxPacketSize)
		endpoint.counters[CounterNumPacketsTooLargeToReceive]++
		return
	}

	prefixByte := packetData[0]

	if (prefixByte & 1) == 0 {
		// regular packet

		endpoint.counters[CounterNumPacketsReceived]++

		sequence, ack, ackBits, packetHeaderBytes := readPacketHeader(endpoint.config.Name, packetData)
		if packetHeaderBytes < 0 {
			logPrintf(LogLevelDebug, "[%s] ignoring invalid packet. could not read packet header\n", endpoint.config.Name)
			endpoint.counters[CounterNumPacketsInvalid]++
			return
		}

		packetPayloadBytes := packetBytes - packetHeaderBytes

		if packetPayloadBytes > endpoint.config.MaxPacketSize {
			logPrintf(LogLevelError, "[%s] packet too large to receive. packet is at %d bytes, maximum is %d\n",
				endpoint.config.Name, packetPayloadBytes, endpoint.config.MaxPacketSize)
			endpoint.counters[CounterNumPacketsTooLargeToReceive]++
			return
		}

		if !endpoint.receivedPackets.testInsert(sequence) {
			logPrintf(LogLevelDebug, "[%s] ignoring stale packet %d\n", endpoint.config.Name, sequence)
			endpoint.counters[CounterNumPacketsStale]++
			return
		}

		if endpoint.receivedPackets.exists(sequence) {
			logPrintf(LogLevelDebug, "[%s] ignoring duplicate packet %d\n", endpoint.config.Name, sequence)
			endpoint.counters[CounterNumPacketsDuplicate]++
			return
		}

		logPrintf(LogLevelDebug, "[%s] processing packet %d\n", endpoint.config.Name, sequence)

		if endpoint.config.ProcessPacketFunction(endpoint.config.Context, endpoint.config.ID, sequence, packetData[packetHeaderBytes:]) {
			logPrintf(LogLevelDebug, "[%s] process packet %d successful\n", endpoint.config.Name, sequence)

			receivedPacket := endpoint.receivedPackets.insert(sequence)

			endpoint.fragmentReassembly.advanceWithCleanup(sequence, fragmentReassemblyCleanup)

			receivedPacket.time = endpoint.time
			receivedPacket.packetBytes = uint32(endpoint.config.PacketHeaderSize + packetBytes)

			for i := 0; i < 32; i++ {
				if ackBits&1 != 0 {
					ackSequence := ack - uint16(i)

					sentPacket := endpoint.sentPackets.find(ackSequence)

					if sentPacket != nil && !sentPacket.acked {
						if len(endpoint.acks) < endpoint.config.AckBufferSize {
							logPrintf(LogLevelDebug, "[%s] acked packet %d\n", endpoint.config.Name, ackSequence)
							endpoint.acks = append(endpoint.acks, ackSequence)
							endpoint.counters[CounterNumPacketsAcked]++
							sentPacket.acked = true

							rtt := (endpoint.time - sentPacket.time) * 1000.0

							index := int(ackSequence) % endpoint.config.RTTHistorySize

							endpoint.rttHistoryBuffer[index] = rtt

							if (endpoint.rtt == 0.0 && rtt > 0.0) || math.Abs(endpoint.rtt-rtt) < 0.00001 {
								endpoint.rtt = rtt
							} else {
								endpoint.rtt += (rtt - endpoint.rtt) * endpoint.config.RTTSmoothingFactor
							}
						} else {
							logPrintf(LogLevelError, "[%s] ack buffer is full. dropped ack for packet %d. make sure you call Endpoint.ClearAcks\n",
								endpoint.config.Name, ackSequence)
						}
					}
				}
				ackBits >>= 1
			}
		} else {
			logPrintf(LogLevelError, "[%s] process packet failed\n", endpoint.config.Name)
		}
	} else {
		// fragment packet

		fragmentID, numFragments, _, sequence, ack, ackBits, fragmentHeaderBytes := readFragmentHeader(endpoint.config.Name, packetData, endpoint.config.MaxFragments, endpoint.config.FragmentSize)

		if fragmentHeaderBytes < 0 {
			logPrintf(LogLevelDebug, "[%s] ignoring invalid fragment. could not read fragment header\n", endpoint.config.Name)
			endpoint.counters[CounterNumFragmentsInvalid]++
			return
		}

		if endpoint.receivedPackets.exists(sequence) {
			logPrintf(LogLevelDebug, "[%s] ignoring fragment %d of packet %d. packet already received\n",
				endpoint.config.Name, fragmentID, sequence)
			return
		}

		reassemblyData := endpoint.fragmentReassembly.find(sequence)

		if reassemblyData == nil {
			reassemblyData = endpoint.fragmentReassembly.insertWithCleanup(sequence, fragmentReassemblyCleanup)

			if reassemblyData == nil {
				logPrintf(LogLevelError, "[%s] ignoring invalid fragment. could not insert in reassembly buffer (stale)\n", endpoint.config.Name)
				endpoint.counters[CounterNumFragmentsInvalid]++
				return
			}

			endpoint.receivedPackets.advance(sequence)

			packetBufferSize := MaxPacketHeaderBytes + numFragments*endpoint.config.FragmentSize

			reassemblyData.sequence = sequence
			reassemblyData.ack = 0
			reassemblyData.ackBits = 0
			reassemblyData.numFragmentsReceived = 0
			reassemblyData.numFragmentsTotal = numFragments
			reassemblyData.packetData = make([]byte, packetBufferSize)
			reassemblyData.packetBytes = 0
			reassemblyData.packetHeaderBytes = 0
			reassemblyData.fragmentReceived = [256]bool{}
		}

		if numFragments != reassemblyData.numFragmentsTotal {
			logPrintf(LogLevelError, "[%s] ignoring invalid fragment. fragment count mismatch. expected %d, got %d\n",
				endpoint.config.Name, reassemblyData.numFragmentsTotal, numFragments)
			endpoint.counters[CounterNumFragmentsInvalid]++
			return
		}

		if reassemblyData.fragmentReceived[fragmentID] {
			logPrintf(LogLevelError, "[%s] ignoring fragment %d of packet %d. fragment already received\n",
				endpoint.config.Name, fragmentID, sequence)
			return
		}

		logPrintf(LogLevelDebug, "[%s] received fragment %d of packet %d (%d/%d)\n",
			endpoint.config.Name, fragmentID, sequence, reassemblyData.numFragmentsReceived+1, numFragments)

		reassemblyData.numFragmentsReceived++
		reassemblyData.fragmentReceived[fragmentID] = true

		endpoint.storeFragmentData(reassemblyData, sequence, ack, ackBits, fragmentID, endpoint.config.FragmentSize, packetData[fragmentHeaderBytes:])

		if reassemblyData.numFragmentsReceived == reassemblyData.numFragmentsTotal {
			logPrintf(LogLevelDebug, "[%s] completed reassembly of packet %d\n", endpoint.config.Name, sequence)

			start := MaxPacketHeaderBytes - reassemblyData.packetHeaderBytes
			finish := MaxPacketHeaderBytes + reassemblyData.packetBytes

			endpoint.ReceivePacket(reassemblyData.packetData[start:finish])

			endpoint.fragmentReassembly.removeWithCleanup(sequence, fragmentReassemblyCleanup)
		}

		endpoint.counters[CounterNumFragmentsReceived]++
	}
}

// Acks returns the sequence numbers of sent packets acked since the last call
// to ClearAcks. The returned slice aliases internal storage and is only valid
// until the next call to ClearAcks or Reset.
func (endpoint *Endpoint) Acks() []uint16 {
	return endpoint.acks
}

// ClearAcks clears the ack array. Call this once per-frame after processing
// acks. If you don't, the ack buffer fills up and new acks are dropped.
func (endpoint *Endpoint) ClearAcks() {
	endpoint.acks = endpoint.acks[:0]
}

// Reset resets the endpoint to its initial state: acks, counters, sequence
// number and all tracking buffers are cleared.
func (endpoint *Endpoint) Reset() {
	endpoint.acks = endpoint.acks[:0]
	endpoint.sequence = 0

	endpoint.counters = [NumCounters]uint64{}

	for i := 0; i < endpoint.config.FragmentReassemblyBufferSize; i++ {
		reassemblyData := endpoint.fragmentReassembly.atIndex(i)

		if reassemblyData != nil && reassemblyData.packetData != nil {
			reassemblyData.packetData = nil
		}
	}

	endpoint.sentPackets.reset()
	endpoint.receivedPackets.reset()
	endpoint.fragmentReassembly.reset()
}

// Update updates rtt, jitter, packet loss and bandwidth stats. Call once
// per-frame with the current time in seconds.
func (endpoint *Endpoint) Update(time float64) {
	endpoint.time = time

	// calculate min and max rtt
	{
		minRTT := 10000.0
		maxRTT := 0.0
		sumRTT := 0.0
		count := 0
		for i := 0; i < endpoint.config.RTTHistorySize; i++ {
			rtt := endpoint.rttHistoryBuffer[i]
			if rtt >= 0.0 {
				if rtt < minRTT {
					minRTT = rtt
				}
				if rtt > maxRTT {
					maxRTT = rtt
				}
				sumRTT += rtt
				count++
			}
		}
		if minRTT == 10000.0 {
			minRTT = 0.0
		}
		endpoint.rttMin = minRTT
		endpoint.rttMax = maxRTT
		if count > 0 {
			endpoint.rttAvg = sumRTT / float64(count)
		} else {
			endpoint.rttAvg = 0.0
		}
	}

	// calculate average jitter vs. min rtt
	{
		sum := 0.0
		count := 0
		for i := 0; i < endpoint.config.RTTHistorySize; i++ {
			if endpoint.rttHistoryBuffer[i] >= 0.0 {
				sum += endpoint.rttHistoryBuffer[i] - endpoint.rttMin
				count++
			}
		}
		if count > 0 {
			endpoint.jitterAvgVsMinRTT = sum / float64(count)
		} else {
			endpoint.jitterAvgVsMinRTT = 0.0
		}
	}

	// calculate max jitter vs. min rtt
	{
		max := 0.0
		for i := 0; i < endpoint.config.RTTHistorySize; i++ {
			if endpoint.rttHistoryBuffer[i] >= 0.0 {
				difference := endpoint.rttHistoryBuffer[i] - endpoint.rttMin
				if difference > max {
					max = difference
				}
			}
		}
		endpoint.jitterMaxVsMinRTT = max
	}

	// calculate stddev jitter vs. avg rtt
	{
		sum := 0.0
		count := 0
		for i := 0; i < endpoint.config.RTTHistorySize; i++ {
			if endpoint.rttHistoryBuffer[i] >= 0.0 {
				deviation := endpoint.rttHistoryBuffer[i] - endpoint.rttAvg
				sum += deviation * deviation
				count++
			}
		}
		if count > 0 {
			endpoint.jitterStddevVsAvgRTT = math.Sqrt(sum / float64(count))
		} else {
			endpoint.jitterStddevVsAvgRTT = 0.0
		}
	}

	// calculate packet loss
	{
		baseSequence := endpoint.sentPackets.sequence - uint16(endpoint.config.SentPacketsBufferSize) + 1 + 0xFFFF
		numSent := 0
		numDropped := 0
		numSamples := endpoint.config.SentPacketsBufferSize / 2
		for i := 0; i < numSamples; i++ {
			sequence := baseSequence + uint16(i)
			sentPacket := endpoint.sentPackets.find(sequence)
			if sentPacket != nil {
				numSent++
				if !sentPacket.acked {
					numDropped++
				}
			}
		}
		if numSent > 0 {
			packetLoss := float64(numDropped) / float64(numSent) * 100.0
			if math.Abs(endpoint.packetLoss-packetLoss) > 0.00001 {
				endpoint.packetLoss += (packetLoss - endpoint.packetLoss) * endpoint.config.PacketLossSmoothingFactor
			} else {
				endpoint.packetLoss = packetLoss
			}
		} else {
			endpoint.packetLoss = 0.0
		}
	}

	// calculate sent bandwidth
	{
		baseSequence := endpoint.sentPackets.sequence - uint16(endpoint.config.SentPacketsBufferSize) + 1 + 0xFFFF
		bytesSent := 0
		startTime := math.MaxFloat64
		finishTime := 0.0
		numSamples := endpoint.config.SentPacketsBufferSize / 2
		for i := 0; i < numSamples; i++ {
			sequence := baseSequence + uint16(i)
			sentPacket := endpoint.sentPackets.find(sequence)
			if sentPacket == nil {
				continue
			}
			bytesSent += int(sentPacket.packetBytes)
			if sentPacket.time < startTime {
				startTime = sentPacket.time
			}
			if sentPacket.time > finishTime {
				finishTime = sentPacket.time
			}
		}
		if startTime != math.MaxFloat64 && finishTime > startTime {
			sentBandwidthKbps := float64(bytesSent) / (finishTime - startTime) * 8.0 / 1000.0
			if math.Abs(endpoint.sentBandwidthKbps-sentBandwidthKbps) > 0.00001 {
				endpoint.sentBandwidthKbps += (sentBandwidthKbps - endpoint.sentBandwidthKbps) * endpoint.config.BandwidthSmoothingFactor
			} else {
				endpoint.sentBandwidthKbps = sentBandwidthKbps
			}
		}
	}

	// calculate received bandwidth
	{
		baseSequence := endpoint.receivedPackets.sequence - uint16(endpoint.config.ReceivedPacketsBufferSize) + 1 + 0xFFFF
		bytesReceived := 0
		startTime := math.MaxFloat64
		finishTime := 0.0
		numSamples := endpoint.config.ReceivedPacketsBufferSize / 2
		for i := 0; i < numSamples; i++ {
			sequence := baseSequence + uint16(i)
			receivedPacket := endpoint.receivedPackets.find(sequence)
			if receivedPacket == nil {
				continue
			}
			bytesReceived += int(receivedPacket.packetBytes)
			if receivedPacket.time < startTime {
				startTime = receivedPacket.time
			}
			if receivedPacket.time > finishTime {
				finishTime = receivedPacket.time
			}
		}
		if startTime != math.MaxFloat64 && finishTime > startTime {
			receivedBandwidthKbps := float64(bytesReceived) / (finishTime - startTime) * 8.0 / 1000.0
			if math.Abs(endpoint.receivedBandwidthKbps-receivedBandwidthKbps) > 0.00001 {
				endpoint.receivedBandwidthKbps += (receivedBandwidthKbps - endpoint.receivedBandwidthKbps) * endpoint.config.BandwidthSmoothingFactor
			} else {
				endpoint.receivedBandwidthKbps = receivedBandwidthKbps
			}
		}
	}

	// calculate acked bandwidth
	{
		baseSequence := endpoint.sentPackets.sequence - uint16(endpoint.config.SentPacketsBufferSize) + 1 + 0xFFFF
		bytesSent := 0
		startTime := math.MaxFloat64
		finishTime := 0.0
		numSamples := endpoint.config.SentPacketsBufferSize / 2
		for i := 0; i < numSamples; i++ {
			sequence := baseSequence + uint16(i)
			sentPacket := endpoint.sentPackets.find(sequence)
			if sentPacket == nil || !sentPacket.acked {
				continue
			}
			bytesSent += int(sentPacket.packetBytes)
			if sentPacket.time < startTime {
				startTime = sentPacket.time
			}
			if sentPacket.time > finishTime {
				finishTime = sentPacket.time
			}
		}
		if startTime != math.MaxFloat64 && finishTime > startTime {
			ackedBandwidthKbps := float64(bytesSent) / (finishTime - startTime) * 8.0 / 1000.0
			if math.Abs(endpoint.ackedBandwidthKbps-ackedBandwidthKbps) > 0.00001 {
				endpoint.ackedBandwidthKbps += (ackedBandwidthKbps - endpoint.ackedBandwidthKbps) * endpoint.config.BandwidthSmoothingFactor
			} else {
				endpoint.ackedBandwidthKbps = ackedBandwidthKbps
			}
		}
	}
}

// RTT returns the exponentially smoothed moving average round trip time in
// milliseconds.
func (endpoint *Endpoint) RTT() float64 {
	return endpoint.rtt
}

// RTTMin returns the minimum round trip time in milliseconds across the rtt
// history window.
func (endpoint *Endpoint) RTTMin() float64 {
	return endpoint.rttMin
}

// RTTMax returns the maximum round trip time in milliseconds across the rtt
// history window.
func (endpoint *Endpoint) RTTMax() float64 {
	return endpoint.rttMax
}

// RTTAvg returns the average round trip time in milliseconds across the rtt
// history window.
func (endpoint *Endpoint) RTTAvg() float64 {
	return endpoint.rttAvg
}

// JitterAvgVsMinRTT returns the average jitter relative to the minimum rtt in
// milliseconds.
func (endpoint *Endpoint) JitterAvgVsMinRTT() float64 {
	return endpoint.jitterAvgVsMinRTT
}

// JitterMaxVsMinRTT returns the maximum jitter relative to the minimum rtt in
// milliseconds.
func (endpoint *Endpoint) JitterMaxVsMinRTT() float64 {
	return endpoint.jitterMaxVsMinRTT
}

// JitterStddevVsAvgRTT returns the standard deviation of jitter relative to
// the average rtt in milliseconds.
func (endpoint *Endpoint) JitterStddevVsAvgRTT() float64 {
	return endpoint.jitterStddevVsAvgRTT
}

// PacketLoss returns the packet loss as a percentage.
func (endpoint *Endpoint) PacketLoss() float64 {
	return endpoint.packetLoss
}

// Bandwidth returns the sent, received and acked bandwidth in kilobits
// per-second.
func (endpoint *Endpoint) Bandwidth() (sentBandwidthKbps, receivedBandwidthKbps, ackedBandwidthKbps float64) {
	return endpoint.sentBandwidthKbps, endpoint.receivedBandwidthKbps, endpoint.ackedBandwidthKbps
}

// Counters returns the endpoint counters. Index with the Counter* constants.
func (endpoint *Endpoint) Counters() [NumCounters]uint64 {
	return endpoint.counters
}
