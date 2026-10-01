package billing

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Naive reference implementation
//
// naiveEngine independently re-derives every line straight from the problem
// statement: the initial prepay line, then per accepted change a refund of the
// current plan's price*r/D rounded down and a charge of the new plan's
// price*r/D rounded up. It deliberately shares no arithmetic or storage code
// with Engine, so an agreement is a genuine cross-check rather than self-talk.
// ---------------------------------------------------------------------------

type naiveLine struct {
	kind   LineKind
	day    int
	plan   string
	price  int64
	remain int
	amount int64
}

type naiveEngine struct {
	d       int
	prices  map[string]int64
	current string
	lastDay int
	lines   []naiveLine
}

func newNaive(D int, prices map[string]int64, initial string) *naiveEngine {
	n := &naiveEngine{d: D, prices: prices, current: initial, lastDay: -1}
	p := prices[initial]
	n.lines = append(n.lines, naiveLine{LineCharge, 0, initial, p, D, p})
	return n
}

func (n *naiveEngine) change(day int, plan string) (Reason, bool) {
	if _, exists := n.prices[plan]; !exists {
		return ReasonUnknownPlan, false
	}
	if plan == n.current {
		return ReasonSamePlan, false
	}
	if day < 1 || day > n.d-1 {
		return ReasonDayOutOfRange, false
	}
	if n.lastDay >= 0 {
		if day == n.lastDay {
			return ReasonDayRepeated, false
		}
		if day < n.lastDay {
			return ReasonDayOutOfOrder, false
		}
	}
	r := n.d - day
	old := n.prices[n.current]
	refund := -(old * int64(r) / int64(n.d))
	n.lines = append(n.lines, naiveLine{LineRefund, day, n.current, old, r, refund})
	np := n.prices[plan]
	charge := (np*int64(r) + int64(n.d) - 1) / int64(n.d)
	n.lines = append(n.lines, naiveLine{LineCharge, day, plan, np, r, charge})
	n.current = plan
	n.lastDay = day
	return ReasonUnknown, true
}

// settle closes the period, returns its lines, and prepays on the new period.
func (n *naiveEngine) settle() []naiveLine {
	out := make([]naiveLine, len(n.lines))
	copy(out, n.lines)
	n.lines = nil
	n.lastDay = -1
	p := n.prices[n.current]
	n.lines = append(n.lines, naiveLine{LineCharge, 0, n.current, p, n.d, p})
	return out
}

// ---------------------------------------------------------------------------
// Test logging: every test prints its inputs, outputs and judgement basis.
// ---------------------------------------------------------------------------

type testLog struct{ b strings.Builder }

func (l *testLog) printf(format string, args ...any) {
	fmt.Fprintf(&l.b, format+"\n", args...)
}

func (l *testLog) dump(t *testing.T) {
	t.Helper()
	t.Log("\n" + l.b.String())
}

func kindName(k LineKind) string {
	if k == LineCharge {
		return "CHARGE"
	}
	return "REFUND"
}

func logLines(l *testLog, label string, lines []Line) int64 {
	l.printf("%s: %d line(s)", label, len(lines))
	var sum int64
	for _, ln := range lines {
		sum += ln.Amount
		l.printf("  period=%d seq=%d %-6s day=%-2d plan=%-6s price=%4d remain=%d amount=%5d",
			ln.Period, ln.Seq, kindName(ln.Kind), ln.Day, ln.Plan, ln.Price, ln.Remain, ln.Amount)
	}
	l.printf("  => algebraic total = %d", sum)
	return sum
}

func crossCheck(t *testing.T, l *testLog, e *Engine, n *naiveEngine) {
	t.Helper()
	snap := e.Query()
	if len(snap.Lines) != len(n.lines) {
		t.Fatalf("line count mismatch: engine=%d naive=%d", len(snap.Lines), len(n.lines))
	}
	var sum int64
	for i, nl := range n.lines {
		el := snap.Lines[i]
		sum += el.Amount
		if el.Kind != nl.kind || el.Day != nl.day || el.Plan != nl.plan ||
			el.Price != nl.price || el.Remain != nl.remain || el.Amount != nl.amount {
			t.Fatalf("line %d mismatch:\n engine=%+v\n naive =%+v", i+1, el, nl)
		}
	}
	if sum != snap.Total {
		t.Fatalf("Query total %d != recomputed sum %d", snap.Total, sum)
	}
	l.printf("cross-check OK: %d lines; recomputed sum %d == Query.Total %d", len(n.lines), sum, snap.Total)
}

type op struct {
	day  int
	plan string
}

func runScenario(t *testing.T, name string, D int, prices map[string]int64, initial string, ops []op, expectedErrs []Reason) {
	t.Run(name, func(t *testing.T) {
		l := &testLog{}
		l.printf("INPUT D=%d initial=%q prices=%v", D, initial, prices)
		for i, o := range ops {
			l.printf("INPUT op[%d]: Change(day=%d, plan=%q) wantErr=%v", i, o.day, o.plan, expectedErrs[i])
		}

		e, err := New(D, prices, initial)
		if err != nil {
			t.Fatalf("unexpected New error: %v", err)
		}
		n := newNaive(D, prices, initial)
		crossCheck(t, l, e, n)

		for i, o := range ops {
			err := e.Change(o.day, o.plan)
			reason, accepted := n.change(o.day, o.plan)
			if expectedErrs[i] != ReasonUnknown {
				be, ok := err.(*Error)
				if err == nil || !ok {
					t.Fatalf("op %d: expected rejection %v, got %v", i, expectedErrs[i], err)
				}
				if be.Reason != expectedErrs[i] || accepted || reason != expectedErrs[i] {
					t.Fatalf("op %d: engine reason %v / naive reason %v accepted=%v, want %v",
						i, be.Reason, reason, accepted, expectedErrs[i])
				}
				l.printf("OUTPUT op[%d]: REJECTED reason=%v detail=%q; state unchanged", i, be.Reason, be.Error())
			} else if err != nil {
				t.Fatalf("op %d: unexpected rejection: %v", i, err)
			} else {
				l.printf("OUTPUT op[%d]: accepted", i)
				logLines(l, fmt.Sprintf("state after op[%d]", i), e.Query().Lines)
			}
			crossCheck(t, l, e, n)
		}

		settled, total := e.Settle()
		naiveSettled := n.settle()
		l.printf("OUTPUT Settle:")
		naiveTotal := logLines(l, "settled period", settled)
		if total != naiveTotal {
			t.Fatalf("settle total engine=%d naive=%d", total, naiveTotal)
		}
		if len(settled) != len(naiveSettled) {
			t.Fatalf("settled line count mismatch: %d vs %d", len(settled), len(naiveSettled))
		}
		for i, nl := range naiveSettled {
			el := settled[i]
			if el.Kind != nl.kind || el.Day != nl.day || el.Plan != nl.plan ||
				el.Price != nl.price || el.Remain != nl.remain || el.Amount != nl.amount {
				t.Fatalf("settled line %d mismatch:\n engine=%+v\n naive =%+v", i+1, el, nl)
			}
		}
		l.printf("JUDGEMENT: settle lines and total %d match line-by-line naive recomputation", total)
		crossCheck(t, l, e, n)
		l.dump(t)
	})
}

const noErr = ReasonUnknown

// ---------------------------------------------------------------------------
// Required scenarios
// ---------------------------------------------------------------------------

func TestRoundingFloorVsCeilOneCentGap(t *testing.T) {
	// D=3, price 100, r=2: floor(200/3)=66 vs ceil(200/3)=67.
	// Lines: +100, -66, +67 => total 101.
	prices := map[string]int64{"A": 100, "B": 100}
	runScenario(t, "non_divisible_floor66_ceil67", 3, prices, "A",
		[]op{{day: 1, plan: "B"}},
		[]Reason{noErr})

	l := &testLog{}
	e, _ := New(3, prices, "A")
	l.printf("INPUT D=3 A=100 B=100 initial=A; Change(1,B)")
	if err := e.Change(1, "B"); err != nil {
		t.Fatal(err)
	}
	lines, total := e.Settle()
	logLines(l, "OUTPUT settled", lines)
	if lines[1].Amount != -66 || lines[2].Amount != 67 {
		t.Fatalf("rounding amounts = %d,%d, want -66,67", lines[1].Amount, lines[2].Amount)
	}
	if total != 101 {
		t.Fatalf("total = %d, want 101", total)
	}
	l.printf("JUDGEMENT: refund -66 = -floor(100*2/3); charge +67 = ceil(100*2/3); gap = 1 cent")
	l.dump(t)
}

func TestExactDivisionFloorEqualsCeil(t *testing.T) {
	// 100*2/4 = 50 exactly: refund -50 and charge +50, no cent gap.
	prices := map[string]int64{"A": 100, "B": 100}
	runScenario(t, "divisible_floor_eq_ceil", 4, prices, "A",
		[]op{{day: 2, plan: "B"}},
		[]Reason{noErr})
}

func TestDayBoundariesOneAndDMinusOne(t *testing.T) {
	prices := map[string]int64{"A": 100, "B": 300, "C": 50}
	// D=5: day=1 -> r=4; day=4 (=D-1) -> r=1.
	// day1: -floor(100*4/5)=-80, +ceil(300*4/5)=240
	// day4: -floor(300*1/5)=-60, +ceil(50*1/5)=10
	// total = 100-80+240-60+10 = 210
	runScenario(t, "day_1_and_D_minus_1", 5, prices, "A",
		[]op{{day: 1, plan: "B"}, {day: 4, plan: "C"}},
		[]Reason{noErr, noErr})
}

func TestThreeChangesSecondRefundUsesSecondPlan(t *testing.T) {
	prices := map[string]int64{"P1": 300, "P2": 100, "P3": 200, "P4": 10}
	// D=6, changes on days 1, 2, 3 (r = 5, 4, 3).
	// day1 P1->P2: -floor(300*5/6)=-250, +ceil(100*5/6)=84
	// day2 P2->P3: -floor(100*4/6)=-66  <-- refund priced at P2 (current),
	//               +ceil(200*4/6)=134
	// day3 P3->P4: -floor(200*3/6)=-100, +ceil(10*3/6)=5
	// total = 300-250+84-66+134-100+5 = 107
	l := &testLog{}
	runScenario(t, "three_changes_second_refund_uses_P2", 6, prices, "P1",
		[]op{{day: 1, plan: "P2"}, {day: 2, plan: "P3"}, {day: 3, plan: "P4"}},
		[]Reason{noErr, noErr, noErr})

	e, _ := New(6, prices, "P1")
	l.printf("INPUT D=6 prices P1=300 P2=100 P3=200 P4=10; changes day1 P2, day2 P3, day3 P4")
	for _, o := range []op{{1, "P2"}, {2, "P3"}, {3, "P4"}} {
		if err := e.Change(o.day, o.plan); err != nil {
			t.Fatal(err)
		}
	}
	lines, total := e.Settle()
	logLines(l, "OUTPUT settled", lines)
	// seq4 is the refund line generated by the second change; its price field
	// must be P2's list price 100 even though P1's 300 was prepaid.
	if lines[3].Kind != LineRefund || lines[3].Plan != "P2" ||
		lines[3].Price != 100 || lines[3].Amount != -66 {
		t.Fatalf("second refund line wrong: %+v", lines[3])
	}
	if total != 107 {
		t.Fatalf("total = %d, want 107", total)
	}
	l.printf("JUDGEMENT: second refund seq=4 uses CURRENT plan P2 price 100: -floor(100*4/6)=-66; total 107")
	l.dump(t)
}

func TestUpgradeThenImmediateDowngradeNet(t *testing.T) {
	prices := map[string]int64{"low": 100, "high": 400, "mid": 200}
	// D=10: day4 low->high (r=6): -60, +240; day5 high->mid (r=5): -200, +100.
	runScenario(t, "upgrade_then_immediate_downgrade", 10, prices, "low",
		[]op{{day: 4, plan: "high"}, {day: 5, plan: "mid"}},
		[]Reason{noErr, noErr})

	l := &testLog{}
	e, _ := New(10, prices, "low")
	l.printf("INPUT D=10 low=100 high=400 mid=200 initial=low; Change(4,high); Change(5,mid)")
	if err := e.Change(4, "high"); err != nil {
		t.Fatal(err)
	}
	if err := e.Change(5, "mid"); err != nil {
		t.Fatal(err)
	}
	lines, total := e.Settle()
	logLines(l, "OUTPUT settled", lines)
	if total != 180 {
		t.Fatalf("net total = %d, want 180", total)
	}
	l.printf("JUDGEMENT: net = 100-60+240-200+100 = 180; the day-5 refund uses high's price 400")
	l.dump(t)
}

func TestZeroPricePlan(t *testing.T) {
	prices := map[string]int64{"free": 0, "paid": 120}
	// free -> paid -> free, D=7, days 2 and 5. Zero price yields zero lines.
	runScenario(t, "zero_price_round_trip", 7, prices, "free",
		[]op{{day: 2, plan: "paid"}, {day: 5, plan: "free"}},
		[]Reason{noErr, noErr})

	// paid -> free: refund of paid for r=3 days, charge of free rounds to 0.
	l := &testLog{}
	e, _ := New(4, prices, "paid")
	l.printf("INPUT D=4 free=0 paid=120 initial=paid; Change(1,free)")
	if err := e.Change(1, "free"); err != nil {
		t.Fatal(err)
	}
	lines, total := e.Settle()
	logLines(l, "OUTPUT settled", lines)
	// -floor(120*3/4) = -90; ceil(0*3/4) = 0; total = 30.
	if lines[2].Amount != 0 || total != 30 {
		t.Fatalf("zero charge line / total wrong: amount=%d total=%d", lines[2].Amount, total)
	}
	l.printf("JUDGEMENT: free plan prorates to -floor(120*3/4)=-90 refund and ceil(0)=0 charge; total 30")
	l.dump(t)
}

func TestSettleOpensNewPeriodWithPrepay(t *testing.T) {
	l := &testLog{}
	prices := map[string]int64{"A": 100, "B": 201}
	e, _ := New(4, prices, "A")
	l.printf("INPUT New(D=4,A=100,B=201,initial=A); Change(1,B); Settle(); Query()")
	if err := e.Change(1, "B"); err != nil {
		t.Fatal(err)
	}
	p1, t1 := e.Settle()
	logLines(l, "OUTPUT period 1", p1)
	// +100 -floor(100*3/4)=-75 +ceil(201*3/4)=151 => 176.
	if t1 != 176 {
		t.Fatalf("period1 total = %d, want 176", t1)
	}
	snap := e.Query()
	if snap.Period != 2 || snap.CurrentPlan != "B" || snap.LastDay != -1 || snap.Days != 4 {
		t.Fatalf("new period state wrong: %+v", snap)
	}
	logLines(l, "OUTPUT period 2 snapshot", snap.Lines)
	if len(snap.Lines) != 1 || snap.Lines[0].Day != 0 || snap.Lines[0].Plan != "B" ||
		snap.Lines[0].Amount != 201 || snap.Lines[0].Seq != 1 || snap.Total != 201 {
		t.Fatalf("new period prepay line wrong: %+v total=%d", snap.Lines, snap.Total)
	}
	// Period 2 evolves independently; its settle returns only its own lines,
	// and the slice returned for period 1 must stay untouched.
	if err := e.Change(2, "A"); err != nil {
		t.Fatal(err)
	}
	p2, t2 := e.Settle()
	logLines(l, "OUTPUT period 2 settled", p2)
	// +201 -floor(201*2/4)=-100 +ceil(100*2/4)=50 => 151.
	if t2 != 151 || len(p2) != 3 {
		t.Fatalf("period2 total=%d lines=%d, want 151/3", t2, len(p2))
	}
	if len(p1) != 3 {
		t.Fatalf("period1 returned slice was mutated: %d lines", len(p1))
	}
	l.printf("JUDGEMENT: new period prepays current plan B at 201 on day 0; settled slices are independent copies")
	l.dump(t)
}

// ---------------------------------------------------------------------------
// Rejection ordering and constructor validation
// ---------------------------------------------------------------------------

func TestRejectionOrder(t *testing.T) {
	prices := map[string]int64{"A": 100, "B": 200}

	type tc struct {
		name   string
		day    int
		plan   string
		setup  func(e *Engine)
		reason Reason
	}
	cases := []tc{
		{"unknown_plan_first", 1, "X", nil, ReasonUnknownPlan},
		{"same_plan_before_day_check", 9, "A", nil, ReasonSamePlan},
		{"day_zero_out_of_range", 0, "B", nil, ReasonDayOutOfRange},
		{"day_eq_D_out_of_range", 5, "B", nil, ReasonDayOutOfRange},
		{"negative_day_out_of_range", -1, "B", nil, ReasonDayOutOfRange},
		// After a change on day 2, same day -> repeated, earlier day -> out
		// of order; unknown/same-plan still take precedence.
		// Setup moved A->B on day 2, so current plan is B.
		{"repeated_day", 2, "A", func(e *Engine) { _ = e.Change(2, "B") }, ReasonDayRepeated},
		{"earlier_day_out_of_order", 1, "A", func(e *Engine) { _ = e.Change(2, "B") }, ReasonDayOutOfOrder},
		{"unknown_plan_beats_repeated", 2, "X", func(e *Engine) { _ = e.Change(2, "B") }, ReasonUnknownPlan},
		{"same_plan_beats_repeated", 2, "B", func(e *Engine) { _ = e.Change(2, "B") }, ReasonSamePlan},
		{"same_plan_beats_out_of_range", 9, "B", func(e *Engine) { _ = e.Change(2, "B") }, ReasonSamePlan},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &testLog{}
			e, _ := New(5, prices, "A")
			if c.setup != nil {
				c.setup(e)
			}
			before := e.Query()
			l.printf("INPUT D=5 current=%q lastDay=%d; Change(day=%d,plan=%q)",
				before.CurrentPlan, before.LastDay, c.day, c.plan)
			err := e.Change(c.day, c.plan)
			be, ok := err.(*Error)
			if err == nil || !ok || be.Reason != c.reason {
				t.Fatalf("want reason %v, got %v", c.reason, err)
			}
			after := e.Query()
			if after.CurrentPlan != before.CurrentPlan || after.LastDay != before.LastDay ||
				len(after.Lines) != len(before.Lines) || after.Total != before.Total {
				t.Fatalf("rejected op mutated state:\n before=%+v\n after =%+v", before, after)
			}
			l.printf("OUTPUT REJECTED reason=%v; plan/lastDay/lines unchanged (%d lines, total %d)",
				be.Reason, len(after.Lines), after.Total)
			l.printf("JUDGEMENT: first violation in the defined order is %v; no state change", c.reason)
			l.dump(t)
		})
	}

	// Switching back to a previously held plan is allowed; only equality with
	// the *current* plan is rejected.
	e, _ := New(5, prices, "A")
	if err := e.Change(1, "B"); err != nil {
		t.Fatal(err)
	}
	if err := e.Change(4, "A"); err != nil {
		t.Fatalf("change back to A should succeed: %v", err)
	}
}

func TestConstructorValidation(t *testing.T) {
	good := map[string]int64{"A": 100}
	type tc struct {
		name    string
		D       int
		prices  map[string]int64
		initial string
		reason  Reason
	}
	cases := []tc{
		{"D_zero", 0, good, "A", ReasonInvalidPeriodDays},
		{"D_negative", -3, good, "A", ReasonInvalidPeriodDays},
		{"D_too_large", 367, good, "A", ReasonInvalidPeriodDays},
		{"D_min_boundary", 1, good, "A", ReasonUnknown},
		{"D_max_boundary", 366, good, "A", ReasonUnknown},
		{"price_negative", 10, map[string]int64{"A": -1}, "A", ReasonInvalidPrice},
		{"price_too_large", 10, map[string]int64{"A": MaxPrice + 1}, "A", ReasonInvalidPrice},
		{"price_boundaries_0_and_max", 10, map[string]int64{"Z": 0, "A": MaxPrice}, "A", ReasonUnknown},
		{"unknown_initial", 10, good, "Z", ReasonUnknownPlan},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &testLog{}
			l.printf("INPUT New(D=%d, prices=%v, initial=%q)", c.D, c.prices, c.initial)
			e, err := New(c.D, c.prices, c.initial)
			if c.reason == ReasonUnknown {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				s := e.Query()
				l.printf("OUTPUT ok: period=%d prepay total=%d", s.Period, s.Total)
			} else {
				be, ok := err.(*Error)
				if err == nil || !ok || be.Reason != c.reason {
					t.Fatalf("want reason %v, got %v", c.reason, err)
				}
				if e != nil {
					t.Fatalf("rejected New returned non-nil engine")
				}
				l.printf("OUTPUT REJECTED reason=%v detail=%q", be.Reason, be.Error())
			}
			l.printf("JUDGEMENT: constructor check %q behaves as specified", c.name)
			l.dump(t)
		})
	}

	// D=1 admits no change day, so every change is out of range.
	e, _ := New(1, map[string]int64{"A": 10, "B": 20}, "A")
	err := e.Change(1, "B")
	if err == nil || err.(*Error).Reason != ReasonDayOutOfRange {
		t.Fatalf("D=1 change must be rejected as out of range, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Deterministic replay
// ---------------------------------------------------------------------------

func TestReplayProducesIdenticalLines(t *testing.T) {
	l := &testLog{}
	prices := map[string]int64{"P1": 300, "P2": 100, "P3": 200, "P4": 10}
	ops := []op{{1, "P2"}, {2, "P3"}, {3, "P4"}}
	l.printf("INPUT replay script: New(D=6,...); changes %v; Settle()", ops)

	play := func() ([]Line, int64) {
		e, err := New(6, prices, "P1")
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range ops {
			if err := e.Change(o.day, o.plan); err != nil {
				t.Fatal(err)
			}
		}
		return e.Settle()
	}

	first, total1 := play()
	for run := 2; run <= 5; run++ {
		again, total := play()
		if total != total1 || len(again) != len(first) {
			t.Fatalf("replay %d differs in total/length", run)
		}
		for i := range first {
			if again[i] != first[i] {
				t.Fatalf("replay %d line %d differs:\n first=%+v\n again=%+v", run, i, first[i], again[i])
			}
		}
		l.printf("OUTPUT replay run %d: %d identical Line structs, total %d", run, len(again), total)
	}
	logLines(l, "reference settled lines", first)
	l.printf("JUDGEMENT: identical script replays field-by-field identical lines (incl. Period/Seq) and total %d", total1)
	l.dump(t)
}

// ---------------------------------------------------------------------------
// Concurrency: Change / Settle / Query race against each other. Every
// observed snapshot must be internally consistent (line sum == total, first
// line is a day-0 prepay, refund days are strictly increasing), which can
// only hold if all calls linearize through the engine's lock.
// ---------------------------------------------------------------------------

func TestConcurrentLinearizable(t *testing.T) {
	prices := map[string]int64{"P1": 300, "P2": 100, "P3": 200, "P4": 10}
	const goroutines = 16
	const iterations = 300

	l := &testLog{}
	l.printf("INPUT %d goroutines x %d iterations mixing Change/Settle/Query, D=6", goroutines, iterations)

	for run := 0; run < 8; run++ {
		e, _ := New(6, prices, "P1")
		var wg sync.WaitGroup

		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				// Each goroutine walks increasing days, so its own accepted
				// changes are always ordered; Settles reset that sequence.
				days := []int{1, 2, 3, 4, 5}
				plans := []string{"P2", "P3", "P4", "P1", "P2"}
				for k := 0; k < iterations; k++ {
					switch (g + k) % 7 {
					case 0, 1, 2, 3, 4:
						idx := (g + k) % 5
						// Accepted or rejected: the Query branch below is
						// what validates every reachable state. Another
						// goroutine may Settle between this Change and a
						// follow-up read, which is still a legal serial order.
						_ = e.Change(days[idx], plans[idx])
					case 5:
						e.Settle()
					default:
						s := e.Query()
						var sum int64
						prevRefundDay := -1
						seq := 0
						for _, ln := range s.Lines {
							seq++
							if ln.Seq != seq {
								t.Errorf("non-contiguous Seq: %+v", ln)
								return
							}
							if ln.Period != s.Period {
								t.Errorf("line period %d != snapshot period %d", ln.Period, s.Period)
								return
							}
							sum += ln.Amount
							switch ln.Kind {
							case LineCharge:
								if ln.Amount < 0 || ln.Amount != ceilDiv(ln.Price*int64(ln.Remain), int64(s.Days)) {
									t.Errorf("charge line violates ceil formula: %+v", ln)
									return
								}
							case LineRefund:
								if ln.Amount > 0 || ln.Amount != -floorDiv(ln.Price*int64(ln.Remain), int64(s.Days)) {
									t.Errorf("refund line violates floor formula: %+v", ln)
									return
								}
								if ln.Day <= prevRefundDay {
									t.Errorf("refund days not strictly increasing: %d after %d", ln.Day, prevRefundDay)
									return
								}
								prevRefundDay = ln.Day
							}
						}
						if sum != s.Total {
							t.Errorf("invariant broken: recomputed %d != Total %d", sum, s.Total)
							return
						}
						if s.LastDay != prevRefundDay {
							t.Errorf("LastDay %d != last refund day %d", s.LastDay, prevRefundDay)
							return
						}
						if len(s.Lines) == 0 || s.Lines[0].Day != 0 || s.Lines[0].Kind != LineCharge ||
							s.Lines[0].Amount != s.Lines[0].Price {
							t.Errorf("first line is not a day-0 prepay: %+v", s.Lines)
							return
						}
					}
				}
			}(g)
		}
		wg.Wait()

		// Final settle must itself be consistent and leave a fresh period.
		lines, total := e.Settle()
		var sum int64
		for _, ln := range lines {
			sum += ln.Amount
		}
		if sum != total {
			t.Fatalf("run %d: final settle sum %d != total %d", run, sum, total)
		}
		s := e.Query()
		if len(s.Lines) != 1 || s.LastDay != -1 || s.Total != s.Lines[0].Amount {
			t.Fatalf("run %d: post-settle state wrong: %+v", run, s)
		}
	}
	l.printf("OUTPUT 8 runs x %d ops completed; every observed snapshot matched recomputed invariants; final states consistent",
		goroutines*iterations)
	l.printf("JUDGEMENT: concurrent Change/Settle/Query are equivalent to some serial order (single-mutex linearization, copy-on-read)")
	l.dump(t)
}
