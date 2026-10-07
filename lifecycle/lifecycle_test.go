package lifecycle

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

func testEngine(t *testing.T) (*Engine, *ManualClock, *SliceLogger) {
	t.Helper()
	clock := NewManualClock(t0)
	logger := &SliceLogger{}
	return NewEngine(clock.ClockFunc(), logger), clock, logger
}

// TestMultiStepExpiryChain settles a chain of several due expiry rings in
// one read and proves no rings fire before any access happens.
func TestMultiStepExpiryChain(t *testing.T) {
	eng, clock, logger := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
			{Name: "e2", From: "B", To: "C", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
			{Name: "e3", From: "C", To: "D", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))

	clock.Advance(35 * time.Minute)
	if got := len(logger.CallsCopy()); got != 0 {
		t.Fatalf("no access should log settlement, got %d calls", got)
	}

	snap, err := eng.Get("d1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != "D" {
		t.Fatalf("want D after whole chain, got %s", snap.State)
	}
	// Entry time must advance to the last ring's deterministic due instant
	// (09:30), not the observation time (09:35).
	if want := t0.Add(30 * time.Minute); !snap.EnteredAt.Equal(want) {
		t.Fatalf("entered at %s, want %s", snap.EnteredAt, want)
	}

	calls := logger.CallsCopy()
	if len(calls) != 1 || len(calls[0].Rings) != 3 {
		t.Fatalf("want one call settling 3 rings, got %+v", calls)
	}

	// Second read: nothing new to settle (idempotent, no repeat firing).
	snap2, err := eng.Get("d1")
	if err != nil || snap2.Version != snap.Version {
		t.Fatalf("second read changed state: %+v err=%v", snap2, err)
	}
	calls = logger.CallsCopy()
	if len(calls[1].Rings) != 0 {
		t.Fatalf("second read settled rings: %+v", calls[1].Rings)
	}
}

// TestExpiryGuardStopsChain covers requirement: a due expiry ring with a
// failing non-temporal guard stops on the current state and outranks every
// other error class.
func TestExpiryGuardStopsChain(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
			{
				Name: "e2", From: "B", To: "C", Trigger: TriggerExpiry, Duration: 10 * time.Minute,
				Guard: GuardFunc(func(ctx *EvalContext) bool {
					v, _ := ctx.Property("approved")
					return v == "yes"
				}),
			},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))
	clock.Advance(35 * time.Minute)

	_, err := eng.Get("d1")
	if !errors.Is(err, ErrExpiryGuard) {
		t.Fatalf("want ErrExpiryGuard, got %v", err)
	}
	snap, _ := eng.Get("d1")
	if snap.State != "B" {
		t.Fatalf("chain must stop at B, got %s", snap.State)
	}

	// Once the property is satisfied, settlement continues from B.
	mustT(t, eng.SetProperty("d1", "approved", "yes"))
	snap, err = eng.Get("d1")
	if err != nil || snap.State != "C" {
		t.Fatalf("want C after guard satisfied, got %s err=%v", snap.State, err)
	}
}

// TestActionJudgedAfterSettlement: an action available only post-settlement
// must work; one available only pre-settlement must be rejected because
// its precondition is checked after expiry settlement.
func TestActionJudgedAfterSettlement(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
			{Name: "goA", From: "A", To: "X", Trigger: TriggerAction, Actions: []string{"a"}},
			{Name: "goB", From: "B", To: "Y", Trigger: TriggerAction, Actions: []string{"b"}},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))
	clock.Advance(20 * time.Minute)

	snap, err := eng.Act("d1", "a")
	if !errors.Is(err, ErrActionPrecondition) {
		t.Fatalf("stale-state action must be rejected, got state=%s err=%v", snap.State, err)
	}
	if snap.State != "B" {
		t.Fatalf("rejected action must not change state, got %s", snap.State)
	}
	snap, err = eng.Act("d1", "b")
	if err != nil || snap.State != "Y" {
		t.Fatalf("post-settlement action should succeed, got %s err=%v", snap.State, err)
	}
}

// TestRejectedActionChangesNothing verifies properties/states are untouched
// by a rejected action while earlier committed rings remain.
func TestRejectedActionChangesNothing(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))
	clock.Advance(20 * time.Minute)
	// First settle due rings (the rejection comparison must be against the
	// post-settlement state, and the rejected action changes nothing).
	pre, gerr := eng.Get("d1")
	if gerr != nil || pre.State != "B" {
		t.Fatalf("setup: %s %v", pre.State, gerr)
	}
	vBefore, _ := eng.snapVersion("d1")
	_, err := eng.Act("d1", "nope")
	if err == nil {
		t.Fatal("expected rejection")
	}
	vAfter, _ := eng.snapVersion("d1")
	if vBefore != vAfter {
		t.Fatalf("rejected action changed version %d -> %d", vBefore, vAfter)
	}
}

func (e *Engine) snapVersion(id string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ferr := e.mustGetLocked(id)
	if ferr != nil {
		return 0, ferr
	}
	return in.version, nil
}

func mustT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestClockRegression verifies fired rings stay committed after the clock
// moves backwards, and the anomaly is the lowest-priority category.
func TestClockRegression(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
			{Name: "e2", From: "B", To: "C", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))
	clock.Advance(15 * time.Minute)
	snap, err := eng.Get("d1")
	if err != nil || snap.State != "B" {
		t.Fatalf("settlement setup failed: %s %v", snap.State, err)
	}

	clock.Regress(20 * time.Minute)
	snap, err = eng.Get("d1")
	if !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want clock regression error, got %v", err)
	}
	if snap.State != "B" {
		t.Fatalf("committed ring was undone: state=%s", snap.State)
	}

	// Advance forward again; subsequent judgments use the real clock.
	clock.Advance(25 * time.Minute)
	snap, err = eng.Get("d1")
	if err != nil || snap.State != "C" {
		t.Fatalf("later ring should fire after recovery, got %s err=%v", snap.State, err)
	}
}

// TestConcurrentDedup fires the same expiry from many goroutines and
// asserts the ring fires exactly once.
func TestConcurrentDedup(t *testing.T) {
	eng, clock, logger := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))
	clock.Advance(20 * time.Minute)

	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = eng.Get("d1")
		}()
	}
	close(start)
	wg.Wait()

	fired := 0
	for _, c := range logger.CallsCopy() {
		for _, r := range c.Rings {
			if r.Transition == "e1" {
				fired++
			}
		}
	}
	if fired != 1 {
		t.Fatalf("ring fired %d times, want exactly 1", fired)
	}
	hist, _ := eng.History("d1")
	if countEvents(hist, EventExpiry) != 1 {
		t.Fatalf("want one committed expiry event, got %+v", hist)
	}
}

func countEvents(hist []Event, kind EventKind) int {
	n := 0
	for _, ev := range hist {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}
