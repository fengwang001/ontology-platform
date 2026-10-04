package creation

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"ontology/basket"
)

type naiveModel struct {
	t           *testing.T
	hold        map[string]map[string]int64
	fund        map[string]int64
	cash        map[string]int64
	units       map[string]int64
	locked      map[string]int64
	fundCash    int64
	usedToday   int64
	prices      map[string]int64
	pending     []naivePending
	debt        map[string]int64
	usedIDs     map[string]int64
	cashDiffV   int64
	maxRatioV   int64
	maxUnitsV   int64
	cashCredit  int64
	stockCredit map[string]int64
}

type naivePending struct {
	acct    string
	id      string
	sym     string
	deficit int64
	charged int64
}

type naiveOp struct {
	kind string
	now  int64
	acct string
	sym  string
	id   string
	n    int64
	p    int64
	amt  int64
}

func TestRandomNaiveModel(t *testing.T) {
	items := []Item{
		{Sym: "a", Qty: 2, Flag: basket.Forbidden},
		{Sym: "b", Qty: 3, Flag: basket.Allowed, Prem: 500},
		{Sym: "m", Qty: 1, Flag: basket.Mandatory, Fixed: 30},
	}
	for seed := int64(1); seed <= 1500; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cashDiff := int64(rng.Intn(21) - 10)
			maxRatio := int64(1 + rng.Intn(100))
			maxUnits := int64(1 + rng.Intn(5))
			p := mustProcessor(t, items, cashDiff, maxRatio, maxUnits)
			model := newNaiveModel(t)
			model.cashDiffV = cashDiff
			model.maxRatioV = maxRatio
			model.maxUnitsV = maxUnits
			now := int64(0)
			ids := map[string]bool{}
			ended := false
			for step := int64(0); step < 24; step++ {
				now++
				op := randomOp(rng, now, step, ids, ended)
				gotErr := runActual(p, op)
				wantErr, reason := model.run(op)
				t.Logf("input=%+v output=%v basis=%s", op, errorName(gotErr), reason)
				if !sameError(gotErr, wantErr) {
					t.Fatalf("seed=%d step=%d got=%v want=%v", seed, step, gotErr, wantErr)
				}
				if gotErr == nil && op.kind == "eod" {
					ended = true
				}
				model.assertMatches(p)
			}
		})
	}
}

func newNaiveModel(t *testing.T) *naiveModel {
	return &naiveModel{
		t:           t,
		hold:        map[string]map[string]int64{},
		fund:        map[string]int64{},
		cash:        map[string]int64{},
		units:       map[string]int64{},
		locked:      map[string]int64{},
		prices:      map[string]int64{},
		debt:        map[string]int64{},
		usedIDs:     map[string]int64{},
		stockCredit: map[string]int64{},
	}
}

func randomOp(rng *rand.Rand, now, step int64, ids map[string]bool, ended bool) naiveOp {
	acct := fmt.Sprintf("u%d", rng.Intn(2))
	op := naiveOp{now: now, acct: acct}
	if step < 4 {
		op.kind = []string{"price", "credit", "cash"}[rng.Intn(3)]
	} else {
		choices := []string{"credit", "cash", "price", "create", "redeem", "create", "redeem"}
		if !ended && rng.Intn(8) == 0 {
			choices = append(choices, "eod")
		}
		op.kind = choices[rng.Intn(len(choices))]
	}
	op.sym = []string{"a", "b", "m"}[rng.Intn(3)]
	if op.kind == "price" {
		op.sym = []string{"a", "b"}[rng.Intn(2)]
		op.p = int64(1 + rng.Intn(20))
	}
	op.n = int64(1 + rng.Intn(3))
	op.amt = int64(1 + rng.Intn(300))
	id := fmt.Sprintf("id-%d-%d", step, rng.Int63())
	if !ids[id] {
		op.id = id
	}
	return op
}

func runActual(p *Processor, op naiveOp) error {
	switch op.kind {
	case "credit":
		return p.Credit(op.now, op.acct, op.sym, op.amt)
	case "cash":
		return p.CreditCash(op.now, op.acct, op.amt)
	case "price":
		return p.SetPrice(op.now, op.sym, op.p)
	case "create":
		return p.Create(op.now, op.id, op.acct, op.n)
	case "redeem":
		return p.Redeem(op.now, op.acct, op.n)
	case "eod":
		_, err := p.EndOfDay(op.now)
		return err
	default:
		return ErrInvalidArgument
	}
}

func (m *naiveModel) run(op naiveOp) (error, string) {
	reason := "committed"
	switch op.kind {
	case "credit":
		m.ensure(op.acct)
		m.hold[op.acct][op.sym] += op.amt
		m.stockCredit[op.sym] += op.amt
	case "cash":
		m.ensure(op.acct)
		m.cash[op.acct] += op.amt
		m.cashCredit += op.amt
	case "price":
		m.prices[op.sym] = op.p
	case "create":
		return m.create(op)
	case "redeem":
		return m.redeem(op)
	case "eod":
		m.settle()
		m.usedToday = 0
		for acct := range m.locked {
			m.locked[acct] = 0
		}
	}
	return nil, reason
}

func (m *naiveModel) create(op naiveOp) (error, string) {
	if _, ok := m.cash[op.acct]; !ok {
		return ErrNotFound, "account absent"
	}
	if _, ok := m.usedIDs[op.id]; ok {
		return ErrDuplicateID, "duplicate id"
	}
	if _, ok := m.prices["a"]; !ok {
		return ErrNotFound, "missing price a"
	}
	if _, ok := m.prices["b"]; !ok {
		return ErrNotFound, "missing price b"
	}
	if m.usedToday+op.n > m.maxUnits() {
		return ErrDailyLimit, "daily quota"
	}
	qty := map[string]int64{"a": 2 * op.n, "b": 3 * op.n}
	if m.hold[op.acct]["a"] < qty["a"] {
		return ErrInsufficientSec, "N holding shortage a"
	}
	deliverB := minInt64(m.hold[op.acct]["b"], qty["b"])
	deficitB := qty["b"] - deliverB
	x := deficitB*m.prices["b"] + 30*op.n
	y := qty["a"]*m.prices["a"] + qty["b"]*m.prices["b"] + 30*op.n
	if x*100 > m.maxRatioV*y {
		return ErrRatioExceeded, fmt.Sprintf("%d*100 > %d*%d", x, m.maxRatioV, y)
	}
	charged := ceil64(deficitB*m.prices["b"]*10500, 10000)
	net := charged + 30*op.n + m.cashDiffV*op.n
	if net > 0 && m.cash[op.acct] < net {
		return ErrInsufficientCash, "cash net payable"
	}
	m.hold[op.acct]["a"] -= qty["a"]
	m.hold[op.acct]["b"] -= deliverB
	m.fund["a"] += qty["a"]
	m.fund["b"] += deliverB
	m.cash[op.acct] -= net
	m.fundCash += net
	m.units[op.acct] += op.n
	m.locked[op.acct] += op.n
	m.usedToday += op.n
	m.usedIDs[op.id] = op.now
	if deficitB > 0 {
		m.pending = append(m.pending, naivePending{op.acct, op.id, "b", deficitB, charged})
	}
	return nil, fmt.Sprintf("deliver a=%d b=%d deficit=%d charged=%d net=%d", qty["a"], deliverB, deficitB, charged, net)
}

func (m *naiveModel) redeem(op naiveOp) (error, string) {
	if _, ok := m.cash[op.acct]; !ok {
		return ErrNotFound, "account absent"
	}
	if m.units[op.acct]-m.locked[op.acct] < op.n {
		return ErrInsufficientUnit, "redeemable units"
	}
	if _, ok := m.prices["a"]; !ok {
		return ErrNotFound, "missing price a"
	}
	if _, ok := m.prices["b"]; !ok {
		return ErrNotFound, "missing price b"
	}
	needA := 2 * op.n
	if m.fund["a"] < needA {
		return ErrInsufficientInv, "fund inventory a"
	}
	needB := 3 * op.n
	deliverB := minInt64(m.fund["b"], needB)
	deficitB := needB - deliverB
	payB := deficitB * m.prices["b"] * 9500 / 10000
	net := payB + 30*op.n + m.cashDiffV*op.n
	if net < 0 && m.cash[op.acct] < -net {
		return ErrInsufficientCash, "cash negative net income"
	}
	m.fund["a"] -= needA
	m.fund["b"] -= deliverB
	m.hold[op.acct]["a"] += needA
	m.hold[op.acct]["b"] += deliverB
	m.cash[op.acct] += net
	m.fundCash -= net
	m.units[op.acct] -= op.n
	return nil, fmt.Sprintf("deliver a=%d b=%d deficit=%d payB=%d net=%d", needA, deliverB, deficitB, payB, net)
}

func (m *naiveModel) settle() {
	for _, pending := range m.pending {
		price := m.prices[pending.sym]
		cost := pending.deficit * price
		difference := pending.charged - cost
		if difference >= 0 {
			m.cash[pending.acct] += difference
			m.fundCash -= difference
		} else {
			due := -difference
			paid := minInt64(m.cash[pending.acct], due)
			m.cash[pending.acct] -= paid
			m.fundCash += due
			m.debt[pending.acct] += due - paid
		}
	}
	m.pending = nil
}

func (m *naiveModel) ensure(acct string) {
	if _, ok := m.hold[acct]; !ok {
		m.hold[acct] = map[string]int64{}
		m.cash[acct] = 0
		m.units[acct] = 0
		m.locked[acct] = 0
	}
}

func (m *naiveModel) maxUnits() int64 { return m.maxUnitsV }

func (m *naiveModel) assertMatches(p *Processor) {
	for acct := range m.cash {
		if p.Cash(acct).Cmp(big.NewInt(m.cash[acct])) != 0 {
			m.t.Fatalf("cash %s: %d != %s", acct, m.cash[acct], p.Cash(acct))
		}
		if p.Units(acct).Cmp(big.NewInt(m.units[acct])) != 0 {
			m.t.Fatalf("units %s mismatch", acct)
		}
		for sym, value := range m.hold[acct] {
			if p.Holding(acct, sym).Int64() != value {
				m.t.Fatalf("holding %s/%s mismatch", acct, sym)
			}
		}
		if p.Debt(acct).Int64() != m.debt[acct] {
			m.t.Fatalf("debt %s mismatch", acct)
		}
	}
	for sym, value := range m.fund {
		if p.FundHolding(sym).Int64() != value {
			m.t.Fatalf("fund %s mismatch", sym)
		}
	}
	var accountStock map[string]int64
	accountStock = map[string]int64{}
	for _, holdings := range m.hold {
		for sym, amount := range holdings {
			accountStock[sym] += amount
		}
	}
	for sym := range m.stockCredit {
		if accountStock[sym]+p.FundHolding(sym).Int64() != m.stockCredit[sym] {
			m.t.Fatalf("stock conservation %s mismatch", sym)
		}
	}
	var accountCash, debtTotal, unitTotal int64
	for acct := range m.cash {
		accountCash += m.cash[acct]
		debtTotal += m.debt[acct]
		unitTotal += m.units[acct]
	}
	if unitTotal < 0 {
		m.t.Fatalf("unit total negative: %d", unitTotal)
	}
	if got := accountCash + p.FundCash().Int64() + debtTotal; got != m.cashCredit {
		m.t.Fatalf("cash conservation accounts=%d fund=%s debt=%d total=%d credit=%d", accountCash, p.FundCash(), debtTotal, got, m.cashCredit)
	}
	if p.FundCash().Int64() != m.fundCash {
		m.t.Fatalf("fund cash %s != %d", p.FundCash(), m.fundCash)
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func ceil64(value, divisor int64) int64 {
	return (value + divisor - 1) / divisor
}

func sameError(got, want error) bool {
	return errors.Is(got, want)
}

func errorName(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
