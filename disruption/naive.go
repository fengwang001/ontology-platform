package disruption

import "sort"

// NaiveService is an independent reference implementation. It keeps no
// indexes and derives every answer by scanning all pods and all budgets
// (O(N*M) per decision). It shares only the pure arithmetic/validation
// helpers with Service; all state organization and decision bookkeeping are
// deliberately written differently, making it useful as a differential-test
// oracle for the indexed implementation.
type NaiveService struct {
	pods    map[PodID]*naivePod
	budgets map[BudgetID]PodDisruptionBudget
	evict   map[PodID]*naiveEv
	now     Tick
}

type naivePod struct {
	pod Pod
}

type naiveEv struct {
	deadline     Tick
	readyAtAdmit bool
}

// NewNaiveService creates the reference model over an empty state.
func NewNaiveService() *NaiveService {
	return &NaiveService{
		pods:    map[PodID]*naivePod{},
		budgets: map[BudgetID]PodDisruptionBudget{},
		evict:   map[PodID]*naiveEv{},
	}
}

// accountingReady mirrors the indexed store's ready view: pod.Ready and no
// in-flight eviction.
func (n *NaiveService) accountingReady(id PodID) bool {
	p := n.pods[id]
	if p == nil {
		return false
	}
	if _, on := n.evict[id]; on {
		return false
	}
	return p.pod.Ready
}

// statusOf recomputes one budget status by scanning every pod.
func (n *NaiveService) statusOf(b PodDisruptionBudget) BudgetStatus {
	expected := 0
	ready := 0
	ids := make([]PodID, 0)
	for id, p := range n.pods {
		if id.Namespace != b.ID.Namespace || p.pod.Phase.terminal() {
			continue
		}
		if selectorMatches(b.Selector, p.pod.Labels) {
			expected++
			ids = append(ids, id)
			if n.accountingReady(id) {
				ready++
			}
		}
	}
	req := requiredReady(b, expected)
	return BudgetStatus{
		Expected:          expected,
		CurrentReady:      ready,
		RequiredReady:     req,
		DisruptionAllowed: allowance(ready, req),
	}
}

// matches returns all non-terminal budgets that match the pod, sorted by
// budget name, by scanning every budget.
func (n *NaiveService) matches(p Pod) []PodDisruptionBudget {
	var out []PodDisruptionBudget
	for _, b := range n.budgets {
		if b.ID.Namespace != p.ID.Namespace {
			continue
		}
		if selectorMatches(b.Selector, p.Labels) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Name < out[j].ID.Name })
	return out
}

// tick applies the clock rule and left-closed expiry, returning reinstated ids.
func (n *NaiveService) tick(now Tick) ([]PodID, error) {
	if now < n.now {
		return nil, errf(KindClockBacktrack, "now is before last accepted time")
	}
	n.now = now
	var expired []PodID
	for id, e := range n.evict {
		if e.deadline <= now {
			expired = append(expired, id)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		a, b := expired[i], expired[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	for _, id := range expired {
		e := n.evict[id]
		if p := n.pods[id]; p != nil {
			p.pod.Ready = e.readyAtAdmit
		}
		delete(n.evict, id)
	}
	return expired, nil
}

func (n *NaiveService) evictOne(id PodID, deadline Tick) error {
	p := n.pods[id]
	matched := n.matches(p.pod)
	if len(matched) > 1 {
		return errPod(KindConflict, id, "pod matched by multiple budgets")
	}
	if len(matched) == 0 {
		n.evict[id] = &naiveEv{deadline: deadline, readyAtAdmit: p.pod.Ready}
		return nil
	}
	st := n.statusOf(matched[0])
	if p.pod.Ready {
		if st.DisruptionAllowed <= 0 {
			return errPod(KindInsufficient, id, "no disruption allowance")
		}
	} else if st.CurrentReady < st.RequiredReady {
		return errPod(KindInsufficient, id, "budget already below required ready count")
	}
	n.evict[id] = &naiveEv{deadline: deadline, readyAtAdmit: p.pod.Ready}
	return nil
}

// Evict mirrors Service.Evict.
func (n *NaiveService) Evict(now Tick, id PodID, grace Grace) (EvictionDecision, error) {
	if !validPodID(id) {
		return EvictionDecision{}, errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if grace < 0 {
		return EvictionDecision{}, errf(KindInvalidArgument, "grace must be non-negative")
	}
	if _, err := n.tick(now); err != nil {
		return EvictionDecision{}, err
	}
	p, ok := n.pods[id]
	if !ok {
		return EvictionDecision{}, errPod(KindPodNotFound, id, "pod does not exist")
	}
	if p.pod.Phase.terminal() {
		return EvictionDecision{}, errPod(KindNotEvictablePhase, id, "phase not evictable")
	}
	if _, on := n.evict[id]; on {
		return EvictionDecision{}, errPod(KindAlreadyEvicting, id, "already evicting")
	}
	if err := n.evictOne(id, now+grace); err != nil {
		return EvictionDecision{}, err
	}
	return EvictionDecision{Allowed: true}, nil
}

// EvictBatch mirrors Service.EvictBatch with snapshot-at-start unready checks.
func (n *NaiveService) EvictBatch(now Tick, ids []PodID, grace Grace) (EvictionDecision, error) {
	if grace < 0 {
		return EvictionDecision{}, errf(KindInvalidArgument, "grace must be non-negative")
	}
	if len(ids) == 0 {
		return EvictionDecision{}, errf(KindInvalidArgument, "empty batch")
	}
	seen := map[PodID]struct{}{}
	for _, id := range ids {
		if !validPodID(id) {
			return EvictionDecision{}, errPod(KindInvalidArgument, id, "bad pod id")
		}
		if _, dup := seen[id]; dup {
			return EvictionDecision{}, errPod(KindInvalidArgument, id, "duplicate pod in batch")
		}
		seen[id] = struct{}{}
	}
	if _, err := n.tick(now); err != nil {
		return EvictionDecision{}, err
	}
	type bs struct {
		startReady int
		admits     int
	}
	cs := map[BudgetID]*bs{}
	for _, id := range ids {
		p, ok := n.pods[id]
		if !ok {
			return EvictionDecision{}, errPod(KindPodNotFound, id, "pod does not exist")
		}
		if p.pod.Phase.terminal() {
			return EvictionDecision{}, errPod(KindNotEvictablePhase, id, "phase not evictable")
		}
		if _, on := n.evict[id]; on {
			return EvictionDecision{}, errPod(KindAlreadyEvicting, id, "already evicting")
		}
		matched := n.matches(p.pod)
		if len(matched) > 1 {
			return EvictionDecision{}, errPod(KindConflict, id, "conflict")
		}
		if len(matched) == 1 {
			b := matched[0]
			cp, ok := cs[b.ID]
			if !ok {
				cp = &bs{startReady: n.statusOf(b).CurrentReady}
				cs[b.ID] = cp
			}
			st := n.statusOf(b)
			if p.pod.Ready {
				if cp.startReady-cp.admits-1 < st.RequiredReady {
					return EvictionDecision{}, errPod(KindInsufficient, id, "batch exceeds allowance")
				}
				cp.admits++
			} else if cp.startReady < st.RequiredReady {
				return EvictionDecision{}, errPod(KindInsufficient, id, "below required at batch start")
			}
		}
	}
	for _, id := range ids {
		p := n.pods[id]
		n.evict[id] = &naiveEv{deadline: now + grace, readyAtAdmit: p.pod.Ready}
	}
	return EvictionDecision{Allowed: true}, nil
}

// Confirm mirrors Service.Confirm.
func (n *NaiveService) Confirm(now Tick, id PodID) error {
	if !validPodID(id) {
		return errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if _, err := n.tick(now); err != nil {
		return err
	}
	if _, ok := n.pods[id]; !ok {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	if _, on := n.evict[id]; !on {
		return errPod(KindInvalidArgument, id, "no in-flight eviction")
	}
	delete(n.evict, id)
	delete(n.pods, id)
	return nil
}

// Cancel mirrors Service.Cancel.
func (n *NaiveService) Cancel(now Tick, id PodID) error {
	if !validPodID(id) {
		return errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if _, err := n.tick(now); err != nil {
		return err
	}
	p, ok := n.pods[id]
	if !ok {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	e, on := n.evict[id]
	if !on {
		return errPod(KindInvalidArgument, id, "no in-flight eviction")
	}
	p.pod.Ready = e.readyAtAdmit
	delete(n.evict, id)
	return nil
}

// Expire mirrors Service.Expire.
func (n *NaiveService) Expire(now Tick) ([]PodID, error) {
	return n.tick(now)
}

// BudgetQuota mirrors Service.BudgetQuota.
func (n *NaiveService) BudgetQuota(now Tick, id BudgetID) (BudgetStatus, error) {
	if id.Namespace == "" || id.Name == "" {
		return BudgetStatus{}, errf(KindInvalidArgument, "bad budget id")
	}
	if _, err := n.tick(now); err != nil {
		return BudgetStatus{}, err
	}
	b, ok := n.budgets[id]
	if !ok {
		return BudgetStatus{}, errf(KindPodNotFound, "budget does not exist")
	}
	return n.statusOf(b), nil
}

// UpsertPod mirrors Service.UpsertPod, preserving in-flight evictions.
func (n *NaiveService) UpsertPod(now Tick, p Pod) error {
	if !validPodID(p.ID) {
		return errf(KindInvalidArgument, "bad pod id")
	}
	if _, err := n.tick(now); err != nil {
		return err
	}
	if old, ok := n.pods[p.ID]; ok {
		if _, on := n.evict[p.ID]; on && old.pod.Ready != p.Ready {
			return errPod(KindAlreadyEvicting, p.ID, "cannot change readiness while evicting")
		}
	}
	labels := map[string]string{}
	for k, v := range p.Labels {
		labels[k] = v
	}
	cp := p
	cp.Labels = labels
	if e, on := n.evict[p.ID]; on {
		cp.Ready = e.readyAtAdmit
	}
	n.pods[p.ID] = &naivePod{pod: cp}
	return nil
}

// SetReady mirrors Service.SetReady.
func (n *NaiveService) SetReady(now Tick, id PodID, ready bool) error {
	if !validPodID(id) {
		return errf(KindInvalidArgument, "bad pod id")
	}
	if _, err := n.tick(now); err != nil {
		return err
	}
	p, ok := n.pods[id]
	if !ok {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	if _, on := n.evict[id]; on {
		return errPod(KindAlreadyEvicting, id, "cannot change readiness while evicting")
	}
	p.pod.Ready = ready
	return nil
}

// DeletePod mirrors Service.DeletePod.
func (n *NaiveService) DeletePod(now Tick, id PodID) error {
	if !validPodID(id) {
		return errf(KindInvalidArgument, "bad pod id")
	}
	if _, err := n.tick(now); err != nil {
		return err
	}
	if _, ok := n.pods[id]; !ok {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	delete(n.evict, id)
	delete(n.pods, id)
	return nil
}

// UpsertBudget mirrors Service.UpsertBudget.
func (n *NaiveService) UpsertBudget(now Tick, b PodDisruptionBudget) error {
	if err := validateBudget(b); err != nil {
		return err
	}
	if _, err := n.tick(now); err != nil {
		return err
	}
	sel := Selector{}
	for k, v := range b.Selector {
		sel[k] = v
	}
	cp := b
	cp.Selector = sel
	n.budgets[b.ID] = cp
	return nil
}

// DeleteBudget mirrors Service.DeleteBudget.
func (n *NaiveService) DeleteBudget(now Tick, id BudgetID) error {
	if id.Namespace == "" || id.Name == "" {
		return errf(KindInvalidArgument, "bad budget id")
	}
	if _, err := n.tick(now); err != nil {
		return err
	}
	if _, ok := n.budgets[id]; !ok {
		return errf(KindPodNotFound, "budget does not exist")
	}
	delete(n.budgets, id)
	return nil
}

// IsEvicting mirrors Service.IsEvicting.
func (n *NaiveService) IsEvicting(id PodID) bool {
	_, on := n.evict[id]
	return on
}

// PodReady exposes readiness for differential snapshots.
func (n *NaiveService) PodReady(id PodID) (bool, bool) {
	p, ok := n.pods[id]
	if !ok {
		return false, false
	}
	return p.pod.Ready, true
}
