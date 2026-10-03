package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type refNode struct {
	parent int64
	name   string
}

type referenceState struct {
	nodes map[int64]refNode
	log   map[Key]bool
}

func newReferenceState() referenceState {
	return referenceState{
		nodes: map[int64]refNode{0: {}, 1: {}},
		log:   map[Key]bool{},
	}
}

func referenceApply(state referenceState, operation Op) bool {
	if _, ok := state.nodes[operation.Parent]; !ok {
		return false
	}
	current := operation.Parent
	seen := map[int64]bool{}
	for {
		if current == operation.Node {
			return false
		}
		if current == 0 || current == 1 {
			break
		}
		if seen[current] {
			panic("reference tree contains a cycle")
		}
		seen[current] = true
		current = state.nodes[current].parent
	}
	state.nodes[operation.Node] = refNode{parent: operation.Parent, name: operation.Name}
	return true
}

func replayReference(operations []Op) referenceState {
	ordered := append([]Op(nil), operations...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return compareKey(keyOf(ordered[i]), keyOf(ordered[j])) < 0
	})
	state := newReferenceState()
	for _, operation := range ordered {
		state.log[keyOf(operation)] = referenceApply(state, operation)
	}
	return state
}

func expectedApplyResult(previous []Op, operation Op) ApplyResult {
	before := replayReference(previous)
	after := replayReference(append(append([]Op(nil), previous...), operation))
	result := ApplyResult{Effective: after.log[keyOf(operation)], Changed: []Key{}}
	for k, wasEffective := range before.log {
		if compareKey(k, keyOf(operation)) > 0 && wasEffective != after.log[k] {
			result.Changed = append(result.Changed, k)
		}
	}
	sort.Slice(result.Changed, func(i, j int) bool {
		return compareKey(result.Changed[i], result.Changed[j]) < 0
	})
	return result
}

func expectedRedone(previous []Op, operation Op) int64 {
	var count int64
	newKey := keyOf(operation)
	for _, old := range previous {
		if compareKey(keyOf(old), newKey) > 0 {
			count++
		}
	}
	return count
}

func sameReferenceState(t *testing.T, r *Replayer, ref referenceState, input []Op) {
	t.Helper()
	current := r.currentTree()
	for node := range ref.nodes {
		if node < 2 {
			continue
		}
		got, ok := current.nodes[node]
		want := ref.nodes[node]
		if !ok || got.parent != want.parent || got.name != want.name {
			t.Fatalf("node %d state = %+v/%t, want %+v; input=%v", node, got, ok, want, input)
		}
	}
	for node := range current.nodes {
		if _, ok := ref.nodes[node]; !ok {
			t.Fatalf("unexpected node %d; input=%v", node, input)
		}
	}
	for k, want := range ref.log {
		found := false
		for _, entry := range r.Log() {
			if entry.Key == k {
				found = true
				if entry.Effective != want {
					t.Fatalf("entry %+v effective=%t, want %t; input=%v", k, entry.Effective, want, input)
				}
			}
		}
		if !found {
			t.Fatalf("missing log entry %+v; input=%v", k, input)
		}
	}
}

func TestRandomArrivalMatchesNaiveReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	replicas := []string{"A", "B", "C"}
	for trial := 0; trial < 2000; trial++ {
		operationCount := rng.Intn(16) + 1
		nodeCount := rng.Intn(8) + 2
		operations := make([]Op, 0, operationCount)
		seen := map[Key]bool{}
		for len(operations) < operationCount {
			operation := Op{
				TS:     int64(rng.Intn(12) + 1),
				Rep:    replicas[rng.Intn(len(replicas))],
				Node:   int64(rng.Intn(nodeCount) + 2),
				Parent: int64(rng.Intn(nodeCount + 2)),
				Name:   fmt.Sprintf("n%d", rng.Intn(5)),
			}
			k := keyOf(operation)
			if seen[k] {
				continue
			}
			seen[k] = true
			operations = append(operations, operation)
		}

		orders := make([][]Op, 2)
		for i := range orders {
			orders[i] = append([]Op(nil), operations...)
			rng.Shuffle(len(orders[i]), func(a, b int) {
				orders[i][a], orders[i][b] = orders[i][b], orders[i][a]
			})
		}

		results := make([][]ApplyResult, len(orders))
		expectedResults := make([][]ApplyResult, len(orders))
		redone := make([]int64, len(orders))
		redoneExpectations := make([]int64, len(orders))
		players := make([]*Replayer, len(orders))
		for i, order := range orders {
			players[i] = New(replicas)
			for applied, operation := range order {
				expectedResults[i] = append(expectedResults[i], expectedApplyResult(order[:applied], operation))
				redoneExpectations[i] += expectedRedone(order[:applied], operation)
				result, err := players[i].Apply(operation)
				if err != nil {
					t.Fatalf("trial %d input %+v: %v", trial, operation, err)
				}
				results[i] = append(results[i], result)
			}
			redone[i] = players[i].redone
		}

		ref := replayReference(operations)
		t.Run(fmt.Sprintf("trial%04d", trial), func(t *testing.T) {
			t.Logf("input operations: %v", operations)
			t.Logf("first arrival: %v outputs: %+v redone: %d", orders[0], results[0], redone[0])
			t.Logf("second arrival: %v outputs: %+v redone: %d", orders[1], results[1], redone[1])
			if !reflect.DeepEqual(results[0], expectedResults[0]) {
				t.Fatalf("first outputs = %+v, want %+v", results[0], expectedResults[0])
			}
			if !reflect.DeepEqual(results[1], expectedResults[1]) {
				t.Fatalf("second outputs = %+v, want %+v", results[1], expectedResults[1])
			}
			if redone[0] != redoneExpectations[0] || redone[1] != redoneExpectations[1] {
				t.Fatalf("redone = (%d,%d), want (%d,%d)", redone[0], redone[1], redoneExpectations[0], redoneExpectations[1])
			}
			t.Log("decision basis: skipped when parent is absent or parent's ancestor chain reaches node")
			sameReferenceState(t, players[0], ref, operations)
			sameReferenceState(t, players[1], ref, operations)
			if !reflect.DeepEqual(players[0].Log(), players[1].Log()) {
				t.Fatalf("logs differ: %+v != %+v", players[0].Log(), players[1].Log())
			}
			root := players[0].currentTree()
			for node := range root.nodes {
				if node < 2 {
					continue
				}
				current := root.nodes[node].parent
				for current != 0 && current != 1 {
					if _, ok := root.nodes[current]; !ok {
						t.Fatalf("node %d reaches missing ancestor %d", node, current)
					}
					current = root.nodes[current].parent
				}
			}
		})
	}
}
