// Package hashfam derives d deterministic hash functions from parameters
// alone: no randomness, no clock, no map iteration order.
package hashfam

// Family is a set of d hash functions into [0, w).
type Family struct {
	w     uint64
	seeds []uint64
}

// New builds the family for width w and depth d. Callers must ensure w, d > 0.
func New(w, d int) *Family {
	f := &Family{w: uint64(w), seeds: make([]uint64, d)}
	base := mix(uint64(w)<<32 | uint64(d))
	for i := range f.seeds {
		f.seeds[i] = mix(base ^ uint64(i))
	}
	return f
}

// D returns the number of hash functions (the depth).
func (f *Family) D() int { return len(f.seeds) }

// Index returns the cell of key in the given row: hash_row(key) mod w.
func (f *Family) Index(row int, key string) uint64 {
	h := uint64(14695981039346656037) // FNV-1a offset basis
	seed := f.seeds[row]
	for i := 0; i < 8; i++ {
		h ^= seed >> (8 * i) & 0xff
		h *= 1099511628211
	}
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	return h % f.w
}

// mix is the splitmix64 finalizer: a fixed bijection, fully deterministic.
func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
