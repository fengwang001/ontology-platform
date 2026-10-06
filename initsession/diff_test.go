package initsession

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomDifferential runs many randomized registration sequences
// against both the real Session and the naive literal-rule model and
// asserts identical outcomes: success order, per-unit transitive deps,
// undeclared reporting and cycle reporting. Some generated
// registrations are deliberately invalid or duplicate, exercising
// rejection handling: only accepted operations are mirrored into the
// naive model.
func TestRandomDifferential(t *testing.T) {
	lg := newTestLogger(t)
	const iterations = 400
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		runOneDifferential(t, lg, rng, seed)
	}
	lg.logf("differential: %d randomized sequences matched the naive model", iterations)
}

func runOneDifferential(t *testing.T, lg *testLogger, rng *rand.Rand, seed int64) {
	t.Helper()

	predeclared := []string{"pre1", "pre2"}
	s, err := New(predeclared...)
	if err != nil {
		t.Fatal(err)
	}
	nm := newNaive(predeclared)

	// Small identifier pool creates frequent collisions, forward refs
	// and cycles. Occasionally inject garbage identifiers to trigger
	// invalid-argument rejection.
	pool := []string{"a", "b", "c", "d", "e", "f", "g"}
	funcPool := []string{"fa", "fb", "fc"}

	nDecl := 5 + rng.Intn(12)
	for k := 0; k < nDecl; k++ {
		if rng.Intn(3) == 0 {
			// Function declaration.
			name := pick(rng, funcPool)
			if rng.Intn(10) == 0 {
				name = "1bad"
			}
			refs := randomRefs(rng, pool, funcPool, []string{"pre1", "pre2"},
				[]string{"nope", "ghost"})
			lg.inputf("[seed=%d k=%d] AddFunction(%q, %v)", seed, k, name, refs)
			err := s.AddFunction(name, refs)
			if err != nil {
				lg.outputf("[seed=%d] function rejected: %v", seed, err)
				continue
			}
			nm.apply(true, name, nil, refs)
			lg.outputf("[seed=%d] function accepted", seed)
		} else {
			// Variable unit: 1-3 lhs entries; reuse pool to provoke
			// internal/external redeclarations.
			var vars []string
			for i, n := 0, 1+rng.Intn(3); i < n; i++ {
				v := pick(rng, pool)
				if rng.Intn(6) == 0 {
					v = "_"
				}
				vars = append(vars, v)
			}
			if rng.Intn(15) == 0 {
				vars = nil // empty lhs rejection
			}
			refs := randomRefs(rng, pool, funcPool, []string{"pre1", "pre2"},
				[]string{"nope", "ghost"})
			lg.inputf("[seed=%d k=%d] AddVariableUnit(%v, %v)", seed, k, vars, refs)
			idx, err := s.AddVariableUnit(vars, refs)
			if err != nil {
				lg.outputf("[seed=%d] unit rejected: %v", seed, err)
				continue
			}
			nm.apply(false, "", vars, refs)
			lg.outputf("[seed=%d] unit accepted at %d", seed, idx)
		}
	}

	res, _, serr := s.Solve()
	nr := nm.solve()
	lg.outputf("[seed=%d] real: %v", seed, fmtErr(serr, res))
	lg.outputf("[seed=%d] naive: %s", seed, nr.label())

	switch {
	case nr.undecl != nil:
		if serr == nil || serr.(*Error).Kind != KindUndeclared {
			t.Fatalf("seed %d: want undeclared error, got %v", seed, serr)
		}
		e := serr.(*Error)
		var realDecl string
		if e.Function != "" {
			realDecl = e.Function
		} else if e.UnitIndex >= 0 {
			realDecl = fmt.Sprintf("unit:%d", e.UnitIndex)
		}
		var naiveDecl string
		if nr.undecl.isFunc {
			naiveDecl = nr.undecl.name
		} else {
			naiveDecl = fmt.Sprintf("unit:%d", unitOrder(nm, nr.undecl))
		}
		if e.Name != nr.undeclName || realDecl != naiveDecl {
			t.Fatalf("seed %d: undeclared mismatch real=(%s,%s) naive=(%s,%s)",
				seed, realDecl, e.Name, naiveDecl, nr.undeclName)
		}
		lg.reasonf("[seed=%d] undeclared reports agree: decl=%s name=%s", seed, naiveDecl, e.Name)
	case nr.cycleVars != nil:
		if serr == nil || serr.(*Error).Kind != KindInitCycle {
			t.Fatalf("seed %d: want cycle error, got %v", seed, serr)
		}
		e := serr.(*Error)
		if !reflect.DeepEqual(e.Names, nr.cycleVars) {
			t.Fatalf("seed %d: cycle vars real=%v naive=%v", seed, e.Names, nr.cycleVars)
		}
		lg.reasonf("[seed=%d] cycle vars agree: %v", seed, e.Names)
	default:
		if serr != nil {
			t.Fatalf("seed %d: unexpected error %v", seed, serr)
		}
		if !reflect.DeepEqual(res.Order, nr.order) {
			t.Fatalf("seed %d: order real=%v naive=%v", seed, res.Order, nr.order)
		}
		for ui := range nm.units {
			got := res.Dependencies[ui].Deps
			want := nr.deps[ui]
			if len(got) == 0 {
				got = nil
			}
			if len(want) == 0 {
				want = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d: unit %d deps real=%v naive=%v", seed, ui, got, want)
			}
		}
		lg.reasonf("[seed=%d] success matches: order=%v", seed, res.Order)
	}
}

func unitOrder(nm *naiveModel, d *naiveDecl) int {
	for i, u := range nm.units {
		if u == d {
			return i
		}
	}
	return -1
}

func fmtErr(err error, res *Result) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("ok order=%v", res.Order)
}

func pick(rng *rand.Rand, xs []string) string {
	return xs[rng.Intn(len(xs))]
}

func randomRefs(rng *rand.Rand, pools ...[]string) []string {
	n := rng.Intn(4)
	var out []string
	for i := 0; i < n; i++ {
		pool := pools[rng.Intn(len(pools))]
		out = append(out, pool[rng.Intn(len(pool))])
	}
	if rng.Intn(12) == 0 {
		out = append(out, "_")
	}
	return out
}
