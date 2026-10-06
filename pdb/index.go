package pdb

// addPodIndex inserts an in-stats pod into the label index and updates the
// expected/ready counters of every matching budget.
func (s *Service) addPodIndex(pe *podEntry) {
	p := pe.pod
	if !p.Phase.InStats() {
		return
	}
	ref := PodRef{Namespace: p.Namespace, UID: p.UID}
	for k, v := range p.Labels {
		lk := labelKey{p.Namespace, k, v}
		set := s.podByLabel[lk]
		if set == nil {
			set = map[PodRef]*podEntry{}
			s.podByLabel[lk] = set
		}
		set[ref] = pe
	}
	for _, b := range s.matchBudgets(p.Namespace, p.Labels) {
		b.expected++
		if p.Ready {
			b.ready++
		}
	}
}

// removePodIndex removes an in-stats pod from the label index and counters.
func (s *Service) removePodIndex(pe *podEntry) {
	p := pe.pod
	if !p.Phase.InStats() {
		return
	}
	ref := PodRef{Namespace: p.Namespace, UID: p.UID}
	for k, v := range p.Labels {
		lk := labelKey{p.Namespace, k, v}
		if set := s.podByLabel[lk]; set != nil {
			delete(set, ref)
			if len(set) == 0 {
				delete(s.podByLabel, lk)
			}
		}
	}
	for _, b := range s.matchBudgets(p.Namespace, p.Labels) {
		b.expected--
		if p.Ready {
			b.ready--
		}
	}
}

// applyReadyDelta adjusts ready counters of the budgets currently matching pe.
func (s *Service) applyReadyDelta(_ *budgetEntry, pe *podEntry, delta int) {
	if !pe.pod.Phase.InStats() || delta == 0 {
		return
	}
	for _, b := range s.matchBudgets(pe.pod.Namespace, pe.pod.Labels) {
		b.ready += delta
	}
}

// matchBudgets returns every budget in namespace whose non-empty selector is
// satisfied by labels. Candidate budgets come exclusively from the key index,
// so budgets whose selector shares no key with the labels are never touched.
func (s *Service) matchBudgets(namespace string, labels map[string]string) []*budgetEntry {
	var out []*budgetEntry
	seen := map[*budgetEntry]bool{}
	for k, v := range labels {
		bk := budgetKey{namespace, k}
		for _, b := range s.budgetsByKey[bk] {
			if seen[b] {
				continue
			}
			seen[b] = true
			if s.instrument {
				s.cost.BudgetScans++
			}
			if b.spec.Selector[k] == v && selectorMatchesFull(b.spec.Selector, labels) {
				out = append(out, b)
			}
		}
	}
	return out
}

// selectorMatchesFull checks every selector requirement against labels.
func selectorMatchesFull(sel Selector, labels map[string]string) bool {
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// addBudgetIndex inserts a budget into the per-selector-key index and computes
// its initial counters from the label index.
func (s *Service) addBudgetIndex(b *budgetEntry) {
	ns := b.spec.Namespace
	counted := map[PodRef]bool{}
	for k, v := range b.spec.Selector {
		bk := budgetKey{ns, k}
		set := s.budgetsByKey[bk]
		if set == nil {
			set = map[NamespacedName]*budgetEntry{}
			s.budgetsByKey[bk] = set
		}
		nn := NamespacedName{Namespace: ns, Name: b.spec.Name}
		set[nn] = b

		// Membership count via the index on (namespace,key,value).
		for ref, pe := range s.podByLabel[labelKey{ns, k, v}] {
			if s.instrument {
				s.cost.PodScans++
			}
			if !counted[ref] && pe.pod.Phase.InStats() &&
				selectorMatchesFull(b.spec.Selector, pe.pod.Labels) {
				counted[ref] = true
				b.expected++
				if pe.pod.Ready {
					b.ready++
				}
			}
		}
	}
}

// removeBudgetIndex removes a budget from the key index.
func (s *Service) removeBudgetIndex(b *budgetEntry) {
	ns := b.spec.Namespace
	nn := NamespacedName{Namespace: ns, Name: b.spec.Name}
	for k := range b.spec.Selector {
		if set := s.budgetsByKey[budgetKey{ns, k}]; set != nil {
			delete(set, nn)
			if len(set) == 0 {
				delete(s.budgetsByKey, budgetKey{ns, k})
			}
		}
	}
}
