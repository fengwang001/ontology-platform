package rebalance

import "sort"

// BeginRebalance computes the sorted list of keys whose owning partition
// changes under the target partition count and opens a migration window.
// Nothing is copied or deleted until MigrateSteps is called.
func (s *Store) BeginRebalance(targetPartitions int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == PhaseMigrating {
		s.log("BEGIN target=%d REJECTED: %s | %s", targetPartitions, ErrRebalanceInProgress, s.debugDumpLocked("begin-reject"))
		return ErrRebalanceInProgress
	}
	if targetPartitions < 1 || targetPartitions == s.n {
		s.log("BEGIN target=%d REJECTED: %s | %s", targetPartitions, ErrInvalidPartitionCount, s.debugDumpLocked("begin-reject"))
		return ErrInvalidPartitionCount
	}

	oldN := s.n
	s.target = targetPartitions
	for len(s.partitions) < targetPartitions {
		s.partitions = append(s.partitions, map[string]string{})
	}

	var moveKeys []string
	for pid := 0; pid < oldN; pid++ {
		for key := range s.partitions[pid] {
			if int(s.hash(key)%uint64(targetPartitions)) != pid {
				moveKeys = append(moveKeys, key)
			}
		}
	}
	sort.Strings(moveKeys)

	s.moveKeys = moveKeys
	s.cursor = 0
	s.phase = PhaseMigrating
	s.log("BEGIN %d->%d moveList=%s | %s", oldN, targetPartitions, moveKeys, s.debugDumpLocked("begin"))
	return nil
}

// MigrateSteps migrates up to max records. A non-positive max migrates all
// remaining records. Each record is copied to its target partition and
// removed from the old partition exactly once, in sorted-key order, with
// the cursor advanced after every record.
func (s *Store) MigrateSteps(max int) (done int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseMigrating {
		s.log("MIGRATE REJECTED: %s | %s", ErrNotMigrating, s.debugDumpLocked("migrate-reject"))
		return 0, ErrNotMigrating
	}
	remaining := len(s.moveKeys) - s.cursor
	if max > 0 && max < remaining {
		remaining = max
	}
	for ; done < remaining; done++ {
		key := s.moveKeys[s.cursor]
		oldHome := int(s.hash(key) % uint64(s.n))
		newHome := int(s.hash(key) % uint64(s.target))
		value, ok := s.partitions[oldHome][key]
		if !ok {
			s.log("MIGRATE key=%q REJECTED: %s, key missing at P%d | %s", key, errInvariantViolated, oldHome, s.debugDumpLocked("migrate-invariant"))
			return done, errInvariantViolated
		}
		s.partitions[newHome][key] = value
		delete(s.partitions[oldHome], key)
		s.cursor++
		s.log("MIGRATE key=%q P%d->P%d cursor=%d/%d old=hash%%%d new=hash%%%d | %s",
			key, oldHome, newHome, s.cursor, len(s.moveKeys), s.n, s.target, s.debugDumpLocked("migrate"))
	}
	return done, nil
}

// Commit switches the active partition count. It is accepted only after
// every record on the migration list has been migrated.
func (s *Store) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseMigrating {
		s.log("COMMIT REJECTED: %s | %s", ErrNotMigrating, s.debugDumpLocked("commit-reject"))
		return ErrNotMigrating
	}
	if s.cursor < len(s.moveKeys) {
		s.log("COMMIT REJECTED: %s cursor=%d/%d | %s", ErrMigrationIncomplete, s.cursor, len(s.moveKeys), s.debugDumpLocked("commit-reject"))
		return ErrMigrationIncomplete
	}
	oldN := s.n
	s.n = s.target
	s.phase = PhaseIdle
	s.moveKeys = nil
	s.cursor = 0
	s.log("COMMIT %d->%d | %s", oldN, s.n, s.debugDumpLocked("commit"))
	return nil
}
