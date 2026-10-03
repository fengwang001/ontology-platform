package repo_test

import (
	"errors"
	"testing"

	"ontology/engine"
	"ontology/model"
	"ontology/repo"
)

// 题干例子：
// 1 Start -> 2 AndSplit；2/0->3 Task, 2/1->4 Task；
// 3->5 XorSplit；4->7 OrJoin；5/0->6 Task, 5/1->7；6->8 End；7->8。
// choices {5:[0]}：5->7 被裁掉，OrJoin 7 不应再等任务 3 的令牌。
func exampleGraph() *model.Graph {
	return &model.Graph{
		N: 8,
		Kinds: []model.NodeType{
			0, model.Start, model.AndSplit, model.Task, model.Task,
			model.XorSplit, model.Task, model.OrJoin, model.End,
		},
		Edges: []model.Edge{
			{U: 1, V: 2}, {U: 2, V: 3}, {U: 2, V: 4}, {U: 3, V: 5}, {U: 4, V: 7},
			{U: 5, V: 6}, {U: 5, V: 7}, {U: 6, V: 8}, {U: 7, V: 8},
		},
	}
}

func startExample(t *testing.T, r *repo.Repo, inst string) {
	t.Helper()
	if err := r.Define("d", exampleGraph()); err != nil {
		t.Fatalf("define: %v", err)
	}
	if err := r.Start(inst, "d", map[int][]int{5: {0}}); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func TestExampleOrJoinPrunedBranch(t *testing.T) {
	for _, order := range [][]int{{4, 3, 6}, {3, 4, 6}} {
		r := repo.New()
		startExample(t, r, "i")
		for _, task := range order {
			if err := r.Complete("i", task); err != nil {
				t.Fatalf("complete %d: %v", task, err)
			}
			logState(t, r, task)
		}
		st, err := r.Status("i")
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if st.Status != engine.Completed || st.EndCount != 2 {
			t.Fatalf("order %v: got %+v, want Completed end=2", order, st)
		}
		if fires(st, 7) != 1 {
			t.Fatalf("order %v: OrJoin 7 fires=%d, want 1", order, fires(st, 7))
		}
	}
}

// 中途判定：Complete(4) 后，OrJoin 7 已合并放行，endCount=1。
func TestExampleOrJoinFiresImmediately(t *testing.T) {
	r := repo.New()
	startExample(t, r, "i")
	if err := r.Complete("i", 4); err != nil {
		t.Fatal(err)
	}
	st, _ := r.Status("i")
	logState(t, r, 4)
	if st.EndCount != 1 || fires(st, 7) != 1 || st.Status != engine.Running {
		t.Fatalf("after Complete(4): %+v, want end=1 fires7=1 Running", st)
	}
}

// 版本固定：旧实例按 v1 跑完，Define v2 不影响在途实例。
func TestVersionPinning(t *testing.T) {
	r := repo.New()
	startExample(t, r, "old")

	g2 := &model.Graph{
		N: 3,
		Kinds: []model.NodeType{
			0, model.Start, model.XorSplit, model.End,
		},
		Edges: []model.Edge{{U: 1, V: 2}, {U: 2, V: 3}},
	}
	if err := r.Define("d", g2); err != nil { // 结构不合法，不应占版本
		if !errors.Is(err, model.ErrStructure) {
			t.Fatalf("want ErrStructure, got %v", err)
		}
	}
	if err := r.Define("other", exampleGraph()); err != nil {
		t.Fatal(err)
	}
	// 旧实例仍可按 v1 执行。
	if err := r.Complete("old", 4); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("old", 3); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("old", 6); err != nil {
		t.Fatal(err)
	}
	st, _ := r.Status("old")
	if st.Status != engine.Completed || st.EndCount != 2 {
		t.Fatalf("pinned instance drifted: %+v", st)
	}
	// 新实例从"d"启动，拿到的仍是最初的例子图（非法 Define 未占版本号）。
	if err := r.Start("new", "d", map[int][]int{5: {1}}); err != nil {
		t.Fatal(err)
	}
}

func TestErrorPriority(t *testing.T) {
	r := repo.New()
	if err := r.Define("", exampleGraph()); !errors.Is(err, repo.ErrInvalidArg) {
		t.Fatalf("empty defID: %v", err)
	}
	if err := r.Start("i", "missing", nil); !errors.Is(err, repo.ErrDefNotFound) {
		t.Fatalf("missing def: %v", err)
	}
	startExample(t, r, "i")
	if err := r.Start("i", "d", map[int][]int{5: {0}}); !errors.Is(err, repo.ErrInstanceExist) {
		t.Fatalf("dup instance: %v", err)
	}
	for _, bad := range []map[int][]int{
		nil,              // 缺失
		{5: {2}},         // 越界
		{5: {0, 1}},      // XorSplit 多选
		{5: {0}, 3: {0}}, // 多余节点
		{5: {}},          // 空选择
		{5: {-1}},        // 越界负号
	} {
		if err := r.Start("x", "d", bad); !errors.Is(err, repo.ErrChoice) {
			t.Fatalf("choice %v: got %v, want ErrChoice", bad, err)
		}
	}
	if _, err := r.Status("x"); !errors.Is(err, repo.ErrNoInstance) {
		t.Fatalf("status missing: %v", err)
	}
	if err := r.Complete("i", 6); !errors.Is(err, engine.ErrNoActive) {
		t.Fatalf("no active: %v", err)
	}
	if err := r.Complete("missing", 1); !errors.Is(err, repo.ErrNoInstance) {
		t.Fatalf("complete missing inst: %v", err)
	}
	if err := r.Complete("", 1); !errors.Is(err, repo.ErrInvalidArg) {
		t.Fatalf("empty inst: %v", err)
	}
}

func fires(st engine.State, node int) int {
	for _, j := range st.Joins {
		if j.Node == node {
			return j.Fires
		}
	}
	return -1
}

func logState(t *testing.T, r *repo.Repo, step int) {
	t.Helper()
	st, err := r.Status("i")
	if err != nil {
		st, _ = r.Status("old")
	}
	t.Logf("input=Complete(%d) output=status=%s endCount=%d joins=%+v basis=%q",
		step, st.Status, st.EndCount, st.Joins,
		"effective graph reachability + join fixed-point by node id")
}
