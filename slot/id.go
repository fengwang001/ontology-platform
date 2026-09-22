package slot

// ID is the correlation identifier handed back to the caller.
// The high 32 bits carry the slot index and the low 32 bits carry a
// generation counter, so a recycled slot can never be confused with an
// earlier request that used the same slot.
type ID uint64

const generationBits = 32

// Encode builds an ID from a slot index and generation.
func Encode(index, generation uint32) ID {
	return ID(uint64(index)<<generationBits | uint64(generation))
}

// Index returns the slot index encoded in the ID.
func (id ID) Index() uint32 { return uint32(id >> generationBits) }

// Generation returns the generation encoded in the ID.
func (id ID) Generation() uint32 { return uint32(id) }
