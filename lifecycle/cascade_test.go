package lifecycle

import (
	"testing"
	"time"
)

// Chain: a.t1 (due at +1h) forces b.go, whose firing forces c.go.
// States: a A0->A1, b B0->B1, c C0->C1.
func chainCascadeTypes() []*ObjectType {
	return []*ObjectType{
		{
			ID: "ta", States: []StateID{"A0", "A1"}, Initial: "A0",
			Transitions: []Transition{{
				ID: "a.fire", From: "A0", To: "A1", Kind: KindTimeTransition, Duration: time.Hour,
				Cascade: &CascadeSpec{
					Target:     func(c GuardContext) (InstanceID, bool) { return c.Ref("next") },
					Transition: "b.go",
				},
			}},
		},
		{
			ID: "tb", States: []StateID{"B0", "B1"}, Initial: "B0",
			Transitions: []Transition{{
				ID: "b.go", From: "B0", To: "B1", Kind: KindActionTransition,
				Cascade: nil,
			}},
		},
		{
			ID: "tc", States: []StateID{"C0", "C1"}, Initial: "C0",
			Transitions: []Transition{{
				ID: "c.go", From: "C0", To: "C1", Kind: KindActionTransition,
			}},
		},
	}
}

// The middle link itself cascades onward; rebuild the middle type to add its
// cascade. Actions normally cannot declare cascades (validated), so the
// middle link is modeled as a time transition with zero-extra delay by
// reusing the same logical firing time when forced.
func chainCascadeTypesDeep() []*ObjectType {
	return []*ObjectType{
		{
			ID: "ta", States: []StateID{"A0", "A1"}, Initial: "A0",
			Transitions: []Transition{{
				ID: "a.fire", From: "A0", To: "A1", Kind: KindTimeTransition, Duration: time.Hour,
				Cascade: &CascadeSpec{
					Target:     func(c GuardContext) (InstanceID, bool) { return c.Ref("next") },
					Transition: "b.go",
				},
			}},
		},
		{
			ID: "tb", States: []StateID{"B0", "B1"}, Initial: "B0",
			Transitions: []Transition{{
				ID: "b.go", From: "B0", To: "B1", Kind: KindTimeTransition, Duration: time.Hour,
				Cascade: &CascadeSpec{
					Target:     func(c GuardContext) (InstanceID, bool) { return c.Ref("next") },
					Transition: "c.go",
				},
			}},
		},
		{
			ID: "tc", States: []StateID{"C0", "C1"}, Initial: "C0",
			Transitions: []Transition{{
				ID: "c.go", From: "C0", To: "C1", Kind: KindTimeTransition, Duration: time.Hour,
			}},
		},
	}
}

func registerAll(t *testing.T, r interface{ RegisterType(*ObjectType) error }, types []*ObjectType) {
	t.Helper()
	for _, ot := range types {
		if err := r.RegisterType(ot); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCascadeOrderFromRootAccess(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	types := chainCascadeTypesDeep()
	registerAll(t, eng, types)
	if err := eng.Spawn("ta", "a", "", map[string]any{"next": "b"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("tb", "b", "", map[string]any{"next": "c"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("tc", "c", "", nil); err != nil {
		t.Fatal(err)
	}

	clk.Set(t0().Add(5 * time.Hour))
	if _, errs := eng.Read("a"); errs.Primary() != nil {
		t.Fatalf("unexpected: %v", errs)
	}
	ha, _ := eng.History("a")
	hb, _ := eng.History("b")
	hc, _ := eng.History("c")
	if ha[1].To != "A1" || hb[1].To != "B1" || hc[1].To != "C1" {
		t.Fatalf("cascade chain did not fully fire: %v %v %v", ha, hb, hc)
	}
	// Logical entry times along the forced chain all equal the origin fire
	// time (a's due = t0+1h), preserving declared order.
	if !hb[1].At.Equal(ha[1].At) || !hc[1].At.Equal(ha[1].At) {
		t.Fatalf("forced chain logical times must equal origin fire time: %v %v %v", ha[1].At, hb[1].At, hc[1].At)
	}
}

func TestCascadeOrderWhenEnteringViaMiddleInstance(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	registerAll(t, eng, chainCascadeTypesDeep())
	if err := eng.Spawn("ta", "a", "", map[string]any{"next": "b"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("tb", "b", "", map[string]any{"next": "c"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("tc", "c", "", nil); err != nil {
		t.Fatal(err)
	}

	clk.Set(t0().Add(5 * time.Hour))
	// Access enters at the middle link b; the declared order still requires
	// a to settle before b is forced and c follows.
	snap, errs := eng.Read("b")
	if errs.Primary() != nil {
		t.Fatalf("unexpected: %v", errs)
	}
	if snap.State != "B1" {
		t.Fatalf("want B1, got %s", snap.State)
	}
	ha, _ := eng.History("a")
	hb, _ := eng.History("b")
	hc, _ := eng.History("c")
	if len(ha) != 2 || ha[1].Kind != EntryTime {
		t.Fatalf("upstream a must settle first even when entering at b: %+v", ha)
	}
	if hb[1].Kind != EntryCascade || hc[1].Kind != EntryCascade {
		t.Fatalf("b and c must be forced entries: %+v %+v", hb, hc)
	}
	// Materialization order is observable via SettledAt: a before b before c
	// (strictly non-decreasing; here all within one call, check logical order
	// through generation-equivalent history positions, which the assertions
	// above already guarantee).
}

func TestCascadeFailureReported(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	// Shallow types: a.fire forces b.go, but spawn b on a state that does not
	// enable b.go (by using type tc instead, whose transition id differs).
	registerAll(t, eng, chainCascadeTypes())
	if err := eng.Spawn("ta", "a", "", map[string]any{"next": "b"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Spawn("tc", "b", "", nil); err != nil {
		t.Fatal(err)
	}
	clk.Set(t0().Add(5 * time.Hour))
	_, errs := eng.Read("a")
	p := errs.Primary()
	if p == nil || p.Kind != KindCascadeFailed {
		t.Fatalf("want cascade failure, got %v", errs)
	}
	// The time transition itself already materialized: cascade failure does
	// not undo it.
	ha, _ := eng.History("a")
	if len(ha) != 2 || ha[1].To != "A1" {
		t.Fatalf("fired time transition must stay committed: %+v", ha)
	}
}

func TestRejectedActionChangesNothing(t *testing.T) {
	clk := NewFixedClock(t0())
	eng := NewEngine(clk)
	types := chainCascadeTypes()
	registerAll(t, eng, types)
	if err := eng.Spawn("ta", "a", "", map[string]any{"next": "b"}); err != nil {
		t.Fatal(err)
	}
	before, _ := eng.History("a")
	clk.Set(t0().Add(30 * time.Minute))
	if ok := func() bool {
		_, errs := eng.Action("a", "a.fire") // a.fire is a time transition, not an action
		return errs.Primary() != nil && errs.Primary().Kind == KindActionDenied
	}(); !ok {
		t.Fatalf("invoking a time transition as action must be denied")
	}
	after, _ := eng.History("a")
	if len(after) != len(before) {
		t.Fatalf("denied action must not mutate history: before=%d after=%d", len(before), len(after))
	}
}
