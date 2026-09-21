package bitmap

// naive is an independent, obviously-correct per-bit set used to
// differential-test the compressed-domain implementation.
type naive struct {
	bits []bool
}

func newNaive(size int) *naive {
	return &naive{bits: make([]bool, size)}
}

func (n *naive) set(x uint32)   { n.bits[x] = true }
func (n *naive) clear(x uint32) { n.bits[x] = false }

func (n *naive) setRange(lo, hi uint32) {
	for x := lo; x <= hi; x++ {
		n.bits[x] = true
	}
}

func (n *naive) count() uint64 {
	var c uint64
	for _, b := range n.bits {
		if b {
			c++
		}
	}
	return c
}

func (n *naive) union(o *naive) *naive {
	r := newNaive(len(n.bits))
	for i := range n.bits {
		r.bits[i] = n.bits[i] || o.bits[i]
	}
	return r
}

func (n *naive) intersect(o *naive) *naive {
	r := newNaive(len(n.bits))
	for i := range n.bits {
		r.bits[i] = n.bits[i] && o.bits[i]
	}
	return r
}

func (n *naive) difference(o *naive) *naive {
	r := newNaive(len(n.bits))
	for i := range n.bits {
		r.bits[i] = n.bits[i] && !o.bits[i]
	}
	return r
}

// toBitmap converts the naive set into a Bitmap using range fills,
// so the comparison also exercises canonical encoding uniqueness.
func (n *naive) toBitmap() *Bitmap {
	b := New()
	var lo int
	open := false
	for i, v := range n.bits {
		if v && !open {
			lo, open = i, true
		}
		if !v && open {
			b.SetRange(uint32(lo), uint32(i-1))
			open = false
		}
	}
	if open {
		b.SetRange(uint32(lo), uint32(len(n.bits)-1))
	}
	return b
}
