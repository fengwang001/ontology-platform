// Package rhash builds polynomial rolling-hash prefix tables (double hash)
// and answers substring hashes in O(1) without rescanning the input.
package rhash

const (
	M1 uint64 = 1_000_000_007
	M2 uint64 = 1_000_000_009
	B  uint64 = 1_000_003 // coprime with M1 and M2 (both prime, 0 < B < M1, M2)
)

// Table holds prefix hashes and base powers for one immutable byte string.
type Table struct {
	p1, p2   []uint64 // prefix hashes mod M1, M2; length n+1
	pw1, pw2 []uint64 // B^i mod M1, M2; length n+1
	n        int
	// rescanned counts characters re-scanned while extracting substring
	// hashes. Extraction is O(1) from prefix tables, so it stays 0.
	rescanned uint64
}

// New precomputes prefix hashes and base powers in O(n).
func New(s []byte) *Table {
	n := len(s)
	t := &Table{
		p1:  make([]uint64, n+1),
		p2:  make([]uint64, n+1),
		pw1: make([]uint64, n+1),
		pw2: make([]uint64, n+1),
		n:   n,
	}
	t.pw1[0], t.pw2[0] = 1, 1
	for i := 1; i <= n; i++ {
		c := uint64(s[i-1])
		t.p1[i] = (t.p1[i-1]*B + c) % M1
		t.p2[i] = (t.p2[i-1]*B + c) % M2
		t.pw1[i] = t.pw1[i-1] * B % M1
		t.pw2[i] = t.pw2[i-1] * B % M2
	}
	return t
}

// Len returns the length of the underlying string.
func (t *Table) Len() int { return t.n }

// Hash returns the double hash of s[l:r) in O(1):
// (P[r] - P[l]*B^(r-l)) mod M, adjusted into [0, M). Callers must
// guarantee 0 <= l <= r <= n.
func (t *Table) Hash(l, r int) (uint64, uint64) {
	k := r - l
	h1 := (t.p1[r] + M1 - t.p1[l]*t.pw1[k]%M1) % M1
	h2 := (t.p2[r] + M2 - t.p2[l]*t.pw2[k]%M2) % M2
	return h1, h2
}
