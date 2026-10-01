package ontology

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestEqualPriorityCannotPreempt(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n1", 10)
	mustPlace(t, selector, Pod{ID: "p0", Priority: 5, Request: 10}, "n1")

	_, err := selector.Preempt(Pod{ID: "new", Priority: 5, Request: 1})
	if !errors.Is(err, ErrNoFeasibleNode) {
		t.Fatalf("Preempt() error = %v, want %v", err, ErrNoFeasibleNode)
	}
}

func TestPardonOrderAndExactBoundary(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n1", 10)
	mustSetBudget(t, selector, "g", 1)
	mustPlace(t, selector, Pod{ID: "b", Priority: 1, Request: 4, BudgetGroup: "g"}, "n1")
	mustPlace(t, selector, Pod{ID: "a", Priority: 2, Request: 4, BudgetGroup: "g"}, "n1")
	mustPlace(t, selector, Pod{ID: "x", Priority: 3, Request: 2}, "n1")

	result, err := selector.Preempt(Pod{ID: "new", Priority: 4, Request: 2})
	if err != nil {
		t.Fatalf("Preempt() error = %v", err)
	}
	if result.Node != "n1" {
		t.Fatalf("node = %q, want n1", result.Node)
	}
	if got, want := result.Victims, []string{"a"}; !equalStrings(got, want) {
		t.Fatalf("victims = %v, want %v", got, want)
	}
	if got := selector.budgets["g"]; got != 0 {
		t.Fatalf("budget = %d, want 0", got)
	}
}

func TestBudgetAllowedTwoMakesBothPodsNonViolating(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n1", 10)
	mustSetBudget(t, selector, "g", 2)
	mustPlace(t, selector, Pod{ID: "b", Priority: 1, Request: 2, BudgetGroup: "g"}, "n1")
	mustPlace(t, selector, Pod{ID: "a", Priority: 2, Request: 2, BudgetGroup: "g"}, "n1")
	mustPlace(t, selector, Pod{ID: "x", Priority: 3, Request: 6}, "n1")

	result, err := selector.Preempt(Pod{ID: "new", Priority: 4, Request: 6})
	if err != nil {
		t.Fatalf("Preempt() error = %v", err)
	}
	if got, want := result.Victims, []string{"x"}; !equalStrings(got, want) {
		t.Fatalf("victims = %v, want %v; %s", got, want, dumpScenario("result", result))
	}
}

func TestNodeComparisonLevels(t *testing.T) {
	t.Run("fewer violating victims", func(t *testing.T) {
		selector := NewSelector()
		mustAddNode(t, selector, "A", 3)
		mustAddNode(t, selector, "B", 3)
		mustSetBudget(t, selector, "g", 1)
		mustPlace(t, selector, Pod{ID: "a1", Priority: 0, Request: 1, BudgetGroup: "g"}, "A")
		mustPlace(t, selector, Pod{ID: "a2", Priority: 0, Request: 2, BudgetGroup: "g"}, "A")
		mustPlace(t, selector, Pod{ID: "b1", Priority: 0, Request: 1, BudgetGroup: "h"}, "B")
		mustPlace(t, selector, Pod{ID: "b2", Priority: 0, Request: 2, BudgetGroup: "g"}, "B")

		result, err := selector.Preempt(Pod{ID: "new", Priority: 1, Request: 1})
		if err != nil {
			t.Fatal(err)
		}
		if result.Node != "A" {
			t.Fatalf("node=%q want A; result=%v", result.Node, result)
		}
	})

	t.Run("lower highest victim priority", func(t *testing.T) {
		selector := NewSelector()
		mustAddNode(t, selector, "A", 2)
		mustAddNode(t, selector, "B", 2)
		mustPlace(t, selector, Pod{ID: "a", Priority: 1, Request: 2}, "A")
		mustPlace(t, selector, Pod{ID: "b", Priority: 2, Request: 2}, "B")

		result, err := selector.Preempt(Pod{ID: "new", Priority: 3, Request: 2})
		if err != nil {
			t.Fatal(err)
		}
		if result.Node != "A" {
			t.Fatalf("node=%q want A; result=%v", result.Node, result)
		}
	})

	t.Run("lower weighted priority sum", func(t *testing.T) {
		selector := NewSelector()
		mustAddNode(t, selector, "A", 4)
		mustAddNode(t, selector, "B", 4)
		mustPlace(t, selector, Pod{ID: "a1", Priority: 2, Request: 3}, "A")
		mustPlace(t, selector, Pod{ID: "a2", Priority: 0, Request: 1}, "A")
		mustPlace(t, selector, Pod{ID: "b1", Priority: 2, Request: 2}, "B")
		mustPlace(t, selector, Pod{ID: "b2", Priority: 2, Request: 2}, "B")

		result, err := selector.Preempt(Pod{ID: "new", Priority: 3, Request: 2})
		if err != nil {
			t.Fatal(err)
		}
		if result.Node != "A" {
			t.Fatalf("node=%q want A; result=%v", result.Node, result)
		}
	})

	t.Run("fewer victims then node name", func(t *testing.T) {
		selector := NewSelector()
		mustAddNode(t, selector, "A", 4)
		mustAddNode(t, selector, "B", 4)
		mustPlace(t, selector, Pod{ID: "a1", Priority: -2, Request: 2}, "A")
		mustPlace(t, selector, Pod{ID: "a2", Priority: -2, Request: 2}, "A")
		mustPlace(t, selector, Pod{ID: "b1", Priority: -3, Request: 4}, "B")

		result, err := selector.Preempt(Pod{ID: "new", Priority: 1, Request: 2})
		if err != nil {
			t.Fatal(err)
		}
		if result.Node != "B" {
			t.Fatalf("node=%q want B; result=%v", result.Node, result)
		}

		selector2 := NewSelector()
		mustAddNode(t, selector2, "B", 2)
		mustAddNode(t, selector2, "A", 2)
		mustPlace(t, selector2, Pod{ID: "bn", Priority: 1, Request: 2}, "B")
		mustPlace(t, selector2, Pod{ID: "an", Priority: 1, Request: 2}, "A")
		tied, err := selector2.Preempt(Pod{ID: "new", Priority: 2, Request: 2})
		if err != nil {
			t.Fatal(err)
		}
		if tied.Node != "A" {
			t.Fatalf("node=%q want A by byte order; result=%v", tied.Node, tied)
		}
	})
}

func TestBudgetDeductionNeverNegative(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n1", 2)
	mustPlace(t, selector, Pod{ID: "zero", Priority: 0, Request: 1, BudgetGroup: "g"}, "n1")
	mustPlace(t, selector, Pod{ID: "plain", Priority: 0, Request: 1}, "n1")

	if _, err := selector.Preempt(Pod{ID: "new", Priority: 1, Request: 2}); err != nil {
		t.Fatal(err)
	}
	if got := selector.budgets["g"]; got != 0 {
		t.Fatalf("budget g=%d, want floor 0", got)
	}
}

func TestConcurrentOperationsPreserveInvariants(t *testing.T) {
	selector := NewSelector()
	const workers = 32
	errs := make(chan error, workers*2)
	done := make(chan struct{}, workers*2)

	for worker := 0; worker < workers; worker++ {
		name := fmt.Sprintf("n-%02d", worker)
		go func() {
			defer func() { done <- struct{}{} }()
			if err := selector.AddNode(name, 10); err != nil {
				errs <- err
			}
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	for worker := 0; worker < workers; worker++ {
		pod := Pod{ID: fmt.Sprintf("p-%02d", worker), Priority: 0, Request: 1}
		node := fmt.Sprintf("n-%02d", worker)
		go func() {
			defer func() { done <- struct{}{} }()
			if err := selector.Place(pod, node); err != nil {
				errs <- err
			}
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	selector.mu.Lock()
	defer selector.mu.Unlock()
	for _, node := range selector.nodes {
		if node.used > node.capacity {
			t.Fatalf("node %s used %d > cap %d", node.name, node.used, node.capacity)
		}
	}
}

type naiveState struct {
	nodes    map[string]uint64
	pods     map[string]Pod
	podNodes map[string]string
	budgets  map[string]uint64
}

func newNaiveState() *naiveState {
	return &naiveState{
		nodes:    make(map[string]uint64),
		pods:     make(map[string]Pod),
		podNodes: make(map[string]string),
		budgets:  make(map[string]uint64),
	}
}

type naiveResult struct {
	node    string
	victims []string
	reason  string
}

func TestRandomScenariosAgainstNaiveSimulation(t *testing.T) {
	const scenarios = 2000
	rng := rand.New(rand.NewPCG(1148, 20261001))
	for scenario := range scenarios {
		selector := NewSelector()
		naive := newNaiveState()
		var log []string

		nodeCount := 1 + rng.IntN(4)
		nodeNames := make([]string, 0, nodeCount)
		for index := range nodeCount {
			name := "n" + strconv.Itoa(index)
			capacity := uint64(1 + rng.IntN(12))
			err := selector.AddNode(name, capacity)
			naive.nodes[name] = capacity
			nodeNames = append(nodeNames, name)
			log = append(log, fmt.Sprintf("AddNode(%q,%d)->%v", name, capacity, err))
		}

		groupCount := rng.IntN(3)
		groups := make([]string, 0, groupCount+1)
		groups = append(groups, "")
		for index := range groupCount {
			group := "g" + strconv.Itoa(index)
			allowed := uint64(rng.IntN(4))
			err := selector.SetBudget(group, allowed)
			naive.budgets[group] = allowed
			groups = append(groups, group)
			log = append(log, fmt.Sprintf("SetBudget(%q,%d)->%v", group, allowed, err))
		}

		podSeq := 0
		for step := 0; step < 30; step++ {
			pod := Pod{
				ID:          "p" + strconv.Itoa(podSeq),
				Priority:    int32(rng.IntN(21)) - 10,
				Request:     uint64(1 + rng.IntN(6)),
				BudgetGroup: groups[rng.IntN(len(groups))],
			}
			node := nodeNames[rng.IntN(len(nodeNames))]
			usePreempt := rng.IntN(3) == 0
			podSeq++

			var actual PreemptionResult
			var actualErr error
			var expected naiveResult
			if usePreempt {
				actual, actualErr = selector.Preempt(pod)
				expected = naivePreempt(naive, pod)
				log = append(log, fmt.Sprintf("Preempt(%+v)=node:%q victims:%v err:%v | naive node:%q victims:%v reason:%s",
					pod, actual.Node, actual.Victims, actualErr, expected.node, expected.victims, expected.reason))
				if expected.node == "" {
					wantErr := errByReason(expected.reason)
					if !errors.Is(actualErr, wantErr) {
						t.Fatalf("scenario %d error mismatch\n%s", scenario, strings.Join(log, "\n"))
					}
				} else {
					if actualErr != nil || actual.Node != expected.node || !slices.Equal(actual.Victims, expected.victims) {
						t.Fatalf("scenario %d result mismatch\n%s", scenario, strings.Join(log, "\n"))
					}
				}
			} else {
				actualErr = selector.Place(pod, node)
				expectedReason := naivePlace(naive, pod, node)
				log = append(log, fmt.Sprintf("Place(%+v,%q)->%v | naive:%s", pod, node, actualErr, expectedReason))
				if expectedReason != errString(actualErr) {
					t.Fatalf("scenario %d place mismatch\n%s", scenario, strings.Join(log, "\n"))
				}
			}
			if !sameNaiveState(selector, naive) {
				t.Fatalf("scenario %d state mismatch\n%s", scenario, strings.Join(log, "\n"))
			}
		}
		if testing.Verbose() {
			t.Logf("scenario %d deterministic input/output/decision trace:\n%s", scenario, strings.Join(log, "\n"))
		}
	}
}

func naivePlace(state *naiveState, pod Pod, node string) string {
	if pod.ID == "" || node == "" || pod.Request == 0 || pod.Request > 1_000_000_000_000 {
		return ErrInvalidArgument.Error()
	}
	if _, exists := state.pods[pod.ID]; exists {
		return ErrPodExists.Error()
	}
	capacity, exists := state.nodes[node]
	if !exists {
		return ErrNodeNotFound.Error()
	}
	used := uint64(0)
	for id, current := range state.pods {
		if state.podNodes[id] == node {
			used += current.Request
		}
	}
	if capacity-used < pod.Request {
		return ErrCapacityFull.Error()
	}
	state.pods[pod.ID] = pod
	state.podNodes[pod.ID] = node
	return ""
}

func naivePreempt(state *naiveState, incoming Pod) naiveResult {
	if incoming.ID == "" || incoming.Request == 0 || incoming.Request > 1_000_000_000_000 {
		return naiveResult{reason: ErrInvalidArgument.Error()}
	}
	if _, exists := state.pods[incoming.ID]; exists {
		return naiveResult{reason: ErrPodExists.Error()}
	}

	nodeNames := make([]string, 0, len(state.nodes))
	for name := range state.nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	for _, name := range nodeNames {
		used := uint64(0)
		for id, pod := range state.pods {
			if state.podNodes[id] == name {
				used += pod.Request
			}
		}
		if state.nodes[name]-used >= incoming.Request {
			return naiveResult{reason: ErrNoPreemption.Error()}
		}
	}

	best := naiveResult{reason: ErrNoFeasibleNode.Error()}
	var bestKey *big.Int
	var bestVictims []Pod
	for _, name := range nodeNames {
		currentPods := make([]Pod, 0)
		for id, pod := range state.pods {
			if state.podNodes[id] == name {
				_ = id
				currentPods = append(currentPods, pod)
			}
		}
		protected := uint64(0)
		candidates := make([]Pod, 0)
		for _, pod := range currentPods {
			if pod.Priority >= incoming.Priority {
				protected += pod.Request
			} else {
				candidates = append(candidates, pod)
			}
		}
		if state.nodes[name]-protected < incoming.Request {
			continue
		}

		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Priority != candidates[j].Priority {
				return candidates[i].Priority > candidates[j].Priority
			}
			return candidates[i].ID < candidates[j].ID
		})
		budgetCopy := map[string]uint64{}
		for group, allowed := range state.budgets {
			budgetCopy[group] = allowed
		}
		var violating, nonViolating []Pod
		for _, pod := range candidates {
			if pod.BudgetGroup == "" {
				nonViolating = append(nonViolating, pod)
				continue
			}
			if budgetCopy[pod.BudgetGroup] == 0 {
				violating = append(violating, pod)
			} else {
				budgetCopy[pod.BudgetGroup]--
				nonViolating = append(nonViolating, pod)
			}
		}

		free := state.nodes[name] - protected
		victimSet := map[string]Pod{}
		for _, group := range [][]Pod{violating, nonViolating} {
			for _, pod := range group {
				if free >= pod.Request && free-pod.Request >= incoming.Request {
					free -= pod.Request
					continue
				}
				victimSet[pod.ID] = pod
			}
		}

		victims := make([]Pod, 0, len(victimSet))
		violatingCount := 0
		highest := int32(-1 << 31)
		score := big.NewInt(0)
		for _, pod := range violating {
			if victim, ok := victimSet[pod.ID]; ok {
				violatingCount++
				victims = append(victims, victim)
			}
		}
		for _, pod := range nonViolating {
			if victim, ok := victimSet[pod.ID]; ok {
				victims = append(victims, victim)
			}
		}
		for _, pod := range victims {
			if pod.Priority > highest {
				highest = pod.Priority
			}
			score.Add(score, big.NewInt(int64(pod.Priority)+(1<<31)))
		}

		key := big.NewInt(int64(violatingCount))
		key.Lsh(key, 128)
		key.Or(key, big.NewInt(int64(highest)+(1<<31)))
		key.Lsh(key, 128)
		key.Or(key, score)
		key.Lsh(key, 64)
		key.Or(key, big.NewInt(int64(len(victims))))
		if bestKey == nil || key.Cmp(bestKey) < 0 || (key.Cmp(bestKey) == 0 && name < best.node) {
			bestKey = key
			bestVictims = victims
			best = naiveResult{node: name}
		}
	}
	if best.node == "" {
		return best
	}

	sort.Slice(bestVictims, func(i, j int) bool {
		if bestVictims[i].Priority != bestVictims[j].Priority {
			return bestVictims[i].Priority < bestVictims[j].Priority
		}
		return bestVictims[i].ID < bestVictims[j].ID
	})
	for _, victim := range bestVictims {
		delete(state.pods, victim.ID)
		delete(state.podNodes, victim.ID)
		if victim.BudgetGroup != "" && state.budgets[victim.BudgetGroup] > 0 {
			state.budgets[victim.BudgetGroup]--
		}
	}
	state.pods[incoming.ID] = incoming
	state.podNodes[incoming.ID] = best.node
	best.victims = make([]string, 0, len(bestVictims))
	for _, victim := range bestVictims {
		best.victims = append(best.victims, victim.ID)
	}
	return best
}

func errByReason(reason string) error {
	switch reason {
	case ErrInvalidArgument.Error():
		return ErrInvalidArgument
	case ErrPodExists.Error():
		return ErrPodExists
	case ErrNodeNotFound.Error():
		return ErrNodeNotFound
	case ErrCapacityFull.Error():
		return ErrCapacityFull
	case ErrNoPreemption.Error():
		return ErrNoPreemption
	case ErrNoFeasibleNode.Error():
		return ErrNoFeasibleNode
	default:
		return nil
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sameNaiveState(selector *Selector, state *naiveState) bool {
	if len(selector.nodes) != len(state.nodes) || len(selector.pods) != len(state.pods) {
		return false
	}
	for name, node := range selector.nodes {
		if state.nodes[name] != node.capacity {
			return false
		}
	}
	for id, entry := range selector.pods {
		expected, exists := state.pods[id]
		if !exists || entry.pod != expected || entry.node != state.podNodes[id] {
			return false
		}
	}
	for group, allowed := range state.budgets {
		if selector.budgets[group] != allowed {
			return false
		}
	}
	for group, allowed := range selector.budgets {
		if state.budgets[group] != allowed {
			return false
		}
	}
	return true
}

func TestNegativePriorityScore(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n1", 4)
	mustAddNode(t, selector, "n2", 4)
	mustPlace(t, selector, Pod{ID: "one-a", Priority: -1, Request: 1}, "n1")
	mustPlace(t, selector, Pod{ID: "one-b", Priority: -1, Request: 1}, "n1")
	mustPlace(t, selector, Pod{ID: "two-a", Priority: -2, Request: 1}, "n2")
	mustPlace(t, selector, Pod{ID: "two-b", Priority: -2, Request: 1}, "n2")
	mustPlace(t, selector, Pod{ID: "one-fill", Priority: -10, Request: 2}, "n1")
	mustPlace(t, selector, Pod{ID: "two-fill", Priority: -10, Request: 2}, "n2")

	result, err := selector.Preempt(Pod{ID: "new", Priority: 1, Request: 3})
	if err != nil {
		t.Fatalf("Preempt() error = %v", err)
	}
	if result.Node != "n2" {
		t.Fatalf("node = %q, want n2 because two lower weighted victims tie higher prio and count", result.Node)
	}
}

func TestRejectionOrderAndStateUnchanged(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "n1", 1)
	mustPlace(t, selector, Pod{ID: "p", Priority: 0, Request: 1}, "n1")

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid add", func() error { return selector.AddNode("", 1) }, ErrInvalidArgument},
		{"duplicate node", func() error { return selector.AddNode("n1", 1) }, ErrNodeExists},
		{"invalid budget", func() error { return selector.SetBudget("", 0) }, ErrInvalidArgument},
		{"duplicate pod before missing node", func() error {
			return selector.Place(Pod{ID: "p", Request: 1}, "missing")
		}, ErrPodExists},
		{"missing node", func() error { return selector.Place(Pod{ID: "q", Request: 1}, "missing") }, ErrNodeNotFound},
		{"capacity full", func() error { return selector.Place(Pod{ID: "q", Request: 1}, "n1") }, ErrCapacityFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v; input=%s", err, tc.want, tc.name)
			}
		})
	}

	if len(selector.nodes) != 1 || len(selector.pods) != 1 {
		t.Fatalf("state changed after rejected operations: nodes=%d pods=%d", len(selector.nodes), len(selector.pods))
	}
}

func TestNoPreemptionBeforeNoFeasibleNode(t *testing.T) {
	selector := NewSelector()
	mustAddNode(t, selector, "free", 10)
	mustAddNode(t, selector, "blocked", 1)
	mustPlace(t, selector, Pod{ID: "equal", Priority: 1, Request: 1}, "blocked")

	_, err := selector.Preempt(Pod{ID: "new", Priority: 1, Request: 1})
	if !errors.Is(err, ErrNoPreemption) {
		t.Fatalf("error = %v, want %v", err, ErrNoPreemption)
	}
}

func mustAddNode(t *testing.T, selector *Selector, name string, capacity uint64) {
	t.Helper()
	if err := selector.AddNode(name, capacity); err != nil {
		t.Fatalf("AddNode(%q, %d): %v", name, capacity, err)
	}
}

func mustSetBudget(t *testing.T, selector *Selector, group string, allowed uint64) {
	t.Helper()
	if err := selector.SetBudget(group, allowed); err != nil {
		t.Fatalf("SetBudget(%q, %d): %v", group, allowed, err)
	}
}

func mustPlace(t *testing.T, selector *Selector, pod Pod, node string) {
	t.Helper()
	if err := selector.Place(pod, node); err != nil {
		t.Fatalf("Place(%+v, %q): %v", pod, node, err)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func dumpScenario(title string, value any) string {
	return fmt.Sprintf("%s: %+v", title, value)
}
