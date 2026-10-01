package placement

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

var verboseDifferential = os.Getenv("PLACEMENT_TEST_VERBOSE") == "1"

const diffKeySpace = "ab"

func randomLabels(rng *rand.Rand) map[string]string {
	labels := map[string]string{}
	n := rng.Intn(3)
	for i := 0; i < n; i++ {
		key := string(diffKeySpace[rng.Intn(len(diffKeySpace))])
		if rng.Intn(15) == 0 {
			key = "" // exercise invalid-argument paths
		}
		labels[key] = fmt.Sprintf("%d", rng.Intn(3))
	}
	return labels
}

func randomSelector(rng *rand.Rand) Selector {
	if rng.Intn(5) == 0 {
		return Selector{} // empty selector matches all pods
	}
	return Selector(randomLabels(rng))
}

func randomPod(rng *rand.Rand, id string) *Pod {
	pod := &Pod{ID: id, Labels: randomLabels(rng)}
	if rng.Intn(3) > 0 {
		for range rng.Intn(3) {
			topo := "node"
			if rng.Intn(2) == 0 {
				topo = "zone"
			}
			if rng.Intn(20) == 0 {
				topo = "rack" // invalid topology
			}
			minMatching := 1 + rng.Intn(3)
			if rng.Intn(30) == 0 {
				minMatching = 101 // out of range
			}
			pod.Affinity = append(pod.Affinity, AffinityTerm{
				Selector:    randomSelector(rng),
				Topology:    topo,
				MinMatching: minMatching,
			})
		}
	}
	if rng.Intn(3) > 0 {
		for range rng.Intn(2) {
			topo := "node"
			if rng.Intn(2) == 0 {
				topo = "zone"
			}
			pod.AntiAffinity = append(pod.AntiAffinity, AntiAffinityTerm{
				Selector: randomSelector(rng),
				Topology: topo,
			})
		}
	}
	return pod
}

func generateSequence(rng *rand.Rand) []op {
	nodes := []string{"n1", "n2", "n3", "n4"}
	zones := map[string]string{"n1": "za", "n2": "za", "n3": "zb", "n4": "zb"}
	operations := make([]op, 0, 60)
	for _, node := range nodes {
		operations = append(operations, op{name: "AddNode", node: node, zone: zones[node]})
	}
	liveIDs := []string{}
	removedIDs := []string{}
	nextID := 0
	newID := func() string {
		id := fmt.Sprintf("p%02d", nextID)
		nextID++
		return id
	}
	for range 56 {
		roll := rng.Intn(100)
		switch {
		case roll < 24:
			id := newID()
			operations = append(operations, op{
				name: "Place",
				id:   id,
				node: nodes[rng.Intn(len(nodes))],
				pod:  randomPod(rng, id),
			})
			liveIDs = append(liveIDs, id)
		case roll < 40:
			id := newID()
			node := nodes[rng.Intn(len(nodes))]
			operations = append(operations, op{
				name: "Reserve",
				id:   id,
				node: node,
				pod:  randomPod(rng, id),
			})
			liveIDs = append(liveIDs, id)
		case roll < 50 && len(liveIDs) > 0:
			id := liveIDs[rng.Intn(len(liveIDs))]
			operations = append(operations, op{name: "Commit", id: id})
		case roll < 60 && len(liveIDs) > 0:
			id := liveIDs[rng.Intn(len(liveIDs))]
			operations = append(operations, op{name: "Cancel", id: id})
		case roll < 72 && len(liveIDs) > 0:
			id := liveIDs[rng.Intn(len(liveIDs))]
			operations = append(operations, op{name: "Remove", id: id})
			removedIDs = append(removedIDs, id)
		case roll < 86 && len(liveIDs) > 0:
			id := liveIDs[rng.Intn(len(liveIDs))]
			operations = append(operations, op{name: "Relabel", id: id, labels: randomLabels(rng)})
		case roll < 96:
			operations = append(operations, op{
				name: "Feasible",
				pod:  randomPod(rng, fmt.Sprintf("f%02d", rng.Intn(100000))),
			})
		default:
			name := nodes[rng.Intn(len(nodes))]
			if rng.Intn(2) == 0 {
				operations = append(operations, op{name: "RemoveNode", node: name})
			} else {
				operations = append(operations, op{name: "AddNode", node: name, zone: zones[name]})
			}
		}
	}
	return operations
}

func runOnScheduler(s *Scheduler, operation op) string {
	var err error
	var feasible []string
	switch operation.name {
	case "AddNode":
		err = s.AddNode(operation.node, operation.zone)
	case "RemoveNode":
		err = s.RemoveNode(operation.node)
	case "Place":
		err = s.Place(operation.pod, operation.node)
	case "Reserve":
		err = s.Reserve(operation.pod, operation.node)
	case "Commit":
		err = s.Commit(operation.id)
	case "Cancel":
		err = s.Cancel(operation.id)
	case "Remove":
		err = s.Remove(operation.id)
	case "Relabel":
		err = s.Relabel(operation.id, operation.labels)
	case "Feasible":
		feasible, err = s.Feasible(operation.pod)
	}
	if err != nil {
		if rj, ok := err.(*Reject); ok {
			return reasonString(rj)
		}
		return "ERROR " + err.Error()
	}
	if operation.name != "Feasible" {
		return "ok"
	}
	return "ok feasible=[" + strings.Join(feasible, ",") + "]"
}

func formatOp(operation op) string {
	switch operation.name {
	case "AddNode":
		return fmt.Sprintf("node=%q zone=%q", operation.node, operation.zone)
	case "RemoveNode":
		return fmt.Sprintf("node=%q", operation.node)
	case "Place", "Reserve":
		return fmt.Sprintf("id=%q node=%q labels=%v affinity=%v antiAffinity=%v",
			operation.pod.ID, operation.node, operation.pod.Labels,
			operation.pod.Affinity, operation.pod.AntiAffinity)
	case "Commit", "Cancel", "Remove":
		return fmt.Sprintf("id=%q", operation.id)
	case "Relabel":
		return fmt.Sprintf("id=%q labels=%v", operation.id, operation.labels)
	case "Feasible":
		return fmt.Sprintf("id=%q labels=%v affinity=%v antiAffinity=%v",
			operation.pod.ID, operation.pod.Labels,
			operation.pod.Affinity, operation.pod.AntiAffinity)
	}
	return ""
}

func TestDifferentialRandomSequences(t *testing.T) {
	iterations := 2000
	if testing.Short() {
		iterations = 50
	}
	for seed := int64(0); seed < int64(iterations); seed++ {
		rng := rand.New(rand.NewSource(seed))
		operations := generateSequence(rng)
		scheduler := NewScheduler(3)
		model := newNaive(3)
		firstRun := make([]string, len(operations))
		var logBuilder strings.Builder
		fmt.Fprintf(&logBuilder, "--- seed=%d steps=%d ---\n", seed, len(operations))
		for step, operation := range operations {
			got := runOnScheduler(scheduler, operation)
			want := model.apply(operation)
			firstRun[step] = got
			fmt.Fprintf(&logBuilder, "step=%02d op=%s input={%s}\n    output scheduler=%q | naive=%q\n",
				step, operation.name, formatOp(operation), got, want)
			if got != want {
				t.Fatalf("mismatch seed=%d step=%d op=%s\nscheduler=%q\nnaive=%q\n\n%s",
					seed, step, operation.name, got, want, logBuilder.String())
			}
		}
		// Replay: identical serialized inputs must produce identical outputs.
		replay := NewScheduler(3)
		replayNaive := newNaive(3)
		for step, operation := range operations {
			replayNaiveResult := replayNaive.apply(operation)
			replayResult := runOnScheduler(replay, operation)
			if replayResult != firstRun[step] || replayResult != replayNaiveResult {
				t.Fatalf("replay mismatch seed=%d step=%d op=%s first=%q replay=%q",
					seed, step, operation.name, firstRun[step], replayResult)
			}
		}
		if seed == 0 {
			t.Logf("differential example input/output log:\n%s", logBuilder.String())
		}
		if verboseDifferential && seed > 0 {
			t.Logf("%s", logBuilder.String())
		}
	}
}
