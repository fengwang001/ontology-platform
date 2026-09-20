package seqwin

// bitmap is a fixed-size bit set recording which sequence numbers in
// the window have been seen. Bit index 0 always corresponds to the
// highest sequence number seen so far; index i corresponds to
// highest-i. Its memory footprint depends only on the window size,
// never on how many sequence numbers have been processed.
type bitmap struct {
	bits []uint64
	size int // number of valid bits
}

func newBitmap(size int) bitmap {
	return bitmap{bits: make([]uint64, (size+63)/64), size: size}
}

func (b *bitmap) get(i int) bool {
	return b.bits[i/64]&(uint64(1)<<uint(i%64)) != 0
}

func (b *bitmap) set(i int) {
	b.bits[i/64] |= uint64(1) << uint(i%64)
}

// shiftLeft moves every bit d positions toward higher indices,
// dropping bits that fall past the window size and zero-filling the
// low end. It is used when the window's right edge advances by d, so
// that a sequence number previously at index i ends up at index i+d.
func (b *bitmap) shiftLeft(d int) {
	if d >= b.size {
		for i := range b.bits {
			b.bits[i] = 0
		}
		return
	}
	words, off := d/64, uint(d%64)
	for i := len(b.bits) - 1; i >= 0; i-- {
		var v uint64
		if i-words >= 0 {
			v = b.bits[i-words] << off
			if off != 0 && i-words-1 >= 0 {
				v |= b.bits[i-words-1] >> (64 - off)
			}
		}
		b.bits[i] = v
	}
	// Clear any bits shifted beyond the window size in the top word.
	if rem := uint(b.size % 64); rem != 0 {
		b.bits[len(b.bits)-1] &= (uint64(1) << rem) - 1
	}
}
