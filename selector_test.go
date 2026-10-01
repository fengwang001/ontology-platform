package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	selector := NewSelector()
	if err := selector.AddNode("n", 64); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pod := Pod{ID: fmt.Sprintf("p-%02d", i), Prio: int32(i), Req: 2}
			_ = selector.Place(pod, "n")
			_ = selector.SetBudget(fmt.Sprintf("g-%02d", i), 0)
		}(i)
	}
	wg.Wait()

	selector.mu.Lock()
	defer selector.mu.Unlock()
	if selector.nodes["n"].used > selector.nodes["n"].capacity {
		t.Fatalf("used %d exceeds cap %d", selector.nodes["n"].used, selector.nodes["n"].capacity)
	}
}

func TestEqualPriorityIsNotPreemptible(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n", 5)
	mustPlace(t, selector, Pod{ID: "same", Prio: 10, Req: 3}, "n")
	mustPlace(t, selector, Pod{ID: "lower", Prio: 9, Req: 2}, "n")

	_, _, err := selector.Preempt(Pod{ID: "new", Prio: 10, Req: 4})
	if got := rejectReason(t, err); got != RejectNoFeasibleNode {
		t.Fatalf("reason = %q, want %q", got, RejectNoFeasibleNode)
	}
}

func TestBudgetViolationAndReprieveOrder(t *testing.T) {
	allowedCases := []struct {
		allowed int64
		victims []string
	}{
		{allowed: 1, victims: []string{"c", "a"}},
		{allowed: 2, victims: []string{"c", "b"}},
	}

	for _, tc := range allowedCases {
		t.Run("", func(t *testing.T) {
			selector := NewSelector()
			mustAddNode(t, selector, "n", 8)
			mustSetBudget(t, selector, "g", tc.allowed)
			mustPlace(t, selector, Pod{ID: "a", Prio: 30, Req: 3, BudgetGroup: "g"}, "n")
			mustPlace(t, selector, Pod{ID: "b", Prio: 20, Req: 3, BudgetGroup: "g"}, "n")
			mustPlace(t, selector, Pod{ID: "c", Prio: 10, Req: 2}, "n")

			node, victims, err := selector.Preempt(Pod{ID: "new", Prio: 100, Req: 5})
			if err != nil {
				t.Fatalf("Preempt: %v", err)
			}
			if node != "n" {
				t.Fatalf("node = %q, want n", node)
			}
			if !equalStrings(victims, tc.victims) {
				t.Fatalf("victims = %v, want %v", victims, tc.victims)
			}
		})
	}
}

func TestReprieveAtExactRemainingRequirement(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n", 10)
	mustPlace(t, selector, Pod{ID: "fixed", Prio: 100, Req: 3}, "n")
	mustPlace(t, selector, Pod{ID: "high", Prio: 20, Req: 4}, "n")
	mustPlace(t, selector, Pod{ID: "low", Prio: 10, Req: 2}, "n")

	node, victims, err := selector.Preempt(Pod{ID: "new", Prio: 50, Req: 5})
	if err != nil {
		t.Fatalf("Preempt: %v", err)
	}
	if node != "n" || !equalStrings(victims, []string{"high"}) {
		t.Fatalf("node = %q, victims = %v; want n/high", node, victims)
	}
}

func TestNegativePriorityScore(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "one", 5)
	mustAddNode(t, selector, "two", 10)
	mustPlace(t, selector, Pod{ID: "single", Prio: -10, Req: 5}, "one")
	mustPlace(t, selector, Pod{ID: "left-a", Prio: -10, Req: 5}, "two")
	mustPlace(t, selector, Pod{ID: "left-b", Prio: -10, Req: 5}, "two")

	node, victims, err := selector.Preempt(Pod{ID: "new", Prio: 100, Req: 5})
	if err != nil {
		t.Fatalf("Preempt: %v", err)
	}
	if node != "one" || !equalStrings(victims, []string{"single"}) {
		t.Fatalf("node = %q, victims = %v; want one/single", node, victims)
	}
}

func TestNodeComparisonTiers(t *testing.T) {
	tests := []struct {
		name     string
		budgets  map[string]int64
		left     []Pod
		right    []Pod
		wantNode string
	}{
		{
			name:     "violating count",
			budgets:  map[string]int64{"g": 0},
			left:     []Pod{{ID: "l-g", Prio: 5, Req: 5, BudgetGroup: "g"}},
			right:    []Pod{{ID: "r-a", Prio: 4, Req: 3, BudgetGroup: "g"}, {ID: "r-b", Prio: 3, Req: 2, BudgetGroup: "g"}},
			wantNode: "left",
		},
		{
			name:     "highest victim priority",
			left:     []Pod{{ID: "l", Prio: 5, Req: 5}},
			right:    []Pod{{ID: "r", Prio: 4, Req: 5}},
			wantNode: "right",
		},
		{
			name:     "priority offset sum",
			left:     []Pod{{ID: "l", Prio: 3, Req: 5}},
			right:    []Pod{{ID: "r1", Prio: 3, Req: 3}, {ID: "r2", Prio: 1, Req: 2}},
			wantNode: "left",
		},
		{
			name:     "victim count",
			left:     []Pod{{ID: "l1", Prio: 2, Req: 3}, {ID: "l2", Prio: 2, Req: 2}},
			right:    []Pod{{ID: "r", Prio: 2, Req: 5}},
			wantNode: "right",
		},
		{
			name:     "node name",
			left:     []Pod{{ID: "a", Prio: 2, Req: 5}},
			right:    []Pod{{ID: "b", Prio: 2, Req: 5}},
			wantNode: "left",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selector := NewSelector()
			mustAddNode(t, selector, "left", 5)
			mustAddNode(t, selector, "right", 5)
			for group, allowed := range tc.budgets {
				mustSetBudget(t, selector, group, allowed)
			}
			for _, pod := range tc.left {
				mustPlace(t, selector, pod, "left")
			}
			for _, pod := range tc.right {
				mustPlace(t, selector, pod, "right")
			}

			node, _, err := selector.Preempt(Pod{ID: "new", Prio: 100, Req: 5})
			if err != nil {
				t.Fatalf("Preempt: %v", err)
			}
			if node != tc.wantNode {
				t.Fatalf("node = %q, want %q", node, tc.wantNode)
			}
		})
	}
}

func TestNoPreemptionBeforeNoFeasibleNode(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "free", 5)
	mustAddNode(t, selector, "full", 5)
	mustPlace(t, selector, Pod{ID: "fixed", Prio: 10, Req: 5}, "full")

	_, _, err := selector.Preempt(Pod{ID: "new", Prio: 9, Req: 5})
	if got := rejectReason(t, err); got != RejectNoPreemption {
		t.Fatalf("reason = %q, want %q", got, RejectNoPreemption)
	}
}

func TestBudgetDecrementClampsAtZero(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n", 5)
	mustSetBudget(t, selector, "g", 0)
	pod := Pod{ID: "victim", Prio: 1, Req: 5, BudgetGroup: "g"}
	mustPlace(t, selector, pod, "n")

	if _, _, err := selector.Preempt(Pod{ID: "new", Prio: 100, Req: 5}); err != nil {
		t.Fatalf("Preempt: %v", err)
	}
	if got := selector.budgets["g"]; got != 0 {
		t.Fatalf("budget = %d, want 0", got)
	}
}

func TestRejectReasonOrderAndNoStateChange(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n", 5)
	mustSetBudget(t, selector, "g", 1)
	existing := Pod{ID: "existing", Prio: 1, Req: 1}
	mustPlace(t, selector, existing, "n")

	if err := selector.AddNode("", 1); !rejectIs(err, RejectInvalidArgument) {
		t.Fatalf("empty AddNode reason = %v", err)
	}
	if err := selector.AddNode("n", 1); !rejectIs(err, RejectNodeExists) {
		t.Fatalf("duplicate AddNode reason = %v", err)
	}
	if err := selector.SetBudget("", 1); !rejectIs(err, RejectInvalidArgument) {
		t.Fatalf("empty SetBudget reason = %v", err)
	}
	if err := selector.Place(Pod{ID: "", Req: 1}, "n"); !rejectIs(err, RejectInvalidArgument) {
		t.Fatalf("invalid Place reason = %v", err)
	}
	if err := selector.Place(existing, "n"); !rejectIs(err, RejectPodExists) {
		t.Fatalf("duplicate Place reason = %v", err)
	}
	if err := selector.Place(Pod{ID: "missing-node", Req: 1}, "missing"); !rejectIs(err, RejectNodeNotFound) {
		t.Fatalf("missing node Place reason = %v", err)
	}
	if err := selector.Place(Pod{ID: "too-large", Req: 6}, "n"); !rejectIs(err, RejectCapacityExceeded) {
		t.Fatalf("capacity Place reason = %v", err)
	}
	if err := selector.Place(Pod{ID: "fills", Req: 4}, "n"); err != nil {
		t.Fatalf("fill node: %v", err)
	}
	if err := selector.Place(Pod{ID: "rejected", Req: 1}, "n"); !rejectIs(err, RejectCapacityExceeded) {
		t.Fatalf("second capacity Place reason = %v", err)
	}
	mustAddNode(t, selector, "free", 5)

	if _, _, err := selector.Preempt(Pod{ID: "", Prio: 0, Req: 1}); !rejectIs(err, RejectInvalidArgument) {
		t.Fatalf("invalid Preempt reason = %v", err)
	}
	if _, _, err := selector.Preempt(Pod{ID: "existing", Prio: 0, Req: 1}); !rejectIs(err, RejectPodExists) {
		t.Fatalf("duplicate Preempt reason = %v", err)
	}
	if _, _, err := selector.Preempt(Pod{ID: "rejected", Prio: 0, Req: 1}); !rejectIs(err, RejectNoPreemption) {
		t.Fatalf("rejected state reason = %v", err)
	}

	if len(selector.nodes) != 2 {
		t.Fatalf("node count = %d, want 2", len(selector.nodes))
	}
	if len(selector.pods) != 2 {
		t.Fatalf("pod count = %d, want 2", len(selector.pods))
	}
	if selector.budgets["g"] != 1 {
		t.Fatalf("budget changed to %d, want 1", selector.budgets["g"])
	}
	if _, ok := selector.pods["rejected"]; ok {
		t.Fatal("rejected pod was inserted")
	}
}

func mustAddNode(t *testing.T, selector *Selector, name string, capacity int64) {
	t.Helper()
	if err := selector.AddNode(name, capacity); err != nil {
		t.Fatalf("AddNode(%q, %d): %v", name, capacity, err)
	}
}

func mustSetBudget(t *testing.T, selector *Selector, group string, allowed int64) {
	t.Helper()
	if err := selector.SetBudget(group, allowed); err != nil {
		t.Fatalf("SetBudget(%q, %d): %v", group, allowed, err)
	}
}

func mustPlace(t *testing.T, selector *Selector, pod Pod, nodeName string) {
	t.Helper()
	if err := selector.Place(pod, nodeName); err != nil {
		t.Fatalf("Place(%+v, %q): %v", pod, nodeName, err)
	}
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var rejectErr *RejectError
	if !errors.As(err, &rejectErr) {
		t.Fatalf("error %v is not RejectError", err)
	}
	return rejectErr.Reason
}

func rejectIs(err error, reason RejectReason) bool {
	var rejectErr *RejectError
	return errors.As(err, &rejectErr) && rejectErr.Reason == reason
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
