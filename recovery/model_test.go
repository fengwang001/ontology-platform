package recovery_test

import (
	"sort"

	"ontology/recovery"
)

// naive is an independent, straightforward step-by-step simulation of the
// specification, used as the reference model for randomized differential
// testing. It deliberately mirrors the spec text rather than the engine's
// internal structure.
type naive struct {
	lmax    int
	log     []recovery.Record
	pages   map[int]int64
	last    map[int]int
	state   map[int]int // 0 active, 1 committed, 2 aborted
	crashed bool
	started bool
	losers  map[int]int // loser txn -> current next pointer
	pending []int       // losers whose E record must be appended next
}

func newNaive(lmax int) *naive {
	return &naive{
		lmax:  lmax,
		pages: map[int]int64{},
		last:  map[int]int{},
		state: map[int]int{},
	}
}

func (m *naive) append(typ recovery.RecType, txn, page int, delta int64, prev, undoNext int) int {
	lsn := len(m.log) + 1
	m.log = append(m.log, recovery.Record{
		LSN: lsn, Type: typ, Txn: txn, Page: page,
		Delta: delta, PrevLSN: prev, UndoNext: undoNext,
	})
	m.last[txn] = lsn
	if typ == recovery.RecUpdate || typ == recovery.RecCompensation {
		m.pages[page] += delta
	}
	return lsn
}

func (m *naive) nextOf(txn int) int {
	l := m.last[txn]
	if l == 0 {
		return 0
	}
	if r := m.log[l-1]; r.Type == recovery.RecCompensation {
		return r.UndoNext
	}
	return l
}

func validTxnID(id int) bool { return id >= 1 && id <= recovery.MaxTxnID }

func (m *naive) active(id int) (int, error) {
	st, ok := m.state[id]
	if !ok {
		return 0, recovery.ErrTxnNotFound
	}
	if st != 0 {
		return 0, recovery.ErrTxnTerminated
	}
	return 0, nil
}

func (m *naive) Begin(id int) error {
	if !validTxnID(id) {
		return recovery.ErrInvalidArg
	}
	if m.crashed {
		return recovery.ErrCrashed
	}
	if _, ok := m.state[id]; ok {
		return recovery.ErrTxnExists
	}
	m.state[id] = 0
	m.last[id] = 0
	return nil
}

func (m *naive) Update(id, page int, d int64) (int, error) {
	if !validTxnID(id) || page < 0 || page > recovery.MaxPageID ||
		d == 0 || d < -recovery.MaxDelta || d > recovery.MaxDelta {
		return 0, recovery.ErrInvalidArg
	}
	if m.crashed {
		return 0, recovery.ErrCrashed
	}
	if _, err := m.active(id); err != nil {
		return 0, err
	}
	if len(m.log) >= m.lmax {
		return 0, recovery.ErrLogFull
	}
	return m.append(recovery.RecUpdate, id, page, d, m.last[id], 0), nil
}

func (m *naive) Save(id int) (int, error) {
	if !validTxnID(id) {
		return 0, recovery.ErrInvalidArg
	}
	if m.crashed {
		return 0, recovery.ErrCrashed
	}
	if _, err := m.active(id); err != nil {
		return 0, err
	}
	return m.last[id], nil
}

func (m *naive) Commit(id int) error {
	if !validTxnID(id) {
		return recovery.ErrInvalidArg
	}
	if m.crashed {
		return recovery.ErrCrashed
	}
	if _, err := m.active(id); err != nil {
		return err
	}
	if len(m.log) >= m.lmax {
		return recovery.ErrLogFull
	}
	m.append(recovery.RecCommit, id, 0, 0, m.last[id], 0)
	m.state[id] = 1
	return nil
}

func (m *naive) countUndo(id, s int) int {
	k := 0
	for q := m.nextOf(id); q > s; {
		r := m.log[q-1]
		if r.Type == recovery.RecCompensation {
			q = r.UndoNext
		} else {
			k++
			q = r.PrevLSN
		}
	}
	return k
}

func (m *naive) doRollback(id, s int) int {
	n := 0
	for q := m.nextOf(id); q > s; {
		r := m.log[q-1]
		if r.Type == recovery.RecCompensation {
			q = r.UndoNext
			continue
		}
		m.append(recovery.RecCompensation, id, r.Page, -r.Delta, m.last[id], r.PrevLSN)
		n++
		q = r.PrevLSN
	}
	return n
}

func (m *naive) Rollback(id, s int) (int, error) {
	if !validTxnID(id) {
		return 0, recovery.ErrInvalidArg
	}
	if m.crashed {
		return 0, recovery.ErrCrashed
	}
	if _, err := m.active(id); err != nil {
		return 0, err
	}
	if s != 0 && (s < 0 || s > len(m.log) || m.log[s-1].Txn != id) {
		return 0, recovery.ErrBadSavepoint
	}
	if len(m.log)+m.countUndo(id, s) > m.lmax {
		return 0, recovery.ErrLogFull
	}
	return m.doRollback(id, s), nil
}

func (m *naive) Abort(id int) (int, error) {
	if !validTxnID(id) {
		return 0, recovery.ErrInvalidArg
	}
	if m.crashed {
		return 0, recovery.ErrCrashed
	}
	if _, err := m.active(id); err != nil {
		return 0, err
	}
	if len(m.log)+m.countUndo(id, 0)+1 > m.lmax {
		return 0, recovery.ErrLogFull
	}
	n := m.doRollback(id, 0)
	m.append(recovery.RecEnd, id, 0, 0, m.last[id], 0)
	m.state[id] = 2
	return n + 1, nil
}

func (m *naive) Crash() error {
	if m.crashed {
		return recovery.ErrCrashed
	}
	m.crashed = true
	m.started = false
	m.pending = nil
	m.losers = map[int]int{}
	for id, st := range m.state {
		if st == 0 {
			m.losers[id] = m.nextOf(id)
		}
	}
	return nil
}

func (m *naive) appendEnd(id int) {
	m.append(recovery.RecEnd, id, 0, 0, m.last[id], 0)
	m.state[id] = 2
	delete(m.losers, id)
}

func (m *naive) restartOne() bool {
	if len(m.pending) > 0 {
		m.appendEnd(m.pending[0])
		m.pending = m.pending[1:]
		return true
	}
	if !m.started {
		m.started = true
		ids := make([]int, 0, len(m.losers))
		for id, nx := range m.losers {
			if nx == 0 {
				ids = append(ids, id)
			}
		}
		sort.Ints(ids)
		m.pending = append(m.pending, ids...)
		if len(m.pending) > 0 {
			return m.restartOne()
		}
	}
	best, bestNext := -1, 0
	for id, nx := range m.losers {
		if nx > bestNext {
			best, bestNext = id, nx
		}
	}
	if best < 0 {
		m.crashed = false
		return false
	}
	q := bestNext
	for {
		r := m.log[q-1]
		if r.Type == recovery.RecCompensation {
			q = r.UndoNext
			m.losers[best] = q
			if q == 0 {
				m.appendEnd(best)
				return true
			}
			continue
		}
		m.append(recovery.RecCompensation, best, r.Page, -r.Delta, m.last[best], r.PrevLSN)
		m.losers[best] = r.PrevLSN
		if r.PrevLSN == 0 {
			m.pending = append(m.pending, best)
		}
		return true
	}
}

func (m *naive) Restart() (int, error) {
	if !m.crashed {
		return 0, recovery.ErrNotCrashed
	}
	n := 0
	for m.restartOne() {
		n++
	}
	return n, nil
}

func (m *naive) RestartStep(n int) (int, error) {
	if n < 1 {
		return 0, recovery.ErrInvalidArg
	}
	if !m.crashed {
		return 0, recovery.ErrNotCrashed
	}
	c := 0
	for c < n && m.restartOne() {
		c++
	}
	return c, nil
}
