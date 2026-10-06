package pdb

import (
	"testing"
	"time"
)

var t0 = time.Unix(1_000_000, 0)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func mkPod(ns, uid string, ready bool, phase Phase, labels map[string]string) Pod {
	return Pod{UID: uid, Namespace: ns, Ready: ready, Phase: phase, Labels: labels}
}

func minAv(n int) *Value  { return &Value{Amount: n} }
func maxUn(n int) *Value  { return &Value{Amount: n} }
func minPct(n int) *Value { return &Value{Amount: n, IsPercent: true} }
func maxPct(n int) *Value { return &Value{Amount: n, IsPercent: true} }

func must(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func wantReason(t *testing.T, err error, r Reason) {
	t.Helper()
	if got := ErrReason(err); got != r {
		t.Fatalf("reason = %s, want %s (err=%v)", ReasonName(got), ReasonName(r), err)
	}
}

// TestRounding covers ceil for minAvailable percentages and floor for
// maxUnavailable percentages, including boundary values 0 and 100.
func TestRounding(t *testing.T) {
	lg := newOpLogger(t)
	ceilCases := []struct{ pct, want int }{
		{0, 0}, {1, 1}, {10, 1}, {11, 2}, {15, 2}, {25, 3}, {99, 10}, {100, 10},
	}
	for _, c := range ceilCases {
		got := resolveValue(minPct(c.pct), 10, true)
		lg.log("resolve minAvailable=%d%% of 10 ceil => %d (want %d)", c.pct, got, c.want)
		if got != c.want {
			t.Fatalf("ceil pct=%d: got %d want %d", c.pct, got, c.want)
		}
	}
	floorCases := []struct{ pct, want int }{
		{0, 0}, {1, 0}, {10, 1}, {11, 1}, {15, 1}, {25, 2}, {99, 9}, {100, 10},
	}
	for _, c := range floorCases {
		got := resolveValue(maxPct(c.pct), 10, false)
		lg.log("resolve maxUnavailable=%d%% of 10 floor => %d (want %d)", c.pct, got, c.want)
		if got != c.want {
			t.Fatalf("floor pct=%d: got %d want %d", c.pct, got, c.want)
		}
	}
	if resolveValue(minPct(50), 0, true) != 0 || resolveValue(maxPct(50), 0, false) != 0 {
		t.Fatal("percentages of zero expected must resolve to zero")
	}
	if got := resolveValue(minPct(34), 3, true); got != 2 {
		t.Fatalf("ceil 34%% of 3 = %d want 2", got)
	}
	if got := resolveValue(maxPct(34), 3, false); got != 1 {
		t.Fatalf("floor 34%% of 3 = %d want 1", got)
	}
	lg.log("rounding boundary checks complete")
}

// TestEmptySelectorAndIsolation verifies empty selector matches no pod and
// budgets only apply within their own namespace.
func TestEmptySelectorAndIsolation(t *testing.T) {
	lg := newOpLogger(t)
	s := New(time.Minute)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns1",
		Selector: Selector{}, MinAvailable: minAv(0)}, at(0)), "budget")
	must(t, s.UpsertPod(mkPod("ns1", "p1", true, PhaseRunning, map[string]string{"app": "x"}), at(1)), "p1")
	must(t, s.UpsertPod(mkPod("ns2", "p2", true, PhaseRunning, map[string]string{"app": "x"}), at(2)), "p2")
	st, err := s.Budget("ns1", "b", at(3))
	must(t, err, "query")
	lg.log("empty-selector status: expected=%d ready=%d required=%d allowed=%d matched=%v",
		st.Expected, st.CurrentReady, st.RequiredReady, st.DisruptionAllowed, st.MatchedPods)
	if st.Expected != 0 || len(st.MatchedPods) != 0 {
		t.Fatal("empty selector must match no pod")
	}
	ref := PodRef{"ns1", "p1"}
	must(t, s.Evict(ref, at(4)), "unmatched pod must pass")
	lg.log("evict ns1/p1 => ALLOW (unmatched)")
	must(t, s.Confirm(ref, at(5)), "confirm")

	// Same-namespace isolation: a budget in ns3 with matching selector does not
	// constrain ns1 pods.
	must(t, s.UpsertBudget(BudgetSpec{Name: "b3", Namespace: "ns3",
		Selector: Selector{"app": "x"}, MaxUnavailable: maxUn(0)}, at(6)), "ns3 budget")
	must(t, s.UpsertPod(mkPod("ns1", "p3", true, PhaseRunning, map[string]string{"app": "x"}), at(7)), "p3")
	must(t, s.Evict(PodRef{"ns1", "p3"}, at(8)), "cross namespace")
	lg.log("evict ns1/p3 with maxUnavailable=0 budget only in ns3 => ALLOW")
}

// TestConflict checks multi-budget matches are denied.
func TestConflict(t *testing.T) {
	lg := newOpLogger(t)
	s := New(time.Minute)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b1", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(0)}, at(0)), "b1")
	must(t, s.UpsertBudget(BudgetSpec{Name: "b2", Namespace: "ns",
		Selector: Selector{"app": "x", "tier": "fe"}, MinAvailable: minAv(0)}, at(1)), "b2")
	must(t, s.UpsertPod(mkPod("ns", "p", true, PhaseRunning,
		map[string]string{"app": "x", "tier": "fe"}), at(2)), "pod")
	err := s.Evict(PodRef{"ns", "p"}, at(3))
	lg.log("evict pod matched by b1,b2 => %s", errStr(err))
	wantReason(t, err, ReasonConflict)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b2", Namespace: "ns",
		Selector: Selector{"app": "x", "tier": "be"}, MinAvailable: minAv(0)}, at(4)), "b2 change")
	must(t, s.Evict(PodRef{"ns", "p"}, at(5)), "conflict removed")
	lg.log("after b2 stops matching, evict => ALLOW")
}
