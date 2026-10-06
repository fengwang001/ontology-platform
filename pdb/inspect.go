package pdb

import "time"

// matchedPodsLocked enumerates in-stats pods matched by b using the label index.
// Only pods carrying one of the selector's (key,value) pairs are visited.
func (s *Service) matchedPodsLocked(b *budgetEntry) []PodRef {
	if selectorEmpty(b.spec.Selector) {
		return nil
	}
	var out []PodRef
	seen := map[PodRef]bool{}
	ns := b.spec.Namespace
	for k, v := range b.spec.Selector {
		for ref, pe := range s.podByLabel[labelKey{ns, k, v}] {
			if s.instrument {
				s.cost.PodScans++
			}
			if seen[ref] {
				continue
			}
			if pe.pod.Phase.InStats() && selectorMatchesFull(b.spec.Selector, pe.pod.Labels) {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	return out
}

// EnableInstrumentation turns on work counters used by complexity proofs.
func (s *Service) EnableInstrumentation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instrument = true
	s.cost = costStats{}
}

// CostCounters is a snapshot of indexed-work counters.
type CostCounters struct {
	PodScans    int64
	BudgetScans int64
}

// ReadCounters returns and resets the instrumentation counters.
func (s *Service) ReadCounters() CostCounters {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := CostCounters{PodScans: s.cost.PodScans, BudgetScans: s.cost.BudgetScans}
	s.cost = costStats{}
	return c
}

// PodState is an inspection snapshot of one pod.
type PodState struct {
	Pod      Pod
	Evicting bool
	Deadline time.Time
	WasReady bool
}

// InspectPod returns a snapshot for tests/debugging.
func (s *Service) InspectPod(ref PodRef) (PodState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pe, ok := s.lookupPod(ref)
	if !ok {
		return PodState{}, false
	}
	st := PodState{Pod: pe.pod}
	p := Pod{}
	p.UID = pe.pod.UID
	p.Namespace = pe.pod.Namespace
	p.Phase = pe.pod.Phase
	p.Ready = pe.pod.Ready
	p.Labels = cloneLabels(pe.pod.Labels)
	st.Pod = p
	if pe.evicting != nil {
		st.Evicting = true
		st.Deadline = pe.evicting.deadline
		st.WasReady = pe.evicting.wasReady
	}
	return st, true
}
