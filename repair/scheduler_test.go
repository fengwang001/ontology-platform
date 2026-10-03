package repair_test

import (
	"reflect"
	"sync"
	"testing"

	"ontology/repair"
)

func mustNew(t *testing.T, k, m, tau int, A, R, Cap, D, Q int64) *repair.Scheduler {
	t.Helper()
	s, err := repair.New(k, m, tau, A, R, Cap, D, Q)
	if err != nil {
		t.Fatalf("New(k=%d,m=%d,tau=%d,A=%d,R=%d,Cap=%d,D=%d,Q=%d): %v", k, m, tau, A, R, Cap, D, Q, err)
	}
	return s
}

func mustAdd(t *testing.T, s *repair.Scheduler, now, id int64) {
	t.Helper()
	if err := s.AddStripe(now, id); err != nil {
		t.Fatalf("AddStripe(now=%d,id=%d): %v", now, id, err)
	}
	t.Logf("AddStripe(now=%d,id=%d): ok", now, id)
}

func mustLose(t *testing.T, s *repair.Scheduler, now, id int64, shard int) {
	t.Helper()
	if err := s.Lose(now, id, shard); err != nil {
		t.Fatalf("Lose(now=%d,id=%d,shard=%d): %v", now, id, shard, err)
	}
	t.Logf("Lose(now=%d,id=%d,shard=%d): ok", now, id, shard)
}

func mustTick(t *testing.T, s *repair.Scheduler, now int64) repair.Report {
	t.Helper()
	rep, err := s.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	return rep
}

func wantReport(t *testing.T, label string, got, want repair.Report) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %+v, want %+v", label, got, want)
	}
	t.Logf("%s: report=%+v 与逐步推演一致", label, got)
}

func wantStatus(t *testing.T, s *repair.Scheduler, id int64, want repair.Status) {
	t.Helper()
	got, err := s.Status(id)
	if err != nil {
		t.Fatalf("Status(%d): %v", id, err)
	}
	if got != want {
		t.Fatalf("Status(%d): got %+v, want %+v", id, got, want)
	}
	t.Logf("Status(%d)=%+v 与逐步推演一致", id, got)
}

func wantTokens(t *testing.T, s *repair.Scheduler, want int64) {
	t.Helper()
	if got := s.Tokens(); got != want {
		t.Fatalf("Tokens(): got %d, want %d", got, want)
	}
	t.Logf("Tokens()=%d 与逐步推演一致", want)
}

func wantErr(t *testing.T, label string, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got err %v, want %v", label, got, want)
	}
	t.Logf("%s: 拒绝为 %v，符合错误次序", label, want)
}

func TestNewParamValidation(t *testing.T) {
	bad := []struct {
		k, m, tau       int
		A, R, Cap, D, Q int64
	}{
		{0, 1, 0, 1, 1, 1, 1, 1},       // k < 1
		{1, 0, 0, 1, 1, 1, 1, 1},       // m < 1
		{16, 17, 0, 1, 1, 1, 1, 1},     // n > 32
		{1, 1, -1, 1, 1, 1, 1, 1},      // tau < 0
		{1, 1, 2, 1, 1, 1, 1, 1},       // tau > m
		{1, 1, 0, 0, 1, 1, 1, 1},       // A < 1
		{1, 1, 0, 1, 0, 1, 1, 1},       // R < 1
		{1, 1, 0, 1, 5, 4, 1, 1},       // Cap < R
		{1, 1, 0, 1, 1, 1e9 + 1, 1, 1}, // Cap > 1e9
		{1, 1, 0, 1, 1, 1, 0, 1},       // D < 1
		{1, 1, 0, 1, 1, 1, 1, 0},       // Q < 1
	}
	for i, p := range bad {
		if _, err := repair.New(p.k, p.m, p.tau, p.A, p.R, p.Cap, p.D, p.Q); err != repair.ErrParam {
			t.Fatalf("case %d: got %v, want ErrParam", i, err)
		}
		t.Logf("case %d: 参数 %+v 判定 ErrParam", i, p)
	}
	if _, err := repair.New(1, 1, 0, 1, 1, 1, 1, 1); err != nil {
		t.Fatalf("minimal valid params: %v", err)
	}
	if _, err := repair.New(16, 16, 16, 1, 1, 1e9, 1, 1); err != nil {
		t.Fatalf("boundary valid params n=32, Cap=1e9: %v", err)
	}
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	s := mustNew(t, 4, 2, 1, 100, 4, 8, 3, 2)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustLose(t, s, 1, 1, 0)
	mustLose(t, s, 2, 2, 1)
	mustLose(t, s, 2, 2, 4)
	wantStatus(t, s, 2, repair.Status{State: repair.Degraded, Alive: 4, Lost: 2, Margin: 0, FirstLost: 2})

	rep := mustTick(t, s, 2)
	// tokens = min(8, 0+4*2) = 8; stripe 2 (margin 0) before stripe 1 (margin 1).
	wantReport(t, "Tick(2)", rep, repair.Report{
		Completed: []int64{},
		Started: []repair.Start{
			{Stripe: 2, Targets: []int{1, 4}, Finish: 5},
			{Stripe: 1, Targets: []int{0}, Finish: 5},
		},
	})
	wantTokens(t, s, 0)
	wantStatus(t, s, 1, repair.Status{State: repair.Repairing, Alive: 5, Lost: 0, Rebuilding: 1, Margin: 1, FirstLost: 1})

	rep = mustTick(t, s, 5)
	// Same finish 5 for both; completion order by (finish, id) => 1 then 2.
	wantReport(t, "Tick(5)", rep, repair.Report{
		Completed: []int64{1, 2},
		Started:   []repair.Start{},
	})
	wantTokens(t, s, 8) // min(8, 0+4*3)
	wantStatus(t, s, 1, repair.Status{State: repair.Healthy, Alive: 6, Margin: 2, FirstLost: -1})
	wantStatus(t, s, 2, repair.Status{State: repair.Healthy, Alive: 6, Margin: 2, FirstLost: -1})
}

// Tier 0 (aged) stripes are scheduled before tier 1 stripes with a smaller
// margin.
func TestAgedBeatsSmallerMargin(t *testing.T) {
	s := mustNew(t, 4, 3, 0, 10, 10, 100, 5, 2)
	mustAdd(t, s, 0, 1)     // X
	mustAdd(t, s, 0, 2)     // Y
	mustLose(t, s, 0, 1, 0) // X: margin 2, firstLost 0
	mustLose(t, s, 9, 2, 0)
	mustLose(t, s, 9, 2, 1)
	mustLose(t, s, 9, 2, 2) // Y: margin 0, firstLost 9
	rep := mustTick(t, s, 10)
	// X aged 10 >= A=10 => tier 0; Y tier 1. X starts first despite margin 2 > 0.
	wantReport(t, "Tick(10)", rep, repair.Report{
		Completed: []int64{},
		Started: []repair.Start{
			{Stripe: 1, Targets: []int{0}, Finish: 15},
			{Stripe: 2, Targets: []int{0, 1, 2}, Finish: 15},
		},
	})
}

// margin == tau is eligible; margin == tau+1 is not (while not aged).
func TestMarginBoundaryTau(t *testing.T) {
	s := mustNew(t, 4, 3, 1, 1000, 4, 4, 5, 2)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustLose(t, s, 1, 1, 0)
	mustLose(t, s, 1, 1, 1) // stripe 1: alive 5, margin 1 == tau
	mustLose(t, s, 1, 2, 0) // stripe 2: alive 6, margin 2 == tau+1

	rep := mustTick(t, s, 1)
	// tokens = min(4, 4) = 4 = k: exactly one repair; only stripe 1 eligible.
	wantReport(t, "Tick(1)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 1, Targets: []int{0, 1}, Finish: 6}},
	})
	wantTokens(t, s, 0)

	rep = mustTick(t, s, 2)
	// Stripe 2 still margin 2 > tau and age 1 < A: not a candidate.
	wantReport(t, "Tick(2)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantStatus(t, s, 2, repair.Status{State: repair.Degraded, Alive: 6, Lost: 1, Margin: 2, FirstLost: 1})
}

// now - firstLost == A forces repair even when margin > tau.
func TestAgeBoundaryA(t *testing.T) {
	s := mustNew(t, 2, 2, 0, 10, 1, 100, 3, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 0, 1, 0) // margin 1 > tau 0

	rep := mustTick(t, s, 9)
	// age 9 < A=10: not eligible.
	wantReport(t, "Tick(9)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 9)

	rep = mustTick(t, s, 10)
	// age 10 >= A=10 (equality holds): eligible, tier 0.
	wantReport(t, "Tick(10)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 1, Targets: []int{0}, Finish: 13}},
	})
	wantTokens(t, s, 8)
}

// Equal margin: smaller firstLost first; equal firstLost: smaller id first.
func TestSameMarginOrdering(t *testing.T) {
	s := mustNew(t, 2, 2, 2, 1000, 2, 2, 5, 3)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustAdd(t, s, 0, 3)
	mustLose(t, s, 1, 3, 0) // firstLost 1
	mustLose(t, s, 3, 1, 0) // firstLost 3
	mustLose(t, s, 3, 2, 0) // firstLost 3

	rep := mustTick(t, s, 4)
	// tokens = min(2, 2*4) = 2 = k: one repair; all margin 1; firstLost 1 wins.
	wantReport(t, "Tick(4)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 3, Targets: []int{0}, Finish: 9}},
	})
	rep = mustTick(t, s, 5)
	// Stripes 1 and 2 tie on firstLost 3: smaller id first.
	wantReport(t, "Tick(5)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 1, Targets: []int{0}, Finish: 10}},
	})
	rep = mustTick(t, s, 6)
	wantReport(t, "Tick(6)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 2, Targets: []int{0}, Finish: 11}},
	})
}

// The Q check runs before the token check: with Q stripes in flight nothing
// starts even when tokens are plentiful; at Q-1 in flight one more starts.
func TestInFlightLimitQ(t *testing.T) {
	s := mustNew(t, 2, 2, 2, 1000, 100, 1000, 10, 2)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustAdd(t, s, 0, 3)
	mustLose(t, s, 0, 1, 0)
	mustLose(t, s, 0, 2, 0)
	mustLose(t, s, 0, 3, 0)

	rep := mustTick(t, s, 1)
	// tokens = 100; stripes 1 and 2 start; in-flight reaches Q=2 and stops.
	wantReport(t, "Tick(1)", rep, repair.Report{
		Completed: []int64{},
		Started: []repair.Start{
			{Stripe: 1, Targets: []int{0}, Finish: 11},
			{Stripe: 2, Targets: []int{0}, Finish: 11},
		},
	})
	wantTokens(t, s, 96)

	rep = mustTick(t, s, 2)
	// In-flight == Q: stripe 3 waits although tokens suffice.
	wantReport(t, "Tick(2)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 196)

	rep = mustTick(t, s, 11)
	// Both repairs finish; in-flight drops to 0 (<= Q-1) so stripe 3 starts.
	wantReport(t, "Tick(11)", rep, repair.Report{
		Completed: []int64{1, 2},
		Started:   []repair.Start{{Stripe: 3, Targets: []int{0}, Finish: 21}},
	})
	wantTokens(t, s, 998) // min(1000, 196+900) - 2
}

// tokens == k exactly pays for exactly one repair.
func TestTokensExactlyK(t *testing.T) {
	s := mustNew(t, 3, 1, 1, 100, 1, 10, 2, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 0, 1, 0)
	rep := mustTick(t, s, 3)
	// tokens = min(10, 3) = 3 == k: repair starts, balance drops to 0.
	wantReport(t, "Tick(3)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 1, Targets: []int{0}, Finish: 5}},
	})
	wantTokens(t, s, 0)

	// tokens < k: no repair starts (repairs are never skipped or partial).
	mustAdd(t, s, 3, 2)
	mustLose(t, s, 3, 2, 0)
	rep = mustTick(t, s, 4)
	wantReport(t, "Tick(4)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 1)
}

// Accrual spans multiple time units and is capped at Cap; rejected
// operations move neither the clock nor the accrual baseline.
func TestTokenCapAndDelta(t *testing.T) {
	s := mustNew(t, 2, 1, 1, 100, 5, 12, 1, 1)
	mustAdd(t, s, 0, 1)
	rep := mustTick(t, s, 10)
	wantReport(t, "Tick(10)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 12) // min(12, 5*10)
	rep = mustTick(t, s, 13)
	wantReport(t, "Tick(13)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 12) // min(12, 12+15): capped

	// A rejected operation at now=20 does not advance lastTick.
	wantErr(t, "Lose(20, unknown)", s.Lose(20, 99, 0), repair.ErrUnknown)
	rep = mustTick(t, s, 20)
	wantReport(t, "Tick(20)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 12) // min(12, 12+5*7): delta measured from lastTick=13

	// A rejected Tick (clock backwards) does not advance lastTick either.
	_, tickErr := s.Tick(19)
	wantErr(t, "Tick(19)", tickErr, repair.ErrClock)
	rep = mustTick(t, s, 25)
	wantReport(t, "Tick(25)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 12) // delta measured from lastTick=20

	// Non-Tick operations do not accrue tokens.
	s2 := mustNew(t, 2, 1, 1, 100, 5, 100, 1, 1)
	mustTick(t, s2, 5)
	wantTokens(t, s2, 25)
	mustAdd(t, s2, 10, 7)
	mustTick(t, s2, 10)
	wantTokens(t, s2, 50) // 25 + 5*(10-5): AddStripe did not touch lastTick
}

// Two repairs finishing at the same time complete in (finish, id) order.
func TestSameFinishOrder(t *testing.T) {
	s := mustNew(t, 2, 2, 2, 1000, 100, 1000, 4, 5)
	mustAdd(t, s, 0, 5)
	mustAdd(t, s, 0, 3)
	mustLose(t, s, 0, 5, 0)
	mustLose(t, s, 0, 3, 0)
	rep := mustTick(t, s, 1)
	// Equal margin and firstLost: id order 3 then 5; both finish at 5.
	wantReport(t, "Tick(1)", rep, repair.Report{
		Completed: []int64{},
		Started: []repair.Start{
			{Stripe: 3, Targets: []int{0}, Finish: 5},
			{Stripe: 5, Targets: []int{0}, Finish: 5},
		},
	})
	rep = mustTick(t, s, 5)
	wantReport(t, "Tick(5)", rep, repair.Report{
		Completed: []int64{3, 5},
		Started:   []repair.Start{},
	})
	wantStatus(t, s, 3, repair.Status{State: repair.Healthy, Alive: 4, Margin: 2, FirstLost: -1})
	wantStatus(t, s, 5, repair.Status{State: repair.Healthy, Alive: 4, Margin: 2, FirstLost: -1})
}

// Losing another shard during repair: alive < k with a repair in flight is
// not Dead; after completion the stripe stays Degraded and firstLost is
// unchanged.
func TestLossDuringRepair(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 100, 10, 100, 5, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 0, 1, 0)
	rep := mustTick(t, s, 1)
	wantReport(t, "Tick(1)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 1, Targets: []int{0}, Finish: 6}},
	})
	mustLose(t, s, 2, 1, 1)
	// alive=1 < k=2 but a repair is in flight: not Dead.
	wantStatus(t, s, 1, repair.Status{State: repair.Repairing, Alive: 1, Lost: 1, Rebuilding: 1, Margin: -1, FirstLost: 0})

	rep = mustTick(t, s, 6)
	// Shard 0 back Alive: alive=2, one Lost remains. Degraded, not Dead, and
	// firstLost keeps its original value 0. The stripe is a candidate again
	// in the same Tick (margin 0 <= tau) and is rescheduled.
	wantReport(t, "Tick(6)", rep, repair.Report{
		Completed: []int64{1},
		Started:   []repair.Start{{Stripe: 1, Targets: []int{1}, Finish: 11}},
	})
	wantStatus(t, s, 1, repair.Status{State: repair.Repairing, Alive: 2, Lost: 0, Rebuilding: 1, Margin: 0, FirstLost: 0})

	rep = mustTick(t, s, 11)
	wantReport(t, "Tick(11)", rep, repair.Report{Completed: []int64{1}, Started: []repair.Start{}})
	wantStatus(t, s, 1, repair.Status{State: repair.Healthy, Alive: 3, Margin: 1, FirstLost: -1})
}

// alive < k after a repair completes turns the stripe Dead; Dead is sticky,
// rejects Lose, and is never scheduled again.
func TestDeadAfterCompletion(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 100, 10, 100, 5, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 0, 1, 0)
	rep := mustTick(t, s, 1)
	wantReport(t, "Tick(1)", rep, repair.Report{
		Completed: []int64{},
		Started:   []repair.Start{{Stripe: 1, Targets: []int{0}, Finish: 6}},
	})
	mustLose(t, s, 2, 1, 1)
	mustLose(t, s, 2, 1, 2)
	// alive=0 < k=2 but repair in flight: delayed judgement, still Repairing.
	wantStatus(t, s, 1, repair.Status{State: repair.Repairing, Alive: 0, Lost: 2, Rebuilding: 1, Margin: -2, FirstLost: 0})

	rep = mustTick(t, s, 6)
	// Shard 0 returns: alive=1 < k=2 and no repair in flight => Dead.
	wantReport(t, "Tick(6)", rep, repair.Report{Completed: []int64{1}, Started: []repair.Start{}})
	wantStatus(t, s, 1, repair.Status{State: repair.Dead, Alive: 1, Lost: 2, Margin: -1, FirstLost: 0})

	// Dead stripes reject Lose and never enter the candidate set.
	wantErr(t, "Lose on Dead", s.Lose(7, 1, 0), repair.ErrDead)
	tokensBefore := s.Tokens()
	rep = mustTick(t, s, 7)
	wantReport(t, "Tick(7)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	if got, want := s.Tokens(), tokensBefore+10; got != want {
		t.Fatalf("Tokens(): got %d, want %d (Dead stripe must not consume tokens)", got, want)
	}
	wantStatus(t, s, 1, repair.Status{State: repair.Dead, Alive: 1, Lost: 2, Margin: -1, FirstLost: 0})
}

// alive < k without a repair in flight turns Dead immediately after Lose.
func TestDeadImmediateOnLose(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 100, 10, 100, 5, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 0, 1, 0)
	mustLose(t, s, 0, 1, 1) // alive=1 < k=2, no repair in flight
	wantStatus(t, s, 1, repair.Status{State: repair.Dead, Alive: 1, Lost: 2, Margin: -1, FirstLost: 0})
	wantErr(t, "Lose on Dead", s.Lose(1, 1, 2), repair.ErrDead)
	rep := mustTick(t, s, 1)
	wantReport(t, "Tick(1)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantStatus(t, s, 1, repair.Status{State: repair.Dead, Alive: 1, Lost: 2, Margin: -1, FirstLost: 0})
}

// Every rejected operation leaves state, clock and the accrual baseline
// untouched.
func TestRejectedOpsNoStateChange(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 100, 5, 100, 2, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 1, 1, 0)
	mustTick(t, s, 5) // tokens = 25, repair of shard 0 starts (finish 7)
	wantTokens(t, s, 23)
	before, err := s.Status(1)
	if err != nil {
		t.Fatalf("Status(1): %v", err)
	}

	rejections := []struct {
		label string
		op    func() error
		want  error
	}{
		{"AddStripe now<0", func() error { return s.AddStripe(-1, 2) }, repair.ErrParam},
		{"AddStripe id<=0", func() error { return s.AddStripe(6, 0) }, repair.ErrParam},
		{"AddStripe clock backwards", func() error { return s.AddStripe(4, 2) }, repair.ErrClock},
		{"AddStripe exists", func() error { return s.AddStripe(6, 1) }, repair.ErrExists},
		{"Lose now<0", func() error { return s.Lose(-1, 1, 1) }, repair.ErrParam},
		{"Lose shard out of range", func() error { return s.Lose(6, 1, 3) }, repair.ErrParam},
		{"Lose clock backwards", func() error { return s.Lose(4, 1, 1) }, repair.ErrClock},
		{"Lose unknown stripe", func() error { return s.Lose(6, 99, 1) }, repair.ErrUnknown},
		{"Lose shard not Alive", func() error { return s.Lose(6, 1, 0) }, repair.ErrNotAlive},
		{"Tick now<0", func() error { _, err := s.Tick(-1); return err }, repair.ErrParam},
		{"Tick clock backwards", func() error { _, err := s.Tick(4); return err }, repair.ErrClock},
		{"Status id<=0", func() error { _, err := s.Status(0); return err }, repair.ErrParam},
		{"Status unknown", func() error { _, err := s.Status(99); return err }, repair.ErrUnknown},
	}
	for _, r := range rejections {
		wantErr(t, r.label, r.op(), r.want)
		after, err := s.Status(1)
		if err != nil {
			t.Fatalf("Status(1) after %s: %v", r.label, err)
		}
		if after != before {
			t.Fatalf("%s changed stripe state: before %+v, after %+v", r.label, before, after)
		}
		wantTokens(t, s, 23)
	}

	// The clock did not move: an operation at now=5 is still accepted.
	mustLose(t, s, 5, 1, 1)
	// lastTick did not move: accrual is measured from the last successful Tick.
	rep := mustTick(t, s, 6)
	wantReport(t, "Tick(6)", rep, repair.Report{Completed: []int64{}, Started: []repair.Start{}})
	wantTokens(t, s, 28) // 23 + 5*(6-5)
}

// Error precedence: ErrParam > ErrClock > ErrUnknown > ErrDead > ErrNotAlive.
func TestErrorPrecedence(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 100, 5, 100, 2, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 0, 1, 0)
	mustLose(t, s, 0, 1, 1) // stripe 1 is Dead now
	mustTick(t, s, 5)

	// ErrParam beats ErrClock and ErrUnknown.
	wantErr(t, "Lose bad shard + backwards clock", s.Lose(4, 1, 9), repair.ErrParam)
	wantErr(t, "Lose bad shard + unknown id", s.Lose(6, 99, 9), repair.ErrParam)
	_, negTickErr := s.Tick(-1)
	wantErr(t, "Tick negative now", negTickErr, repair.ErrParam)
	// ErrClock beats ErrUnknown and ErrDead.
	wantErr(t, "Lose backwards clock + unknown id", s.Lose(4, 99, 0), repair.ErrClock)
	wantErr(t, "Lose backwards clock + dead stripe", s.Lose(4, 1, 2), repair.ErrClock)
	// ErrUnknown beats ErrDead/ErrNotAlive by id resolution.
	wantErr(t, "Lose unknown id", s.Lose(6, 99, 0), repair.ErrUnknown)
	// ErrDead beats ErrNotAlive.
	wantErr(t, "Lose dead stripe with lost shard", s.Lose(6, 1, 0), repair.ErrDead)
	wantErr(t, "Lose dead stripe with alive shard", s.Lose(6, 1, 2), repair.ErrDead)
}

// Concurrent calls behave as some serial order: the scheduler stays
// consistent and no goroutine observes a partial Tick.
func TestConcurrentCalls(t *testing.T) {
	s := mustNew(t, 2, 2, 1, 50, 3, 100, 2, 2)
	const stripes = 8
	for id := int64(1); id <= stripes; id++ {
		mustAdd(t, s, 0, id)
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(i) // identical, non-decreasing across goroutines
				id := int64((g+i)%stripes + 1)
				_ = s.Lose(now, id, (g+i)%4)
				_, _ = s.Tick(now)
				_, _ = s.Status(id)
				_ = s.Tokens()
			}
		}(g)
	}
	wg.Wait()
	// Invariants after the concurrent phase.
	for id := int64(1); id <= stripes; id++ {
		st, err := s.Status(id)
		if err != nil {
			t.Fatalf("Status(%d): %v", id, err)
		}
		if st.Alive+st.Lost+st.Rebuilding != 4 {
			t.Fatalf("Status(%d): shard counts %+v do not sum to n=4", id, st)
		}
		if st.Margin != st.Alive-2 {
			t.Fatalf("Status(%d): margin %d != alive-k", id, st.Margin)
		}
		if st.State == repair.Dead && st.Rebuilding != 0 {
			t.Fatalf("Status(%d): Dead stripe with in-flight repair: %+v", id, st)
		}
		if st.Lost == 0 && st.Rebuilding == 0 && st.State != repair.Dead && st.FirstLost != -1 {
			t.Fatalf("Status(%d): undamaged stripe keeps firstLost: %+v", id, st)
		}
	}
	if got := s.Tokens(); got < 0 || got > 100 {
		t.Fatalf("Tokens()=%d outside [0, Cap]", got)
	}
}

// Replaying the same operation sequence yields identical reports and states.
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]repair.Report, []repair.Status, int64) {
		s := mustNew(t, 3, 2, 1, 4, 2, 9, 3, 2)
		mustAdd(t, s, 0, 1)
		mustAdd(t, s, 0, 2)
		mustAdd(t, s, 1, 3)
		mustLose(t, s, 2, 1, 0)
		mustLose(t, s, 3, 2, 4)
		mustLose(t, s, 4, 1, 1)
		mustLose(t, s, 5, 3, 2)
		var reps []repair.Report
		for now := int64(6); now <= 30; now++ {
			rep, err := s.Tick(now)
			if err != nil {
				t.Fatalf("Tick(%d): %v", now, err)
			}
			reps = append(reps, rep)
		}
		var stats []repair.Status
		for id := int64(1); id <= 3; id++ {
			st, err := s.Status(id)
			if err != nil {
				t.Fatalf("Status(%d): %v", id, err)
			}
			stats = append(stats, st)
		}
		return reps, stats, s.Tokens()
	}
	reps1, stats1, tokens1 := run()
	reps2, stats2, tokens2 := run()
	if !reflect.DeepEqual(reps1, reps2) || !reflect.DeepEqual(stats1, stats2) || tokens1 != tokens2 {
		t.Fatalf("replay mismatch:\nrun1: %v %v %d\nrun2: %v %v %d", reps1, stats1, tokens1, reps2, stats2, tokens2)
	}
	t.Logf("两次重放结果完全一致: reports=%d statuses=%v tokens=%d", len(reps1), stats1, tokens1)
}
