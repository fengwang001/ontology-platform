package disruption

import "sync"

// Grace is the eviction lifetime: an eviction not confirmed by admitTick+grace
// is automatically void and the pod is reinstated to its pre-admit readiness.
type Grace = Tick

// Service is the eviction adjudication service backed by an indexed State.
//
// Every public method holds mu for its whole duration, so any concurrent call
// history is equivalent to some serial interleaving and a batch is atomic
// with respect to all other calls. Methods first validate arguments, then
// enforce the monotonic clock; a rejected call mutates nothing, including the
// clock.
type Service struct {
	mu sync.Mutex
	st *State
}

// NewService creates a Service over an empty state.
func NewService() *Service {
	return &Service{st: NewState()}
}

// begin is the shared preamble: argument validation already done by the
// caller, enforce the clock and make any due expiry visible BEFORE the
// adjudication decision.
func (s *Service) begin(now Tick) error {
	if err := s.st.advance(now); err != nil {
		return err
	}
	s.st.expireDue(now)
	return nil
}

func validPodID(id PodID) bool { return id.Namespace != "" && id.Name != "" }

// Evict adjudicates a single pod eviction. grace is the eviction lifetime;
// it must be non-negative.
//
// Precedence of rejection kinds (highest first): InvalidArgument,
// ClockBacktrack, PodNotFound, NotEvictablePhase, AlreadyEvicting, Conflict,
// Insufficient.
func (s *Service) Evict(now Tick, id PodID, grace Grace) (EvictionDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPodID(id) {
		return EvictionDecision{}, errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if grace < 0 {
		return EvictionDecision{}, errf(KindInvalidArgument, "grace must be non-negative")
	}
	if err := s.begin(now); err != nil {
		return EvictionDecision{}, err
	}
	r, ok := s.st.getPod(id)
	if !ok {
		return EvictionDecision{}, errPod(KindPodNotFound, id, "pod does not exist")
	}
	if r.pod.Phase.terminal() {
		return EvictionDecision{}, errPod(KindNotEvictablePhase, id, "pod phase "+r.pod.Phase.String()+" is not evictable")
	}
	if r.evicting {
		return EvictionDecision{}, errPod(KindAlreadyEvicting, id, "pod already has an in-flight eviction")
	}
	matched := s.st.matchBudgets(r.pod)
	if len(matched) > 1 {
		return EvictionDecision{}, errPod(KindConflict, id, "pod matched by multiple budgets")
	}
	if len(matched) == 0 {
		// Unmatched pods are admitted but still enter the evicting state, so
		// repeated evictions are rejected and grace/expiry applies uniformly.
		s.st.startEviction(r, now+grace, nil)
		return EvictionDecision{Allowed: true}, nil
	}
	b := matched[0]
	status := s.st.quota(b)
	if r.pod.Ready {
		if status.DisruptionAllowed <= 0 {
			return EvictionDecision{}, errPod(KindInsufficient, id, "no disruption allowance")
		}
	} else {
		// Unready: admitted without consuming allowance, provided the budget
		// invariant currentReady >= required currently holds.
		if status.CurrentReady < status.RequiredReady {
			return EvictionDecision{}, errPod(KindInsufficient, id, "budget already below required ready count")
		}
	}
	s.st.startEviction(r, now+grace, matched)
	return EvictionDecision{Allowed: true}, nil
}

// batchPlan records the tentative admission of one batch position.
type batchPlan struct {
	rec     *podRec
	matched []*budgetRec // empty when unmatched
}

// EvictBatch is the all-or-nothing node-drain adjudication. Unready pods are
// judged against the state at batch start; ready pods accumulate consumption
// per budget. Duplicate pods are InvalidArgument. On rejection nothing
// changes and the first offending pod in submission order is reported.
func (s *Service) EvictBatch(now Tick, ids []PodID, grace Grace) (EvictionDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if grace < 0 {
		return EvictionDecision{}, errf(KindInvalidArgument, "grace must be non-negative")
	}
	if len(ids) == 0 {
		return EvictionDecision{}, errf(KindInvalidArgument, "batch must contain at least one pod")
	}
	seen := make(map[PodID]struct{}, len(ids))
	for _, id := range ids {
		if !validPodID(id) {
			return EvictionDecision{}, errPod(KindInvalidArgument, id, "pod namespace and name are required")
		}
		if _, dup := seen[id]; dup {
			return EvictionDecision{}, errPod(KindInvalidArgument, id, "duplicate pod in batch")
		}
		seen[id] = struct{}{}
	}
	if err := s.begin(now); err != nil {
		return EvictionDecision{}, err
	}
	// Snapshot budgets' ready counts at batch start; ready admissions consume
	// as we validate, unready admissions use the snapshot.
	type snap struct {
		readyAtStart int
		readyAdmits  int
	}
	consume := map[*budgetRec]*snap{}
	plans := make([]batchPlan, 0, len(ids))
	for _, id := range ids {
		r, ok := s.st.getPod(id)
		if !ok {
			return EvictionDecision{}, errPod(KindPodNotFound, id, "pod does not exist")
		}
		if r.pod.Phase.terminal() {
			return EvictionDecision{}, errPod(KindNotEvictablePhase, id, "pod phase "+r.pod.Phase.String()+" is not evictable")
		}
		if r.evicting {
			return EvictionDecision{}, errPod(KindAlreadyEvicting, id, "pod already has an in-flight eviction")
		}
		matched := s.st.matchBudgets(r.pod)
		if len(matched) > 1 {
			return EvictionDecision{}, errPod(KindConflict, id, "pod matched by multiple budgets")
		}
		plan := batchPlan{rec: r}
		if len(matched) == 1 {
			b := matched[0]
			plan.matched = matched
			cp, seen := consume[b]
			if !seen {
				cp = &snap{readyAtStart: b.ready}
				consume[b] = cp
			}
			status := s.st.quota(b)
			if r.pod.Ready {
				// Tentative cumulative consumption is cp.readyAdmits+1.
				if cp.readyAtStart-cp.readyAdmits-1 < status.RequiredReady {
					return EvictionDecision{}, errPod(KindInsufficient, id, "batch exceeds disruption allowance")
				}
				cp.readyAdmits++
			} else {
				// Unready: judged purely on the batch-start state and is not
				// affected by ready admissions earlier in the same batch.
				if cp.readyAtStart < status.RequiredReady {
					return EvictionDecision{}, errPod(KindInsufficient, id, "budget below required ready count at batch start")
				}
			}
		}
		plans = append(plans, plan)
	}
	// All positions validated: commit atomically.
	for _, p := range plans {
		s.st.startEviction(p.rec, now+grace, p.matched)
	}
	return EvictionDecision{Allowed: true}, nil
}

// Confirm removes an evicted pod after successful drain. Confirming a pod
// that is not currently evicting is InvalidArgument.
func (s *Service) Confirm(now Tick, id PodID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPodID(id) {
		return errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if err := s.begin(now); err != nil {
		return err
	}
	r, ok := s.st.getPod(id)
	if !ok {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	if !r.evicting {
		return errPod(KindInvalidArgument, id, "pod has no in-flight eviction to confirm")
	}
	// Confirmed pods are gone for good: drop memberships (no readiness
	// restoration), the namespace index, the pod and the eviction together.
	s.st.deletePod(id)
	return nil
}

// Cancel aborts an eviction before the drain completes; the pod returns to its
// pre-admit readiness immediately.
func (s *Service) Cancel(now Tick, id PodID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPodID(id) {
		return errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if err := s.begin(now); err != nil {
		return err
	}
	r, ok := s.st.getPod(id)
	if !ok {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	if !r.evicting {
		return errPod(KindInvalidArgument, id, "pod has no in-flight eviction to cancel")
	}
	s.st.endEviction(r, true)
	return nil
}

// Expire makes due expiry visible without another adjudicating action; it
// returns the reinstated pods. Mostly useful in tests and heartbeat loops.
func (s *Service) Expire(now Tick) ([]PodID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.st.advance(now); err != nil {
		return nil, err
	}
	return s.st.expireDue(now), nil
}

// BudgetQuota returns the derived status of one budget. Like every call it
// first applies due expiry and enforces the monotonic clock, so no stale
// value is ever observable.
func (s *Service) BudgetQuota(now Tick, id BudgetID) (BudgetStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id.Namespace == "" || id.Name == "" {
		return BudgetStatus{}, errf(KindInvalidArgument, "budget namespace and name are required")
	}
	if err := s.begin(now); err != nil {
		return BudgetStatus{}, err
	}
	b, ok := s.st.getBudget(id)
	if !ok {
		return BudgetStatus{}, errf(KindPodNotFound, "budget does not exist")
	}
	return s.st.quota(b), nil
}

// UpsertPod adds or replaces a pod. Flipping readiness of an evicting pod
// through this call is rejected as AlreadyEvicting; label/phase changes of an
// evicting pod are allowed (membership moves follow immediately). Expiry is
// applied first, as for every call.
func (s *Service) UpsertPod(now Tick, p Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPodID(p.ID) {
		return errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if err := s.begin(now); err != nil {
		return err
	}
	if old, ok := s.st.getPod(p.ID); ok && old.evicting {
		if old.pod.Ready != p.Ready {
			return errPod(KindAlreadyEvicting, p.ID, "cannot change readiness while evicting")
		}
	}
	s.st.putPod(p)
	return nil
}

// SetReady flips pod readiness. Readiness mutation of an evicting pod is
// rejected as AlreadyEvicting and changes nothing.
func (s *Service) SetReady(now Tick, id PodID, ready bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPodID(id) {
		return errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if err := s.begin(now); err != nil {
		return err
	}
	r, ok := s.st.getPod(id)
	if !ok {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	if r.evicting {
		return errPod(KindAlreadyEvicting, id, "cannot change readiness while evicting")
	}
	if r.pod.Ready == ready {
		return nil
	}
	p := r.pod
	p.Ready = ready
	s.st.putPod(p)
	return nil
}

// DeletePod removes a pod (and any in-flight eviction).
func (s *Service) DeletePod(now Tick, id PodID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPodID(id) {
		return errf(KindInvalidArgument, "pod namespace and name are required")
	}
	if err := s.begin(now); err != nil {
		return err
	}
	if !s.st.deletePod(id) {
		return errPod(KindPodNotFound, id, "pod does not exist")
	}
	return nil
}

// UpsertBudget adds or replaces a budget, recomputing its membership
// immediately. Existing in-flight evictions are untouched; afterwards ready
// evictions are refused whenever the budget would drop below required.
func (s *Service) UpsertBudget(now Tick, b PodDisruptionBudget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateBudget(b); err != nil {
		return err
	}
	if err := s.begin(now); err != nil {
		return err
	}
	s.st.putBudget(b)
	return nil
}

// DeleteBudget removes a budget; matched pods become unmatched until another
// budget covers them.
func (s *Service) DeleteBudget(now Tick, id BudgetID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id.Namespace == "" || id.Name == "" {
		return errf(KindInvalidArgument, "budget namespace and name are required")
	}
	if err := s.begin(now); err != nil {
		return err
	}
	if !s.st.deleteBudget(id) {
		return errf(KindPodNotFound, "budget does not exist")
	}
	return nil
}

// State accessors used by the reference model and tests.

// ProbeSnapshot exposes primitive-touch counters for complexity proofs.
func (s *Service) ProbeSnapshot() Probe {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.ProbeSnapshot()
}

// ResetProbe zeroes primitive-touch counters.
func (s *Service) ResetProbe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.ResetProbe()
}

// IsEvicting reports the evicting flag of a pod.
func (s *Service) IsEvicting(id PodID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.st.pods[id]
	return ok && r.evicting
}

// PodReady returns the current readiness of a pod (false if absent) and
// whether it exists.
func (s *Service) PodReady(id PodID) (ready bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, exists := s.st.pods[id]
	if !exists {
		return false, false
	}
	return r.pod.Ready, true
}
