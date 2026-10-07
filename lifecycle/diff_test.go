package lifecycle_test

import (
	"math/rand"
	"testing"
	"time"

	"ontology/lifecycle"
	"ontology/naive"
)

// buildLazyWorld and buildNaiveWorld construct structurally identical
// scenarios in the two independent implementations.
type worlds struct {
	lazy  *lifecycle.Engine
	naive *naive.World
	clock *lifecycle.ManualClock
	ids   []string
}

func buildWorlds(rng *rand.Rand, withChain bool) *worlds {
	start := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	clock := lifecycle.NewManualClock(start)
	lz := lifecycle.NewEngine(clock.ClockFunc(), nil)
	nv := naive.NewWorld()

	guardProp := "flag"
	if err := lz.RegisterType(&lifecycle.ObjectType{
		Name: "Doc",
		Transitions: []lifecycle.TransitionDef{
			{Name: "a-b", From: "A", To: "B", Trigger: lifecycle.TriggerExpiry,
				Duration: 5 * time.Minute,
				Guard: lifecycle.GuardFunc(func(ctx *lifecycle.EvalContext) bool {
					v, _ := ctx.Property(guardProp)
					return v != "block"
				})},
			{Name: "b-c", From: "B", To: "C", Trigger: lifecycle.TriggerExpiry, Duration: 7 * time.Minute},
			{Name: "c-d", From: "C", To: "D", Trigger: lifecycle.TriggerExpiry, Duration: 11 * time.Minute},
			{Name: "abort", From: "A", To: "X", Trigger: lifecycle.TriggerAction, Actions: []string{"abort"}},
			{Name: "finish", From: "D", To: "Z", Trigger: lifecycle.TriggerAction, Actions: []string{"finish"}},
		},
	}); err != nil {
		panic(err)
	}
	nv.RegisterType(&naive.Type{
		Name: "Doc",
		Transitions: []naive.Transition{
			{Name: "a-b", From: "A", To: "B", Duration: 5 * time.Minute,
				Guard: func(w *naive.World, id string) bool {
					return w.Property(id, guardProp) != "block"
				}},
			{Name: "b-c", From: "B", To: "C", Duration: 7 * time.Minute},
			{Name: "c-d", From: "C", To: "D", Duration: 11 * time.Minute},
			{Name: "abort", From: "A", To: "X", Action: "abort"},
			{Name: "finish", From: "D", To: "Z", Action: "finish"},
		},
	})

	n := 4
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = "doc" + string(rune('0'+i))
		if err := lz.RegisterInstance(ids[i], "Doc", "A", start); err != nil {
			panic(err)
		}
		nv.RegisterInstance(ids[i], "Doc", "A", start)
	}
	return &worlds{lazy: lz, naive: nv, clock: clock, ids: ids}
}

// TestRandomDifferential generates random time advances and random
// access/action/property sequences and compares the lazy engine against the
// independent periodic-scan model after every operation.
func TestRandomDifferential(t *testing.T) {
	const iterations = 400
	rng := rand.New(rand.NewSource(20261007))

	for iter := 0; iter < iterations; iter++ {
		w := buildWorlds(rng, false)
		start := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
		lockedGuard := map[string]bool{}
		steps := 20 + rng.Intn(40)
		for step := 0; step < steps; step++ {
			// Differential fuzz drives monotone time only; clock
			// regression is covered by dedicated irreversibility tests,
			// where the models intentionally differ in reporting but agree
			// that committed rings stay committed.
			w.clock.Advance(time.Duration(rng.Intn(8)) * time.Minute)
			now := w.clock.Now()
			id := w.ids[rng.Intn(len(w.ids))]

			op := rng.Intn(6)
			switch op {
			case 0, 1, 2: // read
				ls, lerr := w.lazy.Get(id)
				ns, nerr := w.naive.State(id, now)
				if now.Sub(start) >= 5*time.Minute {
					lockedGuard[id] = true
				}
				compare(t, iter, step, id, "Get", ls.State, ns, lerr, nerr)
			case 3: // abort action (enabled only in A)
				ls, lerr := w.lazy.Act(id, "abort")
				ns, nerr := w.naive.Act(id, "abort", now)
				lockedGuard[id] = true
				compare(t, iter, step, id, "abort", ls.State, ns, lerr, nerr)
			case 4: // finish action (enabled only in D)
				ls, lerr := w.lazy.Act(id, "finish")
				ns, nerr := w.naive.Act(id, "finish", now)
				lockedGuard[id] = true
				compare(t, iter, step, id, "finish", ls.State, ns, lerr, nerr)
			case 5: // toggle the blocking flag, but only before the first
				// ring can become due; afterwards the guard is held fixed
				// so both models judge the same ring at the same inputs.
				if now.Sub(start) < 5*time.Minute && !lockedGuard[id] {
					v := "ok"
					if rng.Intn(2) == 0 {
						v = "block"
					}
					_ = w.lazy.SetProperty(id, "flag", v)
					w.naive.SetProperty(id, "flag", v)
				}
			}

			// After every step every instance must agree across models.
			for _, xid := range w.ids {
				ls, lerr := w.lazy.Get(xid)
				ns, nerr := w.naive.State(xid, now)
				compare(t, iter, step, xid, "consistency", ls.State, ns, lerr, nerr)
			}
		}
	}
}

func compare(t *testing.T, iter, step int, id, op, ls, ns string, lerr, nerr error) {
	t.Helper()
	// The lazy model exposes category-4 regression errors on reads; the
	// naive model records them via LastCat. Compare states regardless of
	// regression reporting; other failures must match in presence.
	if cat, ok := lifecycle.CategoryOf(lerr); ok && cat == lifecycle.CategoryClockRegression {
		if ns != ls {
			t.Fatalf("iter=%d step=%d %s/%s state mismatch lazy=%s naive=%s (regression)",
				iter, step, op, id, ls, ns)
		}
		return
	}
	if (lerr != nil) != (nerr != nil) {
		t.Fatalf("iter=%d step=%d %s/%s error mismatch: lazy=%v naive=%v",
			iter, step, op, id, lerr, nerr)
	}
	if ls != ns {
		t.Fatalf("iter=%d step=%d %s/%s state mismatch lazy=%s naive=%s",
			iter, step, op, id, ls, ns)
	}
}
