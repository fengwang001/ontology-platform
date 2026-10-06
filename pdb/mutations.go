package pdb

import "time"

func validRef(ref PodRef) bool { return ref.Namespace != "" && ref.UID != "" }

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// UpsertPod adds or updates a pod. Readiness, label and phase changes take
// effect immediately. An evicting pod rejects any readiness change.
func (s *Service) UpsertPod(p Pod, now time.Time) error {
	if p.UID == "" || p.Namespace == "" {
		return errf(ReasonInvalidArgument, PodRef{Namespace: p.Namespace, UID: p.UID}, -1,
			"pod namespace and uid are required")
	}
	if p.Phase < PhaseRunning || p.Phase > PhaseFailed {
		return errf(ReasonInvalidArgument, PodRef{Namespace: p.Namespace, UID: p.UID}, -1,
			"invalid pod phase")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}

	ref := PodRef{Namespace: p.Namespace, UID: p.UID}
	old, existed := s.lookupPod(ref)
	if existed && old.evicting != nil && old.pod.Ready != p.Ready {
		return errf(ReasonAlreadyEvicting, ref, -1,
			"pod is already being evicted; readiness cannot change")
	}

	if existed {
		s.removePodIndex(old)
	}
	pe := &podEntry{pod: Pod{
		UID:       p.UID,
		Namespace: p.Namespace,
		Labels:    cloneLabels(p.Labels),
		Phase:     p.Phase,
		Ready:     p.Ready,
	}}
	if existed {
		pe.evicting = old.evicting
	}

	ns := s.pods[p.Namespace]
	if ns == nil {
		ns = map[string]*podEntry{}
		s.pods[p.Namespace] = ns
	}
	ns[p.UID] = pe
	s.addPodIndex(pe)

	s.commit(now)
	return nil
}

// removePodLocked deletes a pod entry and its index memberships.
func (s *Service) removePodLocked(pe *podEntry) {
	p := pe.pod
	s.removePodIndex(pe)
	if ns := s.pods[p.Namespace]; ns != nil {
		delete(ns, p.UID)
		if len(ns) == 0 {
			delete(s.pods, p.Namespace)
		}
	}
}

// DeletePod removes a pod and cancels any in-flight eviction on it silently.
func (s *Service) DeletePod(ref PodRef, now time.Time) error {
	if !validRef(ref) {
		return errf(ReasonInvalidArgument, ref, -1, "pod namespace and uid are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}
	pe, ok := s.lookupPod(ref)
	if !ok {
		return errf(ReasonPodNotFound, ref, -1, "pod not found")
	}
	if pe.evicting != nil {
		heapRemove(&s.deadlines, pe.evicting)
		delete(s.evictions, ref)
	}
	s.removePodLocked(pe)
	s.commit(now)
	return nil
}

// UpsertBudget adds or replaces a budget with the same (namespace,name).
// Existing in-flight evictions keep pointing at the prior budget definition.
func (s *Service) UpsertBudget(b BudgetSpec, now time.Time) error {
	if err := validateSpec(b); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}

	ns := s.budgets[b.Namespace]
	if ns == nil {
		ns = map[string]*budgetEntry{}
		s.budgets[b.Namespace] = ns
	}
	if old := ns[b.Name]; old != nil {
		s.removeBudgetIndex(old)
	}
	nb := &budgetEntry{spec: BudgetSpec{
		Name:           b.Name,
		Namespace:      b.Namespace,
		Selector:       cloneSelector(b.Selector),
		MinAvailable:   cloneValue(b.MinAvailable),
		MaxUnavailable: cloneValue(b.MaxUnavailable),
	}}
	ns[b.Name] = nb
	// Empty selectors match nothing: they are stored but never indexed.
	if !selectorEmpty(nb.spec.Selector) {
		s.addBudgetIndex(nb)
	}
	s.commit(now)
	return nil
}

// DeleteBudget removes a budget. In-flight evictions are unaffected.
func (s *Service) DeleteBudget(nn NamespacedName, now time.Time) error {
	if nn.Namespace == "" || nn.Name == "" {
		return errf(ReasonInvalidArgument, PodRef{Namespace: nn.Namespace, UID: nn.Name}, -1,
			"budget namespace and name are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.begin(now); err != nil {
		return err
	}
	ns := s.budgets[nn.Namespace]
	if ns == nil || ns[nn.Name] == nil {
		return errf(ReasonPodNotFound, PodRef{Namespace: nn.Namespace, UID: nn.Name}, -1,
			"budget not found")
	}
	s.removeBudgetIndex(ns[nn.Name])
	delete(ns, nn.Name)
	if len(ns) == 0 {
		delete(s.budgets, nn.Namespace)
	}
	s.commit(now)
	return nil
}

func cloneSelector(in Selector) Selector {
	out := make(Selector, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneValue(v *Value) *Value {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}
