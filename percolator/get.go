package percolator

// Get returns the newest Put/Delete version visible at rts.
//
// A lock with startTS <= rts blocks the read: the primary's status is
// resolved first. A committed primary rolls the secondary lock forward
// with the primary's commit timestamp; a rolled-back primary removes the
// lock with a Rollback record; a live lock produces ErrKeyLocked without
// changing state.
func (s *Store) Get(key string, rts, now int64) (GetResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if key == "" || rts < 0 {
		return GetResult{}, ErrInvalidArg
	}
	if err := s.data.checkTimeRange(now); err != nil {
		return GetResult{}, err
	}
	if now < s.data.water {
		return GetResult{}, ErrClockSkew
	}
	s.data.water = now

	k := s.data.keys[key]
	if k == nil {
		return GetResult{Exists: false}, nil
	}
	if k.lock != nil && k.lock.StartTS <= rts {
		lk := k.lock
		res := resolveLockedPrimary(&s.data, lk.Primary, lk.StartTS, now)
		switch res.Status {
		case StatusCommitted:
			// The primary resolution may have been for the same key; only
			// roll forward when the lock is still present.
			if k.lock != nil && k.lock.StartTS == lk.StartTS {
				fwd := k.lock
				k.lock = nil
				k.putVersion(Version{
					CommitTS: res.CommitTS,
					StartTS:  fwd.StartTS,
					Kind:     fwd.Kind,
					Value:    fwd.Value,
				})
			}
		case StatusRolledBack:
			if k.lock != nil && k.lock.StartTS == lk.StartTS {
				k.lock = nil
				addRollback(k, lk.StartTS)
			}
		default:
			return GetResult{}, ErrKeyLocked
		}
	}

	for _, v := range k.versions {
		if v.CommitTS <= rts {
			if v.Kind == Rollback {
				continue
			}
			if v.Kind == Delete {
				return GetResult{Exists: false}, nil
			}
			return GetResult{Exists: true, Value: v.Value}, nil
		}
	}
	return GetResult{Exists: false}, nil
}
