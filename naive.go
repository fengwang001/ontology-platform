package ontology

// Naive is a reference set implementation backed by a plain map, used
// by tests and the demo to differentially validate the compressed
// bitmap. It is intentionally simple and not part of the bitmap itself.
type Naive struct {
	m map[uint32]struct{}
}

// NewNaive returns an empty Naive set.
func NewNaive() *Naive {
	return &Naive{m: make(map[uint32]struct{})}
}

// Set adds bit to the set.
func (n *Naive) Set(bit uint32) {
	n.m[bit] = struct{}{}
}

// Clear removes bit from the set.
func (n *Naive) Clear(bit uint32) {
	delete(n.m, bit)
}

// Contains reports whether bit is in the set.
func (n *Naive) Contains(bit uint32) bool {
	_, ok := n.m[bit]
	return ok
}

// Count returns the cardinality of the set.
func (n *Naive) Count() int {
	return len(n.m)
}

// Union returns the naive union of n and o.
func (n *Naive) Union(o *Naive) *Naive {
	out := NewNaive()
	for bit := range n.m {
		out.m[bit] = struct{}{}
	}
	for bit := range o.m {
		out.m[bit] = struct{}{}
	}
	return out
}

// Intersect returns the naive intersection of n and o.
func (n *Naive) Intersect(o *Naive) *Naive {
	out := NewNaive()
	for bit := range n.m {
		if o.Contains(bit) {
			out.m[bit] = struct{}{}
		}
	}
	return out
}

// Difference returns the naive difference of n and o.
func (n *Naive) Difference(o *Naive) *Naive {
	out := NewNaive()
	for bit := range n.m {
		if !o.Contains(bit) {
			out.m[bit] = struct{}{}
		}
	}
	return out
}

// EqualsBitmap reports whether n and b contain exactly the same bits.
func (n *Naive) EqualsBitmap(b *Bitmap) bool {
	count, _ := b.Count()
	if count != uint64(len(n.m)) {
		return false
	}
	for bit := range n.m {
		if !b.Contains(bit) {
			return false
		}
	}
	return true
}
