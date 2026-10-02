package percolator

import "sync"

type txnState int

const (
	stateStarted txnState = iota
	statePrewritten
	stateCommitted
	stateAborted
)

type txnInfo struct {
	state    txnState
	muts     []Mutation
	primary  string
	commitTs uint64
}

type keyState struct {
	versions []Version
	lock     *Lock
}

func (s *Store) key(k string) *keyState {
	ks := s.keys[k]
	if ks == nil {
		ks = &keyState{}
		s.keys[k] = ks
	}
	return ks
}

func hasRollbackAt(ks *keyState, st uint64) bool {
	for _, v := range ks.versions {
		if v.StartTs == st && v.Type == Rollback {
			return true
		}
	}
	return false
}

// addRollback writes (commitTs=st, startTs=st, Rollback) unless an identical
// rollback record already exists.
func addRollback(ks *keyState, st uint64) {
	if hasRollbackAt(ks, st) {
		return
	}
	ks.versions = append(ks.versions, Version{
		CommitTs: st,
		StartTs:  st,
		Type:     Rollback,
	})
}

func committedAfter(ks *keyState, ts uint64) bool {
	for _, v := range ks.versions {
		if v.Type != Rollback && v.CommitTs > ts {
			return true
		}
	}
	return false
}

func (s *Store) checkNow(now uint64) error {
	if now > maxNow {
		return errf(ErrInvalidTime, "now %d exceeds %d", now, uint64(maxNow))
	}
	if now < s.watermark {
		return errf(ErrClockSkew, "now %d below watermark %d", now, s.watermark)
	}
	return nil
}

// Store is a concurrency-safe, deterministic Percolator-style transaction engine.
type Store struct {
	mu        sync.Mutex
	oracle    uint64
	watermark uint64
	txns      map[uint64]*txnInfo
	keys      map[string]*keyState
}

// NewStore creates an empty store: oracle starts at 0 and time watermark at 0.
func NewStore() *Store {
	return &Store{
		txns: map[uint64]*txnInfo{},
		keys: map[string]*keyState{},
	}
}

// Begin allocates and returns a fresh start timestamp.
func (s *Store) Begin() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oracle++
	s.txns[s.oracle] = &txnInfo{state: stateStarted}
	return s.oracle
}

// Prewrite is a stub.
// Prewrite attempts to lock every key of muts atomically.
func (s *Store) Prewrite(st uint64, muts []Mutation, primary string, ttl uint64, now uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Parameter validation first.
	if len(muts) == 0 {
		return errf(ErrInvalidArgument, "muts must be non-empty")
	}
	seen := map[string]bool{}
	primaryPresent := false
	for _, m := range muts {
		if m.Key == "" {
			return errf(ErrInvalidArgument, "mutation key must not be empty")
		}
		if m.Type != Put && m.Type != Delete {
			return errf(ErrInvalidArgument, "mutation type must be Put or Delete")
		}
		if seen[m.Key] {
			return errf(ErrInvalidArgument, "duplicate mutation key %q", m.Key)
		}
		seen[m.Key] = true
		if m.Key == primary {
			primaryPresent = true
		}
	}
	if primary == "" {
		return errf(ErrInvalidArgument, "primary key must not be empty")
	}
	if !primaryPresent {
		return errf(ErrInvalidArgument, "primary %q must be one of the mutation keys", primary)
	}
	if ttl < 1 || ttl > maxTTL {
		return errf(ErrInvalidArgument, "ttl must be in [1,%d]", maxTTL)
	}

	t, ok := s.txns[st]
	if !ok {
		return errf(ErrTxnNotFound, "start ts %d was never allocated", st)
	}
	if t.state != stateStarted {
		return errf(ErrInvalidState, "transaction %d already prewritten", st)
	}
	if now > maxNow {
		return errf(ErrInvalidTime, "now %d exceeds %d", now, uint64(maxNow))
	}
	if now < s.watermark {
		return errf(ErrClockSkew, "now %d below watermark %d", now, s.watermark)
	}

	expire := now + ttl

	// Whole-batch atomic check, key by key, in muts order.
	checked := make([]*keyState, 0, len(muts))
	for _, m := range muts {
		ks := s.key(m.Key)
		switch {
		case hasRollbackAt(ks, st):
			return errf(ErrAlreadyAborted, "key %q already has a rollback record for %d", m.Key, st)
		case ks.lock != nil && ks.lock.StartTs != st:
			return errf(ErrKeyLocked, "key %q locked by transaction %d", m.Key, ks.lock.StartTs)
		case committedAfter(ks, st):
			return errf(ErrWriteConflict, "key %q has a committed version newer than %d", m.Key, st)
		}
		checked = append(checked, ks)
	}

	// All checks passed: install every lock.
	for i, m := range muts {
		checked[i].lock = &Lock{
			StartTs: st,
			Primary: primary,
			Expire:  expire,
			Type:    m.Type,
			Value:   m.Value,
		}
	}
	mutsCopy := make([]Mutation, len(muts))
	copy(mutsCopy, muts)
	t.state = statePrewritten
	t.muts = mutsCopy
	t.primary = primary
	s.watermark = now
	return nil
}

// CommitPrimary is a stub.
// CommitPrimary commits the primary key and allocates the commit timestamp.
func (s *Store) CommitPrimary(st uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.txns[st]
	if !ok {
		return 0, errf(ErrTxnNotFound, "start ts %d was never allocated", st)
	}
	if t.state != statePrewritten {
		return 0, errf(ErrInvalidState, "transaction %d is not in prewritten state", st)
	}
	ks := s.key(t.primary)
	if ks.lock == nil || ks.lock.StartTs != st {
		return 0, errf(ErrLockLost, "primary key %q no longer holds the lock of %d", t.primary, st)
	}

	s.oracle++
	commitTs := s.oracle
	lk := ks.lock
	ks.versions = append(ks.versions, Version{
		CommitTs: commitTs,
		StartTs:  st,
		Type:     lk.Type,
		Value:    lk.Value,
	})
	ks.lock = nil
	t.state = stateCommitted
	t.commitTs = commitTs
	return commitTs, nil
}

// CommitKeys is a stub.
// CommitKeys commits secondary keys that still hold the transaction's lock,
// using the primary's commit timestamp.
func (s *Store) CommitKeys(st uint64, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.txns[st]
	if !ok {
		return errf(ErrTxnNotFound, "start ts %d was never allocated", st)
	}
	if t.state != stateCommitted {
		return errf(ErrInvalidState, "transaction %d is not committed", st)
	}
	owned := map[string]bool{}
	for _, m := range t.muts {
		owned[m.Key] = true
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if k == "" {
			return errf(ErrInvalidArgument, "key must not be empty")
		}
		if seen[k] {
			return errf(ErrInvalidArgument, "duplicate key %q", k)
		}
		seen[k] = true
		if !owned[k] {
			return errf(ErrInvalidArgument, "key %q is not part of transaction %d's write set", k, st)
		}
	}

	for _, k := range keys {
		ks := s.key(k)
		if ks.lock == nil || ks.lock.StartTs != st {
			continue
		}
		lk := ks.lock
		ks.versions = append(ks.versions, Version{
			CommitTs: t.commitTs,
			StartTs:  st,
			Type:     lk.Type,
			Value:    lk.Value,
		})
		ks.lock = nil
	}
	return nil
}

// Abort is a stub.
// Abort rolls back every lock the transaction still holds and records
// Rollback versions on each key.
func (s *Store) Abort(st uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.txns[st]
	if !ok {
		return errf(ErrTxnNotFound, "start ts %d was never allocated", st)
	}
	if t.state != statePrewritten {
		return errf(ErrInvalidState, "transaction %d is not in prewritten state", st)
	}

	for _, m := range t.muts {
		ks := s.key(m.Key)
		if ks.lock != nil && ks.lock.StartTs == st {
			ks.lock = nil
		}
		addRollback(ks, st)
	}
	t.state = stateAborted
	return nil
}

// CheckTxnStatus is a stub.
// CheckTxnStatus resolves the state of a transaction via its primary key.
// The returned timestamp is the commit ts when committed, otherwise 0.
func (s *Store) CheckTxnStatus(primary string, st uint64, now uint64) (TxnStatus, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if primary == "" {
		return 0, 0, errf(ErrInvalidArgument, "primary key must not be empty")
	}
	if st < 1 {
		return 0, 0, errf(ErrInvalidArgument, "start ts must be at least 1")
	}
	if err := s.checkNow(now); err != nil {
		return 0, 0, err
	}

	status, commitTs := s.resolveTxn(primary, st, now)
	s.watermark = now
	return status, commitTs, nil
}

// Get is a stub.
// Get returns the latest Put/Delete version with commitTs <= rts after
// resolving any blocking lock. A Delete (or no visible version) means absent.
func (s *Store) Get(key string, rts int64, now uint64) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if key == "" {
		return false, "", errf(ErrInvalidArgument, "key must not be empty")
	}
	if rts < 0 {
		return false, "", errf(ErrInvalidArgument, "read ts must not be negative")
	}
	if err := s.checkNow(now); err != nil {
		return false, "", err
	}

	ks := s.key(key)
	if ks.lock != nil && ks.lock.StartTs <= uint64(rts) {
		lk := ks.lock
		status, commitTs := s.resolveTxn(lk.Primary, lk.StartTs, now)
		switch status {
		case StatusCommitted:
			ks.versions = append(ks.versions, Version{
				CommitTs: commitTs,
				StartTs:  lk.StartTs,
				Type:     lk.Type,
				Value:    lk.Value,
			})
			ks.lock = nil
		case StatusRolledBack:
			ks.lock = nil
			addRollback(ks, lk.StartTs)
			// Lock cleaned; fall through to reading versions older than rts.
		default:
			// Live primary lock: the read is blocked and nothing changes.
			return false, "", errf(ErrLocked, "key %q locked by live transaction %d", key, lk.StartTs)
		}
	}

	s.watermark = now

	var best *Version
	for i := range ks.versions {
		v := &ks.versions[i]
		if v.Type == Rollback || v.CommitTs > uint64(rts) {
			continue
		}
		if best == nil || v.CommitTs > best.CommitTs {
			best = v
		}
	}
	if best == nil || best.Type == Delete {
		return false, "", nil
	}
	return true, best.Value, nil
}

// resolveTxn implements the primary-key resolution table. Caller holds s.mu
// and has already validated arguments and time. It never advances the
// watermark.
func (s *Store) resolveTxn(primary string, st uint64, now uint64) (TxnStatus, uint64) {
	ks := s.key(primary)
	if ks.lock != nil && ks.lock.StartTs == st {
		if now >= ks.lock.Expire {
			ks.lock = nil
			addRollback(ks, st)
			return StatusRolledBack, 0
		}
		return StatusLive, 0
	}
	for _, v := range ks.versions {
		if v.StartTs != st {
			continue
		}
		if v.Type == Rollback {
			return StatusRolledBack, 0
		}
		return StatusCommitted, v.CommitTs
	}
	// Neither lock nor version: protective rollback.
	addRollback(ks, st)
	return StatusRolledBack, 0
}
