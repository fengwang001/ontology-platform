package microgrid

import (
	"errors"
	"math/rand/v2"
	"sort"
	"testing"
)

// naiveModel is an independent, deliberately straightforward restatement of
// the specification: every check is a linear scan over sorted slot maps and
// forecasts, with no prefix sums or other shared structures.
type naiveModel struct {
	p       Params
	soc     int
	tp      int
	current int
	mode    Mode
	locked  bool
	load    map[int]int
	surplus map[int]int
	plan    map[int]SlotPlan
}

func newNaive(p Params, soc int) *naiveModel {
	return &naiveModel{
		p: p, soc: soc, mode: GridTied,
		load: map[int]int{}, surplus: map[int]int{}, plan: map[int]SlotPlan{},
	}
}

func (n *naiveModel) stored(amount int) int {
	return amount * (n.p.LossDenominator - n.p.LossNumerator) / n.p.LossDenominator
}

func (n *naiveModel) loadSum(lo, hi int) (int, int) {
	sum, cnt := 0, 0
	for t := lo; t <= hi; t++ {
		if v, ok := n.load[t]; ok {
			sum += v
			cnt++
		}
	}
	return sum, cnt
}

func sortedPlanSlots(m map[int]SlotPlan) []int {
	ks := make([]int, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}

// validate performs the fixed-precedence per-slot checks against a candidate
// plan map and returns the first failing slot and reason (0 reason = ok).
func (n *naiveModel) validate(plans map[int]SlotPlan, soc int) (int, Reason) {
	for _, t := range sortedPlanSlots(plans) {
		pl := plans[t]
		load, hasLoad := n.load[t]
		switch pl.Action {
		case Idle:
			if pl.Amount != 0 {
				return t, ReasonInvalid
			}
		case Charge, Discharge:
			if pl.Amount <= 0 {
				return t, ReasonInvalid
			}
		default:
			return t, ReasonInvalid
		}
		if n.locked && pl.Action == Discharge {
			if !(n.mode == Island && hasLoad && pl.Amount <= load) {
				return t, ReasonMaintenance
			}
		}
		if n.mode == Island {
			switch pl.Action {
			case Charge:
				if pl.Amount > n.surplus[t] {
					return t, ReasonMode
				}
			case Discharge:
				if !hasLoad || pl.Amount > load {
					return t, ReasonMode
				}
			}
		}
		if pl.Action == Charge && pl.Amount > n.p.MaxCharge {
			return t, ReasonBounds
		}
		if pl.Action == Discharge && pl.Amount > n.p.MaxDischarge {
			return t, ReasonBounds
		}
		next := soc
		switch pl.Action {
		case Charge:
			next += n.stored(pl.Amount)
		case Discharge:
			next -= pl.Amount
		}
		if next < n.p.SoCLower || next > n.p.SoCUpper {
			return t, ReasonBounds
		}
		need, cnt := n.loadSum(t+1, t+n.p.ReserveSlots)
		if next-n.p.SoCLower < need {
			return t, ReasonReserve
		}
		if cnt != n.p.ReserveSlots || !hasLoad {
			return t, ReasonNoForecast
		}
		soc = next
	}
	return 0, 0
}

func (n *naiveModel) revalidate(cause string) []Revocation {
	if len(n.plan) == 0 {
		return nil
	}
	from, r := n.validate(n.plan, n.soc)
	if r == 0 {
		return nil
	}
	var dropped []int
	for k := range n.plan {
		if k >= from {
			dropped = append(dropped, k)
			delete(n.plan, k)
		}
	}
	sort.Ints(dropped)
	if len(dropped) == 0 {
		return nil
	}
	return []Revocation{{From: from, Cause: cause, Slots: dropped}}
}

func wellFormedPlan(p SlotPlan) bool {
	switch p.Action {
	case Idle:
		return p.Amount == 0
	case Charge, Discharge:
		return p.Amount > 0
	}
	return false
}

func (n *naiveModel) submit(start int, plans []SlotPlan) (*Outcome, error) {
	if start < 0 || len(plans) == 0 {
		return nil, ErrInvalid
	}
	for i, p := range plans {
		if !wellFormedPlan(p) {
			return nil, &RejectError{Reason: ReasonInvalid, Slot: start + i}
		}
	}
	if start < n.current {
		return nil, &RejectError{Reason: ReasonSlot, Slot: start}
	}
	cand := map[int]SlotPlan{}
	for k, v := range n.plan {
		cand[k] = v
	}
	for i, p := range plans {
		cand[start+i] = p
	}
	if t, r := n.validate(cand, n.soc); r != 0 {
		return nil, &RejectError{Reason: r, Slot: t}
	}
	n.plan = cand
	return &Outcome{Locked: n.locked}, nil
}

func (n *naiveModel) updateLoads(start int, loads []int) (*Outcome, error) {
	if start < 0 || len(loads) == 0 {
		return nil, ErrInvalid
	}
	for _, v := range loads {
		if v < 0 {
			return nil, ErrInvalid
		}
	}
	for i, v := range loads {
		n.load[start+i] = v
	}
	return &Outcome{Revoked: n.revalidate("forecast"), Locked: n.locked}, nil
}

func (n *naiveModel) setSurplus(slot, v int) error {
	if slot < 0 || v < 0 {
		return ErrInvalid
	}
	if slot < n.current {
		return &RejectError{Reason: ReasonSlot, Slot: slot}
	}
	n.surplus[slot] = v
	return nil
}

func (n *naiveModel) actual(slot int, a Action, amount int) (*Outcome, error) {
	if !wellFormedPlan(SlotPlan{Action: a, Amount: amount}) {
		return nil, &RejectError{Reason: ReasonInvalid, Slot: slot}
	}
	if slot != n.current {
		return nil, &RejectError{Reason: ReasonSlot, Slot: slot}
	}
	load, hasLoad := n.load[slot]
	if n.locked && a == Discharge {
		if !(n.mode == Island && hasLoad && amount <= load) {
			return nil, &RejectError{Reason: ReasonMaintenance, Slot: slot}
		}
	}
	if n.mode == Island {
		switch a {
		case Charge:
			if amount > n.surplus[slot] {
				return nil, &RejectError{Reason: ReasonMode, Slot: slot}
			}
		case Discharge:
			if !hasLoad || amount > load {
				return nil, &RejectError{Reason: ReasonMode, Slot: slot}
			}
		}
	}
	next := n.soc
	switch a {
	case Charge:
		next += n.stored(amount)
	case Discharge:
		next -= amount
	}
	if next < n.p.SoCLower || next > n.p.SoCUpper {
		return nil, &RejectError{Reason: ReasonBounds, Slot: slot}
	}
	planned, had := n.plan[slot]
	dev := false
	ap := signedPower(a, amount)
	if had {
		dev = absInt(ap-signedPower(planned.Action, planned.Amount)) > n.p.Tolerance
	} else {
		dev = absInt(ap) > n.p.Tolerance
	}
	n.soc = next
	if a == Discharge {
		n.tp += amount
		if n.tp >= n.p.MaintenanceThreshold {
			n.locked = true
		}
	}
	n.current++
	for k := range n.plan {
		if k < n.current {
			delete(n.plan, k)
		}
	}
	out := &Outcome{Deviation: dev, Locked: n.locked}
	if len(n.plan) > 0 {
		cause := "actual"
		if dev {
			cause = "deviation"
		} else if n.locked {
			cause = "maintenance"
		}
		out.Revoked = n.revalidate(cause)
	}
	return out, nil
}

func (n *naiveModel) switchMode(m Mode) (*Outcome, error) {
	if m != GridTied && m != Island {
		return nil, ErrInvalid
	}
	if m == n.mode {
		return &Outcome{Locked: n.locked}, nil
	}
	n.mode = m
	if m == Island {
		return &Outcome{Revoked: n.revalidate("mode"), Locked: n.locked}, nil
	}
	return &Outcome{Locked: n.locked}, nil
}

func (n *naiveModel) completeMaintenance() {
	n.tp = 0
	n.locked = false
}

type opKind int

const (
	opSubmit opKind = iota
	opUpdateLoad
	opSurplus
	opActual
	opSwitch
	opMaint
)

type randOp struct {
	kind   opKind
	slot   int
	action Action
	amount int
	plans  []SlotPlan
	mode   Mode
	loads  []int
}

func randomParams(rng *rand.Rand) Params {
	capacity := 40 + rng.IntN(60)
	lower := rng.IntN(10)
	return Params{
		Capacity:             capacity,
		SoCLower:             lower,
		SoCUpper:             lower + rng.IntN(capacity-lower+1),
		MaxCharge:            1 + rng.IntN(15),
		MaxDischarge:         1 + rng.IntN(15),
		LossNumerator:        rng.IntN(4),
		LossDenominator:      4 + rng.IntN(8),
		MaintenanceThreshold: 15 + rng.IntN(60),
		ReserveSlots:         rng.IntN(4),
		Tolerance:            rng.IntN(4),
	}
}

func genOp(rng *rand.Rand, cur int) randOp {
	switch rng.IntN(6) {
	case 0:
		start := cur + rng.IntN(4) - 1
		ps := make([]SlotPlan, 1+rng.IntN(4))
		for i := range ps {
			switch rng.IntN(3) {
			case 0:
				ps[i] = SlotPlan{Idle, 0}
			case 1:
				ps[i] = SlotPlan{Charge, 1 + rng.IntN(18)}
			default:
				ps[i] = SlotPlan{Discharge, 1 + rng.IntN(18)}
			}
			if rng.IntN(12) == 0 {
				ps[i].Amount = -1
			}
		}
		return randOp{kind: opSubmit, slot: start, plans: ps}
	case 1:
		lo := cur + rng.IntN(5) - 1
		ls := make([]int, 1+rng.IntN(4))
		for i := range ls {
			ls[i] = rng.IntN(12)
		}
		return randOp{kind: opUpdateLoad, slot: lo, loads: ls}
	case 2:
		return randOp{kind: opSurplus, slot: cur + rng.IntN(5), amount: rng.IntN(12)}
	case 3:
		var a Action
		switch rng.IntN(3) {
		case 0:
			a = Idle
		case 1:
			a = Charge
		default:
			a = Discharge
		}
		slot := cur
		if rng.IntN(6) == 0 {
			slot = cur + rng.IntN(4) - 1
		}
		return randOp{kind: opActual, slot: slot, action: a, amount: rng.IntN(18)}
	case 4:
		if rng.IntN(2) == 0 {
			return randOp{kind: opSwitch, mode: Island}
		}
		return randOp{kind: opSwitch, mode: GridTied}
	default:
		return randOp{kind: opMaint}
	}
}

func sameRevocations(a, b []Revocation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].From != b[i].From || len(a[i].Slots) != len(b[i].Slots) {
			return false
		}
		for j := range a[i].Slots {
			if a[i].Slots[j] != b[i].Slots[j] {
				return false
			}
		}
	}
	return true
}

func controllerPlans(c *Controller) map[int]SlotPlan {
	out := map[int]SlotPlan{}
	for t := c.CurrentSlot(); t < c.CurrentSlot()+40; t++ {
		if p, ok := c.PlanAt(t); ok {
			out[t] = p
		}
	}
	return out
}

func applyNaive(n *naiveModel, op randOp) (*Outcome, error) {
	switch op.kind {
	case opSubmit:
		return n.submit(op.slot, op.plans)
	case opUpdateLoad:
		return n.updateLoads(op.slot, op.loads)
	case opSurplus:
		return nil, n.setSurplus(op.slot, op.amount)
	case opActual:
		return n.actual(op.slot, op.action, op.amount)
	case opSwitch:
		return n.switchMode(op.mode)
	default:
		n.completeMaintenance()
		return nil, nil
	}
}

func applyController(c *Controller, op randOp) (*Outcome, error) {
	switch op.kind {
	case opSubmit:
		return c.SubmitPlan(op.slot, op.plans)
	case opUpdateLoad:
		return c.UpdateLoads(op.slot, op.loads)
	case opSurplus:
		return nil, c.RegisterSurplus(op.slot, op.amount)
	case opActual:
		return c.RegisterActual(op.slot, op.action, op.amount)
	case opSwitch:
		return c.SwitchMode(op.mode)
	default:
		c.CompleteMaintenance()
		return nil, nil
	}
}

func errKey(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, ErrInvalid) {
		var re *RejectError
		if errors.As(err, &re) {
			return "invalid@" + itoa(re.Slot)
		}
		return "invalid"
	}
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason.String() + "@" + itoa(re.Slot)
	}
	return err.Error()
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	neg := x < 0
	if neg {
		x = -x
	}
	var b [24]byte
	i := len(b)
	for x > 0 {
		i--
		b[i] = byte('0' + x%10)
		x /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func opString(op randOp) string {
	switch op.kind {
	case opSubmit:
		return fmtOp("submit", op.slot, op.plans)
	case opUpdateLoad:
		return "updateLoad start=" + itoa(op.slot) + " loads=" + fmtInts(op.loads)
	case opSurplus:
		return "surplus slot=" + itoa(op.slot) + " v=" + itoa(op.amount)
	case opActual:
		return "actual slot=" + itoa(op.slot) + " " + op.action.String() + " " + itoa(op.amount)
	case opSwitch:
		return "switch " + op.mode.String()
	default:
		return "completeMaintenance"
	}
}

func fmtOp(name string, start int, ps []SlotPlan) string {
	s := name + " start=" + itoa(start) + " ["
	for i, p := range ps {
		if i > 0 {
			s += ","
		}
		s += p.Action.String() + ":" + itoa(p.Amount)
	}
	return s + "]"
}

func fmtInts(xs []int) string {
	s := "["
	for i, x := range xs {
		if i > 0 {
			s += ","
		}
		s += itoa(x)
	}
	return s + "]"
}

func TestRandomDifferential(t *testing.T) {
	const sequences, opsPerSeq = 400, 90
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewPCG(uint64(seq*1_000_003+1), 77))
		p := randomParams(rng)
		soc := p.SoCLower + rng.IntN(p.SoCUpper-p.SoCLower+1)
		c, err := New(p, soc)
		if err != nil {
			t.Fatalf("seed %d New: %v", seq, err)
		}
		n := newNaive(p, soc)
		lastController, lastNaive = c, n
		for step := 0; step < opsPerSeq; step++ {
			op := genOp(rng, n.current)
			co, ce := applyController(c, op)
			no, ne := applyNaive(n, op)
			if errKey(ce) != errKey(ne) {
				dumpDivergence(t, seq, step, p, op, ce, ne)
			}
			if ce == nil {
				if devOf(co) != devOf(no) || !sameRevocations(revOf(co), revOf(no)) {
					dumpDivergence(t, seq, step, p, op, ce, ne)
				}
			}
			if c.SoC() != n.soc || c.CurrentSlot() != n.current ||
				c.Throughput() != n.tp || c.Locked() != n.locked || c.Mode() != n.mode {
				dumpDivergence(t, seq, step, p, op, ce, ne)
			}
			cp, np := controllerPlans(c), n.plan
			if len(cp) != len(np) {
				dumpDivergence(t, seq, step, p, op, ce, ne)
			}
			for k, v := range cp {
				if np[k] != v {
					dumpDivergence(t, seq, step, p, op, ce, ne)
				}
			}
		}
	}
}

func dumpDivergence(t *testing.T, seq, step int, p Params, op randOp, ce, ne error) {
	t.Helper()
	// Locate the exact field that differs for a precise, replayable report.
	diff := "error"
	if ce == nil && ne == nil {
		diff = compareLive(lastController, lastNaive)
	}
	t.Fatalf("divergence seed=%d step=%d params=%+v\n  input : %s\n  got   : %v\n  naive : %v\n  field : %s\n  got state   : soc=%d slot=%d tp=%d lock=%v mode=%s plans=%v\n  naive state : soc=%d slot=%d tp=%d lock=%v mode=%s plans=%v",
		seq, step, p, opString(op), ce, ne, diff,
		lastController.SoC(), lastController.CurrentSlot(), lastController.Throughput(),
		lastController.Locked(), lastController.Mode(), controllerPlans(lastController),
		lastNaive.soc, lastNaive.current, lastNaive.tp, lastNaive.locked,
		lastNaive.mode, lastNaive.plan)
}

var (
	lastController *Controller
	lastNaive      *naiveModel
)

func compareLive(c *Controller, n *naiveModel) string {
	switch {
	case c.SoC() != n.soc:
		return "soc"
	case c.CurrentSlot() != n.current:
		return "current"
	case c.Throughput() != n.tp:
		return "throughput"
	case c.Locked() != n.locked:
		return "locked"
	case c.Mode() != n.mode:
		return "mode"
	case len(controllerPlans(c)) != len(n.plan):
		return "plan-len"
	default:
		for k, v := range controllerPlans(c) {
			if n.plan[k] != v {
				return "plan@" + itoa(k)
			}
		}
		return "outcome"
	}
}

// TestRandomDifferentialVerbose logs every input, output and basis for a
// small fixed run; run with: go test -run TestRandomDifferentialVerbose -v
func TestRandomDifferentialVerbose(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 42))
	p := randomParams(rng)
	soc := p.SoCLower + rng.IntN(p.SoCUpper-p.SoCLower+1)
	t.Logf("params=%+v initialSOC=%d", p, soc)
	c, _ := New(p, soc)
	n := newNaive(p, soc)
	for step := 0; step < 25; step++ {
		op := genOp(rng, n.current)
		co, ce := applyController(c, op)
		no, ne := applyNaive(n, op)
		basis := "accepted"
		if ce != nil {
			basis = "rejected: " + errKey(ce)
		} else if co != nil && len(co.Revoked) > 0 {
			basis = "accepted, revocations=" + revSummary(co.Revoked)
		}
		t.Logf("#%02d %-38s => %-24s soc=%d slot=%d lock=%v dev=%v",
			step, opString(op), basis, c.SoC(), c.CurrentSlot(), c.Locked(),
			co != nil && co.Deviation)
		if errKey(ce) != errKey(ne) ||
			(ce == nil && !sameRevocations(revOf(co), revOf(no))) {
			t.Fatalf("verbose divergence at %d: got %v naive %v", step, ce, ne)
		}
	}
}

func revOf(o *Outcome) []Revocation {
	if o == nil {
		return nil
	}
	return o.Revoked
}

func devOf(o *Outcome) bool {
	return o != nil && o.Deviation
}

func revSummary(rs []Revocation) string {
	s := ""
	for i, r := range rs {
		if i > 0 {
			s += ";"
		}
		s += r.Cause + "@" + itoa(r.From) + "x" + itoa(len(r.Slots))
	}
	return s
}
