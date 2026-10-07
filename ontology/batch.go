package ontology

import "sort"

// Commit evaluates and, iff all gates pass, atomically applies a batch.
//
// The gates are evaluated in a fixed order and produce mutually exclusive
// results:
//  1. duplicate precondition for the same instance, rejected before any
//     instance version is read;
//  2. joint version precondition, read once for every instance at one
//     logical instant while all involved instance locks are held;
//  3. link-cardinality constraints evaluated on the would-be post state;
//  4. commit, with each instance advancing along its own version sequence.
//
// All mutation is staged on deep shadow copies; live state is published
// only after every gate has passed, so a rejected batch leaves no trace.
func (s *Store) Commit(b Batch) Result {
	// Gate 1: purely structural, observes no instance state.
	seen := make(map[ID]struct{}, len(b.Preconditions))
	for _, p := range b.Preconditions {
		if _, dup := seen[p.Instance]; dup {
			res := Result{BatchID: b.ID, Status: StatusDuplicatePrecondition, Duplicate: p.Instance}
			s.record(b, res)
			return res
		}
		seen[p.Instance] = struct{}{}
	}

	ids := touchedInstances(b)

	// Acquire every instance lock in global order (strict 2PL; the fixed
	// order keeps the wait-for graph acyclic, so deadlock is impossible).
	s.instanceMu.Lock()
	locked := make([]*instance, 0, len(ids))
	present := make(map[ID]*instance, len(ids))
	for _, id := range ids {
		if it, ok := s.instances[id]; ok {
			it.mu.Lock()
			locked = append(locked, it)
			present[id] = it
		}
	}
	s.instanceMu.Unlock()
	defer func() {
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].mu.Unlock()
		}
	}()
	s.acquireMu.Lock()
	s.acquireCounter++
	acquireOrder := s.acquireCounter
	s.acquireMu.Unlock()
	if s.testHookAcquired != nil {
		s.testHookAcquired(ids)
	}

	// Gate 2: joint version precondition at one logical instant.
	observed := make(map[ID]uint64, len(b.Preconditions))
	mismatches := []Mismatch{}
	for _, p := range b.Preconditions {
		it, ok := present[p.Instance]
		if !ok {
			mismatches = append(mismatches, Mismatch{Instance: p.Instance, Expected: p.ExpectedVersion, Missing: true})
			continue
		}
		observed[p.Instance] = it.version
		if it.version != p.ExpectedVersion {
			mismatches = append(mismatches, Mismatch{
				Instance: p.Instance,
				Expected: p.ExpectedVersion,
				Actual:   it.version,
			})
		}
	}
	for _, id := range ids {
		if _, ok := present[id]; !ok {
			mismatches = appendMissing(mismatches, id)
		}
	}
	sortMismatches(mismatches)
	if len(mismatches) > 0 {
		res := Result{
			AcquireOrder: acquireOrder,
			BatchID:      b.ID,
			Status:       StatusVersionMismatch,
			Mismatches:   mismatches,
			Observed:     observed,
		}
		s.record(b, res)
		return res
	}

	// Deep shadow copies of every touched instance; live maps stay
	// untouched until all gates pass.
	shadows := make(map[ID]*instance, len(ids))
	for _, id := range ids {
		shadows[id] = cloneInstance(present[id])
	}

	if violations := s.stageOps(b, shadows); len(violations) > 0 {
		res := Result{
			AcquireOrder: acquireOrder,
			BatchID:      b.ID,
			Status:       StatusCardinalityViolation,
			Cardinality:  violations,
		}
		s.record(b, res)
		return res
	}

	// Gate 3: cardinality on the would-be post state, checked only for
	// (instance, link type, endpoint) degrees that this batch changes.
	if violations := s.checkCardinality(b, shadows); len(violations) > 0 {
		res := Result{
			AcquireOrder: acquireOrder,
			BatchID:      b.ID,
			Status:       StatusCardinalityViolation,
			Cardinality:  violations,
		}
		s.record(b, res)
		return res
	}

	// Gate 4: commit. Each instance advances by its own step; per-instance
	// version sequences stay independent and strictly monotonic.
	changes := make(map[ID]VersionChange, len(ids))
	for _, id := range ids {
		live := present[id]
		from := live.version
		to := from + live.step
		shadows[id].version = to
		changes[id] = VersionChange{From: from, To: to, Step: live.step}
	}
	for _, id := range ids {
		publishInstance(present[id], shadows[id])
	}

	res := Result{
		AcquireOrder: acquireOrder,
		BatchID:      b.ID,
		Status:       StatusCommitted,
		Versions:     changes,
		Observed:     observed,
	}
	s.record(b, res)
	return res
}

// touchedInstances returns the sorted, deduplicated set of instances a
// batch reads or writes.
func touchedInstances(b Batch) []ID {
	idSet := map[ID]struct{}{}
	for _, p := range b.Preconditions {
		idSet[p.Instance] = struct{}{}
	}
	for _, op := range b.Ops {
		idSet[op.Instance] = struct{}{}
		if op.Kind == OpAddLink || op.Kind == OpRemoveLink {
			idSet[op.Other] = struct{}{}
		}
	}
	ids := make([]ID, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// appendMissing adds a missing-instance mismatch, avoiding duplicates.
func appendMissing(ms []Mismatch, id ID) []Mismatch {
	for _, m := range ms {
		if m.Instance == id && m.Missing {
			return ms
		}
	}
	return append(ms, Mismatch{Instance: id, Missing: true})
}

func sortMismatches(ms []Mismatch) {
	sort.Slice(ms, func(i, j int) bool { return ms[i].Instance < ms[j].Instance })
}

// record assigns the batch its linearisation order and appends the full
// decision evidence (declared preconditions, observed versions, outcome)
// to the journal.
func (s *Store) record(b Batch, res Result) {
	s.orderMu.Lock()
	s.order++
	res.Order = s.order
	order := s.order
	s.orderMu.Unlock()

	batchCopy := b
	resCopy := res
	resCopy.Order = order
	s.journal.append(Record{
		Kind:    "batch",
		BatchID: b.ID,
		Order:   order,
		Batch:   &batchCopy,
		Result:  &resCopy,
	})
}
