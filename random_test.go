package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

func randomToleration(rng *rand.Rand) Toleration {
	seconds := []int64{-1, -1, 0, 1, 5, 30}[rng.Intn(6)]
	effect := []Effect{"", NoSchedule, NoExecute}[rng.Intn(3)]
	if effect != NoExecute {
		seconds = -1
	}
	key := []string{"", "a", "b", "k"}[rng.Intn(4)]
	operator := []TolerationOperator{EqualOperator, ExistsOperator}[rng.Intn(2)]
	value := ""
	if operator == EqualOperator && key != "" && rng.Intn(2) == 0 {
		value = []string{"v", "x"}[rng.Intn(2)]
	}
	return Toleration{Key: key, Operator: operator, Value: value, Effect: effect, Seconds: seconds}
}

func generateRandomOps(rng *rand.Rand) []randomOp {
	nodeNames := []string{"n1", "n2", "n3"}
	podNames := []string{"p1", "p2", "p3", "p4", "p5"}
	keys := []string{"a", "b", "k"}
	values := []string{"v", "x", "old", "new"}
	effects := []Effect{NoSchedule, PreferNoSchedule, NoExecute}
	ops := make([]randomOp, 160)
	now := int64(0)
	for i := range ops {
		now += int64(rng.Intn(4))
		op := randomOp{
			now:    now,
			node:   nodeNames[rng.Intn(len(nodeNames))],
			pod:    podNames[rng.Intn(len(podNames))],
			key:    keys[rng.Intn(len(keys))],
			value:  values[rng.Intn(len(values))],
			effect: effects[rng.Intn(len(effects))],
		}
		switch rng.Intn(8) {
		case 0:
			op.kind = "add"
			op.maxPods = 1 + rng.Intn(4)
		case 1, 2:
			op.kind = "taint"
		case 3:
			op.kind = "untaint"
		case 4, 5:
			op.kind = "schedule"
			for range rng.Intn(3) {
				op.tolerations = append(op.tolerations, randomToleration(rng))
			}
		default:
			op.kind = "tick"
		}
		if rng.Intn(20) == 0 {
			now -= int64(rng.Intn(5))
			if now < 0 {
				now = 0
			}
			op.now = now
		}
		ops[i] = op
	}
	return ops
}

func applyNaive(n *naiveManager, op randomOp) ([]string, ErrorCode) {
	switch op.kind {
	case "add":
		return nil, n.addNode(op)
	case "taint":
		return nil, n.taint(op)
	case "untaint":
		return nil, n.untaint(op)
	case "schedule":
		return nil, n.schedule(op)
	default:
		return n.tick(op)
	}
}

func applyManager(m *Manager, op randomOp) ([]string, ErrorCode) {
	var err error
	var ids []string
	switch op.kind {
	case "add":
		err = m.AddNode(op.node, op.maxPods)
	case "taint":
		err = m.Taint(op.node, Taint{Key: op.key, Value: op.value, Effect: op.effect}, op.now)
	case "untaint":
		err = m.Untaint(op.node, op.key, op.effect, op.now)
	case "schedule":
		err = m.Schedule(op.pod, op.node, op.tolerations, op.now)
	default:
		ids, err = m.Tick(op.now)
	}
	if err != nil {
		return ids, err.(*OperationError).Code
	}
	return ids, ""
}

func formatOp(op randomOp) string {
	return fmt.Sprintf("%s now=%d node=%q pod=%q key=%q value=%q effect=%q maxPods=%d tolerations=%v",
		op.kind, op.now, op.node, op.pod, op.key, op.value, op.effect, op.maxPods, op.tolerations)
}

func sameIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(114514))
	for iteration := 0; iteration < 2000; iteration++ {
		ops := generateRandomOps(rng)
		gracePeriodMS := int64(rng.Intn(4))
		rate := 1 + rng.Intn(3)
		manager, err := NewManager(gracePeriodMS, rate)
		mustOK(t, err)
		naive := newNaiveManager(gracePeriodMS, rate)

		t.Logf("iteration=%d G=%d R=%d", iteration, gracePeriodMS, rate)
		for i, op := range ops {
			actualIDs, actualCode := applyManager(manager, op)
			expectedIDs, expectedCode := applyNaive(naive, op)
			t.Logf("step=%d input={%s} output={ids=%v code=%q} expected={ids=%v code=%q} reason=compare_error_code_and_eviction_order",
				i, formatOp(op), actualIDs, actualCode, expectedIDs, expectedCode)
			if actualCode != expectedCode || !sameIDs(actualIDs, expectedIDs) {
				t.Fatalf("iteration=%d step=%d mismatch: input={%s} actual={ids=%v code=%q} expected={ids=%v code=%q}",
					iteration, i, formatOp(op), actualIDs, actualCode, expectedIDs, expectedCode)
			}

			for nodeName, node := range manager.nodes {
				if len(node.bindings) > node.maxPods {
					t.Fatalf("iteration=%d node=%s has %d pods, maxPods=%d", iteration, nodeName, len(node.bindings), node.maxPods)
				}
			}
		}
	}
}
