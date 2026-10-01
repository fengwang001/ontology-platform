// Package netting implements a multilateral netting and settlement engine.
package netting

import (
	"sort"
	"sync"
)

// Constraints enforced by the engine.
const (
	MaxCap         = 1_000_000_000_000_000 // 10^15, inclusive cap bound
	MinAmount      = 1
	MaxAmount      = 1_000_000_000_000 // 10^12
	MaxObligations = 100_000
)

// maxObligationsForTest is indirection over MaxObligations so tests can
// exercise the full-cycle path without inserting 100k obligations.
var maxObligationsForTest = MaxObligations

// Reject reasons. Submit checks them in exactly this precedence order.
var (
	ErrInvalidArgument = registerReason("netting: invalid argument")
	ErrDuplicateParty  = registerReason("netting: duplicate party registration")

	ErrDuplicateObligation = submitReason("netting: duplicate obligation id in cycle")
	ErrUnknownParty        = submitReason("netting: party not registered")
	ErrSelfCounterparty    = submitReason("netting: obligor equals obligee")
	ErrCycleFull           = submitReason("netting: cycle obligation limit reached")
)

// registerReason is returned by Register.
type registerReason string

func (e registerReason) Error() string { return string(e) }

// submitReason is returned by Submit.
type submitReason string

func (e submitReason) Error() string { return string(e) }

// Obligation is one "from must pay to amount" entry in a cycle.
type Obligation struct {
	OID    string
	From   string
	To     string
	Amount int64
}

// Position is one party's net position.
type Position struct {
	ID  string
	Net int64 // receivables minus payables
}

// Instruction is one final funds transfer from Payer to Payee.
type Instruction struct {
	Payer  string
	Payee  string
	Amount int64
}

// CycleResult is the deterministic outcome of closing one cycle.
type CycleResult struct {
	Cycle        int64
	Defaulters   [][]string // defaulter ids per detection round; each round sorted ascending
	Revoked      []string   // revoked obligation ids, sorted ascending
	Positions    []Position // all registered parties, sorted by id ascending
	Instructions []Instruction
}

// Engine is the concurrency-safe netting engine.
type Engine struct {
	mu      sync.Mutex
	cycle   int64
	parties map[string]int64 // id -> net-debit cap
	oids    map[string]struct{}
	obl     []Obligation
}

// NewEngine creates an empty engine; the first cycle has number 1.
func NewEngine() *Engine {
	return &Engine{
		cycle:   1,
		parties: make(map[string]int64),
		oids:    make(map[string]struct{}),
	}
}

// Register records a party with a net-debit cap.
func (e *Engine) Register(id string, cap int64) error {
	if id == "" || cap < 0 || cap > MaxCap {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.parties[id]; ok {
		return ErrDuplicateParty
	}
	e.parties[id] = cap
	return nil
}

// Submit records an obligation in the current cycle.
func (e *Engine) Submit(oid, from, to string, amount int64) error {
	if oid == "" || amount < MinAmount || amount > MaxAmount {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.oids[oid]; ok {
		return ErrDuplicateObligation
	}
	if _, ok := e.parties[from]; !ok {
		return ErrUnknownParty
	}
	if _, ok := e.parties[to]; !ok {
		return ErrUnknownParty
	}
	if from == to {
		return ErrSelfCounterparty
	}
	if len(e.obl) >= maxObligationsForTest {
		return ErrCycleFull
	}
	e.oids[oid] = struct{}{}
	e.obl = append(e.obl, Obligation{OID: oid, From: from, To: to, Amount: amount})
	return nil
}

// Close settles the current cycle atomically and starts the next one.
func (e *Engine) Close() CycleResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := e.settle()

	// Roll over to the next cycle: obligations are cleared, parties survive.
	e.cycle++
	e.oids = make(map[string]struct{})
	e.obl = nil

	return result
}

// CycleNo reports the number the next Close will settle.
func (e *Engine) CycleNo() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cycle
}

// settle runs the cascade revocation and settlement for the current cycle.
// Callers must hold e.mu.
func (e *Engine) settle() CycleResult {
	type entry struct {
		obl     Obligation
		revoked bool
	}
	entries := make([]*entry, len(e.obl))
	for i, obl := range e.obl {
		entries[i] = &entry{obl: obl}
	}

	defaulted := make(map[string]bool)
	var rounds [][]string
	var revoked []string

	for {
		net := make(map[string]int64)
		for _, en := range entries {
			if en.revoked {
				continue
			}
			net[en.obl.To] += en.obl.Amount
			net[en.obl.From] -= en.obl.Amount
		}

		var round []string
		for id, n := range net {
			if n < 0 && -n > e.parties[id] {
				round = append(round, id)
			}
		}
		sort.Strings(round)
		if len(round) == 0 {
			break
		}

		for _, id := range round {
			defaulted[id] = true
		}
		rounds = append(rounds, round)

		for _, en := range entries {
			if en.revoked {
				continue
			}
			if defaulted[en.obl.From] || defaulted[en.obl.To] {
				en.revoked = true
				revoked = append(revoked, en.obl.OID)
			}
		}
	}

	sort.Strings(revoked)

	// Final net positions over every registered party, including zero ones.
	finalNet := make(map[string]int64, len(e.parties))
	for _, en := range entries {
		if en.revoked {
			continue
		}
		finalNet[en.obl.To] += en.obl.Amount
		finalNet[en.obl.From] -= en.obl.Amount
	}

	ids := make([]string, 0, len(e.parties))
	for id := range e.parties {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	positions := make([]Position, 0, len(ids))
	for _, id := range ids {
		positions = append(positions, Position{ID: id, Net: finalNet[id]})
	}

	// Payer/payee queues: absolute amount descending, id ascending.
	var payerQ, payeeQ []Position
	for _, p := range positions {
		switch {
		case p.Net < 0:
			payerQ = append(payerQ, Position{ID: p.ID, Net: -p.Net})
		case p.Net > 0:
			payeeQ = append(payeeQ, p)
		}
	}
	byAmountThenID := func(a, b Position) bool {
		if a.Net != b.Net {
			return a.Net > b.Net
		}
		return a.ID < b.ID
	}
	sort.Slice(payerQ, func(i, j int) bool { return byAmountThenID(payerQ[i], payerQ[j]) })
	sort.Slice(payeeQ, func(i, j int) bool { return byAmountThenID(payeeQ[i], payeeQ[j]) })

	var instructions []Instruction
	pi, qi := 0, 0
	for pi < len(payerQ) && qi < len(payeeQ) {
		amount := payerQ[pi].Net
		if payeeQ[qi].Net < amount {
			amount = payeeQ[qi].Net
		}
		instructions = append(instructions, Instruction{
			Payer:  payerQ[pi].ID,
			Payee:  payeeQ[qi].ID,
			Amount: amount,
		})
		payerQ[pi].Net -= amount
		payeeQ[qi].Net -= amount
		if payerQ[pi].Net == 0 {
			pi++
		}
		if payeeQ[qi].Net == 0 {
			qi++
		}
	}

	return CycleResult{
		Cycle:        e.cycle,
		Defaulters:   rounds,
		Revoked:      revoked,
		Positions:    positions,
		Instructions: instructions,
	}
}
