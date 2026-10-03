package repo_test

import (
	"errors"
	"testing"

	"ontology/engine"
	"ontology/model"
	"ontology/repo"
)

// xorGraph has XorSplit 2 with edge 0 -> task 3, edge 1 -> task 4.
func xorGraph() *model.Graph {
	return &model.Graph{
		N:     5,
		Kind:  []model.NodeType{model.Start, model.Start, model.XorSplit, model.Task, model.Task, model.End},
		Edges: []model.Edge{{From: 1, To: 2}, {From: 2, To: 3}, {From: 2, To: 4}, {From: 3, To: 5}, {From: 4, To: 5}},
	}
}

// swappedGraph routes edge 0 -> task 4, edge 1 -> task 3 instead.
func swappedGraph() *model.Graph {
	g := xorGraph()
	g.Edges[1], g.Edges[2] = model.Edge{From: 2, To: 4}, model.Edge{From: 2, To: 3}
	return g
}

func cyclicGraph() *model.Graph {
	return &model.Graph{
		N:     5,
		Kind:  []model.NodeType{model.Start, model.Start, model.AndSplit, model.Task, model.Task, model.End},
		Edges: []model.Edge{{From: 1, To: 2}, {From: 2, To: 3}, {From: 2, To: 5}, {From: 3, To: 4}, {From: 4, To: 3}},
	}
}

func TestDefineVersions(t *testing.T) {
	r := repo.New()
	for want := 1; want <= 2; want++ {
		got, err := r.Define("d", xorGraph())
		if err != nil || got != want {
			t.Fatalf("Define #%d: got version %d, err %v", want, got, err)
		}
	}
	if _, err := r.Define("d", cyclicGraph()); !errors.Is(err, model.ErrCycle) {
		t.Fatalf("cyclic define: got %v", err)
	}
	got, err := r.Define("d", xorGraph())
	if err != nil || got != 3 {
		t.Fatalf("rejected Define must not consume a version: got %d, err %v", got, err)
	}
	if _, err := r.Define("", xorGraph()); !errors.Is(err, repo.ErrParam) {
		t.Fatalf("empty defID: got %v", err)
	}
}

func TestStartRejections(t *testing.T) {
	r := repo.New()
	if _, err := r.Define("d", xorGraph()); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("", "d", nil); !errors.Is(err, repo.ErrParam) {
		t.Fatalf("empty inst: got %v", err)
	}
	if err := r.Start("i", "nope", nil); !errors.Is(err, repo.ErrNoDef) {
		t.Fatalf("unknown def: got %v", err)
	}
	if err := r.Start("i", "d", map[int][]int{2: {0}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("i", "d", map[int][]int{2: {0}}); !errors.Is(err, repo.ErrInstExists) {
		t.Fatalf("duplicate inst: got %v", err)
	}
	if err := r.Complete("ghost", 3); !errors.Is(err, repo.ErrNoInst) {
		t.Fatalf("unknown inst: got %v", err)
	}
	if _, err := r.Status("ghost"); !errors.Is(err, repo.ErrNoInst) {
		t.Fatalf("unknown inst status: got %v", err)
	}
}

func TestChoiceRejections(t *testing.T) {
	cases := map[string]map[int][]int{
		"missing split":   {},
		"extra node":      {2: {0}, 3: {0}},
		"index too large": {2: {2}},
		"negative index":  {2: {-1}},
		"duplicate index": {2: {0, 0}},
		"xor needs one":   {2: {0, 1}},
		"xor empty":       {2: {}},
	}
	for name, choices := range cases {
		r := repo.New()
		if _, err := r.Define("d", xorGraph()); err != nil {
			t.Fatal(err)
		}
		if err := r.Start("i", "d", choices); !errors.Is(err, repo.ErrChoice) {
			t.Errorf("%s: want ErrChoice, got %v", name, err)
		}
		if err := r.Start("i", "d", map[int][]int{2: {0}}); err != nil {
			t.Errorf("%s: rejected Start must not create the instance: %v", name, err)
		}
	}
}

func TestVersionPinning(t *testing.T) {
	r := repo.New()
	if _, err := r.Define("d", xorGraph()); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("old", "d", map[int][]int{2: {0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Define("d", swappedGraph()); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("new", "d", map[int][]int{2: {0}}); err != nil {
		t.Fatal(err)
	}
	// old pinned v1: edge 0 -> task 3; new pinned v2: edge 0 -> task 4.
	if err := r.Complete("old", 3); err != nil {
		t.Fatalf("old instance must follow v1: %v", err)
	}
	if err := r.Complete("old", 4); !errors.Is(err, engine.ErrNoActive) {
		t.Fatalf("old instance must not see v2 routing: %v", err)
	}
	if err := r.Complete("new", 4); err != nil {
		t.Fatalf("new instance must follow v2: %v", err)
	}
	for _, inst := range []string{"old", "new"} {
		s, err := r.Status(inst)
		if err != nil || s.State != engine.Completed || s.EndCount != 1 {
			t.Fatalf("%s: got %+v, err %v", inst, s, err)
		}
		t.Logf("inst=%s -> %+v (pinned to its own version)", inst, s)
	}
}
