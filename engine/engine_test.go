package engine

import (
	"errors"
	"testing"

	"ontology/model"
)

func mkGraph(n int, kinds []model.NodeType, edges ...model.Edge) *model.Graph {
	return &model.Graph{N: n, Kinds: kinds, Edges: edges}
}

func mustStart(t *testing.T, g *model.Graph, ch model.Choice) *Instance {
	t.Helper()
	if err := g.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := g.ValidateChoice(ch); err != nil {
		t.Fatalf("choice: %v", err)
	}
	return New(g, ch)
}

// AndSplit -> AndJoin：两个 Task 都完成后汇合才触发一次。
func TestAndSplitAndJoin(t *testing.T) {
	g := mkGraph(6,
		[]model.NodeType{0, model.Start, model.AndSplit, model.Task, model.Task, model.AndJoin, model.End},
		model.Edge{U: 1, V: 2}, model.Edge{U: 2, V: 3}, model.Edge{U: 2, V: 4},
		model.Edge{U: 3, V: 5}, model.Edge{U: 4, V: 5}, model.Edge{U: 5, V: 6})
	in := mustStart(t, g, model.Choice{})
	if in.act[3] != 1 || in.act[4] != 1 {
		t.Fatalf("want act[3]=act[4]=1, got %d %d", in.act[3], in.act[4])
	}
	if err := in.Complete(3); err != nil {
		t.Fatal(err)
	}
	if in.fires[5] != 0 || in.Status().Status != Running {
		t.Fatalf("AndJoin must wait: %+v", in.Status())
	}
	if err := in.Complete(4); err != nil {
		t.Fatal(err)
	}
	st := in.Status()
	if st.Status != Completed || st.EndCount != 1 || firesAt(st, 5) != 1 {
		t.Fatalf("after both: %+v", st)
	}
	t.Logf("input=AndSplit graph; output=%+v; basis=AndJoin waits for every in-edge arr>=1", st)
}

// XorSplit 只沿选定边发令牌。
func TestXorSplitChoice(t *testing.T) {
	g := mkGraph(4,
		[]model.NodeType{0, model.Start, model.XorSplit, model.End, model.End},
		model.Edge{U: 1, V: 2}, model.Edge{U: 2, V: 3}, model.Edge{U: 2, V: 4})
	for _, pick := range []int{0, 1} {
		in := mustStart(t, g, model.Choice{2: {pick}})
		st := in.Status()
		if st.Status != Completed || st.EndCount != 1 {
			t.Fatalf("pick %d: %+v", pick, st)
		}
		t.Logf("input=XorSplit pick=%d output=%+v basis=only selected edge carries token", pick, st)
	}
}

// OrSplit 选单支：OrJoin 触发一次；全选：两支令牌先后到达，仍只触发一次。
func TestOrSplitJoin(t *testing.T) {
	g := mkGraph(6,
		[]model.NodeType{0, model.Start, model.OrSplit, model.Task, model.Task, model.OrJoin, model.End},
		model.Edge{U: 1, V: 2}, model.Edge{U: 2, V: 3}, model.Edge{U: 2, V: 4},
		model.Edge{U: 3, V: 5}, model.Edge{U: 4, V: 5}, model.Edge{U: 5, V: 6})
	// 单支：只有 3 有令牌，Complete(3) 后 5 立即放行。
	in := mustStart(t, g, model.Choice{2: {0}})
	if in.act[3] != 1 || in.act[4] != 0 {
		t.Fatalf("single branch act: %d %d", in.act[3], in.act[4])
	}
	if err := in.Complete(3); err != nil {
		t.Fatal(err)
	}
	st := in.Status()
	if st.Status != Completed || st.EndCount != 1 || firesAt(st, 5) != 1 {
		t.Fatalf("single branch: %+v", st)
	}
	// 全选：两支都激活；先到的令牌会等另一支，汇合只触发一次。
	in = mustStart(t, g, model.Choice{2: {0, 1}})
	if err := in.Complete(3); err != nil {
		t.Fatal(err)
	}
	if firesAt(in.Status(), 5) != 0 {
		t.Fatalf("OrJoin must wait for reachable task 4: %+v", in.Status())
	}
	if err := in.Complete(4); err != nil {
		t.Fatal(err)
	}
	st = in.Status()
	if st.Status != Completed || st.EndCount != 1 || firesAt(st, 5) != 1 {
		t.Fatalf("both branches merge once: %+v", st)
	}
	t.Logf("input=OrSplit both output=%+v basis=OrJoin one fire consumes all arrived tokens", st)
}

// AndJoin 因裁剪缺少分支：无 act 但 arr 非 0 => Stuck。
func TestAndJoinStuckByPruning(t *testing.T) {
	g := mkGraph(6,
		[]model.NodeType{0, model.Start, model.XorSplit, model.Task, model.Task, model.AndJoin, model.End},
		model.Edge{U: 1, V: 2}, model.Edge{U: 2, V: 3}, model.Edge{U: 2, V: 4},
		model.Edge{U: 3, V: 5}, model.Edge{U: 4, V: 5}, model.Edge{U: 5, V: 6})
	in := mustStart(t, g, model.Choice{2: {0}})
	if err := in.Complete(3); err != nil {
		t.Fatal(err)
	}
	st := in.Status()
	if st.Status != Stuck || st.EndCount != 0 || firesAt(st, 5) != 0 {
		t.Fatalf("want Stuck with arr>0, got %+v", st)
	}
	if err := in.Complete(3); !errors.Is(err, ErrNoActive) {
		t.Fatalf("re-complete on stuck: %v", err)
	}
	t.Logf("input=XorSplit pick edge0 only output=%+v basis=AndJoin cannot see pruned branch => Stuck", st)
}

// 多入边 Task：两支令牌先后到达，act 两次激活，需要 Complete 两次。
func TestMultiInTask(t *testing.T) {
	g := mkGraph(6,
		[]model.NodeType{0, model.Start, model.OrSplit, model.Task, model.Task, model.Task, model.End},
		model.Edge{U: 1, V: 2}, model.Edge{U: 2, V: 3}, model.Edge{U: 2, V: 4},
		model.Edge{U: 3, V: 5}, model.Edge{U: 4, V: 5}, model.Edge{U: 5, V: 6})
	in := mustStart(t, g, model.Choice{2: {0, 1}})
	if in.act[3] != 1 || in.act[4] != 1 {
		t.Fatalf("branches: %d %d", in.act[3], in.act[4])
	}
	if err := in.Complete(3); err != nil {
		t.Fatal(err)
	}
	if in.act[5] != 1 {
		t.Fatalf("first arrival act[5]=%d, want 1", in.act[5])
	}
	if err := in.Complete(4); err != nil {
		t.Fatal(err)
	}
	if in.act[5] != 2 {
		t.Fatalf("multi-in task activations=%d, want 2", in.act[5])
	}
	if err := in.Complete(5); err != nil {
		t.Fatal(err)
	}
	if in.act[5] != 1 {
		t.Fatalf("one activation remains: %d", in.act[5])
	}
	if err := in.Complete(5); err != nil {
		t.Fatal(err)
	}
	st := in.Status()
	if st.Status != Completed || st.EndCount != 2 {
		t.Fatalf("multi-in: %+v", st)
	}
	t.Logf("input=OrSplit both into Task5 output=%+v basis=each in-edge arrival activates task once", st)
}

func firesAt(st State, node int) int {
	for _, j := range st.Joins {
		if j.Node == node {
			return j.Fires
		}
	}
	return -1
}
