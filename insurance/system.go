package insurance

import (
	"sort"
	"sync"
)

// policyState is the mutable runtime state of a registered policy.
type policyState struct {
	Policy
	seq            int64 // registration sequence (shares one counter with accidents)
	cancelled      bool
	cancelDate     int64 // cancellation effective day
	totalPaid      int64 // cumulative paid amount
	maxCoveredDate int64 // max occurrence day among covered accidents
	hasCovered     bool  // whether any registered accident is covered by this policy
}

// accidentState is the recorded state of a registered accident.
type accidentState struct {
	Accident
	seq          int64
	subjectIndex int            // position within the subject's accident slice
	covering     []*policyState // covering policies, snapshotted at registration
	payouts      []Payout       // last computed payouts, aligned with covering
	total        int64          // sum of payouts
}

// Stats counts internal work units so complexity claims are verifiable.
type Stats struct {
	CoverageChecks      int64 // policy-coverage evaluations during registration
	RecomputedAccidents int64 // accidents recomputed during corrections
}

// System is the claim allocation engine. All methods are safe for
// concurrent use and behave as if executed in some serial order.
type System struct {
	mu                 sync.Mutex
	seq                int64
	policies           map[string]*policyState
	policiesBySubject  map[string][]*policyState // registration order
	accidents          map[string]*accidentState
	accidentsBySubject map[string][]*accidentState // registration (seq) order
	stats              Stats
}

// NewSystem creates an empty System.
func NewSystem() *System {
	return &System{
		policies:           make(map[string]*policyState),
		policiesBySubject:  make(map[string][]*policyState),
		accidents:          make(map[string]*accidentState),
		accidentsBySubject: make(map[string][]*accidentState),
	}
}

// AddPolicy registers a new policy. A policy never participates in
// accidents registered before it, even if its coverage period would
// include them.
func (s *System) AddPolicy(p Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validatePolicy(p); err != nil {
		return err
	}
	if _, dup := s.policies[p.ID]; dup {
		return newError(ErrKindDuplicateID, "policy %q already exists", p.ID)
	}
	ps := &policyState{Policy: p, seq: s.seq}
	s.seq++
	s.policies[p.ID] = ps
	s.policiesBySubject[p.Subject] = append(s.policiesBySubject[p.Subject], ps)
	return nil
}

func validatePolicy(p Policy) error {
	switch {
	case p.ID == "":
		return newError(ErrKindInvalidParam, "policy id is empty")
	case p.Subject == "":
		return newError(ErrKindInvalidParam, "policy %q: subject is empty", p.ID)
	case p.Insurer == "":
		return newError(ErrKindInvalidParam, "policy %q: insurer is empty", p.ID)
	case p.SumInsured <= 0:
		return newError(ErrKindInvalidParam, "policy %q: sum insured must be positive, got %d", p.ID, p.SumInsured)
	case p.Deductible < 0:
		return newError(ErrKindInvalidParam, "policy %q: deductible must be non-negative, got %d", p.ID, p.Deductible)
	case p.Effective < 0:
		return newError(ErrKindInvalidParam, "policy %q: effective day must be non-negative, got %d", p.ID, p.Effective)
	case p.Expiry < p.Effective:
		return newError(ErrKindInvalidParam, "policy %q: expiry %d is before effective %d", p.ID, p.Expiry, p.Effective)
	case p.Clause != ClauseNormal && p.Clause != ClauseExcess:
		return newError(ErrKindInvalidParam, "policy %q: unknown clause type %d", p.ID, int(p.Clause))
	}
	return nil
}

// RegisterAccident registers an accident and settles it against all
// covering policies. An accident with no covering policy succeeds with
// zero payout.
func (s *System) RegisterAccident(a Accident) (AccidentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateAccident(a); err != nil {
		return AccidentResult{}, err
	}
	if _, dup := s.accidents[a.ID]; dup {
		return AccidentResult{}, newError(ErrKindDuplicateID, "accident %q already exists", a.ID)
	}

	acc := &accidentState{Accident: a, seq: s.seq}
	s.seq++
	// Snapshot the covering policies: same subject, occurrence day inside
	// [Effective, Expiry], and not cancelled before the occurrence day.
	// Policies registered later can never join this snapshot.
	for _, ps := range s.policiesBySubject[a.Subject] {
		s.stats.CoverageChecks++
		if a.Date < ps.Effective || a.Date > ps.Expiry {
			continue
		}
		if ps.cancelled && a.Date >= ps.cancelDate {
			continue
		}
		acc.covering = append(acc.covering, ps)
		if !ps.hasCovered || a.Date > ps.maxCoveredDate {
			ps.maxCoveredDate = a.Date
			ps.hasCovered = true
		}
	}
	settle(acc)
	for i, po := range acc.payouts {
		if po.Amount != 0 {
			acc.covering[i].totalPaid += po.Amount
		}
	}
	acc.subjectIndex = len(s.accidentsBySubject[a.Subject])
	s.accidentsBySubject[a.Subject] = append(s.accidentsBySubject[a.Subject], acc)
	s.accidents[a.ID] = acc
	return resultOf(acc), nil
}

func validateAccident(a Accident) error {
	switch {
	case a.ID == "":
		return newError(ErrKindInvalidParam, "accident id is empty")
	case a.Subject == "":
		return newError(ErrKindInvalidParam, "accident %q: subject is empty", a.ID)
	case a.Date < 0:
		return newError(ErrKindInvalidParam, "accident %q: date must be non-negative, got %d", a.ID, a.Date)
	case a.Loss < 0:
		return newError(ErrKindInvalidParam, "accident %q: loss must be non-negative, got %d", a.ID, a.Loss)
	}
	return nil
}

// settle computes (or recomputes) an accident's payouts from the
// policies' current remaining sums insured. It does not mutate any
// policy state; callers apply the payouts afterwards.
func settle(acc *accidentState) {
	loss := acc.Loss
	var normals, excesses []participant
	for _, ps := range acc.covering {
		indep := loss - ps.Deductible
		if indep < 0 {
			indep = 0
		}
		if rem := ps.SumInsured - ps.totalPaid; indep > rem {
			indep = rem
		}
		pt := participant{policy: ps, indep: indep}
		if ps.Clause == ClauseNormal {
			normals = append(normals, pt)
		} else {
			excesses = append(excesses, pt)
		}
	}

	var sumNormal int64
	for _, pt := range normals {
		sumNormal += pt.indep
	}
	normalTotal := min(sumNormal, loss)
	normalShares := allocateLayer(normals, normalTotal)

	uncompensated := loss - normalTotal
	var sumExcess int64
	for _, pt := range excesses {
		sumExcess += pt.indep
	}
	excessTotal := min(sumExcess, uncompensated)
	excessShares := allocateLayer(excesses, excessTotal)

	shareOf := make(map[*policyState]int64, len(normals)+len(excesses))
	for i, pt := range normals {
		shareOf[pt.policy] = normalShares[i]
	}
	for i, pt := range excesses {
		shareOf[pt.policy] = excessShares[i]
	}
	acc.payouts = make([]Payout, len(acc.covering))
	acc.total = 0
	for i, ps := range acc.covering {
		amount := shareOf[ps]
		acc.payouts[i] = Payout{PolicyID: ps.ID, Amount: amount}
		acc.total += amount
	}
}

// CorrectAccident changes an accident's loss amount, then recomputes
// that accident and all later-registered accidents of the same subject
// in registration order. Earlier accidents are untouched. The cost is
// proportional to the number of recomputed accidents only.
func (s *System) CorrectAccident(id string, newLoss int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newLoss < 0 {
		return newError(ErrKindInvalidParam, "new loss must be non-negative, got %d", newLoss)
	}
	acc, ok := s.accidents[id]
	if !ok {
		return newError(ErrKindNotFound, "accident %q does not exist", id)
	}
	suffix := s.accidentsBySubject[acc.Subject][acc.subjectIndex:]

	// Undo the suffix payouts so every involved policy's remaining sum
	// insured reflects only accidents before the corrected one.
	for _, a := range suffix {
		for i, po := range a.payouts {
			if po.Amount != 0 {
				a.covering[i].totalPaid -= po.Amount
			}
		}
	}
	acc.Loss = newLoss
	// Replay the suffix in registration order.
	for _, a := range suffix {
		settle(a)
		for i, po := range a.payouts {
			if po.Amount != 0 {
				a.covering[i].totalPaid += po.Amount
			}
		}
		s.stats.RecomputedAccidents++
	}
	return nil
}

// CancelPolicy cancels a policy, effective from cancelDate (inclusive):
// accidents occurring on or after cancelDate are no longer covered,
// earlier accidents are unaffected. The cancel date must be later than
// the occurrence day of every accident the policy already covers, and
// only the policy's insurer may cancel it.
func (s *System) CancelPolicy(policyID string, cancelDate int64, insurer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if policyID == "" {
		return newError(ErrKindInvalidParam, "policy id is empty")
	}
	if cancelDate < 0 {
		return newError(ErrKindInvalidParam, "cancel date must be non-negative, got %d", cancelDate)
	}
	ps, ok := s.policies[policyID]
	if !ok {
		return newError(ErrKindNotFound, "policy %q does not exist", policyID)
	}
	if insurer != ps.Insurer {
		return newError(ErrKindInvalidParam, "policy %q belongs to insurer %q, not %q", policyID, ps.Insurer, insurer)
	}
	if ps.cancelled {
		return newError(ErrKindAlreadyCancelled, "policy %q is already cancelled", policyID)
	}
	if ps.hasCovered && cancelDate <= ps.maxCoveredDate {
		return newError(ErrKindInvalidParam,
			"cancel date %d must be later than the latest covered accident day %d", cancelDate, ps.maxCoveredDate)
	}
	ps.cancelled = true
	ps.cancelDate = cancelDate
	return nil
}

// AccidentResultOf returns the current settlement result of an accident.
func (s *System) AccidentResultOf(accidentID string) (AccidentResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accidents[accidentID]
	if !ok {
		return AccidentResult{}, false
	}
	return resultOf(acc), true
}

// PolicyStatusOf returns the current status of a policy.
func (s *System) PolicyStatusOf(policyID string) (PolicyStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, ok := s.policies[policyID]
	if !ok {
		return PolicyStatus{}, false
	}
	return PolicyStatus{
		Policy:     ps.Policy,
		Seq:        int(ps.seq),
		TotalPaid:  ps.totalPaid,
		Remaining:  ps.SumInsured - ps.totalPaid,
		Cancelled:  ps.cancelled,
		CancelDate: ps.cancelDate,
	}, true
}

// Stats returns a copy of the internal work counters.
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// ResetStats zeroes the internal work counters.
func (s *System) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = Stats{}
}

// resultOf builds the public, deterministically ordered result view.
func resultOf(acc *accidentState) AccidentResult {
	payouts := make([]Payout, len(acc.payouts))
	copy(payouts, acc.payouts)
	sort.Slice(payouts, func(i, j int) bool { return payouts[i].PolicyID < payouts[j].PolicyID })
	return AccidentResult{
		AccidentID: acc.ID,
		Seq:        int(acc.seq),
		Loss:       acc.Loss,
		TotalPaid:  acc.total,
		Payouts:    payouts,
	}
}
