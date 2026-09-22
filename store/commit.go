package store

// Commit durably commits and applies the transaction atomically across all
// keys it wrote. After this returns the transaction is either fully visible
// (every key) or, if a crash is injected, subject to all-or-nothing recovery.
func (v *View) Commit() error {
	if v.txn == nil {
		return errReadOnly
	}
	s := v.store
	t := v.txn

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return errCrashed
	}
	if t.done {
		return errTxnClosed
	}

	// Stage 4: durable commit marker. This is the single atomic visibility
	// decision in the log.
	s.journal.Commit(t.id)
	if s.hook != nil && s.hook(t.id, CpAfterCommit) {
		// Volatile nodes still pending => nobody else sees the txn yet.
		s.crashLocked()
		return errCrashed
	}

	// Apply: flip pending nodes to committed and enqueue reclaim candidates
	// for keys that now have >=2 committed versions (the only keys that can
	// contain shadowed garbage).
	for key := range t.keys {
		ch := s.chains[key]
		ch.Commit(t.id, t.id)
		if ch.CommittedCount() >= 2 {
			s.rec.Notify(key)
		}
	}
	t.done = true
	s.reg.Close(v.snap)
	delete(s.txns, t.id)
	return nil
}

// Rollback detaches every version the transaction wrote (volatile and
// journal): it becomes permanently invisible with no residue a reclaimer
// could mistake for live data.
func (v *View) Rollback() error {
	if v.txn == nil {
		return errReadOnly
	}
	s := v.store
	t := v.txn

	s.mu.Lock()
	defer s.mu.Unlock()
	if t.done {
		return errTxnClosed
	}
	for key := range t.keys {
		if ch := s.chains[key]; ch != nil {
			n := ch.Abort(t.id)
			s.total -= n
		}
	}
	delete(s.journal.recs, t.id)
	t.done = true
	s.reg.Close(v.snap)
	delete(s.txns, t.id)
	return nil
}
