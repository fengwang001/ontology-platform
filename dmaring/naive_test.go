package dmaring

// Stepwise naive reference model. It is written straight from the rules
// with no shared-state tricks and is compared against the real Ring in
// differential tests.

type naiveSlot struct {
	OWN, FIRST, LAST int
	Len              int64
	St               int
}

type naiveRing struct {
	n               int
	slots           []naiveSlot
	prod, dev, reap uint64
	faults          map[uint64]bool
}

func newNaive(n int) *naiveRing {
	return &naiveRing{
		n:      n,
		slots:  make([]naiveSlot, n),
		faults: map[uint64]bool{},
	}
}

func (m *naiveRing) free() int { return m.n - int(m.prod-m.reap) }

func (m *naiveRing) naiveSubmit(lens []int64) error {
	nSeg := len(lens)
	if nSeg == 0 {
		return ErrEmptyLens
	}
	for _, ln := range lens {
		if ln <= 0 {
			return ErrBadLen
		}
	}
	if nSeg > m.n {
		return ErrSegmentsOverN
	}
	if nSeg > m.free() {
		return ErrNoFreeSlots
	}
	for i := 0; i < nSeg; i++ {
		m.slots[(m.prod+uint64(i))%uint64(m.n)] = naiveSlot{
			OWN:   1,
			FIRST: b2i(i == 0),
			LAST:  b2i(i == nSeg-1),
			Len:   lens[i],
		}
	}
	m.prod += uint64(nSeg)
	return nil
}

func (m *naiveRing) naiveDeviceRun(k int) (int, error) {
	if k < 0 {
		return 0, ErrNegativeK
	}
	units := 0
	for units < k {
		if m.dev >= m.prod {
			break
		}
		s := &m.slots[m.dev%uint64(m.n)]
		if s.OWN == 0 {
			break
		}
		seq := m.dev
		if m.faults[seq] {
			s.OWN, s.St = 0, StFault
			delete(m.faults, seq)
			m.dev++
			// The cascade covers the rest of THIS packet only. If the
			// faulted descriptor itself is LAST, there is nothing to discard.
			if s.LAST == 0 {
				for {
					t := &m.slots[m.dev%uint64(m.n)]
					t.OWN, t.St = 0, StDrop
					delete(m.faults, m.dev)
					last := t.LAST == 1
					m.dev++
					if last {
						break
					}
				}
			}
		} else {
			s.OWN, s.St = 0, StDone
			m.dev++
		}
		units++
	}
	return units, nil
}

func (m *naiveRing) naiveFault(seq uint64) error {
	if seq < m.dev {
		return ErrFaultBeforeDev
	}
	m.faults[seq] = true
	return nil
}

func (m *naiveRing) naiveReap() (ReapResult, error) {
	if m.reap >= m.prod {
		return ReapResult{}, ErrNoPacket
	}
	off := uint64(0)
	for {
		s := &m.slots[(m.reap+off)%uint64(m.n)]
		if s.OWN == 1 {
			return ReapResult{}, ErrPacketIncomplete
		}
		if s.LAST == 1 {
			break
		}
		off++
	}
	nSeg := int(off) + 1
	res := ReapResult{Segments: nSeg, FaultIndex: -1}
	for i := 0; i < nSeg; i++ {
		s := &m.slots[(m.reap+uint64(i))%uint64(m.n)]
		res.TotalLen += s.Len
		if s.St == StFault {
			res.FaultIndex = i
		}
	}
	m.reap += uint64(nSeg)
	return res, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
