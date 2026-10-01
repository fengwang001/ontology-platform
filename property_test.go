package ontology

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

type oracleNode struct {
	name     string
	capacity int64
	pods     []Pod
}

type oracleState struct {
	nodes   map[string]*oracleNode
	pods    map[string]Pod
	budgets map[string]int64
}

type oracleResult struct {
	node    string
	victims []Pod
	reason  RejectReason
	basis   string
}

type oracleChoice struct {
	victims        []Pod
	violatingCount int
	highestPrio    int32
	score          *big.Int
}

func TestRandomNaiveOracle2000(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x1148_1148_1148_1148, 0xa114_8a11_48a1_148a))

	for scenario := 0; scenario < 2000; scenario++ {
		selector, oracle := buildRandomScenario(t, rng, scenario)
		incoming := randomIncomingPod(rng, scenario)

		gotNode, gotVictims, gotErr := selector.Preempt(incoming)
		want := naivePreempt(oracle, incoming)

		t.Logf("scenario=%d input=%s\nnodes=%s\nbudgets=%v\noutput(node=%q victims=%v err=%v)\nbasis=%s\noracle(node=%q victims=%s)",
			scenario, formatPod(incoming), formatOracleNodes(oracle), oracle.budgets,
			gotNode, gotVictims, gotErr, want.basis, want.node, formatPodList(want.victims))

		gotReason := RejectReason("")
		if gotErr != nil {
			gotReason = rejectReason(t, gotErr)
		}
		if gotReason != want.reason || gotNode != want.node || !equalStrings(gotVictims, podIDs(want.victims)) {
			t.Fatalf("scenario %d mismatch: got (%q, %v, %q), want (%q, %v, %q)",
				scenario, gotNode, gotVictims, gotReason, want.node, podIDs(want.victims), want.reason)
		}

		if want.reason != "" {
			verifyStateMatchesOracle(t, selector, oracle)
			continue
		}
		verifyPreemptedState(t, selector, oracle, incoming, want)
	}
}

func buildRandomScenario(t *testing.T, rng *rand.Rand, scenario int) (*Selector, *oracleState) {
	t.Helper()

	selector := NewSelector()
	oracle := &oracleState{
		nodes:   make(map[string]*oracleNode),
		pods:    make(map[string]Pod),
		budgets: make(map[string]int64),
	}

	nodeCount := 1 + rng.IntN(4)
	nodeNames := make([]string, nodeCount)
	for i := range nodeNames {
		nodeNames[i] = fmt.Sprintf("n%02d", i)
		capacity := int64(1 + rng.IntN(18))
		mustAddNode(t, selector, nodeNames[i], capacity)
		oracle.nodes[nodeNames[i]] = &oracleNode{name: nodeNames[i], capacity: capacity}
	}

	groupCount := rng.IntN(3)
	for i := 0; i < groupCount; i++ {
		group := fmt.Sprintf("g%d", i)
		allowed := int64(rng.IntN(4))
		mustSetBudget(t, selector, group, allowed)
		oracle.budgets[group] = allowed
	}

	podCount := rng.IntN(10)
	for i := 0; i < podCount; i++ {
		pod := Pod{
			ID:   fmt.Sprintf("s%04d-p%02d", scenario, i),
			Prio: int32(rng.IntN(21) - 10),
			Req:  int64(1 + rng.IntN(8)),
		}
		if groupCount > 0 && rng.IntN(2) == 0 {
			pod.BudgetGroup = fmt.Sprintf("g%d", rng.IntN(groupCount))
		}

		nodeName := nodeNames[rng.IntN(nodeCount)]
		if err := selector.Place(pod, nodeName); err == nil {
			node := oracle.nodes[nodeName]
			node.pods = append(node.pods, pod)
			oracle.pods[pod.ID] = pod
		}
	}

	return selector, oracle
}

func randomIncomingPod(rng *rand.Rand, scenario int) Pod {
	incoming := Pod{
		ID:   fmt.Sprintf("s%04d-new", scenario),
		Prio: int32(rng.IntN(17) - 8),
		Req:  int64(1 + rng.IntN(12)),
	}
	if rng.IntN(2) == 0 {
		incoming.BudgetGroup = fmt.Sprintf("g%d", rng.IntN(3))
	}
	return incoming
}

func naivePreempt(oracle *oracleState, incoming Pod) oracleResult {
	nodeNames := make([]string, 0, len(oracle.nodes))
	for name := range oracle.nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	feasible := make([]string, 0)
	for _, name := range nodeNames {
		node := oracle.nodes[name]
		var used int64
		for _, pod := range node.pods {
			used += pod.Req
		}
		if node.capacity-used >= incoming.Req {
			return oracleResult{
				reason: RejectNoPreemption,
				basis:  "a node can fit incoming pod without preemption",
			}
		}

		var reserved int64
		for _, pod := range node.pods {
			if pod.Prio >= incoming.Prio {
				reserved += pod.Req
			}
		}
		if node.capacity-reserved >= incoming.Req {
			feasible = append(feasible, name)
		}
	}

	if len(feasible) == 0 {
		return oracleResult{
			reason: RejectNoFeasibleNode,
			basis:  "every node lacks room after reserving equal-or-higher priority pods",
		}
	}

	var bestName string
	var bestChoice *oracleChoice
	basisParts := make([]string, 0, len(feasible))
	for _, name := range feasible {
		choice := naiveEvaluate(oracle, name, incoming)
		basisParts = append(basisParts, fmt.Sprintf(
			"%s->victims=%s violating=%d high=%d score=%s count=%d",
			name, formatPodList(choice.victims), choice.violatingCount,
			choice.highestPrio, choice.score.String(), len(choice.victims),
		))
		if bestChoice == nil || naiveBetter(choice, bestChoice) {
			bestChoice = choice
			bestName = name
		}
	}

	sort.Slice(bestChoice.victims, func(i, j int) bool {
		if bestChoice.victims[i].Prio != bestChoice.victims[j].Prio {
			return bestChoice.victims[i].Prio < bestChoice.victims[j].Prio
		}
		return bestChoice.victims[i].ID < bestChoice.victims[j].ID
	})

	return oracleResult{
		node:    bestName,
		victims: bestChoice.victims,
		basis:   strings.Join(basisParts, "; ") + "; tie resolved by node byte order",
	}
}

func naiveEvaluate(oracle *oracleState, nodeName string, incoming Pod) *oracleChoice {
	node := oracle.nodes[nodeName]
	candidates := make([]Pod, 0)
	var reserved int64
	for _, pod := range node.pods {
		if pod.Prio >= incoming.Prio {
			reserved += pod.Req
		} else {
			candidates = append(candidates, pod)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Prio != candidates[j].Prio {
			return candidates[i].Prio > candidates[j].Prio
		}
		return candidates[i].ID < candidates[j].ID
	})

	budgets := make(map[string]int64, len(oracle.budgets))
	for group, allowed := range oracle.budgets {
		budgets[group] = allowed
	}

	violating := make([]Pod, 0)
	nonViolating := make([]Pod, 0)
	for _, pod := range candidates {
		if pod.BudgetGroup == "" {
			nonViolating = append(nonViolating, pod)
			continue
		}

		budgets[pod.BudgetGroup]--
		if budgets[pod.BudgetGroup] < 0 {
			violating = append(violating, pod)
		} else {
			nonViolating = append(nonViolating, pod)
		}
	}

	ordered := append(append([]Pod{}, violating...), nonViolating...)
	free := node.capacity - reserved
	victims := make([]Pod, 0)
	for _, pod := range ordered {
		if free-pod.Req >= incoming.Req {
			free -= pod.Req
			continue
		}
		victims = append(victims, pod)
	}

	choice := &oracleChoice{victims: victims, score: big.NewInt(0)}
	for i, victim := range victims {
		if i == 0 || victim.Prio > choice.highestPrio {
			choice.highestPrio = victim.Prio
		}
		if victim.BudgetGroup != "" && budgets[victim.BudgetGroup] < 0 {
			choice.violatingCount++
		}
		choice.score.Add(choice.score, big.NewInt(int64(victim.Prio)+(1<<31)))
	}
	return choice
}

func naiveBetter(left, right *oracleChoice) bool {
	if left.violatingCount != right.violatingCount {
		return left.violatingCount < right.violatingCount
	}
	if left.highestPrio != right.highestPrio {
		return left.highestPrio < right.highestPrio
	}
	if cmp := left.score.Cmp(right.score); cmp != 0 {
		return cmp < 0
	}
	return len(left.victims) < len(right.victims)
}

func verifyStateMatchesOracle(t *testing.T, selector *Selector, oracle *oracleState) {
	t.Helper()
	selector.mu.Lock()
	defer selector.mu.Unlock()

	if len(selector.nodes) != len(oracle.nodes) ||
		len(selector.pods) != len(oracle.pods) ||
		len(selector.budgets) != len(oracle.budgets) {
		t.Fatalf("map sizes mismatch: selector nodes=%d pods=%d budgets=%d, oracle nodes=%d pods=%d budgets=%d",
			len(selector.nodes), len(selector.pods), len(selector.budgets),
			len(oracle.nodes), len(oracle.pods), len(oracle.budgets))
	}

	for name, oracleNode := range oracle.nodes {
		node, ok := selector.nodes[name]
		if !ok {
			t.Fatalf("selector missing node %s", name)
		}

		var oracleUsed int64
		oraclePods := make(map[string]Pod, len(oracleNode.pods))
		for _, pod := range oracleNode.pods {
			oracleUsed += pod.Req
			oraclePods[pod.ID] = pod
		}

		if node.capacity != oracleNode.capacity ||
			node.used != oracleUsed ||
			node.used > node.capacity ||
			len(node.pods) != len(oraclePods) {
			t.Fatalf("node %s mismatch: selector cap=%d used=%d count=%d, oracle cap=%d used=%d count=%d",
				name, node.capacity, node.used, len(node.pods),
				oracleNode.capacity, oracleUsed, len(oraclePods))
		}

		for _, pod := range node.pods {
			want, ok := oraclePods[pod.ID]
			if !ok || pod != want {
				t.Fatalf("node %s has pod %+v, oracle pod = %+v", name, pod, want)
			}
		}
	}

	for id, wantPod := range oracle.pods {
		if _, ok := selector.pods[id]; !ok {
			t.Fatalf("selector missing pod index %s", id)
		}
		_ = wantPod
	}

	for group, allowed := range oracle.budgets {
		if selector.budgets[group] != allowed {
			t.Fatalf("budget %s = %d, oracle = %d", group, selector.budgets[group], allowed)
		}
	}
}

func verifyPreemptedState(t *testing.T, selector *Selector, oracle *oracleState, incoming Pod, result oracleResult) {
	t.Helper()

	target := oracle.nodes[result.node]
	remaining := make([]Pod, 0, len(target.pods)+1)
	for _, pod := range target.pods {
		isVictim := false
		for _, victim := range result.victims {
			if pod.ID == victim.ID {
				isVictim = true
				break
			}
		}
		if !isVictim {
			remaining = append(remaining, pod)
		}
	}
	remaining = append(remaining, incoming)

	budgetDecrements := make(map[string]int)
	for _, victim := range result.victims {
		if victim.BudgetGroup != "" {
			budgetDecrements[victim.BudgetGroup]++
		}
		delete(oracle.pods, victim.ID)
	}
	for group, decrement := range budgetDecrements {
		current := oracle.budgets[group]
		oracle.budgets[group] = max64(0, current-int64(decrement))
	}

	target.pods = remaining
	oracle.pods[incoming.ID] = incoming

	verifyStateMatchesOracle(t, selector, oracle)
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func formatPod(pod Pod) string {
	return fmt.Sprintf("{id:%q prio:%d req:%d group:%q}", pod.ID, pod.Prio, pod.Req, pod.BudgetGroup)
}

func formatPodList(pods []Pod) string {
	parts := make([]string, 0, len(pods))
	for _, pod := range pods {
		parts = append(parts, formatPod(pod))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatOracleNodes(oracle *oracleState) string {
	names := make([]string, 0, len(oracle.nodes))
	for name := range oracle.nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		node := oracle.nodes[name]
		parts = append(parts, fmt.Sprintf("%s{cap:%d pods:%s}", name, node.capacity, formatPodList(node.pods)))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func podIDs(pods []Pod) []string {
	ids := make([]string, len(pods))
	for i, pod := range pods {
		ids[i] = pod.ID
	}
	return ids
}
