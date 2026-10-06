package billing

// This file holds an independently written naive model of the billing
// semantics, used to differential-test the real Service. The model is
// deliberately naive: it accrues late fees day by day instead of in
// closed form, and it scans every bill ever generated (including closed
// ones) for payment allocation and queries, instead of keeping a
// maintained open-bill list.

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveBill struct {
	seq            int64
	id             string
	period         string
	due            int64
	principal      int64
	principalPaid  int64
	lateFee        int64
	latePaid       int64
	lateWaived     int64
	frac           int64
	accruedThrough int64
	stage          int
	closed         bool
	disputed       bool
	disputeStart   int64
	disputedDays   int64
	everDisputed   bool
	ruled          bool
}

func (nb *naiveBill) unpaidP() int64 { return nb.principal - nb.principalPaid }
func (nb *naiveBill) unpaidL() int64 { return nb.lateFee - nb.latePaid - nb.lateWaived }

// accrueTo walks day by day, carrying the sub-unit fraction.
func (nb *naiveBill) accrueTo(cfg Config, t int64) {
	for d := nb.accruedThrough + 1; d <= t; d++ {
		if nb.closed || nb.disputed {
			return
		}
		nb.accruedThrough = d
		unpaid := nb.unpaidP()
		if unpaid <= 0 || cfg.RateNum == 0 {
			continue
		}
		raw := nb.frac + unpaid*cfg.RateNum
		add := raw / cfg.RateDen
		nb.frac = raw % cfg.RateDen
		cap := nb.principal * cfg.CapNum / cfg.CapDen
		if room := cap - nb.lateFee; add >= room {
			if room > 0 {
				nb.lateFee += room
			}
			nb.frac = 0
		} else {
			nb.lateFee += add
		}
	}
}

// projectedLateFee simulates day-by-day accrual on a scratch copy.
func (nb *naiveBill) projectedLateFee(cfg Config, t int64) int64 {
	late, frac := nb.lateFee, nb.frac
	if nb.closed || nb.disputed {
		return late
	}
	for d := nb.accruedThrough + 1; d <= t; d++ {
		unpaid := nb.unpaidP()
		if unpaid <= 0 || cfg.RateNum == 0 {
			continue
		}
		raw := frac + unpaid*cfg.RateNum
		add := raw / cfg.RateDen
		frac = raw % cfg.RateDen
		cap := nb.principal * cfg.CapNum / cfg.CapDen
		if cap < late {
			cap = late
		}
		if late+add >= cap {
			late = cap
			frac = 0
		} else {
			late += add
		}
	}
	return late
}

func (nb *naiveBill) advanceStage(cfg Config, now int64) {
	if nb.closed || nb.disputed {
		return
	}
	overdue := now - nb.due - nb.disputedDays
	if overdue < 0 {
		overdue = 0
	}
	for i, th := range cfg.StageThresholds {
		if overdue >= th && i+1 > nb.stage {
			nb.stage = i + 1
		}
	}
}

type naiveHousehold struct {
	bills     []*naiveBill // every bill ever generated, generation order
	prepay    int64
	totalPaid int64
	allocP    int64
	allocL    int64
}

type naiveModel struct {
	cfg        Config
	now        int64
	seq        int64
	households map[string]*naiveHousehold
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, now: -1, households: map[string]*naiveHousehold{}}
}

func (m *naiveModel) checkClock(now int64) *Error {
	if now < 0 {
		return paramErr("now must be >= 0, got %d", now)
	}
	if now < m.now {
		return clockErr("now %d is before last accepted now %d", now, m.now)
	}
	return nil
}

func (m *naiveModel) household(id string) (*naiveHousehold, *Error) {
	h, ok := m.households[id]
	if !ok {
		return nil, notFoundErr("household %q not found", id)
	}
	return h, nil
}

func (m *naiveModel) findBill(h *naiveHousehold, id string) (*naiveBill, *Error) {
	for _, b := range h.bills {
		if b.id == id {
			return b, nil
		}
	}
	return nil, notFoundErr("bill %q not found", id)
}

func (m *naiveModel) addHousehold(id string, now int64) error {
	if id == "" {
		return paramErr("household id is empty")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.households[id]; ok {
		return stateErr("household %q already exists", id)
	}
	m.now = now
	m.households[id] = &naiveHousehold{}
	return nil
}

func (m *naiveModel) generateBill(hid, period string, principal, due, now int64) (string, error) {
	if hid == "" || period == "" {
		return "", paramErr("household id and period must be non-empty")
	}
	if principal <= 0 {
		return "", paramErr("principal must be positive, got %d", principal)
	}
	if due < 0 {
		return "", paramErr("due day must be >= 0, got %d", due)
	}
	if err := m.checkClock(now); err != nil {
		return "", err
	}
	h, herr := m.household(hid)
	if herr != nil {
		return "", herr
	}
	m.now = now
	m.seq++
	b := &naiveBill{
		seq:            m.seq,
		id:             fmt.Sprintf("B%06d", m.seq),
		period:         period,
		due:            due,
		principal:      principal,
		accruedThrough: due + m.cfg.GraceDays,
	}
	if h.prepay > 0 {
		x := min(h.prepay, principal)
		b.principalPaid = x
		h.prepay -= x
		h.allocP += x
	}
	if b.unpaidP() == 0 {
		b.closed = true
	} else {
		b.advanceStage(m.cfg, now)
	}
	h.bills = append(h.bills, b)
	return b.id, nil
}

func (m *naiveModel) pay(hid string, amount, now int64) (*Receipt, error) {
	if hid == "" {
		return nil, paramErr("household id is empty")
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	h, herr := m.household(hid)
	if herr != nil {
		return nil, herr
	}
	if amount <= 0 {
		return nil, amountErr("payment amount must be positive, got %d", amount)
	}
	m.now = now
	// Naive: accrue every bill ever generated (closed ones no-op).
	for _, b := range h.bills {
		b.accrueTo(m.cfg, now)
		b.advanceStage(m.cfg, now)
	}
	remaining := amount
	receipt := &Receipt{Amount: amount}
	skipped := map[*naiveBill]bool{}
	for remaining > 0 {
		// Naive: full scan for the earliest-due candidate each round.
		var target *naiveBill
		for _, b := range h.bills {
			if b.closed || b.disputed || skipped[b] {
				continue
			}
			if target == nil || b.due < target.due || (b.due == target.due && b.seq < target.seq) {
				target = b
			}
		}
		if target == nil {
			break
		}
		if target.stage == StageFinal && remaining < target.unpaidL()+target.unpaidP() {
			skipped[target] = true
			continue
		}
		alloc := Allocation{BillID: target.id}
		if x := min(remaining, target.unpaidL()); x > 0 {
			target.latePaid += x
			alloc.LateFee = x
			remaining -= x
		}
		if x := min(remaining, target.unpaidP()); x > 0 {
			target.principalPaid += x
			alloc.Principal = x
			remaining -= x
		}
		h.allocL += alloc.LateFee
		h.allocP += alloc.Principal
		receipt.Allocations = append(receipt.Allocations, alloc)
		if target.unpaidP() == 0 && target.unpaidL() == 0 {
			target.closed = true
		}
	}
	if remaining > 0 {
		h.prepay += remaining
		receipt.PrepayAdded = remaining
	}
	h.totalPaid += amount
	return receipt, nil
}

func (m *naiveModel) dispute(hid, bid string, now int64) error {
	if hid == "" || bid == "" {
		return paramErr("household id and bill id must be non-empty")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	h, herr := m.household(hid)
	if herr != nil {
		return herr
	}
	b, berr := m.findBill(h, bid)
	if berr != nil {
		return berr
	}
	if b.closed {
		return stateErr("bill %q is closed", bid)
	}
	if b.everDisputed {
		return stateErr("bill %q was already disputed once", bid)
	}
	m.now = now
	b.accrueTo(m.cfg, now-1)
	b.disputed = true
	b.disputeStart = now
	b.everDisputed = true
	return nil
}

func (m *naiveModel) resolve(hid, bid string, now int64, reduce bool, newPrincipal int64) error {
	if hid == "" || bid == "" {
		return paramErr("household id and bill id must be non-empty")
	}
	if reduce && newPrincipal < 0 {
		return paramErr("reduced principal must be >= 0, got %d", newPrincipal)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	h, herr := m.household(hid)
	if herr != nil {
		return herr
	}
	b, berr := m.findBill(h, bid)
	if berr != nil {
		return berr
	}
	if reduce && newPrincipal >= b.principal {
		return paramErr("reduced principal %d is not below current principal %d", newPrincipal, b.principal)
	}
	if !b.disputed {
		return stateErr("bill %q has no open dispute", bid)
	}
	m.now = now
	b.disputed = false
	b.ruled = true
	b.disputedDays += now - b.disputeStart
	if reduce {
		b.principal = newPrincipal
		if b.principalPaid > b.principal {
			excess := b.principalPaid - b.principal
			b.principalPaid = b.principal
			h.allocP -= excess
			h.prepay += excess
		}
	}
	if r := now - 1; r > b.accruedThrough {
		b.accruedThrough = r
	}
	b.advanceStage(m.cfg, now)
	if !b.closed && b.unpaidP() == 0 && b.unpaidL() == 0 {
		b.closed = true
	}
	return nil
}

func (m *naiveModel) waive(hid, bid string, amount, now int64) error {
	if hid == "" || bid == "" {
		return paramErr("household id and bill id must be non-empty")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	h, herr := m.household(hid)
	if herr != nil {
		return herr
	}
	b, berr := m.findBill(h, bid)
	if berr != nil {
		return berr
	}
	if b.closed {
		return stateErr("bill %q is closed", bid)
	}
	unpaid := b.projectedLateFee(m.cfg, now) - b.latePaid - b.lateWaived
	if amount <= 0 || amount > unpaid {
		return amountErr("waiver %d out of range (1..%d)", amount, unpaid)
	}
	m.now = now
	b.accrueTo(m.cfg, now)
	b.lateWaived += amount
	b.advanceStage(m.cfg, now)
	if !b.closed && b.unpaidP() == 0 && b.unpaidL() == 0 {
		b.closed = true
	}
	return nil
}

// totalDue scans every historical bill (naive O(history)).
func (m *naiveModel) totalDue(hid string, day int64) (int64, error) {
	if hid == "" {
		return 0, paramErr("household id is empty")
	}
	if day < 0 {
		return 0, paramErr("day must be >= 0, got %d", day)
	}
	h, herr := m.household(hid)
	if herr != nil {
		return 0, herr
	}
	if day < m.now {
		return 0, paramErr("query day %d is before current now %d", day, m.now)
	}
	var total int64
	for _, b := range h.bills {
		if b.closed {
			continue
		}
		total += b.unpaidP()
		total += b.projectedLateFee(m.cfg, day) - b.latePaid - b.lateWaived
	}
	return total, nil
}

func (m *naiveModel) householdView(id string) HouseholdView {
	h := m.households[id]
	v := HouseholdView{ID: id, Prepay: h.prepay, TotalPaid: h.totalPaid, AllocatedPrincipal: h.allocP, AllocatedLateFee: h.allocL}
	for _, b := range h.bills {
		if b.closed {
			v.ClosedBills++
		} else {
			v.OpenBills++
		}
	}
	return v
}

func (m *naiveModel) billView(id, bid string) BillView {
	h := m.households[id]
	for _, b := range h.bills {
		if b.id == bid {
			return BillView{
				ID:            b.id,
				Period:        b.period,
				DueDay:        b.due,
				Principal:     b.principal,
				PrincipalPaid: b.principalPaid,
				LateFee:       b.lateFee,
				LateFeePaid:   b.latePaid,
				LateFeeWaived: b.lateWaived,
				Stage:         b.stage,
				Closed:        b.closed,
				Disputed:      b.disputed,
				Ruled:         b.ruled,
				DisputedDays:  b.disputedDays,
			}
		}
	}
	panic("model: unknown bill " + bid)
}

// --------------------------------------------------------------------
// Randomized differential test: real Service vs. the naive model.
// --------------------------------------------------------------------

// testOp is one randomized operation in a replayable script.
type testOp struct {
	kind         string // addhh, gen, pay, dispute, resolve, waive, query
	h            string
	bill         string
	period       string
	principal    int64
	due          int64
	amount       int64
	reduce       bool
	newPrincipal int64
	day          int64
	now          int64
}

func (op testOp) String() string {
	switch op.kind {
	case "gen":
		return fmt.Sprintf("gen h=%s period=%s principal=%d due=%d now=%d", op.h, op.period, op.principal, op.due, op.now)
	case "pay":
		return fmt.Sprintf("pay h=%s amount=%d now=%d", op.h, op.amount, op.now)
	case "dispute":
		return fmt.Sprintf("dispute h=%s bill=%s now=%d", op.h, op.bill, op.now)
	case "resolve":
		return fmt.Sprintf("resolve h=%s bill=%s reduce=%v newPrincipal=%d now=%d", op.h, op.bill, op.reduce, op.newPrincipal, op.now)
	case "waive":
		return fmt.Sprintf("waive h=%s bill=%s amount=%d now=%d", op.h, op.bill, op.amount, op.now)
	case "query":
		return fmt.Sprintf("query h=%s day=%d", op.h, op.day)
	default:
		return fmt.Sprintf("%s h=%s now=%d", op.kind, op.h, op.now)
	}
}

func applyReal(s *Service, op testOp) (any, error) {
	switch op.kind {
	case "addhh":
		return nil, s.AddHousehold(op.h, op.now)
	case "gen":
		return s.GenerateBill(op.h, op.period, op.principal, op.due, op.now)
	case "pay":
		return s.Pay(op.h, op.amount, op.now)
	case "dispute":
		return nil, s.Dispute(op.h, op.bill, op.now)
	case "resolve":
		return nil, s.ResolveDispute(op.h, op.bill, op.now, op.reduce, op.newPrincipal)
	case "waive":
		return nil, s.Waive(op.h, op.bill, op.amount, op.now)
	case "query":
		return s.TotalDue(op.h, op.day)
	}
	panic("bad op " + op.kind)
}

func applyModel(m *naiveModel, op testOp) (any, error) {
	switch op.kind {
	case "addhh":
		return nil, m.addHousehold(op.h, op.now)
	case "gen":
		return m.generateBill(op.h, op.period, op.principal, op.due, op.now)
	case "pay":
		return m.pay(op.h, op.amount, op.now)
	case "dispute":
		return nil, m.dispute(op.h, op.bill, op.now)
	case "resolve":
		return nil, m.resolve(op.h, op.bill, op.now, op.reduce, op.newPrincipal)
	case "waive":
		return nil, m.waive(op.h, op.bill, op.amount, op.now)
	case "query":
		return m.totalDue(op.h, op.day)
	}
	panic("bad op " + op.kind)
}

func randomCfg(rng *rand.Rand) Config {
	var th [3]int64
	th[0] = 1 + rng.Int63n(5)
	th[1] = th[0] + 1 + rng.Int63n(6)
	th[2] = th[1] + 1 + rng.Int63n(8)
	return Config{
		GraceDays:       rng.Int63n(4),
		RateNum:         1 + rng.Int63n(5),
		RateDen:         []int64{10, 100, 1000}[rng.Intn(3)],
		CapNum:          1 + rng.Int63n(3),
		CapDen:          2 + rng.Int63n(9),
		StageThresholds: th,
	}
}

// genScript builds a reproducible random operation script. A probe run
// over the naive model tells the generator which bills actually exist
// and which disputes are open, so the script exercises both success and
// rejection paths: duplicate households, unknown ids, zero/overflow
// amounts, clock rollbacks and repeated disputes/rulings.
func genScript(rng *rand.Rand, steps int, cfg Config) ([]testOp, []string) {
	probe := newNaiveModel(cfg)
	households := []string{"H0", "H1", "H2"}
	var ops []testOp
	for _, h := range households {
		op := testOp{kind: "addhh", h: h, now: 0}
		ops = append(ops, op)
		_ = probe.addHousehold(h, 0)
	}
	bills := map[string][]string{}
	var openDisputes [][2]string
	now := int64(0)
	nextBill := 0
	pickH := func() string {
		if rng.Intn(50) == 0 {
			return "ghost"
		}
		return households[rng.Intn(len(households))]
	}
	// pickBill returns an existing bill 4 times out of 5.
	pickBill := func(h string) string {
		if rng.Intn(5) == 0 || len(bills[h]) == 0 {
			return "nope"
		}
		return bills[h][rng.Intn(len(bills[h]))]
	}
	// pickDisputable returns an open, never-disputed bill, if any.
	pickDisputable := func() (string, string, bool) {
		var cands [][2]string
		for _, h := range households {
			for _, b := range probe.households[h].bills {
				if !b.closed && !b.everDisputed {
					cands = append(cands, [2]string{h, b.id})
				}
			}
		}
		if len(cands) == 0 {
			return "", "", false
		}
		c := cands[rng.Intn(len(cands))]
		return c[0], c[1], true
	}
	// pickWaivable returns an open bill with waivable late fee at now.
	pickWaivable := func() (string, string, int64, bool) {
		var cands [][2]string
		var unpaid []int64
		for _, h := range households {
			for _, b := range probe.households[h].bills {
				if b.closed {
					continue
				}
				u := b.projectedLateFee(cfg, now) - b.latePaid - b.lateWaived
				if u > 0 {
					cands = append(cands, [2]string{h, b.id})
					unpaid = append(unpaid, u)
				}
			}
		}
		if len(cands) == 0 {
			return "", "", 0, false
		}
		i := rng.Intn(len(cands))
		return cands[i][0], cands[i][1], unpaid[i], true
	}
	for i := 0; i < steps; i++ {
		// Clock: usually advance a little, sometimes stay, rarely roll back.
		switch r := rng.Intn(100); {
		case r < 70:
			now += rng.Int63n(3)
		case r < 95:
			// stay
		default:
			if now > 0 {
				now-- // rollback attempt: ops are rejected until now catches up
			}
		}
		op := testOp{now: now}
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24:
			due := now + rng.Int63n(20) - 5
			if due < 0 {
				due = 0
			}
			op.kind = "gen"
			op.h = households[rng.Intn(len(households))]
			op.period = fmt.Sprintf("P%d", nextBill)
			op.principal = 1 + rng.Int63n(5000)
			op.due = due
			nextBill++
		case 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49:
			op.kind = "pay"
			op.h = pickH()
			if rng.Intn(20) == 0 {
				op.amount = 0 // amount-out-of-range probe
			} else {
				op.amount = 1 + rng.Int63n(9000)
			}
		case 50, 51, 52, 53, 54, 55, 56, 57, 58, 59:
			op.kind = "dispute"
			if h, b, ok := pickDisputable(); ok && rng.Intn(4) > 0 {
				op.h, op.bill = h, b
			} else {
				op.h = pickH()
				op.bill = pickBill(op.h)
			}
		case 60, 61, 62, 63, 64, 65, 66, 67, 68, 69:
			op.kind = "resolve"
			if len(openDisputes) > 0 && rng.Intn(4) > 0 {
				d := openDisputes[rng.Intn(len(openDisputes))]
				op.h, op.bill = d[0], d[1]
			} else {
				op.h = pickH()
				op.bill = pickBill(op.h)
			}
			op.reduce = rng.Intn(2) == 0
			op.newPrincipal = rng.Int63n(6000) // may be >= principal: param error
		case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79:
			op.kind = "waive"
			if h, b, unpaid, ok := pickWaivable(); ok && rng.Intn(4) > 0 {
				op.h, op.bill = h, b
				if rng.Intn(2) == 0 {
					op.amount = 1 + rng.Int63n(unpaid) // within bounds: succeeds
				} else {
					op.amount = unpaid + 1 + rng.Int63n(2000) // out of bounds
				}
			} else {
				op.h = pickH()
				op.bill = pickBill(op.h)
				op.amount = rng.Int63n(400)
			}
		case 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94:
			op.kind = "query"
			op.h = pickH()
			op.day = now + rng.Int63n(5)
		default:
			op.kind = "addhh"
			op.h = households[rng.Intn(len(households))] // duplicate: state error
		}
		ops = append(ops, op)
		// Track the probe outcome so later ops aim at real state.
		res, err := applyModel(probe, op)
		if err != nil {
			continue
		}
		switch op.kind {
		case "gen":
			bills[op.h] = append(bills[op.h], res.(string))
		case "dispute":
			openDisputes = append(openDisputes, [2]string{op.h, op.bill})
		case "resolve":
			for j, d := range openDisputes {
				if d == [2]string{op.h, op.bill} {
					openDisputes = append(openDisputes[:j], openDisputes[j+1:]...)
					break
				}
			}
		}
	}
	return ops, households
}

// compareAll cross-checks every household and bill view and the
// conservation invariant after a batch of operations.
func compareAll(t *testing.T, s *Service, m *naiveModel, households []string, bills map[string][]string) {
	t.Helper()
	for _, h := range households {
		hv, err := s.HouseholdView(h)
		must(t, err)
		if mv := m.householdView(h); hv != mv {
			t.Fatalf("household %s view mismatch:\nreal  %+v\nmodel %+v", h, hv, mv)
		}
		if hv.TotalPaid != hv.AllocatedPrincipal+hv.AllocatedLateFee+hv.Prepay {
			t.Fatalf("conservation violated for %s: %+v", h, hv)
		}
		for _, b := range bills[h] {
			bv, err := s.BillView(h, b)
			must(t, err)
			if mv := m.billView(h, b); bv != mv {
				t.Fatalf("bill %s of %s mismatch:\nreal  %+v\nmodel %+v", b, h, bv, mv)
			}
		}
	}
}

func runDifferential(t *testing.T, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	cfg := randomCfg(rng)
	ops, households := genScript(rng, steps, cfg)

	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	m := newNaiveModel(cfg)
	bills := map[string][]string{}
	billSeq := int64(0)

	for i, op := range ops {
		realRes, realErr := applyReal(s, op)
		modelRes, modelErr := applyModel(m, op)
		realCode, modelCode := codeOf(t, realErr), codeOf(t, modelErr)
		if realCode != modelCode {
			t.Fatalf("step %d (%s): error code mismatch real=%v model=%v", i, op, realCode, modelCode)
		}
		outcome := "ok"
		if realErr != nil {
			outcome = "rejected: " + realCode.String()
		}
		if realErr == nil {
			switch op.kind {
			case "pay":
				if !reflect.DeepEqual(realRes, modelRes) {
					t.Fatalf("step %d (%s): receipt mismatch:\nreal  %+v\nmodel %+v", i, op, realRes, modelRes)
				}
				outcome = fmt.Sprintf("receipt=%+v", realRes)
			case "gen":
				if realRes != modelRes {
					t.Fatalf("step %d (%s): bill id mismatch real=%v model=%v", i, op, realRes, modelRes)
				}
				billSeq++
				bills[op.h] = append(bills[op.h], realRes.(string))
				outcome = fmt.Sprintf("bill=%s", realRes)
			case "query":
				if realRes != modelRes {
					t.Fatalf("step %d (%s): total due mismatch real=%v model=%v", i, op, realRes, modelRes)
				}
				outcome = fmt.Sprintf("due=%v", realRes)
			}
		}
		t.Logf("step=%04d op=%-45s -> %s", i, op.String(), outcome)
		if i%50 == 49 {
			compareAll(t, s, m, households, bills)
			t.Logf("step=%04d state checkpoint: all household/bill views match, conservation holds", i)
		}
	}
	compareAll(t, s, m, households, bills)
}

func TestDifferentialRandom(t *testing.T) {
	for _, seed := range []int64{1495, 7, 20261006} {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed, 1500)
		})
	}
}

// The same operation sequence replayed on two fresh services must yield
// identical bill states and allocation details.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	cfg := randomCfg(rng)
	ops, households := genScript(rng, 1200, cfg)

	run := func() map[string]any {
		s, err := NewService(cfg)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		bills := map[string][]string{}
		var receipts []*Receipt
		for _, op := range ops {
			res, err := applyReal(s, op)
			if err == nil && op.kind == "gen" {
				bills[op.h] = append(bills[op.h], res.(string))
			}
			if err == nil && op.kind == "pay" {
				receipts = append(receipts, res.(*Receipt))
			}
		}
		state := map[string]any{"receipts": receipts}
		for _, h := range households {
			hv, err := s.HouseholdView(h)
			must(t, err)
			state["hh/"+h] = hv
			ids := append([]string(nil), bills[h]...)
			sort.Strings(ids)
			for _, b := range ids {
				bv, err := s.BillView(h, b)
				must(t, err)
				state["bill/"+b] = bv
			}
		}
		return state
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("replaying the same operation sequence produced different states")
	}
}
