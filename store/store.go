// Package store ties together version chains, snapshots and the
// reclaimer into a multi-version key-value store with snapshot isolation.
package store

import (
	"errors"
	"sync"
	"time"

	"ontology/reclaim"
	"ontology/snapshot"
	"ontology/txid"
	"ontology/version"
)

// Distinguishable resource-limit errors.
var (
	ErrChainTooLong     = errors.New("store: key version chain length limit exceeded")
	ErrTooManySnapshots = errors.New("store: active snapshot limit exceeded")
	ErrTooManyVersions  = errors.New("store: total version limit exceeded")
	ErrTxClosed         = errors.New("store: transaction already closed")
)

// State is the result of a read: never existed, present, or deleted.
type State int

const (
	NeverExisted State = iota
	Present
	Deleted
)

// Options configures a Store. Zero limits mean unlimited.
type Options struct {
	MaxChainLen  int
	MaxSnapshots int
	MaxVersions  int
	Now          func() time.Time // injected clock; defaults to time.Now
	// CrashHook is invoked at each commit stage boundary (0..3) and may
	// panic to simulate a crash; call Recover afterwards.
	CrashHook func(stage int)
}

// Store is safe for concurrent use. No operation blocks on behalf of
// another key or transaction: critical sections are short and global.
type Store struct {
	mu        sync.Mutex
	mgr       *snapshot.Manager
	chains    map[string]*version.Chain
	pendKey   map[string]int // uncommitted versions per key
	pendTotal int
	total     int // committed versions retained
	rec       *reclaim.Reclaimer
	txs       map[txid.ID]*Tx
	opts      Options
}

// New returns an empty Store.
func New(opts Options) *Store {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Store{
		mgr:     snapshot.NewManager(opts.Now),
		chains:  map[string]*version.Chain{},
		pendKey: map[string]int{},
		rec:     reclaim.New(),
		txs:     map[txid.ID]*Tx{},
		opts:    opts,
	}
}

func (s *Store) crash(stage int) {
	if s.opts.CrashHook != nil {
		s.opts.CrashHook(stage)
	}
}

func (s *Store) chainLen(key string) int {
	if c := s.chains[key]; c != nil {
		return c.Len()
	}
	return 0
}

// Commit makes t's writes visible in one atomic finalize step. Crash
// points: 0 before append, 1 after append, 2 after indexing, 3 after
// finalize (fully committed).
func (s *Store) Commit(t *Tx) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.closed {
		return ErrTxClosed
	}
	s.crash(0)
	for key, w := range t.writes { // stage A: append versions
		c := s.chains[key]
		if c == nil {
			c = &version.Chain{}
			s.chains[key] = c
		}
		w.pos = c.Insert(&version.Version{CommitTx: t.id, Value: w.value, Deleted: w.deleted})
		w.appended = true
		s.total++
		s.pendKey[key]--
		s.pendTotal--
	}
	s.crash(1)
	for key, w := range t.writes { // stage B: index shadowed candidates
		if w.pos > 0 {
			below := s.chains[key].At(w.pos - 1)
			s.rec.Add(reclaim.Candidate{Key: key, Tx: below.CommitTx, Above: t.id})
		}
	}
	s.crash(2)
	delete(s.txs, t.id) // stage C: finalize (atomic visibility boundary)
	s.mgr.EndTx(t.id)
	t.snap.Close()
	t.closed = true
	s.crash(3)
	return nil
}

// Rollback discards t's buffered writes, leaving no residue.
func (s *Store) Rollback(t *Tx) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.closed {
		return ErrTxClosed
	}
	for key := range t.writes {
		s.pendKey[key]--
		s.pendTotal--
	}
	delete(s.txs, t.id)
	s.mgr.EndTx(t.id)
	t.snap.Close()
	t.closed = true
	return nil
}

// Recover rolls back every non-finalized transaction after a simulated
// crash and rebuilds the candidate index of each affected key from its
// chain, so the index never points at a missing version.
func (s *Store) Recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, t := range s.txs {
		keys := map[string]bool{}
		for key, w := range t.writes {
			if w.appended {
				s.chains[key].Remove(id)
				s.total--
			} else {
				s.pendKey[key]--
				s.pendTotal--
			}
			keys[key] = true
		}
		for key := range keys {
			s.rec.RemoveKey(key)
			c := s.chains[key]
			if c == nil {
				continue
			}
			for i := 1; i < c.Len(); i++ {
				s.rec.Add(reclaim.Candidate{Key: key, Tx: c.At(i - 1).CommitTx, Above: c.At(i).CommitTx})
			}
		}
		s.mgr.EndTx(id)
		t.snap.Close()
		t.closed = true
		delete(s.txs, id)
	}
}

// Reclaim runs one incremental reclaim pass and returns the number of
// candidates examined.
func (s *Store) Reclaim() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Reclaim(s.mgr.List(), func(c reclaim.Candidate) {
		s.chains[c.Key].Remove(c.Tx)
		s.total--
	})
}
