package lineage

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

// 构造菱形血缘：a -> x -> m <- y <- b。
func buildDiamond(t *testing.T, logOutput *bytes.Buffer) *Tracker {
	t.Helper()
	var w *bytes.Buffer
	if logOutput != nil {
		w = logOutput
	} else {
		w = &bytes.Buffer{}
	}
	tr := New(w)

	a := mustSource(t, tr, "a", "raw-a")
	b := mustSource(t, tr, "b", "raw-b")
	mustDerive(t, tr, DeriveRequest{OutputID: "x", Operation: "filter", Inputs: []Ref{a}, Payload: "x1"})
	mustDerive(t, tr, DeriveRequest{OutputID: "y", Operation: "clean", Inputs: []Ref{b}, Payload: "y1"})
	mustDerive(t, tr, DeriveRequest{OutputID: "m", Operation: "join", Inputs: []Ref{
		mustCurrent(t, tr, "x"), mustCurrent(t, tr, "y"),
	}, Payload: "m1"})
	return tr
}

func mustSource(t *testing.T, tr *Tracker, id, payload string) Ref {
	t.Helper()
	ref, err := tr.PutSource(id, payload)
	if err != nil {
		t.Fatalf("PutSource(%s) 意外失败: %v", id, err)
	}
	return ref
}

func mustDerive(t *testing.T, tr *Tracker, req DeriveRequest) Ref {
	t.Helper()
	ref, err := tr.Derive(req)
	if err != nil {
		t.Fatalf("Derive(%s) 意外失败: %v", req.OutputID, err)
	}
	return ref
}

func mustCurrent(t *testing.T, tr *Tracker, id string) Ref {
	t.Helper()
	ref, ok := tr.Current(id)
	if !ok {
		t.Fatalf("对象 %s 不存在", id)
	}
	return ref
}

func refIDs(refs []Ref) map[string]bool {
	out := map[string]bool{}
	for _, r := range refs {
		out[r.ID] = true
	}
	return out
}

func nodeIDs(g *Graph) map[string]string {
	out := map[string]string{}
	for _, n := range g.Nodes {
		out[n.ID] = n.Version
	}
	return out
}

// 1) 操作时记录血缘：派生成功后每个输入都能查到直接上下游边。
func TestDeriveRecordsLineageAtOperation(t *testing.T) {
	var logs bytes.Buffer
	tr := buildDiamond(t, &logs)

	if edges := tr.Edges(); len(edges) != 4 {
		t.Fatalf("期望记录 4 条血缘边, 实际 %d: %+v", len(edges), edges)
	}

	a := mustCurrent(t, tr, "a")
	x := mustCurrent(t, tr, "x")
	m := mustCurrent(t, tr, "m")

	upM, err := tr.Direct(m, Upstream)
	if err != nil {
		t.Fatalf("m 直接上游查询失败: %v", err)
	}
	if got := refIDs(upM); !got["x"] || !got["y"] || len(got) != 2 {
		t.Fatalf("m 直接上游应为 {x,y}, 实际 %v", got)
	}

	downX, err := tr.Direct(x, Downstream)
	if err != nil {
		t.Fatalf("x 直接下游查询失败: %v", err)
	}
	if got := refIDs(downX); !got["m"] || len(got) != 1 {
		t.Fatalf("x 直接下游应为 {m}, 实际 %v", got)
	}

	downA, err := tr.Direct(a, Downstream)
	if err != nil {
		t.Fatalf("a 直接下游查询失败: %v", err)
	}
	if got := refIDs(downA); !got["x"] {
		t.Fatalf("a 直接下游应包含 x, 实际 %v", got)
	}

	if up, err := tr.Direct(a, Upstream); err != nil || len(up) != 0 {
		t.Fatalf("源对象 a 上游应为空, refs=%v err=%v", up, err)
	}

	logText := logs.String()
	for _, want := range []string{"派生血缘记录", "血缘直接追溯", "判定依据"} {
		if !bytes.Contains([]byte(logText), []byte(want)) {
			t.Fatalf("日志缺少 %q", want)
		}
	}
}

// 2) 双向追溯：上游“谁产生我”、下游“我产生谁”闭包完整。
func TestBidirectionalTrace(t *testing.T) {
	tr := buildDiamond(t, nil)
	m := mustCurrent(t, tr, "m")
	a := mustCurrent(t, tr, "a")
	x := mustCurrent(t, tr, "x")

	up, err := tr.Trace(m, Upstream)
	if err != nil {
		t.Fatalf("m 上游追溯失败: %v", err)
	}
	gotUp := nodeIDs(up)
	for _, want := range []string{"m", "x", "y", "a", "b"} {
		if _, ok := gotUp[want]; !ok {
			t.Fatalf("m 上游血缘缺少 %s, 实际 %v", want, gotUp)
		}
	}
	if len(up.Edges) != 4 {
		t.Fatalf("m 上游图应有 4 条边, 实际 %d", len(up.Edges))
	}

	down, err := tr.Trace(a, Downstream)
	if err != nil {
		t.Fatalf("a 下游追溯失败: %v", err)
	}
	gotDown := nodeIDs(down)
	for _, want := range []string{"a", "x", "m"} {
		if _, ok := gotDown[want]; !ok {
			t.Fatalf("a 下游血缘缺少 %s, 实际 %v", want, gotDown)
		}
	}
	if _, ok := gotDown["y"]; ok {
		t.Fatalf("a 下游血缘不应包含 y 分支, 实际 %v", gotDown)
	}

	both, err := tr.Trace(x, Both)
	if err != nil {
		t.Fatalf("x 双向追溯失败: %v", err)
	}
	gotBoth := nodeIDs(both)
	for _, want := range []string{"x", "a", "m"} {
		if _, ok := gotBoth[want]; !ok {
			t.Fatalf("x 双向血缘缺少 %s, 实际 %v", want, gotBoth)
		}
	}
	for _, unexpected := range []string{"y", "b"} {
		if _, ok := gotBoth[unexpected]; ok {
			t.Fatalf("从 x 出发不可达 %s（m 的另一输入分支），实际 %v", unexpected, gotBoth)
		}
	}
}

// 3) 版本正确：版本随内容/输入变化；旧边标记失效，过期版本被拒绝。
func TestVersioningAndStaleRejection(t *testing.T) {
	var logs bytes.Buffer
	tr := buildDiamond(t, &logs)

	a1 := mustCurrent(t, tr, "a")
	x1 := mustCurrent(t, tr, "x")

	a2 := mustSource(t, tr, "a", "raw-a-v2")
	if a1.Version == a2.Version {
		t.Fatal("源对象内容变化后版本必须变化")
	}
	if cur := mustCurrent(t, tr, "a"); cur.Version != a2.Version {
		t.Fatalf("a 当前版本应为新版本")
	}

	var staleEdge *Edge
	for i := range tr.Edges() {
		e := tr.Edges()[i]
		if e.From == a1 && e.To.ID == "x" && e.To.Version == x1.Version {
			staleEdge = &e
		}
	}
	if staleEdge == nil {
		t.Fatal("未找到 a1 -> x1 的旧血缘边")
	}
	if staleEdge.Active {
		t.Fatalf("旧血缘边必须标记失效: %+v", staleEdge)
	}

	before := len(tr.Edges())
	_, err := tr.Derive(DeriveRequest{
		OutputID: "z", Operation: "stale_in", Inputs: []Ref{a1}, Payload: "z1",
	})
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("过期版本输入应返回 ErrStaleVersion, 实际 %v", err)
	}
	if len(tr.Edges()) != before {
		t.Fatal("被拒绝的派生不得改变血缘")
	}

	if _, err := tr.Trace(a1, Both); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("追溯过期版本应返回 ErrStaleVersion, 实际 %v", err)
	}

	_, err = tr.Derive(DeriveRequest{
		OutputID:  "z",
		Operation: "ghost_in",
		Inputs:    []Ref{{ID: "a", Version: "deadbeefdeadbeef"}},
		Payload:   "z1",
	})
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("不存在的版本应返回 ErrRefNotFound, 实际 %v", err)
	}
	if _, err := tr.Trace(Ref{ID: "ghost", Version: "x"}, Downstream); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("不存在的对象查询应返回 ErrRefNotFound, 实际 %v", err)
	}

	x2 := mustDerive(t, tr, DeriveRequest{
		OutputID: "x", Operation: "filter", Inputs: []Ref{a2}, Payload: "x1",
	})
	if x2.Version == x1.Version {
		t.Fatal("输入版本变化后派生版本必须变化")
	}

	if !bytes.Contains(logs.Bytes(), []byte("过期")) {
		t.Fatal("日志应包含过期版本判定依据")
	}
}

// 4) 无血缘记录的派生必须拒绝，且不改变血缘。
func TestDeriveWithoutLineageRejected(t *testing.T) {
	var logs bytes.Buffer
	tr := buildDiamond(t, &logs)
	before := len(tr.Edges())

	_, err := tr.Derive(DeriveRequest{OutputID: "q", Operation: "noflow", Payload: "q1"})
	if !errors.Is(err, ErrNoLineage) {
		t.Fatalf("无血缘派生应返回 ErrNoLineage, 实际 %v", err)
	}
	if len(tr.Edges()) != before {
		t.Fatal("被拒绝的派生不得写入任何血缘边")
	}
	if _, ok := tr.Current("q"); ok {
		t.Fatal("被拒绝的派生不得登记输出对象")
	}

	_, err = tr.Derive(DeriveRequest{OutputID: "q2", Operation: "dup", Payload: "q"})
	if !errors.Is(err, ErrNoLineage) {
		t.Fatalf("nil 输入应返回 ErrNoLineage, 实际 %v", err)
	}
	if !bytes.Contains(logs.Bytes(), []byte("派生缺少血缘记录")) {
		t.Fatal("日志应打印拒绝原因")
	}
}

// 5) 血缘查询漏上游 / 漏下游必须拒绝（白盒破坏双向索引）。
func TestTraceRejectsMissingUpstreamAndDownstream(t *testing.T) {
	t.Run("漏上游", func(t *testing.T) {
		tr := buildDiamond(t, nil)
		m := mustCurrent(t, tr, "m")
		x := mustCurrent(t, tr, "x")

		tr.mu.Lock()
		delete(tr.in[m.ID][m.Version], refKey(x))
		tr.mu.Unlock()

		_, err := tr.Trace(m, Upstream)
		if !errors.Is(err, ErrMissingUpstream) {
			t.Fatalf("漏上游应返回 ErrMissingUpstream, 实际 %v", err)
		}
		if _, err := tr.Direct(m, Upstream); !errors.Is(err, ErrMissingUpstream) {
			t.Fatalf("Direct 漏上游应返回 ErrMissingUpstream, 实际 %v", err)
		}
	})

	t.Run("漏下游", func(t *testing.T) {
		tr := buildDiamond(t, nil)
		x := mustCurrent(t, tr, "x")
		m := mustCurrent(t, tr, "m")

		tr.mu.Lock()
		delete(tr.out[x.ID][x.Version], refKey(m))
		tr.mu.Unlock()

		_, err := tr.Trace(x, Downstream)
		if !errors.Is(err, ErrMissingDownstream) {
			t.Fatalf("漏下游应返回 ErrMissingDownstream, 实际 %v", err)
		}
		if _, err := tr.Direct(x, Downstream); !errors.Is(err, ErrMissingDownstream) {
			t.Fatalf("Direct 漏下游应返回 ErrMissingDownstream, 实际 %v", err)
		}
	})
}

// 6a) 并发：同一组派生以任意提交顺序得到完全相同的规范化血缘图。
func TestOrderIndependentGraph(t *testing.T) {
	build := func(shuffle bool) *Graph {
		tr := New(&bytes.Buffer{})
		a := mustSource(t, tr, "a", "raw-a")
		b := mustSource(t, tr, "b", "raw-b")

		tasks := []func(){
			func() {
				mustDerive(t, tr, DeriveRequest{
					OutputID: "x", Operation: "filter", Inputs: []Ref{a}, Payload: "x1",
				})
			},
			func() {
				mustDerive(t, tr, DeriveRequest{
					OutputID: "y", Operation: "clean", Inputs: []Ref{b}, Payload: "y1",
				})
			},
		}
		runTasks(t, tasks, shuffle)

		tasks = []func(){
			func() {
				mustDerive(t, tr, DeriveRequest{
					OutputID: "m", Operation: "join",
					Inputs:  []Ref{mustCurrent(t, tr, "x"), mustCurrent(t, tr, "y")},
					Payload: "m1",
				})
			},
		}
		runTasks(t, tasks, shuffle)

		g, err := tr.Trace(mustCurrent(t, tr, "m"), Both)
		if err != nil {
			t.Fatalf("追溯失败: %v", err)
		}
		return g
	}

	g1 := build(false)
	for i := 0; i < 5; i++ {
		g2 := build(true)
		if !graphsEqual(g1, g2) {
			t.Fatalf("第 %d 次乱序执行血缘图不一致:\n%v\nvs\n%v", i, dumpGraph(g1), dumpGraph(g2))
		}
	}
}

// 6b) 并发记录与查询：-race 下不出现数据竞争，最终血缘完整。
func TestConcurrentDeriveAndTrace(t *testing.T) {
	tr := New(&bytes.Buffer{})
	a := mustSource(t, tr, "a", "raw-a")
	b := mustSource(t, tr, "b", "raw-b")

	const workers = 16
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := tr.Derive(DeriveRequest{
			OutputID: "x", Operation: "filter", Inputs: []Ref{a}, Payload: "x1",
		}); err != nil {
			t.Errorf("派生 x 失败: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := tr.Derive(DeriveRequest{
			OutputID: "y", Operation: "clean", Inputs: []Ref{b}, Payload: "y1",
		}); err != nil {
			t.Errorf("派生 y 失败: %v", err)
		}
	}()
	wg.Wait()

	wg.Add(workers * 2)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if _, err := tr.Derive(DeriveRequest{
				OutputID: "m", Operation: "join",
				Inputs:  []Ref{mustCurrent(t, tr, "x"), mustCurrent(t, tr, "y")},
				Payload: "m1",
			}); err != nil {
				t.Errorf("并发派生 m 失败: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := tr.Trace(a, Downstream); err != nil {
				// 在 m 边尚未建立完成的中间时刻，a->x 边始终闭合，
				// 因此下游追溯在任意时刻都应成功。
				t.Errorf("并发查询 a 下游失败: %v", err)
			}
		}()
	}
	wg.Wait()

	m := mustCurrent(t, tr, "m")
	g, err := tr.Trace(m, Both)
	if err != nil {
		t.Fatalf("最终血缘追溯失败: %v", err)
	}
	if len(g.Nodes) != 5 {
		t.Fatalf("完整血缘应有 5 个节点, 实际 %d: %v", len(g.Nodes), dumpGraph(g))
	}
	if len(g.Edges) != 4 {
		t.Fatalf("完整血缘应有 4 条边, 实际 %d: %v", len(g.Edges), dumpGraph(g))
	}
	active := 0
	for _, e := range tr.Edges() {
		if e.Active {
			active++
		}
	}
	if active != 4 {
		t.Fatalf("应有 4 条有效血缘边, 实际 %d", active)
	}
}
