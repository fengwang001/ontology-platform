package lifecycle

import (
	"sync"
	"testing"
	"time"
)

func t0() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// chainType builds A --t1(1h)--> B --t2(2h)--> C (terminal).
func chainType() *ObjectType {
	return &ObjectType{
		ID:      "chain",
		States:  []StateID{"A", "B", "C"},
		Initial: "A",
		Transitions: []Transition{
			{ID: "t1", From: "A", To: "B", Kind: KindTimeTransition, Duration: time.Hour},
			{ID: "t2", From: "B", To: "C", Kind: KindTimeTransition, Duration: 2 * time.Hour},
		},
	}
}

func TestMultiStepLazySettlement(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	if err := eng.RegisterType(chainType()); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("chain", "x", "", nil); err != nil {
		t.Fatal(err)
	}

	clk.Set(t0().Add(30 * time.Minute))
	eng.ResetMetrics()
	snap, errs := eng.Read("x")
	if errs.Primary() != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if snap.State != "A" || eng.Metrics().NewlySettledSteps != 0 {
		t.Fatalf("want A with 0 steps, got %s steps=%d", snap.State, eng.Metrics().NewlySettledSteps)
	}

	// Past BOTH deadlines: one access drains the full chain A->B->C,
	// through the intermediate state, never A->C directly.
	clk.Set(t0().Add(4 * time.Hour))
	snap, errs = eng.Read("x")
	if errs.Primary() != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if snap.State != "C" {
		t.Fatalf("want C, got %s", snap.State)
	}
	if steps := eng.Metrics().NewlySettledSteps; steps != 2 {
		t.Fatalf("want 2 newly settled steps, got %d", steps)
	}
	hist, _ := eng.History("x")
	if len(hist) != 3 || hist[1].To != "B" || hist[2].To != "C" {
		t.Fatalf("history must record full chain via B: %+v", hist)
	}
	if !hist[1].At.Equal(t0().Add(time.Hour)) || !hist[2].At.Equal(t0().Add(3*time.Hour)) {
		t.Fatalf("logical entry times wrong: %v %v", hist[1].At, hist[2].At)
	}
	if !hist[1].SettledAt.Equal(t0().Add(4 * time.Hour)) {
		t.Fatalf("settled-at must be the access time, got %v", hist[1].SettledAt)
	}

	eng.ResetMetrics()
	if _, errs := eng.Read("x"); errs.Primary() != nil || eng.Metrics().NewlySettledSteps != 0 {
		t.Fatalf("second access must settle nothing")
	}
}

func TestActionAfterSettlement(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	ot := chainType()
	ot.Transitions = append(ot.Transitions, Transition{ID: "reset", From: "C", To: "A", Kind: KindActionTransition})
	if err := eng.RegisterType(ot); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("chain", "x", "", nil); err != nil {
		t.Fatal(err)
	}

	clk.Set(t0().Add(30 * time.Minute))
	if _, errs := eng.Action("x", "reset"); errs.Primary() == nil || errs.Primary().Kind != KindActionDenied {
		t.Fatalf("want action denied from pre-settlement A, got %v", errs)
	}

	clk.Set(t0().Add(4 * time.Hour))
	snap, errs := eng.Action("x", "reset")
	if errs.Primary() != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if snap.State != "A" {
		t.Fatalf("want A after reset, got %s", snap.State)
	}
	hist, _ := eng.History("x")
	if len(hist) != 4 || hist[3].Kind != EntryAction || !hist[3].At.Equal(clk.Now()) {
		t.Fatalf("history wrong after action: %+v", hist)
	}
}

func TestTimeGuardHaltsChainAndPreservesClock(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	ot := &ObjectType{
		ID:      "g",
		States:  []StateID{"A", "B", "C"},
		Initial: "A",
		Transitions: []Transition{
			{
				ID: "t1", From: "A", To: "B", Kind: KindTimeTransition, Duration: time.Hour,
				Guard: func(c GuardContext) bool { v, _ := c.Prop("approved"); return v == true },
			},
			{ID: "t2", From: "B", To: "C", Kind: KindTimeTransition, Duration: time.Hour},
		},
	}
	if err := eng.RegisterType(ot); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("g", "x", "", nil); err != nil {
		t.Fatal(err)
	}
	clk.Set(t0().Add(5 * time.Hour))
	snap, errs := eng.Read("x")
	if p := errs.Primary(); p == nil || p.Kind != KindGuardBlocked {
		t.Fatalf("want guard blocked, got %v", errs)
	}
	if snap.State != "A" || !snap.EnteredAt.Equal(t0()) {
		t.Fatalf("state/entry time must be unchanged, got %s %v", snap.State, snap.EnteredAt)
	}
	hist, _ := eng.History("x")
	if len(hist) != 1 {
		t.Fatalf("blocked settlement must append nothing, got %d entries", len(hist))
	}

	eng.mu.Lock()
	eng.instances["x"].props["approved"] = true
	eng.mu.Unlock()
	snap, errs = eng.Read("x")
	if errs.Primary() != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if snap.State != "C" {
		t.Fatalf("want full chain to C once guard passes, got %s", snap.State)
	}
}

func TestClockRollbackDoesNotUndoSettlement(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	if err := eng.RegisterType(chainType()); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("chain", "x", "", nil); err != nil {
		t.Fatal(err)
	}
	clk.Set(t0().Add(4 * time.Hour))
	if snap, _ := eng.Read("x"); snap.State != "C" {
		t.Fatalf("setup: want C, got %s", snap.State)
	}

	clk.Set(t0())
	snap, errs := eng.Read("x")
	if snap.State != "C" {
		t.Fatalf("settled state must survive rollback, got %s", snap.State)
	}
	if p := errs.Primary(); p == nil || p.Kind != KindClockRegression {
		t.Fatalf("want clock regression error, got %v", errs)
	}
	hist, _ := eng.History("x")
	if len(hist) != 3 {
		t.Fatalf("rollback must not rewrite history, got %d entries", len(hist))
	}

	clk.Set(t0().Add(5 * time.Hour))
	if _, errs := eng.Read("x"); errs.Primary() != nil {
		t.Fatalf("want no error after recovery, got %v", errs)
	}
}

func TestConcurrentSettlementDedup(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	if err := eng.RegisterType(chainType()); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("chain", "x", "", nil); err != nil {
		t.Fatal(err)
	}
	clk.Set(t0().Add(4 * time.Hour))

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, _ := eng.Read("x")
			if snap.State != "C" {
				t.Errorf("want C, got %s", snap.State)
			}
		}()
	}
	wg.Wait()
	hist, _ := eng.History("x")
	if len(hist) != 3 {
		t.Fatalf("exactly one settlement must happen, history len=%d", len(hist))
	}
	if steps := eng.Metrics().NewlySettledSteps; steps != 2 {
		t.Fatalf("exactly 2 steps may be settled under concurrency, got %d", steps)
	}
}

func TestSettlementCostIndependentOfHistoryLength(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	ot := &ObjectType{
		ID:      "loop",
		States:  []StateID{"S"},
		Initial: "S",
		Transitions: []Transition{
			{ID: "tick", From: "S", To: "S", Kind: KindTimeTransition, Duration: time.Hour},
		},
	}
	if err := eng.RegisterType(ot); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("loop", "x", "", nil); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 50; i++ {
		clk.Set(t0().Add(time.Duration(i) * time.Hour))
		if _, errs := eng.Read("x"); errs.Primary() != nil {
			t.Fatal(errs)
		}
	}
	hist, _ := eng.History("x")
	if len(hist) != 51 {
		t.Fatalf("setup history len=%d", len(hist))
	}
	eng.ResetMetrics()
	clk.Set(t0().Add(51 * time.Hour))
	if _, errs := eng.Read("x"); errs.Primary() != nil {
		t.Fatal(errs)
	}
	m := eng.Metrics()
	if m.NewlySettledSteps != 1 {
		t.Fatalf("want exactly 1 new step over 50-history, got %d", m.NewlySettledSteps)
	}
	if m.HistoryScans != 0 {
		t.Fatalf("settlement scanned history %d times; must not", m.HistoryScans)
	}
}

func TestStateAtReplayMatchesLazyResult(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	if err := eng.RegisterType(chainType()); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("chain", "x", "", nil); err != nil {
		t.Fatal(err)
	}
	clk.Set(t0().Add(2 * time.Hour))
	if snap, _ := eng.Read("x"); snap.State != "B" {
		t.Fatalf("want B, got %s", snap.State)
	}
	clk.Set(t0().Add(5 * time.Hour))
	if _, errs := eng.Read("x"); errs.Primary() != nil {
		t.Fatal(errs)
	}
	probe := []struct {
		at   time.Time
		want StateID
	}{
		{t0().Add(30 * time.Minute), "A"},
		{t0().Add(time.Hour), "B"},
		{t0().Add(2 * time.Hour), "B"},
		{t0().Add(3 * time.Hour), "C"},
		{t0().Add(100 * time.Hour), "C"},
	}
	for _, p := range probe {
		got, err := eng.StateAt("x", p.at)
		if err != nil || got != p.want {
			t.Fatalf("StateAt(%v)=%s,%v want %s", p.at, got, err, p.want)
		}
	}
	// Replay at 2h equals the state a real lazy access at 2h produced (B).
	if got, _ := eng.StateAt("x", t0().Add(2*time.Hour)); got != "B" {
		t.Fatalf("historical replay diverged from actual lazy result: %s", got)
	}
}

func TestErrorPriorityOrdering(t *testing.T) {
	// Fixed priority independent of reporting order: guard > cascade >
	// action > clock, even when errors are listed low-priority-first.
	lists := [][]ErrorKind{
		{KindClockRegression, KindActionDenied, KindCascadeFailed, KindGuardBlocked},
		{KindActionDenied, KindCascadeFailed},
		{KindClockRegression, KindActionDenied},
		{KindClockRegression, KindGuardBlocked, KindCascadeFailed},
	}
	want := []ErrorKind{KindGuardBlocked, KindCascadeFailed, KindActionDenied, KindGuardBlocked}
	for i, ks := range lists {
		var list ErrorList
		for _, k := range ks {
			list = append(list, &Error{Kind: k})
		}
		if got := list.Primary().Kind; got != want[i] {
			t.Fatalf("case %d: want %s, got %s", i, want[i], got)
		}
	}
}
