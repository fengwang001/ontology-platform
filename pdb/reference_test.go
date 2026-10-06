package pdb

import (
	"sort"
	"time"
)

// naiveModel is an independent, deliberately simple O(pods*budgets) reference
// implementation that mirrors the specification directly. The random
// differential test drives it and the indexed Service with identical
// operation streams and requires identical results after every step.
type naiveModel struct {
	grace    time.Duration
	clock    time.Time
	pods     map[PodRef]*naivePod
	budgets  map[NamespacedName]BudgetSpec
	evicting map[PodRef]*naiveEv
}

type naivePod struct {
	phase  Phase
	ready  bool
	labels map[string]string
}

type naiveEv struct {
	budget   NamespacedName
	matched  bool
	wasReady bool
	start    time.Time
	deadline time.Time
}

func newNaive(grace time.Duration) *naiveModel {
	return &naiveModel{
		grace:    grace,
		pods:     map[PodRef]*naivePod{},
		budgets:  map[NamespacedName]BudgetSpec{},
		evicting: map[PodRef]*naiveEv{},
	}
}

func (m *naiveModel) rollback(now time.Time) bool { return now.Before(m.clock) }

// expire applies all due expiries left-closed and returns the list restored.
func (m *naiveModel) expire(now time.Time) {
	for ref, e := range m.evicting {
		if !now.Before(e.deadline) {
			if p, ok := m.pods[ref]; ok && p.phase.InStats() {
				p.ready = e.wasReady
			}
			delete(m.evicting, ref)
		}
	}
}

func (m *naiveModel) commit(now time.Time) {
	if now.After(m.clock) {
		m.clock = now
	}
}

func (m *naiveModel) matchOne(ref PodRef, labels map[string]string, ns string) []NamespacedName {
	var out []NamespacedName
	for nn, b := range m.budgets {
		if nn.Namespace != ns || len(b.Selector) == 0 {
			continue
		}
		ok := true
		for k, v := range b.Selector {
			if labels[k] != v {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, nn)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *naiveModel) status(nn NamespacedName) (BudgetStatus, bool) {
	b, ok := m.budgets[nn]
	if !ok {
		return BudgetStatus{}, false
	}
	var expected, ready int
	var matched []PodRef
	for ref, p := range m.pods {
		if ref.Namespace != nn.Namespace || !p.phase.InStats() {
			continue
		}
		ok := len(b.Selector) > 0
		for k, v := range b.Selector {
			if p.labels[k] != v {
				ok = false
			}
		}
		if ok {
			expected++
			matched = append(matched, ref)
			if p.ready {
				ready++
			}
		}
	}
	req := naiveRequired(b, expected)
	allow := ready - req
	if allow < 0 {
		allow = 0
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Namespace != matched[j].Namespace {
			return matched[i].Namespace < matched[j].Namespace
		}
		return matched[i].UID < matched[j].UID
	})
	return BudgetStatus{
		Expected:          expected,
		CurrentReady:      ready,
		RequiredReady:     req,
		DisruptionAllowed: allow,
		MatchedPods:       matched,
	}, true
}

func naiveRequired(b BudgetSpec, expected int) int {
	req := 0
	if b.MinAvailable != nil {
		req = naiveResolve(b.MinAvailable, expected, true)
	} else {
		req = expected - naiveResolve(b.MaxUnavailable, expected, false)
	}
	if req < 0 {
		req = 0
	}
	return req
}

func naiveResolve(v *Value, expected int, up bool) int {
	if !v.IsPercent {
		return v.Amount
	}
	num := expected * v.Amount
	if up {
		return (num + 99) / 100
	}
	return num / 100
}

// startEv mirrors the indexed service's allow transition.
func (m *naiveModel) startEv(ref PodRef, nn []NamespacedName, now time.Time) {
	p := m.pods[ref]
	e := &naiveEv{wasReady: p.ready, start: now, deadline: now.Add(m.grace)}
	if len(nn) == 1 {
		e.matched = true
		e.budget = nn[0]
	}
	if p.ready {
		p.ready = false
	}
	m.evicting[ref] = e
}

// decide evaluates a single pod against a snapshot, returning matched budgets
// and an error reason (0 = allow). It performs no mutation.
func (m *naiveModel) decide(ref PodRef) (matched []NamespacedName, reason Reason) {
	p, ok := m.pods[ref]
	if !ok {
		return nil, ReasonPodNotFound
	}
	if !p.phase.InStats() {
		return nil, ReasonNotEvictablePhase
	}
	if _, yes := m.evicting[ref]; yes {
		return nil, ReasonAlreadyEvicting
	}
	mb := m.matchOne(ref, p.labels, ref.Namespace)
	if len(mb) > 1 {
		return nil, ReasonConflict
	}
	if len(mb) == 1 {
		st, _ := m.status(mb[0])
		if !p.ready {
			if st.CurrentReady < st.RequiredReady {
				return nil, ReasonInsufficient
			}
		} else {
			if st.DisruptionAllowed <= 0 {
				return nil, ReasonInsufficient
			}
		}
	}
	return mb, 0
}
