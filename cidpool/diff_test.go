package cidpool

import "bytes"

// naiveEntry is the independent, straightforward model of one stored entry.
type naiveEntry struct {
	cid     []byte
	token   [16]byte
	retired bool
}

// naiveModel reimplements the spec directly, without sharing any code with
// Pool: the entire state is rebuilt and checked on every step.
type naiveModel struct {
	limit   int
	R       uint64
	entries map[uint64]naiveEntry
	retires []uint64
	// paths maps pid to occupied seq; a parked path stores parked=true.
	paths map[uint64]struct {
		seq    uint64
		parked bool
	}
	log bytes.Buffer
}

func newNaiveModel(limit int) *naiveModel {
	m := &naiveModel{
		limit:   limit,
		entries: map[uint64]naiveEntry{},
		paths: map[uint64]struct {
			seq    uint64
			parked bool
		}{},
	}
	var t0 [16]byte
	t0[15] = 0
	m.entries[0] = naiveEntry{cid: append([]byte(nil), cid(0)...), token: t0}
	return m
}

func (m *naiveModel) activeCount() int {
	n := 0
	for _, e := range m.entries {
		if !e.retired {
			n++
		}
	}
	return n
}

// smallestFree returns the smallest active seq absent from occupied.
func (m *naiveModel) smallestFree(occupied map[uint64]bool) (uint64, bool) {
	best, found := uint64(0), false
	for s, e := range m.entries {
		if e.retired || occupied[s] {
			continue
		}
		if !found || s < best {
			best, found = s, true
		}
	}
	return best, found
}

// reassign moves every parked path, in ascending pid order, to the
// smallest unoccupied active entry.
func (m *naiveModel) reassign() {
	occupied := map[uint64]bool{}
	var parked []uint64
	for pid, slot := range m.paths {
		if slot.parked {
			parked = append(parked, pid)
		} else {
			occupied[slot.seq] = true
		}
	}
	// Insertion sort keeps the model deliberately simple and explicit.
	for i := 1; i < len(parked); i++ {
		for j := i; j > 0 && parked[j-1] > parked[j]; j-- {
			parked[j-1], parked[j] = parked[j], parked[j-1]
		}
	}
	for _, pid := range parked {
		best, found := m.smallestFree(occupied)
		if !found {
			continue
		}
		occupied[best] = true
		m.paths[pid] = struct {
			seq    uint64
			parked bool
		}{seq: best}
	}
}

func (m *naiveModel) parkOnRetired() {
	for pid, slot := range m.paths {
		if slot.parked {
			continue
		}
		if e, ok := m.entries[slot.seq]; !ok || e.retired {
			slot.parked = true
			m.paths[pid] = slot
		}
	}
}

func (m *naiveModel) onNew(seq, rpt uint64, c, token []byte) error {
	// (一) Encoding.
	if seq > maxSeq || rpt > seq || len(c) < 1 || len(c) > 20 || len(token) != 16 {
		return ErrEncoding
	}
	// (二) Duplicate / conflict.
	if old, ok := m.entries[seq]; ok {
		if bytes.Equal(old.cid, c) && old.token == toToken(token) {
			return nil // duplicate: no change at all
		}
		return ErrViolation
	}
	for _, e := range m.entries {
		if bytes.Equal(e.cid, c) {
			return ErrViolation
		}
	}
	// (三) Speculate on a scratch copy and commit only if within the limit.
	snapshot := m.snapshot()
	newR := m.R
	if rpt > newR {
		newR = rpt
	}
	var retiring []uint64
	for s, e := range m.entries {
		if !e.retired && s < newR {
			e.retired = true
			m.entries[s] = e
			retiring = append(retiring, s)
		}
	}
	ne := naiveEntry{cid: append([]byte(nil), c...), token: toToken(token), retired: seq < newR}
	m.entries[seq] = ne
	if m.activeCount() > m.limit {
		m.restore(snapshot) // rollback: no tombstone, R unchanged
		return ErrLimit
	}
	m.R = newR
	if ne.retired {
		retiring = append(retiring, seq)
	}
	for i := 1; i < len(retiring); i++ {
		for j := i; j > 0 && retiring[j-1] > retiring[j]; j-- {
			retiring[j-1], retiring[j] = retiring[j], retiring[j-1]
		}
	}
	m.retires = append(m.retires, retiring...)
	m.parkOnRetired()
	m.reassign()
	return nil
}

func toToken(b []byte) [16]byte {
	var t [16]byte
	copy(t[:], b)
	return t
}

type naiveSnapshot struct {
	R       uint64
	entries map[uint64]naiveEntry
	retires []uint64
	paths   map[uint64]struct {
		seq    uint64
		parked bool
	}
}

func (m *naiveModel) snapshot() naiveSnapshot {
	entries := make(map[uint64]naiveEntry, len(m.entries))
	for s, e := range m.entries {
		e.cid = append([]byte(nil), e.cid...)
		entries[s] = e
	}
	paths := make(map[uint64]struct {
		seq    uint64
		parked bool
	}, len(m.paths))
	for pid, slot := range m.paths {
		paths[pid] = slot
	}
	return naiveSnapshot{R: m.R, entries: entries, retires: append([]uint64(nil), m.retires...), paths: paths}
}

func (m *naiveModel) restore(s naiveSnapshot) {
	m.R = s.R
	m.entries = s.entries
	m.retires = s.retires
	m.paths = s.paths
}

func (m *naiveModel) newPath(pid uint64) error {
	if _, ok := m.paths[pid]; ok {
		return ErrArg
	}
	occupied := map[uint64]bool{}
	for _, slot := range m.paths {
		if !slot.parked {
			occupied[slot.seq] = true
		}
	}
	best, found := m.smallestFree(occupied)
	if !found {
		return ErrNoCID
	}
	m.paths[pid] = struct {
		seq    uint64
		parked bool
	}{seq: best}
	return nil
}

func (m *naiveModel) freePath(pid uint64) error {
	if _, ok := m.paths[pid]; !ok {
		return ErrArg
	}
	delete(m.paths, pid)
	m.reassign()
	return nil
}

func (m *naiveModel) retire(seq uint64) error {
	e, ok := m.entries[seq]
	if !ok || e.retired {
		return ErrArg
	}
	e.retired = true
	m.entries[seq] = e
	m.retires = append(m.retires, seq)
	m.parkOnRetired()
	m.reassign()
	return nil
}
