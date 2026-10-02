package percolator

// CommitPrimary commits the primary key and allocates the commit timestamp.
func (s *Store) CommitPrimary(st int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := s.data.txns[st]
	if t == nil {
		return 0, ErrNoSuchTxn
	}
	if t.state != txnPrewritten {
		return 0, ErrTxnState
	}
	primary := s.data.keyOf(t.primary)
	if primary.lock == nil || primary.lock.StartTS != st {
		return 0, ErrLockLost
	}

	s.data.oracle++
	commitTS := s.data.oracle
	lk := primary.lock
	primary.lock = nil
	primary.putVersion(Version{
		CommitTS: commitTS,
		StartTS:  st,
		Kind:     lk.Kind,
		Value:    lk.Value,
	})
	t.state = txnCommitted
	t.commitTS = commitTS
	return commitTS, nil
}

// CommitKeys commits any listed keys that still hold st's lock, using the
// transaction's single commit timestamp. Keys whose lock is already gone
// (resolved by a reader or rolled back) are silently skipped.
func (s *Store) CommitKeys(st int64, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := s.data.txns[st]
	if t == nil {
		return ErrNoSuchTxn
	}
	if t.state != txnCommitted {
		return ErrTxnState
	}
	allowed := map[string]struct{}{}
	for _, m := range t.muts {
		allowed[m.Key] = struct{}{}
	}
	for _, key := range keys {
		if _, ok := allowed[key]; !ok {
			return ErrInvalidArg
		}
	}

	for _, key := range keys {
		k := s.data.keyOf(key)
		if k.lock == nil || k.lock.StartTS != st {
			continue
		}
		lk := k.lock
		k.lock = nil
		k.putVersion(Version{
			CommitTS: t.commitTS,
			StartTS:  st,
			Kind:     lk.Kind,
			Value:    lk.Value,
		})
	}
	return nil
}

// Abort rolls back every lock the transaction still holds and writes a
// Rollback record per key. Reader-driven rollbacks do not change the
// transaction state, so a prewritten transaction whose locks were all
// cleaned by readers can still be Abort-ed.
func (s *Store) Abort(st int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := s.data.txns[st]
	if t == nil {
		return ErrNoSuchTxn
	}
	if t.state != txnPrewritten {
		return ErrTxnState
	}

	for _, m := range t.muts {
		k := s.data.keyOf(m.Key)
		if k.lock != nil && k.lock.StartTS == st {
			k.lock = nil
		}
		if !k.hasRollback(st) {
			k.putVersion(Version{CommitTS: st, StartTS: st, Kind: Rollback})
		}
	}
	t.state = txnAborted
	return nil
}

// addRollback writes the protective Rollback record for st on k unless one
// already exists. The caller must hold the store mutex.
func addRollback(k *keyState, st int64) {
	if k.hasRollback(st) {
		return
	}
	k.putVersion(Version{CommitTS: st, StartTS: st, Kind: Rollback})
}

// resolveLockedPrimary inspects the primary key's state for st. It expires
// an overdue lock and writes protective rollbacks where needed. The caller
// must hold the store mutex.
func resolveLockedPrimary(d *storeData, primary string, st, now int64) TxnStatusResult {
	k := d.keyOf(primary)
	if k.lock != nil && k.lock.StartTS == st {
		if now >= k.lock.Deadline {
			k.lock = nil
			addRollback(k, st)
			return TxnStatusResult{Status: StatusRolledBack}
		}
		return TxnStatusResult{Status: StatusLive}
	}
	var committed *Version
	for i := range k.versions {
		v := &k.versions[i]
		if v.StartTS != st {
			continue
		}
		if v.Kind == Rollback {
			return TxnStatusResult{Status: StatusRolledBack}
		}
		if committed == nil || v.CommitTS > committed.CommitTS {
			committed = v
		}
	}
	if committed != nil {
		return TxnStatusResult{Status: StatusCommitted, CommitTS: committed.CommitTS}
	}
	addRollback(k, st)
	return TxnStatusResult{Status: StatusRolledBack}
}

// CheckTxnStatus resolves the state of a primary key / start timestamp.
func (s *Store) CheckTxnStatus(primary string, st, now int64) (TxnStatusResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if primary == "" || st < 1 {
		return TxnStatusResult{}, ErrInvalidArg
	}
	if err := s.data.checkTimeRange(now); err != nil {
		return TxnStatusResult{}, err
	}
	if now < s.data.water {
		return TxnStatusResult{}, ErrClockSkew
	}
	s.data.water = now
	return resolveLockedPrimary(&s.data, primary, st, now), nil
}
