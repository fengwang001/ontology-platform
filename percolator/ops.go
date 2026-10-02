package percolator

// cloneMuts returns a defensive copy of a mutation batch.
func cloneMuts(muts []Mutation) []Mutation {
	out := make([]Mutation, len(muts))
	copy(out, muts)
	return out
}

// Prewrite attempts to place the locks of one transaction batch.
//
// Checks are atomic over every key, in batch order, with per-key priority
// already-rolled-back, locked-by-another-txn, write-conflict. Only when
// every key passes are locks installed and the transaction marked
// prewritten. Any rejection leaves all state untouched.
func (s *Store) Prewrite(st int64, muts []Mutation, primary string, ttl, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Argument validation first: non-empty batch of distinct keys...
	if len(muts) == 0 {
		return ErrInvalidArg
	}
	seen := map[string]struct{}{}
	primaryPresent := false
	for _, m := range muts {
		if m.Key == "" || m.Kind != Put && m.Kind != Delete {
			return ErrInvalidArg
		}
		if _, dup := seen[m.Key]; dup {
			return ErrInvalidArg
		}
		seen[m.Key] = struct{}{}
		if m.Key == primary {
			primaryPresent = true
		}
	}
	if primary == "" || !primaryPresent || ttl < 1 || ttl > 1_000_000_000 {
		return ErrInvalidArg
	}
	// Range check early (before state checks), but do not advance the
	// water mark until all per-key checks pass.
	if err := s.data.checkTimeRange(now); err != nil {
		return err
	}

	// ...then transaction existence...
	t := s.data.txns[st]
	if t == nil {
		return ErrNoSuchTxn
	}
	// ...then transaction state (must be a fresh Begin, never prewritten)...
	if t.state != txnFresh {
		return ErrTxnState
	}

	// Validate every key before touching anything.
	type planned struct {
		ks *keyState
		m  Mutation
	}
	plans := make([]planned, 0, len(muts))
	for _, m := range muts {
		k := s.data.keys[m.Key] // do not create keys on a rejected path
		if k == nil {
			plans = append(plans, planned{nil, m})
			continue
		}
		switch {
		case k.hasRollback(st):
			return ErrAlreadyAbort
		case k.lock != nil && k.lock.StartTS != st:
			return ErrKeyLocked
		case k.committedAfter(st):
			return ErrWriteConflict
		}
		plans = append(plans, planned{k, m})
	}

	// Whole batch commits atomically.
	if err := s.data.checkClockAndAdvance(now); err != nil {
		return err
	}
	deadline := now + ttl
	for _, p := range plans {
		if p.ks == nil {
			p.ks = s.data.keyOf(p.m.Key)
		}
		p.ks.lock = &Lock{
			StartTS:  st,
			Primary:  primary,
			Deadline: deadline,
			Kind:     p.m.Kind,
			Value:    p.m.Value,
		}
	}
	t.state = txnPrewritten
	t.muts = cloneMuts(muts)
	t.primary = primary
	return nil
}
