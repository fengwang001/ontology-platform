package lifecycle

import (
	"errors"
	"testing"
	"time"
)

func chainTypes(t *testing.T, eng *Engine) {
	t.Helper()
	// A --expiry--> ... forces B (via link "b"), which forces C (via "c").
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "TA",
		Transitions: []TransitionDef{
			{
				Name: "a-expire", From: "A0", To: "A1", Trigger: TriggerExpiry, Duration: 10 * time.Minute,
				Chain: []ChainEffect{{Link: "b", TargetFrom: "B0", TransitionName: "b-force"}},
			},
		},
	}))
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "TB",
		Transitions: []TransitionDef{
			{
				Name: "b-force", From: "B0", To: "B1", Trigger: TriggerForced,
				Chain: []ChainEffect{{Link: "c", TargetFrom: "C0", TransitionName: "c-force"}},
			},
			{Name: "b-expire", From: "B1", To: "B2", Trigger: TriggerExpiry, Duration: 15 * time.Minute},
		},
	}))
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "TC",
		Transitions: []TransitionDef{
			{Name: "c-force", From: "C0", To: "C1", Trigger: TriggerForced},
			{Name: "c-expire", From: "C1", To: "C2", Trigger: TriggerExpiry, Duration: 20 * time.Minute},
		},
	}))
}

// TestCrossInstanceChainOrder verifies that accessing a middle instance
// still settles predecessors first, and the whole chain follows declared
// order A, B, C.
func TestCrossInstanceChainOrder(t *testing.T) {
	eng, clock, logger := testEngine(t)
	chainTypes(t, eng)
	mustT(t, eng.RegisterInstance("a", "TA", "A0", t0))
	mustT(t, eng.RegisterInstance("b", "TB", "B0", t0))
	mustT(t, eng.RegisterInstance("c", "TC", "C0", t0))
	mustT(t, eng.SetLink("a", "b", "b"))
	mustT(t, eng.SetLink("b", "c", "c"))

	clock.Advance(50 * time.Minute)

	// Enter through the middle instance b.
	snap, err := eng.Get("b")
	if err != nil {
		t.Fatal(err)
	}

	var gotOrder []string
	calls := logger.CallsCopy()
	var fired *SettleCall
	for i := range calls {
		if calls[i].Now.Equal(t0.Add(50 * time.Minute)) {
			fired = &calls[i]
		}
	}
	if fired == nil {
		t.Fatalf("no settlement logged at 50m: %+v", calls)
	}
	{
		for _, r := range fired.Rings {
			gotOrder = append(gotOrder, r.Instance+":"+r.Transition)
		}
	}
	wantOrder := []string{
		"a:a-expire", "b:b-force", "c:c-force",
		"b:b-expire", "c:c-expire",
	}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("rings order=%v want %v", gotOrder, wantOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("ring %d = %s want %s; full=%v", i, gotOrder[i], wantOrder[i], gotOrder)
		}
	}

	if snap.State != "B2" {
		t.Fatalf("b should cascade to B2, got %s", snap.State)
	}
	if cs, _ := eng.Get("c"); cs.State != "C2" {
		t.Fatalf("c should be C2, got %s", cs.State)
	}

	// Forced transitions inherit the source ring's virtual due instant,
	// which anchors the downstream expiry chain deterministically.
	bh, _ := eng.History("b")
	var forcedAt time.Time
	for _, ev := range bh {
		if ev.Kind == EventForced {
			forcedAt = ev.OccurredAt
		}
	}
	if want := t0.Add(10 * time.Minute); !forcedAt.Equal(want) {
		t.Fatalf("b forced at %s want %s", forcedAt, want)
	}
}

// TestChainTriggerFailureClass verifies category-2 reporting for an
// unbound link and a target in the wrong state.
func TestChainTriggerFailureClass(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "TA",
		Transitions: []TransitionDef{
			{
				Name: "a-expire", From: "A0", To: "A1", Trigger: TriggerExpiry, Duration: time.Minute,
				Chain: []ChainEffect{{Link: "b", TargetFrom: "B0", TransitionName: "b-force"}},
			},
		},
	}))
	mustT(t, eng.RegisterType(&ObjectType{
		Name:        "TB",
		Transitions: []TransitionDef{{Name: "b-force", From: "B0", To: "B1", Trigger: TriggerForced}},
	}))
	mustT(t, eng.RegisterInstance("a", "TA", "A0", t0))
	mustT(t, eng.RegisterInstance("b", "TB", "B0", t0))
	// Deliberately do NOT bind a.b.
	clock.Advance(2 * time.Minute)
	_, err := eng.Get("a")
	if !errors.Is(err, ErrChainTrigger) {
		t.Fatalf("want chain trigger error, got %v", err)
	}
	// The source expiry ring remains committed even though the link failed.
	snap, _ := eng.Get("a")
	if snap.State != "A1" {
		t.Fatalf("source ring should remain, got %s", snap.State)
	}
}
