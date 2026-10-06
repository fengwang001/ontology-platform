package initsession

import "testing"

// TestComplexityCounters verifies the verifiable complexity claims:
//
//  1. A function referenced by many units has its body/closure
//     expanded exactly once (FunctionClosuresComputed equals the
//     number of functions, and unit-side reuse only hits the shared
//     closure; there is no per-referencer re-expansion).
//  2. The scheduler never performs a per-round full rescan:
//     ReadyChecks is bounded by Units + ReverseNotifications (initial
//     one check per unit plus at most one check per reverse edge).
func TestComplexityCounters(t *testing.T) {
	lg := newTestLogger(t)

	t.Run("shared_function_closure", func(t *testing.T) {
		s := mustSession(t)
		const callers = 500
		addUnit(t, lg, s, []string{"root"}, nil)
		addFunc(t, lg, s, "big", []string{"root", "nested"})
		addFunc(t, lg, s, "nested", []string{"root"})
		for i := 0; i < callers; i++ {
			addUnit(t, lg, s, []string{varName(i)}, []string{"big"})
		}
		res, st, err := s.Solve()
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Order) != callers+1 {
			t.Fatalf("order size = %d", len(res.Order))
		}
		// Every caller must report root as its (single, name-sorted)
		// transitive dependency.
		for i := 1; i <= callers; i++ {
			if got := res.Dependencies[i].Deps; len(got) != 1 || got[0] != "root" {
				t.Fatalf("caller %d deps = %v, want [root]", i, got)
			}
		}
		// Exactly one closure computation per function, regardless of
		// the 500 callers. Callers reuse the SCC closure (hits counted
		// per caller).
		if st.FunctionClosuresComputed != 2 {
			t.Fatalf("closures computed = %d, want 2 (no per-caller expansion)",
				st.FunctionClosuresComputed)
		}
		if st.FunctionCacheHits < callers {
			t.Fatalf("cache hits = %d, want >= %d callers", st.FunctionCacheHits, callers)
		}
		lg.reasonf("shared closure: callers=%d closures-computed=%d hits=%d",
			callers, st.FunctionClosuresComputed, st.FunctionCacheHits)
	})

	t.Run("no_round_rescan", func(t *testing.T) {
		s := mustSession(t)
		const n = 300
		// Reverse source-order dependency chain: unit n-1 depends on
		// n-2 etc., so exactly one unit is ready per round. A naive
		// rescan would do O(n^2) checks; here it stays linear.
		for i := 0; i < n; i++ {
			var refs []string
			if i > 0 {
				refs = []string{varName(i - 1)}
			}
			addUnit(t, lg, s, []string{varName(i)}, refs)
		}
		_, st, err := s.Solve()
		if err != nil {
			t.Fatal(err)
		}
		bound := st.Units + st.ReverseNotifications
		if st.ReadyChecks > bound {
			t.Fatalf("ReadyChecks=%d exceeds linear bound Units(%d)+edges(%d)=%d",
				st.ReadyChecks, st.Units, st.ReverseNotifications, bound)
		}
		lg.reasonf("reverse chain n=%d ready-checks=%d edges=%d (naive rescan would do ~%d)",
			n, st.ReadyChecks, st.ReverseNotifications, n*(n+1)/2)
	})
}

func varName(i int) string {
	return "v" + itoa(i)
}
