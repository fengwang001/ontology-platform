package disruption

import "testing"

// TestPercentRounding covers ceil/floor directions and boundaries:
//   - minAvailable rounds UP;
//   - maxUnavailable rounds DOWN;
//   - 0% and 100% on both sides;
//   - exact-divisibility boundaries (e.g. 3 of 10, 3*10%=3).
func TestPercentRounding(t *testing.T) {
	log := newOpLogger(t)
	cases := []struct {
		name     string
		budget   PodDisruptionBudget
		expected int
		req      int
		allow    int
		ready    int
	}{
		{"minAvail 33% of 10 ceil=4", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: pct(33)}, 10, 4, 1, 5},
		{"minAvail 30% of 10 exact=3", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: pct(30)}, 10, 3, 0, 3},
		{"minAvail 0% of 10", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: pct(0)}, 10, 0, 5, 5},
		{"minAvail 100% of 10", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: pct(100)}, 10, 10, 0, 10},
		{"minAvail 1% of 3 ceil=1", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: pct(1)}, 3, 1, 0, 1},
		{"maxUnavail 33% of 10 floor=3 -> req 7", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MaxUnavail: pct(33)}, 10, 7, 0, 7},
		{"maxUnavail 30% of 10 exact=3 -> req 7", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MaxUnavail: pct(30)}, 10, 7, 1, 8},
		{"maxUnavail 0% of 10 -> req 10", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MaxUnavail: pct(0)}, 10, 10, 0, 10},
		{"maxUnavail 100% of 10 -> req 0", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MaxUnavail: pct(100)}, 10, 0, 10, 10},
		{"maxUnavail abs 2 of 5 -> req 3", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MaxUnavail: abs(2)}, 5, 3, 2, 5},
		{"maxUnavail abs oversized -> clamp 0", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MaxUnavail: abs(99)}, 5, 0, 2, 2},
		{"minAvail abs oversized keeps value", PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: abs(8)}, 5, 8, 0, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := requiredReady(tc.budget, tc.expected)
			if got != tc.req {
				t.Fatalf("%s: requiredReady=%d want %d", tc.name, got, tc.req)
			}
			if got := allowance(tc.ready, tc.req); got != tc.allow {
				t.Fatalf("%s: allowance=%d want %d", tc.name, got, tc.allow)
			}
			log.logf("arithmetic input=%s expected=%d ready=%d => required=%d allowance=%d (basis: ceil for minAvail%%, floor for maxUnavail%%, clamp>=0)",
				tc.name, tc.expected, tc.ready, tc.req, tc.allow)
		})
	}
}

func TestBudgetValidation(t *testing.T) {
	if err := validateBudget(PodDisruptionBudget{ID: BudgetID{"ns", ""}, MinAvailable: abs(1)}); err == nil {
		t.Fatal("empty budget name must be invalid")
	}
	if err := validateBudget(PodDisruptionBudget{ID: BudgetID{"ns", "b"}}); err == nil {
		t.Fatal("neither field set must be invalid")
	}
	if err := validateBudget(PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: abs(1), MaxUnavail: abs(1)}); err == nil {
		t.Fatal("both fields set must be invalid")
	}
	mustKind(t, validateBudget(PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: pct(101)}), KindInvalidArgument)
	mustKind(t, validateBudget(PodDisruptionBudget{ID: BudgetID{"ns", "b"}, MinAvailable: abs(-1)}), KindInvalidArgument)
}

func TestEmptySelectorMatchesNothing(t *testing.T) {
	if selectorMatches(Selector{}, map[string]string{"app": "x"}) {
		t.Fatal("empty selector must match nothing")
	}
	if selectorMatches(nil, nil) {
		t.Fatal("nil selector must match nothing")
	}
	if !selectorMatches(Selector{"a": "b"}, map[string]string{"a": "b", "c": "d"}) {
		t.Fatal("all-equalities selector must match superset labels")
	}
	if selectorMatches(Selector{"a": "b"}, map[string]string{"a": "c"}) {
		t.Fatal("inequality must not match")
	}
	if selectorMatches(Selector{"a": "b"}, map[string]string{}) {
		t.Fatal("missing key must not match")
	}
}
