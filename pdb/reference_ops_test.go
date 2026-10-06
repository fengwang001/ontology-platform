package pdb

import "time"

type naiveResult struct {
	reason Reason
	index  int
	status BudgetStatus
	found  bool
}

func (m *naiveModel) upsertPod(p Pod, now time.Time) naiveResult {
	if p.UID == "" || p.Namespace == "" || p.Phase < PhaseRunning || p.Phase > PhaseFailed {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback}
	}
	m.expire(now)
	ref := PodRef{p.Namespace, p.UID}
	if old, ok := m.pods[ref]; ok {
		if _, yes := m.evicting[ref]; yes && old.ready != p.Ready {
			return naiveResult{reason: ReasonAlreadyEvicting}
		}
	}
	labels := map[string]string{}
	for k, v := range p.Labels {
		labels[k] = v
	}
	m.pods[ref] = &naivePod{phase: p.Phase, ready: p.Ready, labels: labels}
	m.commit(now)
	return naiveResult{}
}

func (m *naiveModel) deletePod(ref PodRef, now time.Time) naiveResult {
	if !validRef(ref) {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback}
	}
	m.expire(now)
	if _, ok := m.pods[ref]; !ok {
		return naiveResult{reason: ReasonPodNotFound}
	}
	delete(m.evicting, ref)
	delete(m.pods, ref)
	m.commit(now)
	return naiveResult{}
}

func (m *naiveModel) upsertBudget(b BudgetSpec, now time.Time) naiveResult {
	if err := validateSpec(b); err != nil {
		return naiveResult{reason: ErrReason(err)}
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback}
	}
	m.expire(now)
	nn := NamespacedName{b.Namespace, b.Name}
	cp := BudgetSpec{
		Name:           b.Name,
		Namespace:      b.Namespace,
		Selector:       cloneSelector(b.Selector),
		MinAvailable:   cloneValue(b.MinAvailable),
		MaxUnavailable: cloneValue(b.MaxUnavailable),
	}
	m.budgets[nn] = cp
	m.commit(now)
	return naiveResult{}
}

func (m *naiveModel) deleteBudget(nn NamespacedName, now time.Time) naiveResult {
	if nn.Namespace == "" || nn.Name == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback}
	}
	m.expire(now)
	if _, ok := m.budgets[nn]; !ok {
		return naiveResult{reason: ReasonPodNotFound}
	}
	delete(m.budgets, nn)
	m.commit(now)
	return naiveResult{}
}

func (m *naiveModel) evict(ref PodRef, now time.Time) naiveResult {
	if !validRef(ref) {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback}
	}
	m.expire(now)
	mb, reason := m.decide(ref)
	if reason != 0 {
		return naiveResult{reason: reason}
	}
	m.startEv(ref, mb, now)
	m.commit(now)
	return naiveResult{}
}

func (m *naiveModel) batch(refs []PodRef, now time.Time) naiveResult {
	seen := map[PodRef]bool{}
	for i, ref := range refs {
		if !validRef(ref) {
			return naiveResult{reason: ReasonInvalidArgument, index: i}
		}
		if seen[ref] {
			return naiveResult{reason: ReasonInvalidArgument, index: i}
		}
		seen[ref] = true
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback, index: -1}
	}
	m.expire(now)

	type plan struct {
		ref PodRef
		mb  []NamespacedName
	}
	plans := make([]plan, 0, len(refs))
	consumed := map[NamespacedName]int{}
	for i, ref := range refs {
		p, ok := m.pods[ref]
		if !ok {
			return naiveResult{reason: ReasonPodNotFound, index: i}
		}
		if !p.phase.InStats() {
			return naiveResult{reason: ReasonNotEvictablePhase, index: i}
		}
		if _, yes := m.evicting[ref]; yes {
			return naiveResult{reason: ReasonAlreadyEvicting, index: i}
		}
		mb := m.matchOne(ref, p.labels, ref.Namespace)
		if len(mb) > 1 {
			return naiveResult{reason: ReasonConflict, index: i}
		}
		if len(mb) == 1 {
			st, _ := m.status(mb[0])
			if !p.ready {
				if st.CurrentReady < st.RequiredReady {
					return naiveResult{reason: ReasonInsufficient, index: i}
				}
			} else {
				if consumed[mb[0]]+1 > st.DisruptionAllowed {
					return naiveResult{reason: ReasonInsufficient, index: i}
				}
				consumed[mb[0]]++
			}
		}
		plans = append(plans, plan{ref: ref, mb: mb})
	}
	for _, pl := range plans {
		m.startEv(pl.ref, pl.mb, now)
	}
	m.commit(now)
	return naiveResult{}
}

func (m *naiveModel) confirm(ref PodRef, now time.Time, cancel bool) naiveResult {
	if !validRef(ref) {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback}
	}
	m.expire(now)
	e, ok := m.evicting[ref]
	if !ok {
		if _, pok := m.pods[ref]; !pok {
			return naiveResult{reason: ReasonPodNotFound}
		}
		return naiveResult{reason: ReasonInvalidArgument}
	}
	delete(m.evicting, ref)
	if cancel {
		if p, ok := m.pods[ref]; ok && p.phase.InStats() {
			p.ready = e.wasReady
		}
	} else {
		delete(m.pods, ref)
	}
	m.commit(now)
	return naiveResult{}
}

func (m *naiveModel) budget(namespace, name string, now time.Time) naiveResult {
	if namespace == "" || name == "" {
		return naiveResult{reason: ReasonInvalidArgument}
	}
	if m.rollback(now) {
		return naiveResult{reason: ReasonClockRollback}
	}
	m.expire(now)
	nn := NamespacedName{namespace, name}
	st, ok := m.status(nn)
	if !ok {
		return naiveResult{reason: ReasonPodNotFound}
	}
	m.commit(now)
	return naiveResult{status: st, found: true}
}
