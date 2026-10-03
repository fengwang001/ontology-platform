package engine_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/engine"
	"ontology/model"
)

func gr(n int, kind []model.NodeType, edges [][2]int) *model.Graph {
	k := append([]model.NodeType{model.Start, model.Start}, kind...)
	g := &model.Graph{N: n, Kind: k}
	for _, e := range edges {
		g.Edges = append(g.Edges, model.Edge{From: e[0], To: e[1]})
	}
	if err := model.Validate(g); err != nil {
		panic(err)
	}
	return g
}

// The example from the specification.
func specGraph() *model.Graph {
	return gr(8,
		[]model.NodeType{model.AndSplit, model.Task, model.Task, model.XorSplit, model.Task, model.OrJoin, model.End},
		[][2]int{{1, 2}, {2, 3}, {2, 4}, {3, 5}, {4, 7}, {5, 6}, {5, 7}, {6, 8}, {7, 8}})
}

func TestSpecExample(t *testing.T) {
	for _, order := range [][]int{{4, 3, 6}, {3, 6, 4}} {
		in := engine.NewInstance(specGraph(), map[int][]int{5: {0}})
		for i, task := range order {
			if err := in.Complete(task); err != nil {
				t.Fatalf("order %v: %v", order, err)
			}
			if i == 0 && task == 4 {
				s := in.Status()
				if s.EndCount != 1 || s.Fires[7] != 1 {
					t.Fatalf("pruned graph must let OrJoin 7 fire at once, got %+v", s)
				}
			}
		}
		s := in.Status()
		if s.State != engine.Completed || s.EndCount != 2 || s.Fires[7] != 1 {
			t.Fatalf("order %v: got %+v", order, s)
		}
		t.Logf("order=%v -> state=%v end=%d fires=%v", order, s.State, s.EndCount, s.Fires)
	}
}

func TestAndJoinSemantics(t *testing.T) {
	in := engine.NewInstance(gr(6,
		[]model.NodeType{model.AndSplit, model.Task, model.Task, model.AndJoin, model.End},
		[][2]int{{1, 2}, {2, 3}, {2, 4}, {3, 5}, {4, 5}, {5, 6}}), nil)
	mustComplete(t, in, 3)
	if s := in.Status(); s.Fires[5] != 0 || s.EndCount != 0 {
		t.Fatalf("AndJoin must wait for all in-edges, got %+v", s)
	}
	mustComplete(t, in, 4)
	if s := in.Status(); s.State != engine.Completed || s.EndCount != 1 || s.Fires[5] != 1 {
		t.Fatalf("AndJoin should fire once, got %+v", s)
	}
}

func TestOrJoinSemantics(t *testing.T) {
	in := engine.NewInstance(gr(6,
		[]model.NodeType{model.AndSplit, model.Task, model.Task, model.OrJoin, model.End},
		[][2]int{{1, 2}, {2, 3}, {2, 4}, {3, 5}, {4, 5}, {5, 6}}), nil)
	mustComplete(t, in, 3)
	if s := in.Status(); s.Fires[5] != 0 || s.EndCount != 0 {
		t.Fatalf("OrJoin must wait while task 4 can still reach it, got %+v", s)
	}
	mustComplete(t, in, 4)
	s := in.Status()
	if s.State != engine.Completed || s.EndCount != 1 || s.Fires[5] != 1 {
		t.Fatalf("one trigger must merge both arrivals, got %+v", s)
	}
	t.Logf("two arrivals merged into one trigger: %+v", s)
}

func TestXorSplitChoice(t *testing.T) {
	in := engine.NewInstance(gr(6,
		[]model.NodeType{model.Task, model.XorSplit, model.Task, model.Task, model.End},
		[][2]int{{1, 2}, {2, 3}, {3, 4}, {3, 5}, {4, 6}, {5, 6}}),
		map[int][]int{3: {1}})
	mustComplete(t, in, 2)
	if err := in.Complete(4); !errors.Is(err, engine.ErrNoActive) {
		t.Fatalf("unchosen branch must stay inactive, got %v", err)
	}
	mustComplete(t, in, 5)
	if s := in.Status(); s.State != engine.Completed || s.EndCount != 1 {
		t.Fatalf("got %+v", s)
	}
}

func TestOrSplitSingleAndAll(t *testing.T) {
	build := func() *model.Graph {
		return gr(6, []model.NodeType{model.OrSplit, model.Task, model.Task, model.Task, model.End},
			[][2]int{{1, 2}, {2, 3}, {2, 4}, {2, 5}, {3, 6}, {4, 6}, {5, 6}})
	}
	one := engine.NewInstance(build(), map[int][]int{2: {1}})
	if err := one.Complete(3); !errors.Is(err, engine.ErrNoActive) {
		t.Fatalf("unselected edge must not produce a token, got %v", err)
	}
	mustComplete(t, one, 4)
	if s := one.Status(); s.State != engine.Completed || s.EndCount != 1 {
		t.Fatalf("single selection: got %+v", s)
	}
	all := engine.NewInstance(build(), map[int][]int{2: {0, 1, 2}})
	for _, task := range []int{3, 4, 5} {
		mustComplete(t, all, task)
	}
	if s := all.Status(); s.State != engine.Completed || s.EndCount != 3 {
		t.Fatalf("full selection: got %+v", s)
	}
}

func TestAndJoinStuckByPruning(t *testing.T) {
	in := engine.NewInstance(gr(7,
		[]model.NodeType{model.AndSplit, model.Task, model.XorSplit, model.Task, model.AndJoin, model.End},
		[][2]int{{1, 2}, {2, 3}, {2, 4}, {3, 6}, {4, 5}, {4, 6}, {5, 7}, {6, 7}}),
		map[int][]int{4: {0}})
	mustComplete(t, in, 3)
	mustComplete(t, in, 5)
	s := in.Status()
	if s.State != engine.Stuck || s.EndCount != 1 || s.Fires[6] != 0 {
		t.Fatalf("AndJoin waits for pruned branch forever: got %+v", s)
	}
	t.Logf("stuck as expected: %+v", s)
}

func TestMultiInTask(t *testing.T) {
	in := engine.NewInstance(gr(6,
		[]model.NodeType{model.AndSplit, model.Task, model.Task, model.Task, model.End},
		[][2]int{{1, 2}, {2, 3}, {2, 4}, {3, 5}, {4, 5}, {5, 6}}), nil)
	mustComplete(t, in, 3)
	mustComplete(t, in, 4)
	mustComplete(t, in, 5)
	if s := in.Status(); s.State != engine.Running || s.EndCount != 1 {
		t.Fatalf("task 5 activated twice, one token left: got %+v", s)
	}
	mustComplete(t, in, 5)
	if s := in.Status(); s.State != engine.Completed || s.EndCount != 2 {
		t.Fatalf("got %+v", s)
	}
}

func TestConcurrentComplete(t *testing.T) {
	in := engine.NewInstance(gr(9,
		[]model.NodeType{model.AndSplit, model.Task, model.Task, model.Task, model.Task, model.Task, model.AndJoin, model.End},
		[][2]int{{1, 2}, {2, 3}, {2, 4}, {2, 5}, {2, 6}, {2, 7},
			{3, 8}, {4, 8}, {5, 8}, {6, 8}, {7, 8}, {8, 9}}), nil)
	var wg sync.WaitGroup
	for task := 3; task <= 7; task++ {
		wg.Add(1)
		go func() { defer wg.Done(); mustComplete(t, in, task) }()
	}
	wg.Wait()
	if s := in.Status(); s.State != engine.Completed || s.EndCount != 1 || s.Fires[8] != 1 {
		t.Fatalf("concurrent completes must equal a serial order, got %+v", s)
	}
}

func TestOrJoinChainSizes(t *testing.T) {
	for _, n := range []int{10, 64} {
		kind := []model.NodeType{model.AndSplit, model.Task, model.Task, model.OrJoin}
		for v := 6; v < n; v++ {
			kind = append(kind, model.Task)
		}
		kind = append(kind, model.End)
		edges := [][2]int{{1, 2}, {2, 3}, {2, 4}, {3, 5}, {4, 5}, {5, 6}}
		for v := 6; v < n; v++ {
			edges = append(edges, [2]int{v, v + 1})
		}
		in := engine.NewInstance(gr(n, kind, edges), nil)
		mustComplete(t, in, 3)
		if s := in.Status(); s.Fires[5] != 0 {
			t.Fatalf("n=%d: task 4 can still reach OrJoin 5, got %+v", n, s)
		}
		mustComplete(t, in, 4)
		for v := 6; v < n; v++ {
			mustComplete(t, in, v)
		}
		if s := in.Status(); s.State != engine.Completed || s.EndCount != 1 || s.Fires[5] != 1 {
			t.Fatalf("n=%d: got %+v", n, s)
		}
		t.Logf("n=%d -> end=1 fires=%v (verdict independent of total node count)", n, in.Status().Fires)
	}
}

func mustComplete(t *testing.T, in *engine.Instance, task int) {
	t.Helper()
	if err := in.Complete(task); err != nil {
		t.Fatalf("Complete(%d): %v", task, err)
	}
}
