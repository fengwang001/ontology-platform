// Package naive is an independent, deliberately straightforward reference
// model of the claim allocation rules. It keeps an append-only event log and
// replays the whole log on every operation, which makes it slow but easy to
// audit against the production implementation.
package naive

import (
	"fmt"
	"sort"
	"sync"

	"ontology/internal/claim"
)

// Re-exported input/output types, identical in meaning to the claim package.
type (
	Policy         = claim.Policy
	Accident       = claim.Accident
	Payment        = claim.Payment
	AccidentResult = claim.AccidentResult
	AddPolicyInput = claim.AddPolicyInput
	RegisterInput  = claim.RegisterAccidentInput
	CorrectInput   = claim.CorrectAccidentInput
	CancelInput    = claim.CancelPolicyInput
)

var (
	ErrInvalidArg      = claim.ErrInvalidArg
	ErrDuplicateID     = claim.ErrDuplicateID
	ErrNotFound        = claim.ErrNotFound
	ErrPolicyCancelled = claim.ErrPolicyCancelled
)

type nPolicy struct {
	p      Policy
	cancel int
	dead   bool
}

type nAccident struct {
	a Accident
}

type eventKind int

const (
	evAddPolicy eventKind = iota
	evRegister
	evCorrect
	evCancel
)

type event struct {
	kind    eventKind
	policy  Policy
	accID   string
	newLoss int64
	day     int
	id      string
}

// Model is the reference implementation.
type Model struct {
	mu     sync.Mutex
	events []event
}

// NewModel creates an empty reference model.
func NewModel() *Model { return &Model{} }

func validPolicy(p Policy) bool {
	return p.ID != "" && p.Subject != "" && p.Limit > 0 && p.Deductible >= 0 &&
		p.StartDay >= 0 && p.EndDay >= p.StartDay &&
		(p.Clause == claim.Ordinary || p.Clause == claim.Excess)
}

func validAccident(a Accident) bool {
	return a.ID != "" && a.Subject != "" && a.Day >= 0 && a.Loss >= 0
}

func errInvalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalidArg, msg) }

func (m *Model) AddPolicy(in AddPolicyInput) error {
	if !validPolicy(in) {
		return errInvalid("policy fields")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.replay()
	if _, ok := state.policies[in.ID]; ok {
		return fmt.Errorf("%w: policy id %q", ErrDuplicateID, in.ID)
	}
	m.events = append(m.events, event{kind: evAddPolicy, policy: in})
	return nil
}

func (m *Model) RegisterAccident(in RegisterInput) (*AccidentResult, error) {
	if !validAccident(in) {
		return nil, errInvalid("accident fields")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.replay()
	if _, ok := state.accidents[in.ID]; ok {
		return nil, fmt.Errorf("%w: accident id %q", ErrDuplicateID, in.ID)
	}
	m.events = append(m.events, event{kind: evRegister, accID: in.ID, policy: Policy{Subject: in.Subject}, day: in.Day, newLoss: in.Loss})
	return m.resultOf(in.ID), nil
}

func (m *Model) CorrectAccident(in CorrectInput) (*AccidentResult, error) {
	if in.AccidentID == "" || in.NewLoss < 0 {
		return nil, errInvalid("correction fields")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.replay()
	if _, ok := state.accidents[in.AccidentID]; !ok {
		return nil, fmt.Errorf("%w: accident id %q", ErrNotFound, in.AccidentID)
	}
	m.events = append(m.events, event{kind: evCorrect, accID: in.AccidentID, newLoss: in.NewLoss})
	return m.resultOf(in.AccidentID), nil
}

func (m *Model) CancelPolicy(in CancelInput) error {
	if in.PolicyID == "" || in.CancelDay < 0 {
		return errInvalid("cancel fields")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.replay()
	p, ok := state.policies[in.PolicyID]
	if !ok {
		return fmt.Errorf("%w: policy id %q", ErrNotFound, in.PolicyID)
	}
	if in.CancelDay > p.p.EndDay {
		return errInvalid("cancel day after end")
	}
	if in.CancelDay < state.maxCovered[in.PolicyID] {
		return errInvalid("cancel day before covered accident")
	}
	if p.dead {
		return ErrPolicyCancelled
	}
	m.events = append(m.events, event{kind: evCancel, id: in.PolicyID, day: in.CancelDay})
	return nil
}

func (m *Model) AccidentResult(id string) (*AccidentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.replay()
	if _, ok := state.accidents[id]; !ok {
		return nil, fmt.Errorf("%w: accident id %q", ErrNotFound, id)
	}
	return m.resultOfLocked(id, state), nil
}

func (m *Model) RemainingLimit(id string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.replay()
	p, ok := state.policies[id]
	if !ok {
		return 0, fmt.Errorf("%w: policy id %q", ErrNotFound, id)
	}
	return p.p.Limit - state.paid[id], nil
}

// snapshot is the result of a full replay.
type snapshot struct {
	policies   map[string]*nPolicy
	accidents  map[string]Accident
	accOrder   []string
	paid       map[string]int64
	results    map[string]AccidentResult
	maxCovered map[string]int
}

// replay re-executes every event from the beginning using only linear scans.
func (m *Model) replay() *snapshot {
	st := &snapshot{
		policies:   map[string]*nPolicy{},
		accidents:  map[string]Accident{},
		paid:       map[string]int64{},
		results:    map[string]AccidentResult{},
		maxCovered: map[string]int{},
	}

	// Determine the current loss of every accident (latest correction wins),
	// but allocations are computed in registration order and policy
	// participation only uses policies added before the registration event.
	type regInfo struct {
		a  Accident
		ev int
	}
	regs := map[string]*regInfo{}
	var order []string
	currentLoss := map[string]int64{}

	for i, e := range m.events {
		switch e.kind {
		case evRegister:
			a := Accident{ID: e.accID, Subject: e.policy.Subject, Day: e.day, Loss: e.newLoss}
			regs[e.accID] = &regInfo{a: a, ev: i}
			order = append(order, e.accID)
			currentLoss[e.accID] = e.newLoss
		case evCorrect:
			currentLoss[e.accID] = e.newLoss
		}
	}

	// Allocations.
	for _, id := range order {
		info := regs[id]
		loss := currentLoss[id]
		a := Accident{ID: id, Subject: info.a.Subject, Day: info.a.Day, Loss: loss}
		st.accidents[id] = a

		// Participants: policies added before the registration event,
		// covering subject and day, and either never cancelled or cancelled
		// strictly after the accident day (cancellation only affects
		// registrations on/after cancel day).
		var ordinary, excess []nCand
		for pi, e := range m.events {
			if e.kind != evAddPolicy || pi >= info.ev {
				continue
			}
			p := e.policy
			if p.Subject != a.Subject || p.StartDay > a.Day || p.EndDay < a.Day {
				continue
			}
			active := true
			for ci := pi + 1; ci < len(m.events); ci++ {
				ce := m.events[ci]
				// Only cancellations that have already happened by the time
				// this accident is registered matter. Effective on its day,
				// a cancel with day <= accident day excludes the policy from
				// a registration being evaluated now.
				if ce.kind != evCancel || ce.id != p.ID {
					continue
				}
				if info.ev < ci {
					// Future cancellation: policy was still active when the
					// accident was registered, so it participated and stays
					// in the frozen participant set (cancel day may only be
					// >= the accident day by validation).
					continue
				}
				if ce.day <= a.Day {
					active = false
				}
			}
			if !active {
				continue
			}
			indep := loss - p.Deductible
			if indep < 0 {
				indep = 0
			}
			rem := p.Limit - st.paid[p.ID]
			if rem < 0 {
				rem = 0
			}
			c := indep
			if c > rem {
				c = rem
			}
			entry := nCand{id: p.ID, start: p.StartDay, cap: c, ord: p.Clause == claim.Ordinary}
			if entry.ord {
				ordinary = append(ordinary, entry)
			} else {
				excess = append(excess, entry)
			}
		}

		res := AccidentResult{AccidentID: id}
		var ordSum int64
		for _, c := range ordinary {
			ordSum += c.cap
		}
		ordPot := ordSum
		if ordPot > loss {
			ordPot = loss
		}
		ordShares := naiveShare(ordinary, ordPot)
		uncomp := loss - ordPot
		var exSum int64
		for _, c := range excess {
			exSum += c.cap
		}
		exPot := exSum
		if exPot > uncomp {
			exPot = uncomp
		}
		exShares := naiveShare(excess, exPot)

		emit := func(shares map[string]int64) {
			var ids []string
			for pid, amt := range shares {
				if amt > 0 {
					ids = append(ids, pid)
				}
			}
			sort.Strings(ids)
			for _, pid := range ids {
				amt := shares[pid]
				res.Payments = append(res.Payments, Payment{PolicyID: pid, Amount: amt})
				res.TotalPaid += amt
				st.paid[pid] += amt
				if a.Day > st.maxCovered[pid] {
					st.maxCovered[pid] = a.Day
				}
			}
		}
		// Any participating policy covers the accident for cancellation
		// validation, even if its payout rounds down to zero.
		for _, c := range ordinary {
			if a.Day > st.maxCovered[c.id] {
				st.maxCovered[c.id] = a.Day
			}
		}
		for _, c := range excess {
			if a.Day > st.maxCovered[c.id] {
				st.maxCovered[c.id] = a.Day
			}
		}
		emit(ordShares)
		emit(exShares)
		st.results[id] = res
	}

	// Populate policies (latest state) for lookup by callers.
	for _, e := range m.events {
		if e.kind == evAddPolicy {
			st.policies[e.policy.ID] = &nPolicy{p: e.policy}
		}
	}
	for _, e := range m.events {
		if e.kind == evCancel {
			if p, ok := st.policies[e.id]; ok {
				p.dead = true
				p.cancel = e.day
			}
		}
	}
	st.accOrder = order
	return st
}

type nCand struct {
	id    string
	start int
	cap   int64
	ord   bool
}

// naiveShare is the most literal implementation: floor shares, then hand out
// the remainder one unit at a time in (start, id) order.
func naiveShare(cs []nCand, pot int64) map[string]int64 {
	out := map[string]int64{}
	var sum int64
	for _, c := range cs {
		out[c.id] = 0
		sum += c.cap
	}
	if sum <= 0 || pot <= 0 {
		return out
	}
	if pot > sum {
		pot = sum
	}
	order := append([]nCand(nil), cs...)
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].start != order[j].start {
			return order[i].start < order[j].start
		}
		return order[i].id < order[j].id
	})
	capOf := map[string]int64{}
	var given int64
	for _, c := range order {
		capOf[c.id] = c.cap
		q := pot * c.cap / sum
		if q > c.cap {
			q = c.cap
		}
		out[c.id] = q
		given += q
	}
	remainder := pot - given
	for remainder > 0 {
		progress := false
		for _, c := range order {
			if remainder <= 0 {
				break
			}
			if out[c.id] < capOf[c.id] {
				out[c.id]++
				remainder--
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return out
}

func (m *Model) resultOf(id string) *AccidentResult {
	return m.resultOfLocked(id, m.replay())
}

func (m *Model) resultOfLocked(id string, st *snapshot) *AccidentResult {
	res := st.results[id]
	res.Seq = indexOf(st.accOrder, id)
	out := res
	out.Payments = append([]Payment(nil), res.Payments...)
	return &out
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
