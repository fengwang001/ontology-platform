package ontology

import "testing"

// naive is an independent per-bit reference implementation used to
// differential-test the compressed-domain algorithms.
type naive struct {
	bits map[uint32]bool
}

func newNaive() *naive { return &naive{bits: make(map[uint32]bool)} }

func (n *naive) set(v uint32) { n.bits[v] = true }

func (n *naive) clear(v uint32) { delete(n.bits, v) }

func (n *naive) has(v uint32) bool { return n.bits[v] }

func (n *naive) count() uint64 { return uint64(len(n.bits)) }

func (n *naive) union(o *naive) *naive {
	r := newNaive()
	for v := range n.bits {
		r.set(v)
	}
	for v := range o.bits {
		r.set(v)
	}
	return r
}

func (n *naive) intersect(o *naive) *naive {
	r := newNaive()
	for v := range n.bits {
		if o.has(v) {
			r.set(v)
		}
	}
	return r
}

func (n *naive) difference(o *naive) *naive {
	r := newNaive()
	for v := range n.bits {
		if !o.has(v) {
			r.set(v)
		}
	}
	return r
}

// assertMatches checks that b agrees with n on every bit of
// [0, universe) and holds no bits outside it.
func (n *naive) assertMatches(t *testing.T, b *Bitmap, universe uint32) {
	t.Helper()
	if err := b.Verify(); err != nil {
		t.Fatalf("result not canonical: %v", err)
	}
	if got, want := b.Count(), n.count(); got != want {
		t.Fatalf("count mismatch: got %d, want %d", got, want)
	}
	for v := uint32(0); v < universe; v++ {
		if got, want := b.Contains(v), n.has(v); got != want {
			t.Fatalf("bit %d: got %v, want %v", v, got, want)
		}
	}
}
