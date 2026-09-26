// Package ch provides the k hash functions h_j(x) = (j*x) mod m, j = 1..k.
// It depends on no other package in this module.
package ch

// Hasher maps a non-negative key to one of m counter indices for each j in 1..k.
type Hasher struct {
	m int
	k int
}

// New returns a Hasher. Callers must guarantee m > 0 and k > 0; the cbf
// package enforces this, so no zero-modulus can occur through it.
func New(m, k int) *Hasher {
	return &Hasher{m: m, k: k}
}

// M and K report the constructor parameters.
func (h *Hasher) M() int { return h.m }
func (h *Hasher) K() int { return h.k }

// Index returns h_j(key) = (j*key) mod m for 1 <= j <= k and key >= 0.
// key is reduced modulo m before multiplication, so j*(key mod m) cannot
// overflow int64 for any m,k that fit in memory as a counter slice.
func (h *Hasher) Index(key int64, j int) int {
	r := key % int64(h.m)
	return int((int64(j) * r) % int64(h.m))
}

// Indices writes all k hit indices for key into dst, which must have length k.
func (h *Hasher) Indices(key int64, dst []int) {
	for j := 1; j <= h.k; j++ {
		dst[j-1] = h.Index(key, j)
	}
}
