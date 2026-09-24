package store

import (
	"ontology/snapshot"
	"ontology/txid"
)

// write is one buffered, not yet committed write of a transaction.
type write struct {
	value    []byte
	deleted  bool
	appended bool // stage A of commit reached this write
	pos      int  // chain position after append
}

// Tx is a write transaction. Writes are buffered privately until Commit,
// so uncommitted data is never visible to other snapshots, while the
// transaction always reads its own writes.
type Tx struct {
	store  *Store
	id     txid.ID
	snap   *snapshot.Snapshot
	writes map[string]*write
	closed bool
}

// Begin opens a write transaction.
func (s *Store) Begin() *Tx {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.mgr.BeginTx()
	t := &Tx{store: s, id: id, snap: s.mgr.Open(), writes: map[string]*write{}}
	s.txs[id] = t
	return t
}

// ID returns the transaction identifier.
func (t *Tx) ID() txid.ID { return t.id }

// buffer returns the write buffer for key, enforcing resource limits
// before mutating any state.
func (t *Tx) buffer(key string) (*write, error) {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.closed {
		return nil, ErrTxClosed
	}
	if w, ok := t.writes[key]; ok {
		return w, nil
	}
	if s.opts.MaxChainLen > 0 && s.chainLen(key)+s.pendKey[key]+1 > s.opts.MaxChainLen {
		return nil, ErrChainTooLong
	}
	if s.opts.MaxVersions > 0 && s.total+s.pendTotal+1 > s.opts.MaxVersions {
		return nil, ErrTooManyVersions
	}
	w := &write{}
	t.writes[key] = w
	s.pendKey[key]++
	s.pendTotal++
	return w, nil
}

// Write buffers a put of key=value.
func (t *Tx) Write(key string, value []byte) error {
	w, err := t.buffer(key)
	if err != nil {
		return err
	}
	w.value = append([]byte(nil), value...)
	w.deleted = false
	return nil
}

// Delete buffers a deletion of key: a tombstone version, not an absence.
func (t *Tx) Delete(key string) error {
	w, err := t.buffer(key)
	if err != nil {
		return err
	}
	w.value = nil
	w.deleted = true
	return nil
}

// Get reads key: the transaction's own buffered write if present,
// otherwise the version visible to the transaction's snapshot.
func (t *Tx) Get(key string) (State, []byte) {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := t.writes[key]; ok {
		if w.deleted {
			return Deleted, nil
		}
		return Present, w.value
	}
	return s.get(t.snap, key)
}

// Commit is shorthand for store.Commit(t).
func (t *Tx) Commit() error { return t.store.Commit(t) }

// Rollback is shorthand for store.Rollback(t).
func (t *Tx) Rollback() error { return t.store.Rollback(t) }

// OpenSnapshot returns a new read snapshot.
func (s *Store) OpenSnapshot() (*snapshot.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.MaxSnapshots > 0 && s.mgr.Count() >= s.opts.MaxSnapshots {
		return nil, ErrTooManySnapshots
	}
	return s.mgr.Open(), nil
}

// Get reads key under snap.
func (s *Store) Get(snap *snapshot.Snapshot, key string) (State, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(snap, key)
}

func (s *Store) get(snap *snapshot.Snapshot, key string) (State, []byte) {
	c := s.chains[key]
	if c == nil {
		return NeverExisted, nil
	}
	v, ok := c.Select(snap.Eligible)
	if !ok {
		return NeverExisted, nil
	}
	if v.Deleted {
		return Deleted, nil
	}
	return Present, v.Value
}

// Stats is a point-in-time read-only report.
type Stats struct {
	ActiveSnapshots int
	Watermark       txid.ID
	TotalVersions   int
}

// Stats returns global statistics without advancing any state.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{ActiveSnapshots: s.mgr.Count(), Watermark: s.rec.Watermark(), TotalVersions: s.total}
}

// KeyStats returns the number of committed versions retained for key.
// Reclaimed or never-existing keys report zero.
func (s *Store) KeyStats(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chainLen(key)
}
