package lifecycle

import (
	"errors"
	"testing"
	"time"
)

func TestErrorCategoryAndPriority(t *testing.T) {
	if int(CategoryExpiryGuard) != 1 || int(CategoryChainTrigger) != 2 ||
		int(CategoryActionPrecondition) != 3 || int(CategoryClockRegression) != 4 {
		t.Fatal("fixed-priority category constants changed")
	}

	err1 := fail(ErrExpiryGuard, "x")
	err2 := fail(ErrChainTrigger, "x")
	err3 := fail(ErrActionPrecondition, "x")
	err4 := fail(ErrClockRegression, "x")

	if !errors.Is(err1, ErrExpiryGuard) || !errors.Is(err2, ErrChainTrigger) ||
		!errors.Is(err3, ErrActionPrecondition) || !errors.Is(err4, ErrClockRegression) {
		t.Fatal("errors.Is must match the category sentinels")
	}
	if cat, ok := CategoryOf(err2); !ok || cat != CategoryChainTrigger {
		t.Fatalf("CategoryOf wrong: %v %v", cat, ok)
	}
	if !HigherPriority(err1, err4) || HigherPriority(err4, err1) {
		t.Fatal("expiry guard must outrank clock regression")
	}
}

// TestGuardOutranksClockRegression: when a ring is due and its guard fails
// AND the clock has regressed, the category-1 guard error is reported.
func TestGuardOutranksClockRegression(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{
				Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: time.Minute,
				Guard: GuardFunc(func(ctx *EvalContext) bool {
					v, _ := ctx.Property("ok")
					return v == "yes"
				}),
			},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))

	// Advance, then regress while also leaving the guard unsatisfied.
	clock.Advance(5 * time.Minute)
	clock.Regress(3 * time.Minute) // now t0+2m: ring is due, guard fails
	_, err := eng.Get("d1")
	if !errors.Is(err, ErrExpiryGuard) {
		t.Fatalf("guard must outrank regression, got %v", err)
	}
}

// TestChainFailureOutranksAction: a chained effect failure must surface
// even though the triggering explicit action itself would otherwise run.
func TestChainFailureOutranksAction(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "TA",
		Transitions: []TransitionDef{
			{
				Name: "go", From: "A0", To: "A1", Trigger: TriggerAction, Actions: []string{"go"},
				Chain: []ChainEffect{{Link: "b", TargetFrom: "B0", TransitionName: "b-force"}},
			},
		},
	}))
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "TB",
		Transitions: []TransitionDef{
			{Name: "b-force", From: "B0", To: "B1", Trigger: TriggerForced},
			{Name: "stay", From: "B1", To: "B1", Trigger: TriggerAction, Actions: []string{"zzz"}},
		},
	}))
	mustT(t, eng.RegisterInstance("a", "TA", "A0", t0))
	mustT(t, eng.RegisterInstance("b", "TB", "B1", t0))
	// link bound but forced transition From mismatches actual state.
	mustT(t, eng.SetLink("a", "b", "b"))

	clock.Advance(time.Minute)
	_, err := eng.Act("a", "go")
	if !errors.Is(err, ErrChainTrigger) {
		t.Fatalf("want chain failure, got %v", err)
	}
	// Source action did commit before its chain failed; target unchanged.
	snap, _ := eng.Get("a")
	if snap.State != "A1" {
		t.Fatalf("source action should remain committed, got %s", snap.State)
	}
	bsnap, _ := eng.Get("b")
	if bsnap.State != "B1" {
		t.Fatalf("target must remain at its pre-chain state B1, got %s", bsnap.State)
	}
}

// TestExpiryGuardOutranksAction verifies an expiry-settlement guard stop
// prevents the explicit action from even being considered.
func TestExpiryGuardOutranksAction(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{
				Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: time.Minute,
				Guard: GuardFunc(func(ctx *EvalContext) bool {
					v, _ := ctx.Property("ok")
					return v == "yes"
				}),
			},
			{Name: "ab", From: "A", To: "X", Trigger: TriggerAction, Actions: []string{"ab"}},
			{Name: "bb", From: "B", To: "Y", Trigger: TriggerAction, Actions: []string{"bb"}},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))
	clock.Advance(5 * time.Minute)
	_, err := eng.Act("d1", "ab")
	if !errors.Is(err, ErrExpiryGuard) {
		t.Fatalf("settlement guard failure must surface over action, got %v", err)
	}
}
