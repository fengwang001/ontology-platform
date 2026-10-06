package billing

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naiveModel is an independently written reference implementation. It uses
// exact rational arithmetic (big.Rat), a day-by-day accrual loop, and scans
// all bills (never indexing open ones), so implementation share with the
// service under test is minimal.
type naiveModel struct {
	cfg     Config
	lastNow int
	seq     int
	hhs     map[string]*naiveHH
}

type naiveHH struct {
	bills     map[string]*naiveBill
	prepay    int64
	totalPaid int64
}

type naiveBill struct {
	id           string
	due          int
	principal    int64
	paidP        int64
	paidL        int64
	waived       int64
	accrued      *big.Rat
	lastDay      int
	disputed     bool
	everDisputed bool
	dispDay      int
	dispDays     int
	stage        int
	closed       bool
	seq          int
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, lastNow: -1, hhs: map[string]*naiveHH{}}
}

func (b *naiveBill) unpaidP() int64 {
	if d := b.principal - b.paidP; d > 0 {
		return d
	}
	return 0
}

func (m *naiveModel) capRat(principal int64) *big.Rat {
	return new(big.Rat).Mul(
		big.NewRat(m.cfg.CapNum, m.cfg.CapDen),
		new(big.Rat).SetInt64(principal))
}

// accrueTo advances accrual day by day up to and including t.
func (m *naiveModel) accrueTo(b *naiveBill, t int) {
	if b.closed || b.disputed {
		return
	}
	capR := m.capRat(b.principal)
	rate := big.NewRat(m.cfg.RateNum, m.cfg.RateDen)
	for d := b.lastDay + 1; d <= t; d++ {
		if b.accrued.Cmp(capR) >= 0 {
			break
		}
		daily := new(big.Rat).Mul(rate, new(big.Rat).SetInt64(b.unpaidP()))
		b.accrued.Add(b.accrued, daily)
		if b.accrued.Cmp(capR) > 0 {
			b.accrued.Set(capR)
		}
	}
	if t > b.lastDay {
		b.lastDay = t
	}
}

func (b *naiveBill) accruedInt() int64 {
	return new(big.Int).Quo(b.accrued.Num(), b.accrued.Denom()).Int64()
}

func (b *naiveBill) unpaidLate() int64 { return b.accruedInt() - b.paidL - b.waived }

func (m *naiveModel) stageAt(b *naiveBill, now int) int {
	if b.closed {
		return b.stage
	}
	eff := now
	if b.disputed {
		eff = b.dispDay
	}
	overdue := eff - b.due - b.dispDays
	s := 0
	for i, t := range m.cfg.Thresholds {
		if overdue >= t {
			s = i + 1
		}
	}
	if s < b.stage {
		s = b.stage
	}
	return s
}

func (m *naiveModel) closeIfSettled(b *naiveBill) {
	if !b.closed && b.unpaidP() == 0 && b.unpaidLate() == 0 {
		b.closed = true
	}
}

func (m *naiveModel) clock(now int) error {
	if now < m.lastNow {
		return newError(ErrClockRollback, "now=%d before %d", now, m.lastNow)
	}
	return nil
}

func (m *naiveModel) household(id string) (*naiveHH, error) {
	h, ok := m.hhs[id]
	if !ok {
		return nil, newError(ErrNotFound, "household %q", id)
	}
	return h, nil
}

func (m *naiveModel) bill(h *naiveHH, id string) (*naiveBill, error) {
	b, ok := h.bills[id]
	if !ok {
		return nil, newError(ErrNotFound, "bill %q", id)
	}
	return b, nil
}

// sortedBills returns all bills (open and closed) in application order.
func (m *naiveModel) sortedBills(h *naiveHH) []*naiveBill {
	out := make([]*naiveBill, 0, len(h.bills))
	for _, b := range h.bills {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].due != out[j].due {
			return out[i].due < out[j].due
		}
		return out[i].seq < out[j].seq
	})
	return out
}

// applyOne applies up to amount to a single bill (late first, then
// principal; stage-3 all-or-nothing). Returns the applied amount.
func (m *naiveModel) applyOne(b *naiveBill, amount int64) (int64, Application) {
	app := Application{BillID: b.id}
	outstanding := b.unpaidLate() + b.unpaidP()
	if outstanding == 0 {
		return 0, app
	}
	pay := amount
	if b.stage == 3 {
		if amount < outstanding {
			return 0, app
		}
		pay = outstanding
	}
	if pay > outstanding {
		pay = outstanding
	}
	app.ToLate = min(pay, b.unpaidLate())
	app.ToPrincipal = pay - app.ToLate
	b.paidL += app.ToLate
	b.paidP += app.ToPrincipal
	m.closeIfSettled(b)
	return pay, app
}

func (m *naiveModel) generate(now int, hhID, billID string, principal int64, due int) error {
	if hhID == "" || billID == "" || principal <= 0 || due < 0 || now < 0 {
		return newError(ErrInvalidParam, "generate")
	}
	if err := m.clock(now); err != nil {
		return err
	}
	h := m.hhs[hhID]
	if h == nil {
		h = &naiveHH{bills: map[string]*naiveBill{}}
		m.hhs[hhID] = h
	}
	if _, dup := h.bills[billID]; dup {
		return newError(ErrInvalidState, "duplicate bill %q", billID)
	}
	b := &naiveBill{
		id: billID, due: due, principal: principal,
		accrued: new(big.Rat), lastDay: due + m.cfg.GraceDays, seq: m.seq,
	}
	m.seq++
	m.accrueTo(b, now)
	b.stage = m.stageAt(b, now)
	h.bills[billID] = b
	if h.prepay > 0 && !b.closed {
		applied, _ := m.applyOne(b, h.prepay)
		h.prepay -= applied
	}
	m.lastNow = now
	return nil
}

func (m *naiveModel) pay(now int, hhID string, amount int64) (PaymentResult, error) {
	if hhID == "" || amount < 0 || now < 0 {
		return PaymentResult{}, newError(ErrInvalidParam, "pay")
	}
	if err := m.clock(now); err != nil {
		return PaymentResult{}, err
	}
	h, err := m.household(hhID)
	if err != nil {
		return PaymentResult{}, err
	}
	if amount == 0 {
		return PaymentResult{}, newError(ErrAmount, "zero payment")
	}
	bills := m.sortedBills(h)
	for _, b := range bills {
		m.accrueTo(b, now)
		b.stage = m.stageAt(b, now)
	}
	res := PaymentResult{}
	remaining := amount
	for _, b := range bills {
		if remaining == 0 {
			break
		}
		if b.closed || b.disputed {
			continue
		}
		applied, app := m.applyOne(b, remaining)
		if applied > 0 {
			res.Applications = append(res.Applications, app)
			remaining -= applied
		}
	}
	res.Prepaid = remaining
	h.prepay += remaining
	h.totalPaid += amount
	m.lastNow = now
	return res, nil
}

func (m *naiveModel) dispute(now int, hhID, billID string) error {
	if hhID == "" || billID == "" || now < 0 {
		return newError(ErrInvalidParam, "dispute")
	}
	if err := m.clock(now); err != nil {
		return err
	}
	h, err := m.household(hhID)
	if err != nil {
		return err
	}
	b, err := m.bill(h, billID)
	if err != nil {
		return err
	}
	if b.closed {
		return newError(ErrInvalidState, "closed")
	}
	if b.everDisputed {
		return newError(ErrInvalidState, "already disputed")
	}
	m.accrueTo(b, now-1)
	b.disputed = true
	b.everDisputed = true
	b.dispDay = now
	b.stage = m.stageAt(b, now)
	m.lastNow = now
	return nil
}

func (m *naiveModel) resolve(now int, hhID, billID string, newPrincipal int64) error {
	if hhID == "" || billID == "" || newPrincipal < 0 || now < 0 {
		return newError(ErrInvalidParam, "resolve")
	}
	if err := m.clock(now); err != nil {
		return err
	}
	h, err := m.household(hhID)
	if err != nil {
		return err
	}
	b, err := m.bill(h, billID)
	if err != nil {
		return err
	}
	if newPrincipal > b.principal {
		return newError(ErrInvalidParam, "new principal exceeds current")
	}
	if !b.disputed {
		return newError(ErrInvalidState, "not disputed")
	}
	b.dispDays += now - b.dispDay
	b.disputed = false
	b.principal = newPrincipal
	b.lastDay = now - 1
	b.stage = m.stageAt(b, now)
	m.closeIfSettled(b)
	m.lastNow = now
	return nil
}

func (m *naiveModel) waive(now int, hhID, billID string, amount int64) error {
	if hhID == "" || billID == "" || amount < 0 || now < 0 {
		return newError(ErrInvalidParam, "waive")
	}
	if err := m.clock(now); err != nil {
		return err
	}
	h, err := m.household(hhID)
	if err != nil {
		return err
	}
	b, err := m.bill(h, billID)
	if err != nil {
		return err
	}
	if b.closed {
		return newError(ErrInvalidState, "closed")
	}
	if amount == 0 {
		return newError(ErrAmount, "zero waive")
	}
	// Hypothetical accrual to now on a scratch copy.
	cp := *b
	cp.accrued = new(big.Rat).Set(b.accrued)
	m.accrueTo(&cp, now)
	if amount > cp.unpaidLate() {
		return newError(ErrAmount, "waive %d exceeds unpaid late %d", amount, cp.unpaidLate())
	}
	m.accrueTo(b, now)
	b.waived += amount
	m.closeIfSettled(b)
	m.lastNow = now
	return nil
}

func (m *naiveModel) totalDue(now int, hhID string) (int64, error) {
	if hhID == "" || now < 0 {
		return 0, newError(ErrInvalidParam, "totalDue")
	}
	if err := m.clock(now); err != nil {
		return 0, err
	}
	h, err := m.household(hhID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, b := range h.bills {
		cp := *b
		cp.accrued = new(big.Rat).Set(b.accrued)
		m.accrueTo(&cp, now)
		total += cp.unpaidP() + cp.unpaidLate()
	}
	return total, nil
}

func (m *naiveModel) view(now int, hhID string) (HouseholdView, error) {
	if hhID == "" || now < 0 {
		return HouseholdView{}, newError(ErrInvalidParam, "view")
	}
	if err := m.clock(now); err != nil {
		return HouseholdView{}, err
	}
	h, err := m.household(hhID)
	if err != nil {
		return HouseholdView{}, err
	}
	v := HouseholdView{Prepay: h.prepay, TotalPaid: h.totalPaid, Bills: map[string]BillStatus{}}
	for id, b := range h.bills {
		cp := *b
		cp.accrued = new(big.Rat).Set(b.accrued)
		m.accrueTo(&cp, now)
		v.Bills[id] = BillStatus{
			DueDay:        b.due,
			Principal:     b.principal,
			PaidPrincipal: b.paidP,
			AccruedLate:   cp.accruedInt(),
			PaidLate:      b.paidL,
			WaivedLate:    b.waived,
			Stage:         m.stageAt(b, now),
			Closed:        b.closed,
			Disputed:      b.disputed,
			DisputedDays:  b.dispDays,
		}
	}
	return v, nil
}

// rndOp is one recorded operation, replayable for the determinism check.
type rndOp struct {
	kind      string
	now       int
	hh, bill  string
	amount    int64
	principal int64
	due       int
}

func (o rndOp) String() string {
	switch o.kind {
	case "generate":
		return fmt.Sprintf("GenerateBill(now=%d hh=%s bill=%s principal=%d due=%d)", o.now, o.hh, o.bill, o.principal, o.due)
	case "pay":
		return fmt.Sprintf("Pay(now=%d hh=%s amount=%d)", o.now, o.hh, o.amount)
	case "dispute":
		return fmt.Sprintf("Dispute(now=%d hh=%s bill=%s)", o.now, o.hh, o.bill)
	case "resolve":
		return fmt.Sprintf("ResolveDispute(now=%d hh=%s bill=%s newPrincipal=%d)", o.now, o.hh, o.bill, o.amount)
	case "waive":
		return fmt.Sprintf("Waive(now=%d hh=%s bill=%s amount=%d)", o.now, o.hh, o.bill, o.amount)
	}
	return "?"
}

func applyOp(s *Service, m *naiveModel, o rndOp) (PaymentResult, PaymentResult, error, error) {
	var sr, mr PaymentResult
	var serr, merr error
	switch o.kind {
	case "generate":
		serr = s.GenerateBill(o.now, o.hh, o.bill, o.principal, o.due)
		merr = m.generate(o.now, o.hh, o.bill, o.principal, o.due)
	case "pay":
		sr, serr = s.Pay(o.now, o.hh, o.amount)
		mr, merr = m.pay(o.now, o.hh, o.amount)
	case "dispute":
		serr = s.Dispute(o.now, o.hh, o.bill)
		merr = m.dispute(o.now, o.hh, o.bill)
	case "resolve":
		serr = s.ResolveDispute(o.now, o.hh, o.bill, o.amount)
		merr = m.resolve(o.now, o.hh, o.bill, o.amount)
	case "waive":
		serr = s.Waive(o.now, o.hh, o.bill, o.amount)
		merr = m.waive(o.now, o.hh, o.bill, o.amount)
	}
	return sr, mr, serr, merr
}

func errCode(err error) string {
	if err == nil {
		return "ok"
	}
	if be, ok := err.(*Error); ok {
		return be.Code.String()
	}
	return "foreign:" + err.Error()
}

func TestDifferentialRandom(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	cfg := Config{
		GraceDays: 2,
		RateNum:   3, RateDen: 7, // odd fraction to stress carryover
		CapNum: 4, CapDen: 5,
		Thresholds: [3]int{3, 6, 9},
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	model := newNaive(cfg)
	rng := rand.New(rand.NewSource(seed))

	hhIDs := []string{"alpha", "beta", "gamma", "ghost"}
	var ops []rndOp
	var payResults []PaymentResult
	now, lastAccepted := 0, -1
	billSeq := 0

	pickBill := func() (string, string) {
		var hh, id string
		if rng.Intn(10) < 8 && len(model.hhs) > 0 {
			keys := make([]string, 0, len(model.hhs))
			for k := range model.hhs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			hh = keys[rng.Intn(len(keys))]
			ids := make([]string, 0, len(model.hhs[hh].bills))
			for k := range model.hhs[hh].bills {
				ids = append(ids, k)
			}
			if len(ids) > 0 {
				sort.Strings(ids)
				id = ids[rng.Intn(len(ids))]
			}
		}
		if hh == "" {
			hh = hhIDs[rng.Intn(len(hhIDs))]
		}
		if id == "" {
			id = fmt.Sprintf("b%d", rng.Intn(billSeq+2))
		}
		return hh, id
	}

	for step := 0; step < 1500; step++ {
		now += rng.Intn(3)
		if now > 0 && rng.Intn(20) == 0 {
			now-- // inject clock rollback attempts
		}
		var op rndOp
		switch rng.Intn(10) {
		case 0, 1, 2:
			due := now - rng.Intn(12)
			if due < 0 {
				due = rng.Intn(2) // occasionally negative -> invalid param
			}
			op = rndOp{kind: "generate", now: now, hh: hhIDs[rng.Intn(3)], bill: fmt.Sprintf("b%d", billSeq),
				principal: int64(1 + rng.Intn(200)), due: due}
			billSeq++
		case 3, 4, 5, 6:
			amt := int64(rng.Intn(300))
			if rng.Intn(15) == 0 {
				amt = 0 // exercise the zero-payment amount error
			}
			op = rndOp{kind: "pay", now: now, hh: hhIDs[rng.Intn(len(hhIDs))], amount: amt}
		case 7:
			hh, id := pickBill()
			op = rndOp{kind: "dispute", now: now, hh: hh, bill: id}
		case 8:
			hh, id := "", ""
			// Bias toward bills currently under dispute so rulings are
			// exercised often; otherwise fall back to a random bill.
			var disputed []struct{ hh, id string }
			for hhid, h := range model.hhs {
				for bid, b := range h.bills {
					if b.disputed {
						disputed = append(disputed, struct{ hh, id string }{hhid, bid})
					}
				}
			}
			if len(disputed) > 0 && rng.Intn(10) < 7 {
				d := disputed[rng.Intn(len(disputed))]
				hh, id = d.hh, d.id
			} else {
				hh, id = pickBill()
			}
			newP := int64(rng.Intn(250)) // may exceed current principal
			op = rndOp{kind: "resolve", now: now, hh: hh, bill: id, amount: newP}
		case 9:
			hh, id := pickBill()
			op = rndOp{kind: "waive", now: now, hh: hh, bill: id, amount: int64(rng.Intn(60))}
		}
		ops = append(ops, op)

		sr, mr, serr, merr := applyOp(svc, model, op)
		if errCode(serr) != errCode(merr) {
			t.Fatalf("step %d %s: error mismatch svc=%s model=%s", step, op, errCode(serr), errCode(merr))
		}
		if op.kind == "pay" && serr == nil {
			if !reflect.DeepEqual(sr, mr) {
				t.Fatalf("step %d %s: payment result mismatch\nsvc:   %+v\nmodel: %+v", step, op, sr, mr)
			}
			payResults = append(payResults, sr)
		}
		if serr == nil {
			lastAccepted = max(lastAccepted, op.now)
		}
		basis := fmt.Sprintf("accepted=%v svc=%s", serr == nil, errCode(serr))
		if op.kind == "pay" && serr == nil {
			basis += fmt.Sprintf(" applications=%+v prepaid=%d", sr.Applications, sr.Prepaid)
		}
		t.Logf("step=%d op=%s -> %s", step, op, basis)

		// Cross-check full state after every step at the last accepted day.
		for _, hh := range hhIDs {
			sv, serr2 := svc.View(lastAccepted, hh)
			mv, merr2 := model.view(lastAccepted, hh)
			if errCode(serr2) != errCode(merr2) {
				t.Fatalf("step %d: view error mismatch for %s: svc=%s model=%s", step, hh, errCode(serr2), errCode(merr2))
			}
			if serr2 != nil {
				continue
			}
			if !reflect.DeepEqual(sv, mv) {
				t.Fatalf("step %d %s: view mismatch for %s\nsvc:   %+v\nmodel: %+v", step, op, hh, sv, mv)
			}
			sd, _ := svc.TotalDue(lastAccepted, hh)
			md, _ := model.totalDue(lastAccepted, hh)
			if sd != md {
				t.Fatalf("step %d: TotalDue(%s) svc=%d model=%d", step, hh, sd, md)
			}
			var appliedP, appliedL int64
			for _, bs := range sv.Bills {
				appliedP += bs.PaidPrincipal
				appliedL += bs.PaidLate
			}
			if sv.TotalPaid != appliedP+appliedL+sv.Prepay {
				t.Fatalf("step %d: conservation violated for %s: paid=%d principal=%d late=%d prepay=%d",
					step, hh, sv.TotalPaid, appliedP, appliedL, sv.Prepay)
			}
		}
	}

	// Replay: the same operation sequence on a fresh service must reproduce
	// identical payment breakdowns and identical final state.
	svc2, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	payIdx := 0
	for i, op := range ops {
		sr, _, serr, _ := applyOp(svc2, newNaive(cfg), op)
		if op.kind == "pay" && serr == nil {
			if !reflect.DeepEqual(sr, payResults[payIdx]) {
				t.Fatalf("replay op %d %s: payment result diverged: %+v vs %+v", i, op, sr, payResults[payIdx])
			}
			payIdx++
		}
	}
	for _, hh := range hhIDs {
		v1, err1 := svc.View(lastAccepted, hh)
		v2, err2 := svc2.View(lastAccepted, hh)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("replay: existence mismatch for %s", hh)
		}
		if err1 == nil && !reflect.DeepEqual(v1, v2) {
			t.Fatalf("replay: final state mismatch for %s", hh)
		}
	}
}
