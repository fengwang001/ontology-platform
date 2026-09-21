package ontology

// naiveBitmap is an independent, obviously-correct bit-by-bit
// implementation used as the differential oracle in tests.
type naiveBitmap struct {
	bits map[uint32]bool
}

func newNaive() *naiveBitmap { return &naiveBitmap{bits: map[uint32]bool{}} }

func (n *naiveBitmap) set(pos uint32)   { n.bits[pos] = true }
func (n *naiveBitmap) clear(pos uint32) { delete(n.bits, pos) }
func (n *naiveBitmap) has(pos uint32) bool {
	return n.bits[pos]
}

func (n *naiveBitmap) union(o *naiveBitmap) *naiveBitmap {
	res := newNaive()
	for p := range n.bits {
		res.bits[p] = true
	}
	for p := range o.bits {
		res.bits[p] = true
	}
	return res
}

func (n *naiveBitmap) intersect(o *naiveBitmap) *naiveBitmap {
	res := newNaive()
	for p := range n.bits {
		if o.bits[p] {
			res.bits[p] = true
		}
	}
	return res
}

func (n *naiveBitmap) difference(o *naiveBitmap) *naiveBitmap {
	res := newNaive()
	for p := range n.bits {
		if !o.bits[p] {
			res.bits[p] = true
		}
	}
	return res
}

// equalSet compares a compressed set against the oracle over [0, limit).
func equalSet(s *Set, n *naiveBitmap, limit uint32) bool {
	for p := uint32(0); p < limit; p++ {
		if s.Contains(p) != n.has(p) {
			return false
		}
	}
	var cnt uint64
	for p := range n.bits {
		if p < limit {
			cnt++
		}
	}
	return s.Count() == cnt
}
