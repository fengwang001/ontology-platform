package pdb

import "time"

// Evict adjudicates one eviction at time now.
func (s *Service) Evict(ref PodRef, now time.Time) error {
	if !validRef(ref) {
		return errf(ReasonInvalidArgument, ref, -1, "pod namespace and uid are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}
	if err := s.checkEvictable(ref, -1, now); err != nil {
		return err
	}
	s.commit(now)
	return nil
}

// checkEvictable performs the single-pod adjudication and, on allow, performs
// the state transition. Must be called under mu with begin already applied.
func (s *Service) checkEvictable(ref PodRef, batchIndex int, now time.Time) error {
	pe, ok := s.lookupPod(ref)
	if !ok {
		return errf(ReasonPodNotFound, ref, batchIndex, "pod not found")
	}
	if !pe.pod.Phase.InStats() {
		return errf(ReasonNotEvictablePhase, ref, batchIndex,
			"pod phase %d cannot be evicted", pe.pod.Phase)
	}
	if pe.evicting != nil {
		return errf(ReasonAlreadyEvicting, ref, batchIndex, "pod is already being evicted")
	}

	matched := s.matchBudgets(ref.Namespace, pe.pod.Labels)
	if len(matched) > 1 {
		return errf(ReasonConflict, ref, batchIndex,
			"pod matched by %d budgets", len(matched))
	}

	var b *budgetEntry
	if len(matched) == 1 {
		b = matched[0]
	}

	wasReady := pe.pod.Ready
	if b == nil {
		// Unmatched pods pass directly.
	} else if !wasReady {
		// Unready pods pass without consuming allowance iff the budget can
		// still meet its required ready count.
		if b.ready < requiredReady(b) {
			return errf(ReasonInsufficient, ref, batchIndex,
				"ready %d below required %d", b.ready, requiredReady(b))
		}
	} else {
		if disruptionAllowed(b) <= 0 {
			return errf(ReasonInsufficient, ref, batchIndex,
				"no disruption allowance: ready %d required %d", b.ready, requiredReady(b))
		}
	}

	s.startEviction(pe, b, wasReady, now)
	return nil
}

// startEviction marks a pod evicting: a ready pod immediately becomes not-ready
// (consuming one allowance); an unready pod keeps its readiness state.
func (s *Service) startEviction(pe *podEntry, b *budgetEntry, wasReady bool, start time.Time) {
	ref := PodRef{Namespace: pe.pod.Namespace, UID: pe.pod.UID}
	e := &eviction{
		ref:       ref,
		budget:    b,
		wasReady:  wasReady,
		start:     start,
		deadline:  start.Add(s.grace),
		heapIndex: -1,
	}
	pe.evicting = e
	s.evictions[ref] = e
	heapPush(&s.deadlines, e)
	if wasReady {
		pe.pod.Ready = false
		if b != nil {
			b.ready--
		}
	}
}

// EvictBatch adjudicates an all-or-nothing node-drain batch at time now.
// Duplicate refs are invalid arguments. Within one budget, ready pods consume
// allowance cumulatively; unready pods are judged against the batch-start
// state and are never affected by other pods in the batch.
func (s *Service) EvictBatch(refs []PodRef, now time.Time) error {
	seenDup := map[PodRef]int{}
	for i, ref := range refs {
		if !validRef(ref) {
			return errf(ReasonInvalidArgument, ref, i, "pod namespace and uid are required")
		}
		if _, dup := seenDup[ref]; dup {
			return errf(ReasonInvalidArgument, ref, i, "duplicate pod in batch")
		}
		seenDup[ref] = i
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}

	type planned struct {
		pe       *podEntry
		budget   *budgetEntry
		wasReady bool
	}
	plans := make([]planned, 0, len(refs))
	// consumed holds the cumulative ready-pod consumption per budget within
	// this batch; unready checks read only the frozen batch-start counters.
	consumed := map[*budgetEntry]int{}

	for i, ref := range refs {
		pe, ok := s.lookupPod(ref)
		if !ok {
			return errf(ReasonPodNotFound, ref, i, "pod not found")
		}
		if !pe.pod.Phase.InStats() {
			return errf(ReasonNotEvictablePhase, ref, i,
				"pod phase %d cannot be evicted", pe.pod.Phase)
		}
		if pe.evicting != nil {
			return errf(ReasonAlreadyEvicting, ref, i, "pod is already being evicted")
		}
		matched := s.matchBudgets(ref.Namespace, pe.pod.Labels)
		if len(matched) > 1 {
			return errf(ReasonConflict, ref, i, "pod matched by %d budgets", len(matched))
		}
		var b *budgetEntry
		if len(matched) == 1 {
			b = matched[0]
		}
		if b != nil && !pe.pod.Ready {
			// Batch-start state: no consumption applied by this pod.
			if b.ready < requiredReady(b) {
				return errf(ReasonInsufficient, ref, i,
					"ready %d below required %d", b.ready, requiredReady(b))
			}
		}
		if b != nil && pe.pod.Ready {
			if consumed[b]+1 > disruptionAllowed(b) {
				return errf(ReasonInsufficient, ref, i,
					"batch allowance exhausted: need %d allowed %d",
					consumed[b]+1, disruptionAllowed(b))
			}
			consumed[b]++
		}
		plans = append(plans, planned{pe: pe, budget: b, wasReady: pe.pod.Ready})
	}

	// All adjudications passed: commit the batch atomically.
	for _, pl := range plans {
		s.startEviction(pl.pe, pl.budget, pl.wasReady, now)
	}
	s.commit(now)
	return nil
}

// Confirm removes an in-flight evicted pod.
func (s *Service) Confirm(ref PodRef, now time.Time) error {
	if !validRef(ref) {
		return errf(ReasonInvalidArgument, ref, -1, "pod namespace and uid are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}
	e, ok := s.evictions[ref]
	if !ok {
		if _, podOK := s.lookupPod(ref); !podOK {
			return errf(ReasonPodNotFound, ref, -1, "pod not found")
		}
		return errf(ReasonInvalidArgument, ref, -1, "pod is not being evicted")
	}
	heapRemove(&s.deadlines, e)
	s.finishEviction(e, true, now)
	s.commit(now)
	return nil
}

// Cancel aborts an in-flight eviction and restores readiness immediately.
func (s *Service) Cancel(ref PodRef, now time.Time) error {
	if !validRef(ref) {
		return errf(ReasonInvalidArgument, ref, -1, "pod namespace and uid are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}
	e, ok := s.evictions[ref]
	if !ok {
		if _, podOK := s.lookupPod(ref); !podOK {
			return errf(ReasonPodNotFound, ref, -1, "pod not found")
		}
		return errf(ReasonInvalidArgument, ref, -1, "pod is not being evicted")
	}
	heapRemove(&s.deadlines, e)
	s.finishEviction(e, false, now)
	s.commit(now)
	return nil
}

// Budget returns the status of one budget derived from current state.
func (s *Service) Budget(namespace, name string, now time.Time) (BudgetStatus, error) {
	if namespace == "" || name == "" {
		return BudgetStatus{}, errf(ReasonInvalidArgument, PodRef{Namespace: namespace, UID: name}, -1,
			"budget namespace and name are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return BudgetStatus{}, err
	}
	ns := s.budgets[namespace]
	var b *budgetEntry
	if ns != nil {
		b = ns[name]
	}
	if b == nil {
		return BudgetStatus{}, errf(ReasonPodNotFound, PodRef{Namespace: namespace, UID: name}, -1,
			"budget not found")
	}
	st := BudgetStatus{
		Expected:          b.expected,
		CurrentReady:      b.ready,
		RequiredReady:     requiredReady(b),
		DisruptionAllowed: disruptionAllowed(b),
		MatchedPods:       s.matchedPodsLocked(b),
	}
	s.commit(now)
	return st, nil
}
