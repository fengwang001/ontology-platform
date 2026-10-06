package disruption

import (
	"container/heap"
	"sort"
)

// podRec is the mutable per-pod record owned by the store.
type podRec struct {
	pod          Pod
	evicting     bool
	deadline     Tick // meaningful only while evicting
	readyAtAdmit bool // readiness captured at admit; restored on expiry/cancel
}

// budgetRec is the mutable per-budget record.
type budgetRec struct {
	budget  PodDisruptionBudget
	members map[PodID]struct{} // non-terminal matched pods (evicting stay members)
	ready   int                // members that are ready AND not evicting
}

// deadlineItem is one entry of the eviction deadline min-heap.
type deadlineItem struct {
	id  PodID
	at  Tick
	seq int64
}

type deadlineHeap []*deadlineItem

func (h deadlineHeap) Len() int { return len(h) }
func (h deadlineHeap) Less(i, j int) bool {
	return h[i].at < h[j].at || (h[i].at == h[j].at && h[i].seq < h[j].seq)
}
func (h deadlineHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *deadlineHeap) Push(x any)   { *h = append(*h, x.(*deadlineItem)) }
func (h *deadlineHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// State is the indexed cluster state.
type State struct {
	pods     map[PodID]*podRec
	budgets  map[BudgetID]*budgetRec
	podsByNs map[string]map[PodID]*podRec
	// labelIndex[namespace]["k\x00v"] is the set of budgets in that
	// namespace whose selector requires that label equality. The candidate
	// set for a pod is the intersection of the posting lists of the pod's
	// labels, re-verified against the full conjunction. Budgets in other
	// namespaces and budgets requiring labels the pod lacks are never probed.
	labelIndex map[string]map[string]map[BudgetID]struct{}

	dh      deadlineHeap
	heapSeq int64

	now   Tick
	probe Probe
}

// NewState returns an empty state at logical time 0.
func NewState() *State {
	return &State{
		pods:       map[PodID]*podRec{},
		podsByNs:   map[string]map[PodID]*podRec{},
		budgets:    map[BudgetID]*budgetRec{},
		labelIndex: map[string]map[string]map[BudgetID]struct{}{},
	}
}

func encodeRequirement(k, v string) string { return k + "\x00" + v }

// isReady is the accounting-level readiness of a pod record: ready and not
// currently evicting.
func (r *podRec) isReady() bool { return r.pod.Ready && !r.evicting }

// candidates returns the budgets in namespace whose selectors satisfy all
// requirements against labels. It probes exactly the returned records.
//
// Exactness: a selector is a conjunction. If budget b matches the pod then
// every requirement (k,v) of b is a pod label, so b appears in every posting
// list intersected below. The final selectorMatches re-check removes the
// (rare) superset candidates caused by posting lists of labels the pod has
// but that overlap another budget's requirements; combined with the
// per-namespace partitioning the result is exact and never touches budgets
// in other namespaces or budgets requiring absent labels.
func (s *State) candidates(namespace string, labels map[string]string) []*budgetRec {
	ns := s.labelIndex[namespace]
	if ns == nil || len(labels) == 0 {
		return nil
	}
	// Take the UNION of the posting lists of every label the pod has: any
	// matching budget requires at least one of those exact equalities, so it
	// appears in at least one list. The full conjunction is then re-verified
	// with selectorMatches, which rejects budgets requiring some label the pod
	// lacks (those are a rare superset of a single posting list). Budgets in
	// other namespaces and budgets whose selector keys are disjoint from the
	// pod's labels never appear in any list and are never probed.
	acc := map[BudgetID]struct{}{}
	for k, v := range labels {
		for id := range ns[encodeRequirement(k, v)] {
			acc[id] = struct{}{}
		}
	}
	if len(acc) == 0 {
		return nil
	}
	ids := make([]BudgetID, 0, len(acc))
	for id := range acc {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Name < ids[j].Name })
	out := make([]*budgetRec, 0, len(ids))
	for _, id := range ids {
		b, ok := s.budgets[id]
		if !ok {
			continue
		}
		if !selectorMatches(b.budget.Selector, labels) {
			continue
		}
		s.probe.BudgetProbe++
		out = append(out, b)
	}
	return out
}

// matchBudgets returns the budgets that count the given pod (caller guarantees
// the pod is non-terminal).
func (s *State) matchBudgets(p Pod) []*budgetRec {
	return s.candidates(p.ID.Namespace, p.Labels)
}

func (s *State) addToBudget(b *budgetRec, id PodID, r *podRec) {
	if _, ok := b.members[id]; ok {
		return
	}
	b.members[id] = struct{}{}
	s.probe.MemberProbe++
	if r.isReady() {
		b.ready++
	}
}

func (s *State) removeFromBudget(b *budgetRec, id PodID, r *podRec) {
	if _, ok := b.members[id]; !ok {
		return
	}
	delete(b.members, id)
	s.probe.MemberProbe++
	if r.isReady() {
		b.ready--
	}
}

func (s *State) indexBudget(b PodDisruptionBudget) {
	ns := s.labelIndex[b.ID.Namespace]
	if ns == nil {
		ns = map[string]map[BudgetID]struct{}{}
		s.labelIndex[b.ID.Namespace] = ns
	}
	for k, v := range b.Selector {
		key := encodeRequirement(k, v)
		set := ns[key]
		if set == nil {
			set = map[BudgetID]struct{}{}
			ns[key] = set
		}
		set[b.ID] = struct{}{}
	}
}

func (s *State) unindexBudget(b PodDisruptionBudget) {
	ns := s.labelIndex[b.ID.Namespace]
	if ns == nil {
		return
	}
	for k, v := range b.Selector {
		key := encodeRequirement(k, v)
		if set := ns[key]; set != nil {
			delete(set, b.ID)
			if len(set) == 0 {
				delete(ns, key)
			}
		}
	}
	if len(ns) == 0 {
		delete(s.labelIndex, b.ID.Namespace)
	}
}

// recomputeBudget rebuilds membership and the ready counter from scratch.
// Used by the cold budget-add/update/delete paths; evict and quota never
// scan pods.
func (s *State) recomputeBudget(b *budgetRec) {
	b.members = map[PodID]struct{}{}
	b.ready = 0
	for id, r := range s.podsByNs[b.budget.ID.Namespace] {
		s.probe.PodProbe++
		if r.pod.Phase.terminal() {
			continue
		}
		if selectorMatches(b.budget.Selector, r.pod.Labels) {
			b.members[id] = struct{}{}
			s.probe.MemberProbe++
			if r.isReady() {
				b.ready++
			}
		}
	}
}

func (s *State) getPod(id PodID) (*podRec, bool) {
	r, ok := s.pods[id]
	s.probe.PodProbe++
	return r, ok
}

// putPod inserts a new pod or replaces an existing one while preserving any
// in-flight eviction, reconciling every affected budget incrementally.
func (s *State) putPod(p Pod) {
	old := s.pods[p.ID]
	labels := make(map[string]string, len(p.Labels))
	for k, v := range p.Labels {
		labels[k] = v
	}
	np := Pod{ID: p.ID, Labels: labels, Phase: p.Phase, Ready: p.Ready}
	nr := &podRec{pod: np}
	if old != nil {
		nr.evicting = old.evicting
		nr.deadline = old.deadline
		nr.readyAtAdmit = old.readyAtAdmit
	}
	s.pods[p.ID] = nr
	s.probe.PodProbe++
	ns := s.podsByNs[p.ID.Namespace]
	if ns == nil {
		ns = map[PodID]*podRec{}
		s.podsByNs[p.ID.Namespace] = ns
	}
	ns[p.ID] = nr

	var oldMatch, newMatch map[*budgetRec]struct{}
	if old != nil && !old.pod.Phase.terminal() {
		oldMatch = map[*budgetRec]struct{}{}
		for _, b := range s.matchBudgets(old.pod) {
			oldMatch[b] = struct{}{}
		}
	}
	if !np.Phase.terminal() {
		newMatch = map[*budgetRec]struct{}{}
		for _, b := range s.matchBudgets(np) {
			newMatch[b] = struct{}{}
		}
	}
	for b := range oldMatch {
		if _, ok := newMatch[b]; !ok {
			s.removeFromBudget(b, p.ID, old)
		}
	}
	for b := range newMatch {
		if _, ok := oldMatch[b]; ok {
			was, is := old.isReady(), nr.isReady()
			switch {
			case was && !is:
				b.ready--
			case !was && is:
				b.ready++
			}
			continue
		}
		s.addToBudget(b, p.ID, nr)
	}
}

func (s *State) deletePod(id PodID) bool {
	r, ok := s.pods[id]
	if !ok {
		return false
	}
	s.probe.PodProbe++
	// Undo any eviction accounting first so the per-budget ready contribution
	// is restored to the pod before membership removal reverses it. Without
	// this, a pod admitted while ready has evicting=true (isReady false) and
	// removal would skip the ready decrement it owed.
	if r.evicting {
		s.endEviction(r, false)
	}
	if !r.pod.Phase.terminal() {
		for _, b := range s.matchBudgets(r.pod) {
			s.removeFromBudget(b, id, r)
		}
	}
	delete(s.pods, id)
	if ns := s.podsByNs[id.Namespace]; ns != nil {
		delete(ns, id)
		if len(ns) == 0 {
			delete(s.podsByNs, id.Namespace)
		}
	}
	return true
}

func (s *State) putBudget(b PodDisruptionBudget) {
	if old, ok := s.budgets[b.ID]; ok {
		s.unindexBudget(old.budget)
	}
	sel := make(Selector, len(b.Selector))
	for k, v := range b.Selector {
		sel[k] = v
	}
	cp := b
	cp.Selector = sel
	rec := &budgetRec{budget: cp, members: map[PodID]struct{}{}}
	s.budgets[b.ID] = rec
	s.indexBudget(cp)
	s.recomputeBudget(rec)
}

func (s *State) deleteBudget(id BudgetID) bool {
	b, ok := s.budgets[id]
	if !ok {
		return false
	}
	s.unindexBudget(b.budget)
	delete(s.budgets, id)
	return true
}

func (s *State) getBudget(id BudgetID) (*budgetRec, bool) {
	b, ok := s.budgets[id]
	s.probe.BudgetProbe++
	return b, ok
}

// startEviction marks the pod evicting with a deadline; matched budget ready
// counters drop immediately if the pod had been contributing readiness.
// matched is the adjudication-time match result and is reused so the hot
// path never resolves matches twice.
func (s *State) startEviction(r *podRec, deadline Tick, matched []*budgetRec) {
	wasReady := r.isReady()
	r.readyAtAdmit = r.pod.Ready
	r.evicting = true
	r.deadline = deadline
	s.heapSeq++
	heap.Push(&s.dh, &deadlineItem{id: r.pod.ID, at: deadline, seq: s.heapSeq})
	if !wasReady {
		return
	}
	for _, b := range matched {
		b.ready--
		s.probe.MemberProbe++
	}
}

// endEviction clears the evicting flag and, on expiry/cancel, restores the
// pre-admit readiness; ready counters are reconciled incrementally.
func (s *State) endEviction(r *podRec, restoreReady bool) {
	wasReady := r.isReady()
	r.evicting = false
	if restoreReady {
		r.pod.Ready = r.readyAtAdmit
	}
	r.deadline = 0
	r.readyAtAdmit = false
	if !wasReady && r.isReady() {
		for _, b := range s.matchBudgets(r.pod) {
			b.ready++
			s.probe.MemberProbe++
		}
	}
}

// expireDue reinstates every eviction with deadline <= now. Left-closed:
// deadline == now counts as expired.
func (s *State) expireDue(now Tick) []PodID {
	var expired []PodID
	for s.dh.Len() > 0 {
		top := s.dh[0]
		s.probe.ExpiryProbe++
		if top.at > now {
			break
		}
		heap.Pop(&s.dh)
		r, ok := s.pods[top.id]
		if !ok || !r.evicting || r.deadline != top.at {
			continue // stale entry: pod deleted and re-admitted anew
		}
		s.endEviction(r, true)
		expired = append(expired, top.id)
	}
	return expired
}

func (s *State) quota(b *budgetRec) BudgetStatus {
	expected := len(b.members)
	req := requiredReady(b.budget, expected)
	return BudgetStatus{
		Expected:          expected,
		CurrentReady:      b.ready,
		RequiredReady:     req,
		DisruptionAllowed: allowance(b.ready, req),
	}
}

func (s *State) advance(now Tick) error {
	if now < s.now {
		return errf(KindClockBacktrack, "now is before last accepted time")
	}
	s.now = now
	return nil
}

// ProbeSnapshot returns the primitive-touch counters.
func (s *State) ProbeSnapshot() Probe { return s.probe }

// ResetProbe zeroes the counters (used to bound a single operation).
func (s *State) ResetProbe() { s.probe = Probe{} }
