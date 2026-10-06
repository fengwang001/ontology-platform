package claim

import (
	"fmt"
	"sort"
	"sync"
)

// policyRec is the stored state of one policy.
type policyRec struct {
	policy     Policy
	cancelled  bool
	cancelDay  int
	maxCovered int // largest day of an accident that ever covered it
	paid       int64
	// participationSeq and participationPaid are parallel slices recording
	// every accident seq this policy participated in (ascending) and the
	// amount it was paid. participationCum is the prefix sum over paid
	// amounts. A correction therefore restores the fixed-prefix total by one
	// binary search and one indexed read, independent of history length.
	participationSeq  []int
	participationPaid []int64
	participationCum  []int64
}

// accidentRec is the stored state of one accident, in registration order.
type accidentRec struct {
	seq      int
	accident Accident
	// participants freezes the set of policies that participated at
	// registration time. Policies added later never join retroactively.
	participants []string
	result       AccidentResult
}

// System is a concurrency-safe claim allocation engine.
type System struct {
	mu sync.Mutex

	policies  map[string]*policyRec
	accidents []*accidentRec
	accByID   map[string]int

	// One active-policy treap per subject.
	indexes map[string]*treap

	// touched counts inspected treap nodes, used by the complexity tests.
	touched           int
	recomputeCount    int
	prefixLedgerCount int
}

// NewSystem creates an empty System.
func NewSystem() *System {
	return &System{
		policies: map[string]*policyRec{},
		accByID:  map[string]int{},
		indexes:  map[string]*treap{},
	}
}

func validPolicyInput(in AddPolicyInput) bool {
	if in.ID == "" || in.Subject == "" {
		return false
	}
	if in.Limit <= 0 || in.Deductible < 0 {
		return false
	}
	if in.StartDay < 0 || in.EndDay < in.StartDay {
		return false
	}
	return in.Clause.valid()
}

func validAccidentInput(in RegisterAccidentInput) bool {
	return in.ID != "" && in.Subject != "" && in.Day >= 0 && in.Loss >= 0
}

// AddPolicy registers a new policy.
func (s *System) AddPolicy(in AddPolicyInput) error {
	if !validPolicyInput(in) {
		return fmt.Errorf("%w: policy fields out of range", ErrInvalidArg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.policies[in.ID]; exists {
		return fmt.Errorf("%w: policy id %q", ErrDuplicateID, in.ID)
	}
	if _, exists := s.accByID[in.ID]; exists {
		return fmt.Errorf("%w: id %q already used by an accident", ErrDuplicateID, in.ID)
	}

	rec := &policyRec{policy: in}
	s.policies[in.ID] = rec
	s.indexFor(in.Subject).insert(in.ID, in.StartDay, in.EndDay)
	return nil
}

func (s *System) indexFor(subject string) *treap {
	tr, ok := s.indexes[subject]
	if !ok {
		tr = newTreap()
		s.indexes[subject] = tr
	}
	return tr
}

// RegisterAccident registers an accident and returns its allocation result.
func (s *System) RegisterAccident(in RegisterAccidentInput) (*AccidentResult, error) {
	if !validAccidentInput(in) {
		return nil, fmt.Errorf("%w: accident fields out of range", ErrInvalidArg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.accByID[in.ID]; exists {
		return nil, fmt.Errorf("%w: accident id %q", ErrDuplicateID, in.ID)
	}
	if _, exists := s.policies[in.ID]; exists {
		return nil, fmt.Errorf("%w: id %q already used by a policy", ErrDuplicateID, in.ID)
	}

	tr := s.indexes[in.Subject]
	var ids []string
	if tr != nil {
		ids = tr.coveringAt(in.Day, nil)
		s.touched += tr.touchedNodes()
	}

	seq := len(s.accidents)
	rec := &accidentRec{
		seq:          seq,
		accident:     in,
		participants: ids,
	}
	s.accidents = append(s.accidents, rec)
	s.accByID[in.ID] = seq

	s.computeResult(rec)

	out := rec.result
	out.Seq = seq
	sort.Slice(out.Payments, func(i, j int) bool {
		return out.Payments[i].PolicyID < out.Payments[j].PolicyID
	})
	return cloneResult(&out), nil
}

// capacityOf returns the independent payout cap of p for a loss.
func capacityOf(p *policyRec, loss int64) int64 {
	independent := loss - p.policy.Deductible
	if independent < 0 {
		independent = 0
	}
	remaining := p.policy.Limit - p.paid
	if remaining < 0 {
		remaining = 0
	}
	if independent > remaining {
		return remaining
	}
	return independent
}

// allocate is the pure two-stage sharing rule: ordinary first, excess up to
// the uncompensated remainder.
func allocate(ordinary, excess []allocationCandidate, loss int64) (map[string]int64, map[string]int64, int64) {
	var ordinaryTotal int64
	for _, c := range ordinary {
		ordinaryTotal += c.Capacity
	}
	if ordinaryTotal > loss {
		ordinaryTotal = loss
	}
	ordinaryShares := shareGroup(ordinary, ordinaryTotal)
	uncompensated := loss - ordinaryTotal
	var excessTotal int64
	for _, c := range excess {
		excessTotal += c.Capacity
	}
	if excessTotal > uncompensated {
		excessTotal = uncompensated
	}
	return ordinaryShares, shareGroup(excess, excessTotal), ordinaryTotal
}

// computeResult runs ordinary then excess sharing, stores the result, and
// updates each participating policy's paid total and participation ledger.
// Current paid totals must already match every earlier accident.
func (s *System) computeResult(rec *accidentRec) {
	s.recomputeCount++
	loss := rec.accident.Loss
	result := AccidentResult{AccidentID: rec.accident.ID}

	var ordinary, excess []allocationCandidate
	for _, id := range rec.participants {
		p := s.policies[id]
		cand := allocationCandidate{
			PolicyID: id,
			StartDay: p.policy.StartDay,
			Capacity: capacityOf(p, loss),
		}
		if p.policy.Clause == Ordinary {
			ordinary = append(ordinary, cand)
		} else {
			excess = append(excess, cand)
		}
	}

	ordinaryShares, excessShares, ordinaryTotal := allocate(ordinary, excess, loss)
	amounts := map[string]int64{}
	result.TotalPaid = ordinaryTotal
	for _, c := range ordinary {
		if amount := ordinaryShares[c.PolicyID]; amount > 0 {
			result.Payments = append(result.Payments, Payment{c.PolicyID, amount})
			amounts[c.PolicyID] = amount
		}
	}
	for _, c := range excess {
		if amount := excessShares[c.PolicyID]; amount > 0 {
			result.Payments = append(result.Payments, Payment{c.PolicyID, amount})
			result.TotalPaid += amount
			amounts[c.PolicyID] = amount
		}
	}
	rec.result = result

	for _, id := range rec.participants {
		p := s.policies[id]
		amount := amounts[id]
		p.paid += amount
		p.participationSeq = append(p.participationSeq, rec.seq)
		p.participationPaid = append(p.participationPaid, amount)
		p.participationCum = append(p.participationCum, p.paid)
		if rec.accident.Day > p.maxCovered {
			p.maxCovered = rec.accident.Day
		}
	}
}

// replayFrom recomputes accidents from fromSeq onward. Only policies that
// participate in that suffix are touched, and each one's fixed-prefix total
// is restored by a binary search over its own participation ledger, so the
// cost is independent of the number of unaffected accidents.
func (s *System) replayFrom(fromSeq int) {
	touched := map[string]bool{}
	for i := fromSeq; i < len(s.accidents); i++ {
		for _, id := range s.accidents[i].participants {
			touched[id] = true
		}
	}
	for id := range touched {
		p := s.policies[id]
		s.prefixLedgerCount++
		cut := sort.SearchInts(p.participationSeq, fromSeq)
		var prefix int64
		if cut > 0 {
			prefix = p.participationCum[cut-1]
		}
		p.paid = prefix
		p.participationSeq = append([]int(nil), p.participationSeq[:cut]...)
		p.participationPaid = append([]int64(nil), p.participationPaid[:cut]...)
		p.participationCum = append([]int64(nil), p.participationCum[:cut]...)
	}
	for i := fromSeq; i < len(s.accidents); i++ {
		s.computeResult(s.accidents[i])
	}
}

// CorrectAccident changes an accident's loss and recomputes from that point.
func (s *System) CorrectAccident(in CorrectAccidentInput) (*AccidentResult, error) {
	if in.AccidentID == "" || in.NewLoss < 0 {
		return nil, fmt.Errorf("%w: correction fields out of range", ErrInvalidArg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	seq, ok := s.accByID[in.AccidentID]
	if !ok {
		return nil, fmt.Errorf("%w: accident id %q", ErrNotFound, in.AccidentID)
	}

	if s.accidents[seq].accident.Loss == in.NewLoss {
		out := s.accidents[seq].result
		out.Seq = seq
		return cloneResult(&out), nil
	}

	s.accidents[seq].accident.Loss = in.NewLoss
	s.replayFrom(seq)

	out := s.accidents[seq].result
	out.Seq = seq
	sort.Slice(out.Payments, func(i, j int) bool {
		return out.Payments[i].PolicyID < out.Payments[j].PolicyID
	})
	return cloneResult(&out), nil
}

// CancelPolicy cancels a policy effective on CancelDay.
func (s *System) CancelPolicy(in CancelPolicyInput) error {
	if in.PolicyID == "" || in.CancelDay < 0 {
		return fmt.Errorf("%w: cancel fields out of range", ErrInvalidArg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.policies[in.PolicyID]
	if !ok {
		return fmt.Errorf("%w: policy id %q", ErrNotFound, in.PolicyID)
	}
	if in.CancelDay > p.policy.EndDay {
		return fmt.Errorf("%w: cancel day after end day", ErrInvalidArg)
	}
	if in.CancelDay < p.maxCovered {
		return fmt.Errorf("%w: cancel day before a covered accident", ErrInvalidArg)
	}
	if p.cancelled {
		return ErrPolicyCancelled
	}

	p.cancelled = true
	p.cancelDay = in.CancelDay
	// Keep the node (it still covers earlier-dated accidents) but make it
	// ineligible for accidents on or after the cancellation day.
	s.indexFor(p.policy.Subject).setCancel(p.policy.StartDay, in.PolicyID, in.CancelDay)
	return nil
}

// AccidentResult returns the current result of an accident.
func (s *System) AccidentResult(accidentID string) (*AccidentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq, ok := s.accByID[accidentID]
	if !ok {
		return nil, fmt.Errorf("%w: accident id %q", ErrNotFound, accidentID)
	}
	out := s.accidents[seq].result
	out.Seq = seq
	return cloneResult(&out), nil
}

// RemainingLimit returns a policy's unconsumed limit.
func (s *System) RemainingLimit(policyID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.policies[policyID]
	if !ok {
		return 0, fmt.Errorf("%w: policy id %q", ErrNotFound, policyID)
	}
	return p.policy.Limit - p.paid, nil
}

func cloneResult(in *AccidentResult) *AccidentResult {
	out := *in
	if in.Payments != nil {
		out.Payments = append([]Payment(nil), in.Payments...)
	}
	return &out
}

// WorkStats reports internal work counters used to verify the complexity
// claims. Counters are cumulative since the System was created.
type WorkStats struct {
	// IndexNodesInspected is the number of treap nodes visited while
	// finding covering policies (point-query cost).
	IndexNodesInspected int
	// RecomputeAccidents is the number of accidents whose allocation was
	// recomputed (driven by registrations and corrections).
	RecomputeAccidents int
	// PrefixLedgerEntries is the number of per-policy ledger entries
	// inspected when restoring fixed-prefix totals during corrections.
	PrefixLedgerEntries int
}

// Stats returns cumulative internal work counters.
func (s *System) Stats() WorkStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return WorkStats{
		IndexNodesInspected: s.touched,
		RecomputeAccidents:  s.recomputeCount,
		PrefixLedgerEntries: s.prefixLedgerCount,
	}
}

// ReplayConsistencyOK replays every accident from scratch and verifies that
// the incrementally maintained state matches a full sequential replay.
func (s *System) ReplayConsistencyOK() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	paid := make(map[string]int64, len(s.policies))
	for _, rec := range s.accidents {
		var ordinary, excess []allocationCandidate
		for _, id := range rec.participants {
			p := s.policies[id]
			cand := allocationCandidate{
				PolicyID: id,
				StartDay: p.policy.StartDay,
				Capacity: capacityOfWithPaid(p, rec.accident.Loss, paid[id]),
			}
			if p.policy.Clause == Ordinary {
				ordinary = append(ordinary, cand)
			} else {
				excess = append(excess, cand)
			}
		}
		var ordinaryTotal int64
		for _, c := range ordinary {
			ordinaryTotal += c.Capacity
		}
		if ordinaryTotal > rec.accident.Loss {
			ordinaryTotal = rec.accident.Loss
		}
		ordinaryShares := shareGroup(ordinary, ordinaryTotal)
		uncompensated := rec.accident.Loss - ordinaryTotal
		var excessTotal int64
		for _, c := range excess {
			excessTotal += c.Capacity
		}
		if excessTotal > uncompensated {
			excessTotal = uncompensated
		}
		excessShares := shareGroup(excess, excessTotal)

		got := AccidentResult{AccidentID: rec.accident.ID}
		for _, c := range ordinary {
			if amount := ordinaryShares[c.PolicyID]; amount > 0 {
				got.Payments = append(got.Payments, Payment{c.PolicyID, amount})
				got.TotalPaid += amount
				paid[c.PolicyID] += amount
			}
		}
		for _, c := range excess {
			if amount := excessShares[c.PolicyID]; amount > 0 {
				got.Payments = append(got.Payments, Payment{c.PolicyID, amount})
				got.TotalPaid += amount
				paid[c.PolicyID] += amount
			}
		}
		sort.Slice(got.Payments, func(i, j int) bool {
			return got.Payments[i].PolicyID < got.Payments[j].PolicyID
		})
		want := rec.result
		sort.Slice(want.Payments, func(i, j int) bool {
			return want.Payments[i].PolicyID < want.Payments[j].PolicyID
		})
		if got.TotalPaid != want.TotalPaid || len(got.Payments) != len(want.Payments) {
			return false
		}
		for i := range got.Payments {
			if got.Payments[i] != want.Payments[i] {
				return false
			}
		}
	}
	for id, p := range s.policies {
		if paid[id] != p.paid {
			return false
		}
	}
	return true
}

func capacityOfWithPaid(p *policyRec, loss, paid int64) int64 {
	independent := loss - p.policy.Deductible
	if independent < 0 {
		independent = 0
	}
	remaining := p.policy.Limit - paid
	if remaining < 0 {
		remaining = 0
	}
	if independent > remaining {
		return remaining
	}
	return independent
}
