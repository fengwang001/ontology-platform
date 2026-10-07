package lifecycle_test

import (
	"math/rand"
	"testing"
	"time"

	"ontology/lifecycle"
	"ontology/naive"
)

// TestRandomDifferentialChains fuzzes the cross-instance chained expiry
// scenario against the naive periodic-scan model.
func TestRandomDifferentialChains(t *testing.T) {
	start := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(778899))

	for iter := 0; iter < 100; iter++ {
		clock := lifecycle.NewManualClock(start)
		lz := lifecycle.NewEngine(clock.ClockFunc(), nil)
		nv := naive.NewWorld()

		if err := lz.RegisterType(&lifecycle.ObjectType{
			Name: "TA",
			Transitions: []lifecycle.TransitionDef{
				{
					Name: "a-expire", From: "A0", To: "A1", Trigger: lifecycle.TriggerExpiry,
					Duration: 5 * time.Minute,
					Chain:    []lifecycle.ChainEffect{{Link: "b", TargetFrom: "B0", TransitionName: "b-force"}},
				},
				{Name: "a-expire2", From: "A1", To: "A2", Trigger: lifecycle.TriggerExpiry, Duration: 8 * time.Minute},
			},
		}); err != nil {
			panic(err)
		}
		if err := lz.RegisterType(&lifecycle.ObjectType{
			Name: "TB",
			Transitions: []lifecycle.TransitionDef{
				{Name: "b-force", From: "B0", To: "B1", Trigger: lifecycle.TriggerForced,
					Chain: []lifecycle.ChainEffect{{Link: "c", TargetFrom: "C0", TransitionName: "c-force"}}},
				{Name: "b-expire", From: "B1", To: "B2", Trigger: lifecycle.TriggerExpiry, Duration: 6 * time.Minute},
			},
		}); err != nil {
			panic(err)
		}
		if err := lz.RegisterType(&lifecycle.ObjectType{
			Name: "TC",
			Transitions: []lifecycle.TransitionDef{
				{Name: "c-force", From: "C0", To: "C1", Trigger: lifecycle.TriggerForced},
				{Name: "c-expire", From: "C1", To: "C2", Trigger: lifecycle.TriggerExpiry, Duration: 9 * time.Minute},
			},
		}); err != nil {
			panic(err)
		}

		nv.RegisterType(&naive.Type{Name: "TA", Transitions: []naive.Transition{
			{Name: "a-expire", From: "A0", To: "A1", Duration: 5 * time.Minute,
				Chain: []naive.ChainEffect{{Link: "b", TargetFrom: "B0", ForcedName: "b-force"}}},
			{Name: "a-expire2", From: "A1", To: "A2", Duration: 8 * time.Minute},
		}})
		nv.RegisterType(&naive.Type{Name: "TB", Transitions: []naive.Transition{
			{Name: "b-force", From: "B0", To: "B1",
				Chain: []naive.ChainEffect{{Link: "c", TargetFrom: "C0", ForcedName: "c-force"}}},
			{Name: "b-expire", From: "B1", To: "B2", Duration: 6 * time.Minute},
		}})
		nv.RegisterType(&naive.Type{Name: "TC", Transitions: []naive.Transition{
			{Name: "c-force", From: "C0", To: "C1"},
			{Name: "c-expire", From: "C1", To: "C2", Duration: 9 * time.Minute},
		}})

		if err := lz.RegisterInstance("a", "TA", "A0", start); err != nil {
			panic(err)
		}
		if err := lz.RegisterInstance("b", "TB", "B0", start); err != nil {
			panic(err)
		}
		if err := lz.RegisterInstance("c", "TC", "C0", start); err != nil {
			panic(err)
		}
		nv.RegisterInstance("a", "TA", "A0", start)
		nv.RegisterInstance("b", "TB", "B0", start)
		nv.RegisterInstance("c", "TC", "C0", start)
		if err := lz.SetLink("a", "b", "b"); err != nil {
			panic(err)
		}
		if err := lz.SetLink("b", "c", "c"); err != nil {
			panic(err)
		}
		nv.SetLink("a", "b", "b")
		nv.SetLink("b", "c", "c")

		ids := []string{"a", "b", "c"}
		steps := 10 + rng.Intn(20)
		for step := 0; step < steps; step++ {
			clock.Advance(time.Duration(1+rng.Intn(7)) * time.Minute)
			now := clock.Now()
			id := ids[rng.Intn(3)]
			ls, lerr := lz.Get(id)
			ns, nerr := nv.State(id, now)
			if (lerr != nil) != (nerr != nil) {
				t.Fatalf("iter=%d step=%d %s err mismatch lazy=%v naive=%v",
					iter, step, id, lerr, nerr)
			}
			if ls.State != ns {
				t.Fatalf("iter=%d step=%d entry %s lazy=%s naive=%s",
					iter, step, id, ls.State, ns)
			}
			// Compare the rest via StateAt-style snapshots WITHOUT adding
			// new access-triggered settlement: lazy Get on an already
			// fully-settled closure is now a no-op, and the naive closure
			// scan on the same seed reaches the same fixed point.
			for _, x := range ids {
				if x == id {
					continue
				}
				l, _ := lz.StateAt(x, now)
				n, _ := nv.State(x, now)
				if l.State != n {
					t.Fatalf("iter=%d step=%d %s state lazy=%s naive=%s",
						iter, step, x, l.State, n)
				}
			}
		}
	}
}
