package epaxos

import "sort"

// commitLocked validates and registers a committed instance.
//
// Validation order (the first failure is reported, state is unchanged):
//  1. the instance or any dep has R out of [0, N) or I < 1
//  2. seq is not positive
//  3. deps contain the instance itself
//  4. deps contain duplicates
//  5. the instance is already registered with a different seq or dep set
//  6. registering a new instance would exceed the capacity
//
// Re-committing an instance with identical seq and dep set (order ignored)
// is a legal no-op, whether or not it has executed.
func (s *Scheduler) commitLocked(inst Instance, seq int, deps []Instance) error {
	if err := s.checkInstance(inst); err != nil {
		return err
	}
	for _, d := range deps {
		if err := s.checkInstance(d); err != nil {
			return err
		}
	}
	if seq <= 0 {
		return newError(ErrInvalidSeq, "epaxos: seq must be positive, got %d for %v", seq, inst)
	}
	depSet := make(map[Instance]struct{}, len(deps))
	for _, d := range deps {
		if d == inst {
			return newError(ErrSelfDependency, "epaxos: %v depends on itself", inst)
		}
		if _, dup := depSet[d]; dup {
			return newError(ErrDuplicateDependency, "epaxos: duplicate dependency %v for %v", d, inst)
		}
		depSet[d] = struct{}{}
	}
	if rec, ok := s.records[inst]; ok {
		if rec.seq == seq && sameDepSet(rec.depSet, depSet) {
			return nil // identical re-commit: legal no-op
		}
		return newError(ErrConflictingCommit, "epaxos: %v already committed with different seq/deps", inst)
	}
	if s.pending >= s.cap {
		return newError(ErrCapacityExceeded, "epaxos: capacity %d reached, cannot commit %v", s.cap, inst)
	}
	canon := make([]Instance, 0, len(deps))
	for d := range depSet {
		canon = append(canon, d)
	}
	sort.Slice(canon, func(a, b int) bool {
		if canon[a].R != canon[b].R {
			return canon[a].R < canon[b].R
		}
		return canon[a].I < canon[b].I
	})
	s.records[inst] = &record{seq: seq, deps: canon, depSet: depSet}
	s.pending++
	return nil
}

func (s *Scheduler) checkInstance(inst Instance) error {
	if inst.R < 0 || inst.R >= s.n || inst.I < 1 {
		return newError(ErrInvalidInstance, "epaxos: instance %v out of range (N=%d)", inst, s.n)
	}
	return nil
}

func sameDepSet(a, b map[Instance]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for d := range a {
		if _, ok := b[d]; !ok {
			return false
		}
	}
	return true
}
