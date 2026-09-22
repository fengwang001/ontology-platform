package hll

import "math/bits"

// split decomposes a 64-bit hash into its register index and rho rank.
//
// Bit layout (every bit is checkable):
//
//	hash = [ high (64-p) bits ][ low p bits ]
//	         ^^^^^^^^^^^^^^^^^^   ^^^^^^^^^^^^
//	         rho is computed here  register index
//
// rho(hash) is the 1-based position of the leftmost 1 bit among the high
// 64-p bits: number of leading zero bits in that field plus one. When the
// whole high field is zero no 1 bit exists; by HLL convention rho then takes
// the field width plus one (the maximum rank), so such a hash still updates
// the register deterministically.
func (e *Estimator) split(hash uint64) (idx uint64, rho uint8) {
	idx = hash & e.regMask
	high := hash >> e.p // exactly the 64-p high bits, shifted to the right
	// LeadingZeros64 counts zeros above the 1 bit; those beyond the 64-p-wide
	// field are structural and must not be counted, hence the subtraction.
	lz := bits.LeadingZeros64(high) - e.p
	if high == 0 {
		return idx, uint8(64 - e.p + 1)
	}
	return idx, uint8(lz + 1)
}

// InspectRegisters returns a defensive copy of all register ranks in
// register-index order. It exists solely to make register semantics testable
// bit for bit: mutating the returned slice can never affect estimator state.
func (e *Estimator) InspectRegisters() []uint8 {
	out := make([]uint8, len(e.regs))
	copy(out, e.regs)
	return out
}
