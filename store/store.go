package store

import (
	"sync"

	"ontology/reclaim"
	"ontology/snapshot"
	"ontology/txid"
	"ontology/version"
)

// CrashPoint identifies every interruption point in the write protocol.
type CrashPoint uint8

const (
	// CpAfterID: after the transaction id is durably begun, before the
	// version body is journaled.
	CpAfterID CrashPoint = iota + 1
	// CpAfterVersion: after the version payload is durable, before the
	// volatile version body exists / is linked into the key index.
	CpAfterVersion
	// CpAfterIndex: after the version is linked in the chain index, before
	// the commit marker.
	CpAfterIndex
	// CpAfterCommit: after the commit marker is durable, before the volatile
	// chains are flipped to committed (apply).
	CpAfterCommit
)

// AllCrashPoints is the exhaustive enumeration tests iterate.
var AllCrashPoints = []CrashPoint{CpAfterID, CpAfterVersion, CpAfterIndex, CpAfterCommit}

// Name renders a crash point for diagnostics.
func (c CrashPoint) Name() string {
	switch c {
	case CpAfterID:
		return "AfterID"
	case CpAfterVersion:
		return "AfterVersion"
	case CpAfterIndex:
		return "AfterIndex"
	case CpAfterCommit:
		return "AfterCommit"
	}
	return "Unknown"
}

// CrashHook, if set, is invoked at each write-protocol boundary. Returning
// true simulates a crash at that point: the volatile store becomes unusable
// and only Recover on the durable journal can resume.
type CrashHook func(txn txid.ID, point CrashPoint) (crash bool)

// Store is the in-process MVCC store. One mutex serializes structural change;
// critical sections do no blocking work, so a long-lived transaction on key A
// never prevents operations on key B (transactions hold no store locks
// between calls).
type Store struct {
	cfg Config

	mu      sync.Mutex
	ids     txid.Source
	chains  map[string]*version.Chain
	txns    map[txid.ID]*writeTxn
	total   int
	journal *journal

	reg *snapshot.Registry
	rec *reclaim.Reclaimer

	hook CrashHook
	dead bool
}

// writeTxn tracks an in-flight write transaction and its snapshot point.
type writeTxn struct {
	id       txid.ID
	snapshot *snapshot.Snapshot
	keys     map[string]struct{}
	done     bool
}

// New constructs a store with internal id source and journal.
func New(cfg Config, opts ...Option) *Store {
	s := &Store{
		cfg:     cfg,
		chains:  make(map[string]*version.Chain),
		txns:    make(map[txid.ID]*writeTxn),
		journal: newJournal(),
	}
	s.ids = txid.NewSource()
	for _, o := range opts {
		o(s)
	}
	s.reg = snapshot.NewRegistry(s.ids, cfg.MaxActiveSnapshots)
	s.rec = reclaim.New(s.reg)
	return s
}

// SetCrashHook installs the crash-injection hook (tests/demo only).
func (s *Store) SetCrashHook(h CrashHook) {
	s.mu.Lock()
	s.hook = h
	s.mu.Unlock()
}

// ActiveSnapshotCount exposes the read-only registry size.
func (s *Store) ActiveSnapshotCount() int { return s.reg.Count() }

// Watermark returns the current reclaim watermark.
func (s *Store) Watermark() txid.ID { return s.rec.Watermark() }

// TotalVersions returns the number of versions currently attached.
func (s *Store) TotalVersions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// activeWritersLocked snapshots the current in-flight writer id set.
func (s *Store) activeWritersLocked() []txid.ID {
	ids := make([]txid.ID, 0, len(s.txns))
	for id, t := range s.txns {
		if !t.done {
			ids = append(ids, id)
		}
	}
	return ids
}
