package reliable

import "bytes"

// writePacketHeader writes a variable length packet header into packetData and
// returns the number of bytes written. The header encodes the packet sequence
// number, the most recent received sequence number (ack) and a bitfield of the
// 32 packets before it (ackBits). Sections of ackBits that are all 1s are
// omitted, and the ack is encoded as an 8 bit offset from sequence when they
// are close together, which is the common case.
func writePacketHeader(packetData []byte, sequence uint16, ack uint16, ackBits uint32) int {
	p := 0

	prefixByte := uint8(0)

	if (ackBits & 0x000000FF) != 0x000000FF {
		prefixByte |= 1 << 1
	}

	if (ackBits & 0x0000FF00) != 0x0000FF00 {
		prefixByte |= 1 << 2
	}

	if (ackBits & 0x00FF0000) != 0x00FF0000 {
		prefixByte |= 1 << 3
	}

	if (ackBits & 0xFF000000) != 0xFF000000 {
		prefixByte |= 1 << 4
	}

	sequenceDifference := int(sequence) - int(ack)
	if sequenceDifference < 0 {
		sequenceDifference += 65536
	}
	if sequenceDifference <= 255 {
		prefixByte |= 1 << 5
	}

	packetData[p] = prefixByte
	p++

	packetData[p] = byte(sequence)
	packetData[p+1] = byte(sequence >> 8)
	p += 2

	if sequenceDifference <= 255 {
		packetData[p] = byte(sequenceDifference)
		p++
	} else {
		packetData[p] = byte(ack)
		packetData[p+1] = byte(ack >> 8)
		p += 2
	}

	if (ackBits & 0x000000FF) != 0x000000FF {
		packetData[p] = byte(ackBits & 0x000000FF)
		p++
	}

	if (ackBits & 0x0000FF00) != 0x0000FF00 {
		packetData[p] = byte((ackBits & 0x0000FF00) >> 8)
		p++
	}

	if (ackBits & 0x00FF0000) != 0x00FF0000 {
		packetData[p] = byte((ackBits & 0x00FF0000) >> 16)
		p++
	}

	if (ackBits & 0xFF000000) != 0xFF000000 {
		packetData[p] = byte((ackBits & 0xFF000000) >> 24)
		p++
	}

	return p
}

// readPacketHeader reads a packet header written by writePacketHeader and
// returns the number of header bytes read, or -1 if the packet is invalid.
func readPacketHeader(name string, packetData []byte) (sequence uint16, ack uint16, ackBits uint32, headerBytes int) {
	packetBytes := len(packetData)

	if packetBytes < 3 {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] packet too small for packet header (1)\n", name)
		}
		return 0, 0, 0, -1
	}

	p := 0

	prefixByte := packetData[p]
	p++

	if (prefixByte & 1) != 0 {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] prefix byte does not indicate a regular packet\n", name)
		}
		return 0, 0, 0, -1
	}

	sequence = uint16(packetData[p]) | uint16(packetData[p+1])<<8
	p += 2

	if prefixByte&(1<<5) != 0 {
		if packetBytes < 3+1 {
			if debugLogging() {
				logPrintf(LogLevelDebug, "[%s] packet too small for packet header (2)\n", name)
			}
			return 0, 0, 0, -1
		}
		sequenceDifference := packetData[p]
		p++
		ack = sequence - uint16(sequenceDifference)
	} else {
		if packetBytes < 3+2 {
			if debugLogging() {
				logPrintf(LogLevelDebug, "[%s] packet too small for packet header (3)\n", name)
			}
			return 0, 0, 0, -1
		}
		ack = uint16(packetData[p]) | uint16(packetData[p+1])<<8
		p += 2
	}

	expectedBytes := 0
	for i := 1; i <= 4; i++ {
		if prefixByte&(1<<i) != 0 {
			expectedBytes++
		}
	}
	if packetBytes < p+expectedBytes {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] packet too small for packet header (4)\n", name)
		}
		return 0, 0, 0, -1
	}

	ackBits = 0xFFFFFFFF

	if prefixByte&(1<<1) != 0 {
		ackBits &= 0xFFFFFF00
		ackBits |= uint32(packetData[p])
		p++
	}

	if prefixByte&(1<<2) != 0 {
		ackBits &= 0xFFFF00FF
		ackBits |= uint32(packetData[p]) << 8
		p++
	}

	if prefixByte&(1<<3) != 0 {
		ackBits &= 0xFF00FFFF
		ackBits |= uint32(packetData[p]) << 16
		p++
	}

	if prefixByte&(1<<4) != 0 {
		ackBits &= 0x00FFFFFF
		ackBits |= uint32(packetData[p]) << 24
		p++
	}

	return sequence, ack, ackBits, p
}

// readFragmentHeader reads a fragment header and returns the number of header
// bytes read, or -1 in headerBytes if the fragment is invalid. Fragment 0 also
// carries the packet header for the fragmented packet, so ack and ackBits are
// only filled in for fragment 0.
func readFragmentHeader(name string, packetData []byte, maxFragments int, fragmentSize int) (fragmentID int, numFragments int, fragmentBytes int, sequence uint16, ack uint16, ackBits uint32, headerBytes int) {
	packetBytes := len(packetData)

	if packetBytes < FragmentHeaderBytes {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] packet is too small to read fragment header\n", name)
		}
		return 0, 0, 0, 0, 0, 0, -1
	}

	p := 0

	prefixByte := packetData[p]
	p++
	if prefixByte != 1 {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] prefix byte is not a fragment\n", name)
		}
		return 0, 0, 0, 0, 0, 0, -1
	}

	sequence = uint16(packetData[p]) | uint16(packetData[p+1])<<8
	p += 2
	fragmentID = int(packetData[p])
	p++
	numFragments = int(packetData[p]) + 1
	p++

	if numFragments > maxFragments {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] num fragments %d outside of range of max fragments %d\n", name, numFragments, maxFragments)
		}
		return 0, 0, 0, 0, 0, 0, -1
	}

	if fragmentID >= numFragments {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] fragment id %d outside of range of num fragments %d\n", name, fragmentID, numFragments)
		}
		return 0, 0, 0, 0, 0, 0, -1
	}

	fragmentBytes = packetBytes - FragmentHeaderBytes

	var packetSequence uint16
	var packetAck uint16
	var packetAckBits uint32

	if fragmentID == 0 {
		var packetHeaderBytes int
		packetSequence, packetAck, packetAckBits, packetHeaderBytes = readPacketHeader(name, packetData[FragmentHeaderBytes:])

		if packetHeaderBytes < 0 {
			if debugLogging() {
				logPrintf(LogLevelDebug, "[%s] bad packet header in fragment\n", name)
			}
			return 0, 0, 0, 0, 0, 0, -1
		}

		if packetSequence != sequence {
			if debugLogging() {
				logPrintf(LogLevelDebug, "[%s] bad packet sequence in fragment. expected %d, got %d\n", name, sequence, packetSequence)
			}
			return 0, 0, 0, 0, 0, 0, -1
		}

		// the packet header is re-encoded canonically during reassembly, so a non-canonical
		// header would shift where the fragment payload lands. reject it here instead.

		var canonicalHeader [MaxPacketHeaderBytes]byte
		canonicalHeaderBytes := writePacketHeader(canonicalHeader[:], packetSequence, packetAck, packetAckBits)
		if canonicalHeaderBytes != packetHeaderBytes || !bytes.Equal(canonicalHeader[:canonicalHeaderBytes], packetData[FragmentHeaderBytes:FragmentHeaderBytes+canonicalHeaderBytes]) {
			if debugLogging() {
				logPrintf(LogLevelDebug, "[%s] non-canonical packet header in fragment\n", name)
			}
			return 0, 0, 0, 0, 0, 0, -1
		}

		fragmentBytes = packetBytes - packetHeaderBytes - FragmentHeaderBytes
	}

	ack = packetAck
	ackBits = packetAckBits

	if fragmentBytes > fragmentSize {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] fragment bytes %d > fragment size %d\n", name, fragmentBytes, fragmentSize)
		}
		return 0, 0, 0, 0, 0, 0, -1
	}

	if fragmentID != numFragments-1 && fragmentBytes != fragmentSize {
		if debugLogging() {
			logPrintf(LogLevelDebug, "[%s] fragment %d is %d bytes, which is not the expected fragment size %d\n", name, fragmentID, fragmentBytes, fragmentSize)
		}
		return 0, 0, 0, 0, 0, 0, -1
	}

	return fragmentID, numFragments, fragmentBytes, sequence, ack, ackBits, p
}
