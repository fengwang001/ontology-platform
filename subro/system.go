package subro

import "sync"

const basisPoints = 10000

// System is the concurrency-safe facade. All operations are
// serialized by a single mutex, so concurrent calls are equivalent
// to some serial order, and replaying the same accepted sequence
// yields identical results.
type System struct {
	mu               sync.Mutex
	hasClock         bool
	lastNow          int64
	adjSeq           int64
	recSeq           int64
	cases            map[string]*caseState
	adjustments      []Adjustment
	acceptedOps      int64
	recoveryCount    int64
	settleIterations int64
}

// NewSystem returns an empty System.
func NewSystem() *System {
	return &System{cases: make(map[string]*caseState)}
}

// RegisterCase registers a new case. insurerPaid must not exceed
// totalLoss; ratioBP must be within [0, 10000].
func (s *System) RegisterCase(now int64, id string, totalLoss, insurerPaid, deadline, ratioBP int64) error {
	if err := checkNow(now, OpRegister, id); err != nil {
		return err
	}
	if id == "" {
		return newError(ErrInvalidParam, OpRegister, id, "empty case id")
	}
	if totalLoss < 0 {
		return newError(ErrInvalidParam, OpRegister, id, "negative total loss")
	}
	if insurerPaid < 0 || insurerPaid > totalLoss {
		return newError(ErrInvalidParam, OpRegister, id, "insurer paid outside [0, totalLoss]")
	}
	if deadline < 0 {
		return newError(ErrInvalidParam, OpRegister, id, "negative deadline")
	}
	if ratioBP < 0 || ratioBP > basisPoints {
		return newError(ErrInvalidParam, OpRegister, id, "ratio outside [0, 10000] basis points")
	}
	_, err := s.run(now, id, OpRegister,
		func() error {
			if _, dup := s.cases[id]; dup {
				return newError(ErrInvalidParam, OpRegister, id, "duplicate case id")
			}
			return nil
		},
		nil,
		func(c *caseState) {
			s.cases[id] = &caseState{
				id:          id,
				totalLoss:   totalLoss,
				insurerPaid: insurerPaid,
				deadline:    deadline,
				ratioBP:     ratioBP,
			}
		},
	)
	return err
}

// Recover registers one recovery with the given gross amount and
// recovery expense (expense <= gross). Accepted only while now is on
// or before the case deadline.
func (s *System) Recover(now int64, id string, gross, expense int64) ([]Adjustment, error) {
	if err := checkNow(now, OpRecover, id); err != nil {
		return nil, err
	}
	if gross < 0 || expense < 0 || expense > gross {
		return nil, newError(ErrInvalidParam, OpRecover, id, "require 0 <= expense <= gross")
	}
	return s.run(now, id, OpRecover, nil,
		func(c *caseState) error {
			if now > c.deadline {
				return newError(ErrPastDeadline, OpRecover, id, "recovery after deadline")
			}
			return nil
		},
		func(c *caseState) {
			net := gross - expense
			c.recoveries = append(c.recoveries, Recovery{
				Seq:     s.recSeq,
				Gross:   gross,
				Expense: expense,
				Net:     net,
				At:      now,
			})
			s.recSeq++
			c.grossTotal += gross
			c.expenseTotal += expense
			c.netTotal += net
			s.recoveryCount++
		},
	)
}

// AdjustRatio changes the third-party liability ratio in basis
// points, which changes the recoverable cap.
func (s *System) AdjustRatio(now int64, id string, ratioBP int64) ([]Adjustment, error) {
	if err := checkNow(now, OpAdjustRate, id); err != nil {
		return nil, err
	}
	if ratioBP < 0 || ratioBP > basisPoints {
		return nil, newError(ErrInvalidParam, OpAdjustRate, id, "ratio outside [0, 10000] basis points")
	}
	return s.run(now, id, OpAdjustRate, nil, nil,
		func(c *caseState) {
			c.ratioBP = ratioBP
		},
	)
}

// SupplementPayment increases the insurer's paid amount (never above
// the total loss) and shrinks the insured's uncompensated amount.
func (s *System) SupplementPayment(now int64, id string, amount int64) ([]Adjustment, error) {
	if err := checkNow(now, OpSupplement, id); err != nil {
		return nil, err
	}
	if amount <= 0 {
		return nil, newError(ErrInvalidParam, OpSupplement, id, "non-positive amount")
	}
	return s.run(now, id, OpSupplement, nil,
		func(c *caseState) error {
			if c.insurerPaid+amount > c.totalLoss {
				return newError(ErrExceedsTotalLoss, OpSupplement, id, "insurer paid would exceed total loss")
			}
			return nil
		},
		func(c *caseState) {
			c.insurerPaid += amount
		},
	)
}

// Waive records the insured's irrevocable waiver of subrogation
// rights. Accepted only once, and only on or before the deadline.
func (s *System) Waive(now int64, id string) ([]Adjustment, error) {
	if err := checkNow(now, OpWaive, id); err != nil {
		return nil, err
	}
	return s.run(now, id, OpWaive, nil,
		func(c *caseState) error {
			if now > c.deadline {
				return newError(ErrPastDeadline, OpWaive, id, "waiver after deadline")
			}
			if c.waived {
				return newError(ErrAlreadyWaived, OpWaive, id, "waiver already declared")
			}
			return nil
		},
		func(c *caseState) {
			c.waived = true
		},
	)
}

// Snapshot returns the current state of a case.
func (s *System) Snapshot(id string) (CaseSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return CaseSnapshot{}, newError(ErrCaseNotFound, "snapshot", id, "no such case")
	}
	return c.snapshot(), nil
}

// Adjustments returns a copy of the full adjustment log of a case.
func (s *System) Adjustments(id string) []Adjustment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Adjustment
	for _, a := range s.adjustments {
		if a.CaseID == id {
			out = append(out, a)
		}
	}
	return out
}

// Recoveries returns a copy of the recovery log of a case.
func (s *System) Recoveries(id string) []Recovery {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil
	}
	return append([]Recovery(nil), c.recoveries...)
}

// Stats returns internal counters.
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Cases:            len(s.cases),
		AcceptedOps:      s.acceptedOps,
		Recoveries:       s.recoveryCount,
		Adjustments:      int64(len(s.adjustments)),
		SettleIterations: s.settleIterations,
	}
}

func checkNow(now int64, op OpKind, id string) error {
	if now < 0 {
		return newError(ErrInvalidParam, op, id, "negative now")
	}
	return nil
}

// run executes one operation under the system lock, enforcing the
// error priority: invalid params (checked by callers and preClock) >
// clock rollback > case not found > state checks. Only accepted
// operations mutate state, emit adjustments and advance the clock.
func (s *System) run(now int64, id string, op OpKind,
	preClock func() error,
	check func(*caseState) error,
	apply func(*caseState),
) ([]Adjustment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preClock != nil {
		if err := preClock(); err != nil {
			return nil, err
		}
	}
	if s.hasClock && now < s.lastNow {
		return nil, newError(ErrClockRollback, op, id, "now is before last accepted operation")
	}
	c, ok := s.cases[id]
	if !ok && op != OpRegister {
		return nil, newError(ErrCaseNotFound, op, id, "no such case")
	}
	if check != nil {
		if err := check(c); err != nil {
			return nil, err
		}
	}
	if apply != nil {
		apply(c)
	}
	if c == nil {
		c = s.cases[id]
	}
	var adjs []Adjustment
	if c != nil {
		adjs = s.settle(c, op)
	}
	s.lastNow = now
	s.hasClock = true
	s.acceptedOps++
	return adjs, nil
}

// settle clears the difference between entitled and paid for every
// party with one minimal adjustment record each. It performs exactly
// three iterations regardless of history size.
func (s *System) settle(c *caseState, op OpKind) []Adjustment {
	ent := c.entitled()
	type partyDelta struct {
		party    Party
		entitled int64
		paid     int64
	}
	parties := [3]partyDelta{
		{PartyInsured, ent.Insured, c.paid.Insured},
		{PartyInsurer, ent.Insurer, c.paid.Insurer},
		{PartyThirdParty, ent.ThirdParty, c.paid.ThirdParty},
	}
	var out []Adjustment
	for _, pd := range parties {
		s.settleIterations++
		delta := pd.entitled - pd.paid
		if delta == 0 {
			continue
		}
		out = append(out, Adjustment{
			Seq:       s.adjSeq,
			CaseID:    c.id,
			Op:        op,
			Party:     pd.party,
			Delta:     delta,
			PaidAfter: pd.entitled,
		})
		s.adjSeq++
	}
	c.paid = ent
	s.adjustments = append(s.adjustments, out...)
	return out
}
