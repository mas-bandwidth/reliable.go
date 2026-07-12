package reliable

// entryEmpty marks an unused slot in a sequence buffer.
const entryEmpty = 0xFFFFFFFF

// sequenceBuffer is a rolling buffer of entries indexed by 16 bit sequence
// number, used to track sent packets, received packets and fragment
// reassembly state.
type sequenceBuffer[T any] struct {
	sequence      uint16
	numEntries    int
	entrySequence []uint32
	entries       []T
}

func newSequenceBuffer[T any](numEntries int) *sequenceBuffer[T] {
	if numEntries <= 0 {
		panic("reliable: sequence buffer must have at least one entry")
	}
	buffer := &sequenceBuffer[T]{
		numEntries:    numEntries,
		entrySequence: make([]uint32, numEntries),
		entries:       make([]T, numEntries),
	}
	for i := range buffer.entrySequence {
		buffer.entrySequence[i] = entryEmpty
	}
	return buffer
}

func (b *sequenceBuffer[T]) reset() {
	b.sequence = 0
	for i := range b.entrySequence {
		b.entrySequence[i] = entryEmpty
	}
}

// removeEntries removes all entries in [startSequence,finishSequence]. The
// cleanup function, if not nil, is called on each entry so it can drop any
// references it holds.
func (b *sequenceBuffer[T]) removeEntries(startSequence, finishSequence int, cleanup func(*T)) {
	if finishSequence < startSequence {
		finishSequence += 65536
	}
	if finishSequence-startSequence < b.numEntries {
		for sequence := startSequence; sequence <= finishSequence; sequence++ {
			index := sequence % b.numEntries
			if cleanup != nil {
				cleanup(&b.entries[index])
			}
			b.entrySequence[index] = entryEmpty
		}
	} else {
		for i := 0; i < b.numEntries; i++ {
			if cleanup != nil {
				cleanup(&b.entries[i])
			}
			b.entrySequence[i] = entryEmpty
		}
	}
}

// testInsert reports whether an entry with this sequence number can be
// inserted, i.e. it is not older than the buffer window.
func (b *sequenceBuffer[T]) testInsert(sequence uint16) bool {
	return !sequenceLessThan(sequence, b.sequence-uint16(b.numEntries))
}

func (b *sequenceBuffer[T]) insert(sequence uint16) *T {
	if sequenceLessThan(sequence, b.sequence-uint16(b.numEntries)) {
		return nil
	}
	if sequenceGreaterThan(sequence+1, b.sequence) {
		b.removeEntries(int(b.sequence), int(sequence), nil)
		b.sequence = sequence + 1
	}
	index := int(sequence) % b.numEntries
	b.entrySequence[index] = uint32(sequence)
	return &b.entries[index]
}

func (b *sequenceBuffer[T]) advance(sequence uint16) {
	if sequenceGreaterThan(sequence+1, b.sequence) {
		b.removeEntries(int(b.sequence), int(sequence), nil)
		b.sequence = sequence + 1
	}
}

func (b *sequenceBuffer[T]) insertWithCleanup(sequence uint16, cleanup func(*T)) *T {
	if sequenceGreaterThan(sequence+1, b.sequence) {
		b.removeEntries(int(b.sequence), int(sequence), cleanup)
		b.sequence = sequence + 1
	} else if sequenceLessThan(sequence, b.sequence-uint16(b.numEntries)) {
		return nil
	}
	index := int(sequence) % b.numEntries
	if b.entrySequence[index] != entryEmpty {
		cleanup(&b.entries[index])
	}
	b.entrySequence[index] = uint32(sequence)
	return &b.entries[index]
}

func (b *sequenceBuffer[T]) advanceWithCleanup(sequence uint16, cleanup func(*T)) {
	if sequenceGreaterThan(sequence+1, b.sequence) {
		b.removeEntries(int(b.sequence), int(sequence), cleanup)
		b.sequence = sequence + 1
	}
}

func (b *sequenceBuffer[T]) removeWithCleanup(sequence uint16, cleanup func(*T)) {
	index := int(sequence) % b.numEntries
	if b.entrySequence[index] != entryEmpty {
		b.entrySequence[index] = entryEmpty
		cleanup(&b.entries[index])
	}
}

func (b *sequenceBuffer[T]) exists(sequence uint16) bool {
	return b.entrySequence[int(sequence)%b.numEntries] == uint32(sequence)
}

func (b *sequenceBuffer[T]) find(sequence uint16) *T {
	index := int(sequence) % b.numEntries
	if b.entrySequence[index] == uint32(sequence) {
		return &b.entries[index]
	}
	return nil
}

func (b *sequenceBuffer[T]) atIndex(index int) *T {
	if b.entrySequence[index] != entryEmpty {
		return &b.entries[index]
	}
	return nil
}

// generateAckBits returns the most recent received sequence number as ack, and
// a bitfield where bit i indicates that packet (ack - i) was received.
func (b *sequenceBuffer[T]) generateAckBits() (ack uint16, ackBits uint32) {
	ack = b.sequence - 1
	mask := uint32(1)
	for i := 0; i < 32; i++ {
		sequence := ack - uint16(i)
		if b.exists(sequence) {
			ackBits |= mask
		}
		mask <<= 1
	}
	return ack, ackBits
}
