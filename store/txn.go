package store

import (
	"errors"

	"ontology/snapshot"
	"ontology/txid"
	"ontology/version"
)

// View is a read handle. It may be a pure snapshot or a write transaction's
// own read view (read-your-writes).
type View struct {
	store *Store
	snap  *snapshot.Snapshot
	txn   *writeTxn // nil for pure snapshots
}

// BeginRead opens an immutable snapshot. Later commits are invisible to it
// for its whole lifetime.
func (s *Store) BeginRead() (*View, error) {
	s.mu.Lock()
	active := s.activeWritersLocked()
	s.mu.Unlock()
	snap, err := s.reg.Open(active)
	if err != nil {
		if errors.Is(err, snapshot.ErrTooManySnapshots) {
			return nil, ErrSnapshotLimit
		}
		return nil, err
	}
	return &View{store: s, snap: snap}, nil
}

// Close releases the snapshot.
func (v *View) Close() { v.store.reg.Close(v.snap) }

// Begin starts a write transaction: allocates its id from the injected
// source, journals Begin, and gives it a private read view.
func (s *Store) Begin() (*View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return nil, errors.New("store: crashed; Recover required")
	}
	if s.cfg.MaxActiveSnapshots > 0 && s.reg.Count() >= s.cfg.MaxActiveSnapshots {
		return nil, ErrSnapshotLimit
	}
	id, err := s.ids.Allocate()
	if err != nil {
		return nil, err
	}
	s.journal.Begin(id)
	if s.hook != nil && s.hook(id, CpAfterID) {
		s.crashLocked()
		return nil, errCrashed
	}
	t := &writeTxn{id: id, keys: make(map[string]struct{})}
	s.txns[id] = t
	// Writer's read view includes all *other* in-flight writers in its active
	// set and itself via WithSelf.
	snap, err := s.reg.Open(s.activeWritersLockedExcluding(id))
	if err != nil {
		delete(s.txns, id)
		if errors.Is(err, snapshot.ErrTooManySnapshots) {
			return nil, ErrSnapshotLimit
		}
		return nil, err
	}
	t.snapshot = snap.WithSelf(id)
	return &View{store: s, snap: t.snapshot, txn: t}, nil
}

func (s *Store) activeWritersLockedExcluding(self txid.ID) []txid.ID {
	ids := make([]txid.ID, 0, len(s.txns))
	for id, t := range s.txns {
		if !t.done && id != self {
			ids = append(ids, id)
		}
	}
	return ids
}

// Put appends a value version for key in the view's write transaction.
func (v *View) Put(key string, value []byte) error {
	return v.store.write(v, key, version.KindValue, value)
}

// Delete appends a tombstone version for key.
func (v *View) Delete(key string) error {
	return v.store.write(v, key, version.KindDelete, nil)
}

// Get returns the three-way lookup result on the view's fixed snapshot.
func (v *View) Get(key string) version.Result {
	v.store.mu.Lock()
	r := version.Result{Outcome: version.Absent}
	if ch := v.store.chains[key]; ch != nil {
		r = ch.Lookup(v.snap)
	}
	v.store.mu.Unlock()
	return r
}

// ID returns the write transaction id, or Invalid for a pure read view.
func (v *View) ID() txid.ID {
	if v.txn == nil {
		return txid.Invalid
	}
	return v.txn.id
}
