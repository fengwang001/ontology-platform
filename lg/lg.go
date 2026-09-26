// Package lg holds the fixed-size register array and the rank (max) update.
// It depends on no other package of this module.
package lg

// Registers is a fixed-length array reg[0..m-1] of per-bucket ranks.
type Registers struct {
	reg []int
}

// New allocates m zero-valued registers. Callers guarantee m > 0.
func New(m int) *Registers {
	return &Registers{reg: make([]int, m)}
}

// Len is the number of registers m.
func (r *Registers) Len() int { return len(r.reg) }

// Add records one element: reg[bucket] = max(reg[bucket], z+1).
// The update only ever increases a register; callers guarantee 0 <= bucket < m.
func (r *Registers) Add(bucket, z int) {
	if rank := z + 1; rank > r.reg[bucket] {
		r.reg[bucket] = rank
	}
}

// At returns register j. Callers guarantee 0 <= j < Len.
func (r *Registers) At(j int) int { return r.reg[j] }
