package model_test

import (
	"errors"
	"testing"

	"ontology/model"
)

func g(n int, kind []model.NodeType, edges ...model.Edge) *model.Graph {
	k := append([]model.NodeType{model.Start, model.Start}, kind...)
	return &model.Graph{N: n, Kind: k, Edges: edges}
}

// chain is a minimal valid graph: 1 Start -> 2 Task -> 3 End.
func chain() *model.Graph {
	return g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3})
}

func TestValidGraph(t *testing.T) {
	if err := model.Validate(chain()); err != nil {
		t.Fatalf("chain graph should be valid, got %v", err)
	}
}

func TestStructureErrors(t *testing.T) {
	cases := map[string]*model.Graph{
		"n too small":     g(0, nil),
		"n too large":     g(65, make([]model.NodeType, 65)),
		"endpoint zero":   g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 0, To: 2}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}),
		"endpoint beyond": g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 1, To: 4}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}),
		"self loop":       g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 2}, model.Edge{From: 2, To: 3}),
		"duplicate edge":  g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}),
		"start has in":    g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 2, To: 1}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}),
		"start two out":   g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 1, To: 3}, model.Edge{From: 2, To: 3}),
		"task no in":      g(3, []model.NodeType{model.Task, model.End}, model.Edge{From: 1, To: 3}, model.Edge{From: 2, To: 3}),
		"task two out":    g(4, []model.NodeType{model.Task, model.End, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}, model.Edge{From: 2, To: 4}),
		"split one out":   g(3, []model.NodeType{model.AndSplit, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}),
		"split two in": g(7, []model.NodeType{model.AndSplit, model.Task, model.Task, model.AndSplit, model.Task, model.End},
			model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}, model.Edge{From: 2, To: 4}, model.Edge{From: 3, To: 5}, model.Edge{From: 4, To: 5},
			model.Edge{From: 5, To: 6}, model.Edge{From: 5, To: 7}, model.Edge{From: 6, To: 7}),
		"join one in":   g(3, []model.NodeType{model.AndJoin, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}),
		"end has out":   g(3, []model.NodeType{model.End, model.End}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}),
		"two starts":    g(4, []model.NodeType{model.Start, model.Task, model.End}, model.Edge{From: 1, To: 3}, model.Edge{From: 2, To: 3}, model.Edge{From: 3, To: 4}),
		"bad node type": {N: 3, Kind: []model.NodeType{model.Start, model.Start, model.NodeType(99), model.End}, Edges: []model.Edge{{1, 2}, {2, 3}}},
	}
	for name, gr := range cases {
		if err := model.Validate(gr); !errors.Is(err, model.ErrStructure) {
			t.Errorf("%s: want ErrStructure, got %v", name, err)
		}
	}
}

func TestCycleError(t *testing.T) {
	// 1 Start -> 2 AndSplit -> {3 Task, 5 End}; 3 -> 4 Task -> 3 (cycle).
	gr := g(5, []model.NodeType{model.AndSplit, model.Task, model.Task, model.End},
		model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}, model.Edge{From: 2, To: 5}, model.Edge{From: 3, To: 4}, model.Edge{From: 4, To: 3})
	if err := model.Validate(gr); !errors.Is(err, model.ErrCycle) {
		t.Fatalf("want ErrCycle, got %v", err)
	}
}

func TestErrorOrder(t *testing.T) {
	// Both a duplicate edge (structure) and a cycle: structure wins.
	gr := g(3, []model.NodeType{model.Task, model.End},
		model.Edge{From: 1, To: 2}, model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 2})
	if err := model.Validate(gr); !errors.Is(err, model.ErrStructure) {
		t.Fatalf("structure must be reported before cycle, got %v", err)
	}
}

// In a structurally valid acyclic graph every node is automatically
// reachable from Start and can reach an End (in-degree >= 1 chains end at
// Start, out-degree >= 1 chains end at End), so ErrReach is a defensive
// check: this test documents that a tricky-looking valid DAG passes.
func TestReachHoldsForValidDAG(t *testing.T) {
	gr := g(6, []model.NodeType{model.AndSplit, model.Task, model.Task, model.AndJoin, model.End},
		model.Edge{From: 1, To: 2}, model.Edge{From: 2, To: 3}, model.Edge{From: 2, To: 4}, model.Edge{From: 3, To: 5}, model.Edge{From: 4, To: 5},
		model.Edge{From: 5, To: 6})
	if err := model.Validate(gr); err != nil {
		t.Fatalf("valid DAG should pass reach check, got %v", err)
	}
}
