package tlb

// A deliberately naive reference implementation of the same spec,
// written with per-mm traversals and per-entry linear searches. The
// randomized differential test replays identical operation sequences
// against Model and naive and demands identical results and state.

import "math"

type naive struct {
	A, C, T, M int
	G          uint64

	active   []pair
	reserved []pair
	pending  []bool
	mms      []mmState
	tlbs     [][]entry // per cpu, MRU first

	mmVisited int // counts per-mm traversals
}

func newNaive(a, c, cap, m int) *naive {
	return &naive{
		A: a, C: c, T: cap, M: m, G: 1,
		active:   make([]pair, c),
		reserved: make([]pair, c),
		pending:  make([]bool, c),
		tlbs:     make([][]entry, c),
	}
}

// asidTaken decides membership by traversing the reserved pairs and
// the whole mm table, instead of consulting a bitmap.
func (n *naive) asidTaken(asid uint32) bool {
	for cpu := 0; cpu < n.C; cpu++ {
		if r := n.reserved[cpu]; r.valid && r.asid == asid {
			return true
		}
	}
	for i := range n.mms {
		n.mmVisited++
		m := n.mms[i]
		if !m.dead && m.gen == n.G && m.asid == asid {
			return true
		}
	}
	return false
}

func (n *naive) isReserved(asid uint32, gen uint64) bool {
	for cpu := 0; cpu < n.C; cpu++ {
		if r := n.reserved[cpu]; r.valid && r.asid == asid && r.gen == gen {
			return true
		}
	}
	return false
}

func (n *naive) createMM() (int, error) {
	if len(n.mms) >= n.M {
		return 0, ErrTooManyMM
	}
	n.mms = append(n.mms, mmState{})
	return len(n.mms) - 1, nil
}

func (n *naive) switchTo(cpu, mm int) (uint32, uint64, bool, bool, error) {
	if cpu < 0 || cpu >= n.C {
		return 0, 0, false, false, ErrBadCPU
	}
	if mm < 0 || mm >= len(n.mms) {
		return 0, 0, false, false, ErrBadMM
	}
	if n.mms[mm].dead {
		return 0, 0, false, false, ErrDead
	}
	m := &n.mms[mm]
	rolled := false
	for {
		if m.gen == n.G {
			break
		}
		if m.asid != 0 && n.isReserved(m.asid, m.gen) {
			m.gen = n.G
			break
		}
		free := uint32(0)
		for a := uint32(1); a <= uint32(n.A); a++ {
			if !n.asidTaken(a) {
				free = a
				break
			}
		}
		if free != 0 {
			m.asid, m.gen = free, n.G
			break
		}
		// Rollover.
		n.G++
		for c := 0; c < n.C; c++ {
			n.reserved[c] = n.active[c]
		}
		for c := 0; c < n.C; c++ {
			n.pending[c] = true
		}
		rolled = true
	}
	flushed := false
	if n.pending[cpu] {
		n.tlbs[cpu] = nil
		n.pending[cpu] = false
		flushed = true
	}
	n.active[cpu] = pair{m.asid, m.gen, true}
	return m.asid, m.gen, flushed, rolled, nil
}

func (n *naive) fill(cpu int, vpn, pfn uint64) (Key, bool, error) {
	if cpu < 0 || cpu >= n.C {
		return Key{}, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 || pfn > math.MaxUint32 {
		return Key{}, false, ErrInvalidParam
	}
	if !n.active[cpu].valid {
		return Key{}, false, ErrNoContext
	}
	asid, v, p := n.active[cpu].asid, uint32(vpn), uint32(pfn)
	tlb := n.tlbs[cpu]
	for i, e := range tlb {
		if e.key.ASID == asid && e.key.VPN == v {
			tlb[i].pfn = p
			n.tlbs[cpu] = append(append([]entry{tlb[i]}, tlb[:i]...), tlb[i+1:]...)
			return Key{}, false, nil
		}
	}
	n.tlbs[cpu] = append([]entry{{key: Key{asid, v}, pfn: p}}, tlb...)
	if len(n.tlbs[cpu]) > n.T {
		victim := n.tlbs[cpu][len(n.tlbs[cpu])-1]
		n.tlbs[cpu] = n.tlbs[cpu][:len(n.tlbs[cpu])-1]
		return victim.key, true, nil
	}
	return Key{}, false, nil
}

func (n *naive) lookup(cpu int, vpn uint64) (uint32, bool, error) {
	if cpu < 0 || cpu >= n.C {
		return 0, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 {
		return 0, false, ErrInvalidParam
	}
	if !n.active[cpu].valid {
		return 0, false, ErrNoContext
	}
	asid, v := n.active[cpu].asid, uint32(vpn)
	tlb := n.tlbs[cpu]
	for i, e := range tlb {
		if e.key.ASID == asid && e.key.VPN == v {
			n.tlbs[cpu] = append(append([]entry{tlb[i]}, tlb[:i]...), tlb[i+1:]...)
			return e.pfn, true, nil
		}
	}
	return 0, false, nil
}

func (n *naive) invalidate(mm int, vpn uint64) (int, error) {
	if mm < 0 || mm >= len(n.mms) {
		return 0, ErrBadMM
	}
	m := &n.mms[mm]
	if m.dead {
		return 0, ErrDead
	}
	if vpn > math.MaxUint32 {
		return 0, ErrInvalidParam
	}
	if m.asid == 0 || (m.gen != n.G && !n.isReserved(m.asid, m.gen)) {
		return 0, nil
	}
	count := 0
	for cpu := 0; cpu < n.C; cpu++ {
		tlb := n.tlbs[cpu]
		for i, e := range tlb {
			if e.key.ASID == m.asid && e.key.VPN == uint32(vpn) {
				n.tlbs[cpu] = append(tlb[:i], tlb[i+1:]...)
				count++
				break
			}
		}
	}
	return count, nil
}

func (n *naive) destroyMM(mm int) (bool, int, error) {
	if mm < 0 || mm >= len(n.mms) {
		return false, 0, ErrBadMM
	}
	m := &n.mms[mm]
	if m.dead {
		return false, 0, ErrDead
	}
	current := m.asid != 0 && m.gen == n.G
	if current {
		for cpu := 0; cpu < n.C; cpu++ {
			if a := n.active[cpu]; a.valid && a.asid == m.asid && a.gen == m.gen {
				return false, 0, ErrBusy
			}
		}
	}
	m.dead = true
	reservedASID := false
	for cpu := 0; cpu < n.C; cpu++ {
		if r := n.reserved[cpu]; r.valid && r.asid == m.asid {
			reservedASID = true
		}
	}
	if !current || reservedASID {
		return false, 0, nil
	}
	removed := 0
	for cpu := 0; cpu < n.C; cpu++ {
		kept := n.tlbs[cpu][:0]
		for _, e := range n.tlbs[cpu] {
			if e.key.ASID == m.asid {
				removed++
			} else {
				kept = append(kept, e)
			}
		}
		n.tlbs[cpu] = kept
	}
	return true, removed, nil
}
