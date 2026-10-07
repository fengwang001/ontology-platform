package ontology

import (
	"strings"
	"sync"
	"testing"
)

// 错误固定优先级：类型不匹配 > 实例不存在 > 运行时环拒绝 > 维护回滚。
func TestErrorPriorities(t *testing.T) {
	g := NewGraph()
	g.AddObjectType("A")
	g.AddObjectType("B")
	mustOK(t, g.AddLinkType("ab", "A", "B"))
	mustOK(t, g.CreateObject("a", "A", nil))
	mustOK(t, g.CreateObject("b", "B", nil))

	_, err := g.AddLink("xx", "nope1", "nope2", LinkOptions{})
	mustErrKind(t, err, KindTypeMismatch)

	_, err = g.AddLink("ab", "a", "ghost", LinkOptions{})
	mustErrKind(t, err, KindInstanceNotFound)

	_, err = g.AddLink("ab", "b", "ghost2", LinkOptions{})
	mustErrKind(t, err, KindTypeMismatch)

	err = g.RegisterView(ViewSpec{Name: "bad",
		Path: Path{Types: [][]string{{"A"}, {"A"}}, Links: []string{"ab"}}, Attr: "v"})
	mustErrKind(t, err, KindTypeMismatch)

	mustOK(t, g.RegisterView(ViewSpec{Name: "ok",
		Path: Path{Types: [][]string{{"A"}, {"B"}}, Links: []string{"ab"}}, Attr: "v"}))
	_, err = g.Query("ok", "ghost")
	mustErrKind(t, err, KindInstanceNotFound)
}

// 运行时环拒绝仅作用于无法静态排除环的视图；拒绝后整体回滚。
func TestCycleRejectedAtRuntime(t *testing.T) {
	g := NewGraph()
	g.AddObjectType("N")
	mustOK(t, g.AddLinkType("nn", "N", "N"))
	mustOK(t, g.CreateObject("s", "N", nil))
	mustOK(t, g.CreateObject("x", "N", map[string]int64{"v": 1}))
	spec := ViewSpec{Name: "r",
		Path: Path{Types: [][]string{{"N"}, {"N"}, {"N"}}, Links: []string{"nn", "nn"}}, Attr: "v"}
	mustOK(t, g.RegisterView(spec))
	if g.byName["r"].cycleExcludable {
		t.Fatalf("repeated type N cannot be statically cycle-free")
	}
	_, err := g.AddLink("nn", "s", "x", LinkOptions{RejectCycle: true})
	mustOK(t, err)
	_, err = g.AddLink("nn", "x", "s", LinkOptions{RejectCycle: true})
	mustErrKind(t, err, KindCycleRejected)
	if g.edgeMul("nn", "x", "s") != 0 {
		t.Fatalf("rejected edge must be rolled back")
	}
	// 不带 RejectCycle 时环被正常支持。
	_, err = g.AddLink("nn", "x", "s", LinkOptions{})
	mustOK(t, err)
}

func TestStaticCycleExclusion(t *testing.T) {
	g := chainGraph(t)
	mustOK(t, g.RegisterView(twoHopSpec()))
	if !g.byName["v2"].cycleExcludable {
		t.Fatalf("disjoint layer types must be statically cycle-free")
	}
}

// 维护失败整体回滚：注入失败后图与聚合状态都必须保持变更前一致。
func TestMaintenanceRollback(t *testing.T) {
	g := chainGraph(t)
	mustOK(t, g.RegisterView(twoHopSpec()))
	_, err := g.AddLink("ab", "a1", "b1", LinkOptions{})
	mustOK(t, err)
	_, err = g.AddLink("bc", "b1", "c1", LinkOptions{})
	mustOK(t, err)
	before := queryAgg(t, g, "v2", "a1")

	g.injectFailure("v2")
	_, err = g.AddLink("bc", "b1", "c2", LinkOptions{})
	mustErrKind(t, err, KindMaintenanceRollback)

	// 边回滚。
	if g.edgeMul("bc", "b1", "c2") != 0 {
		t.Fatalf("edge must be rolled back after maintenance failure")
	}
	// 聚合状态回滚。
	if a := queryAgg(t, g, "v2", "a1"); a != before {
		t.Fatalf("aggregate must roll back: before=%+v after=%+v", before, a)
	}
}

// 跳类型集合结构性新增成员：既有连通不失效，新连通按对生效。
func TestAddTypeToHop(t *testing.T) {
	g := NewGraph()
	for _, ty := range []string{"A", "A2", "E", "E2"} {
		g.AddObjectType(ty)
	}
	// 同一链接名声明两个类型对。
	mustOK(t, g.AddLinkType("l", "A", "E"))
	mustOK(t, g.AddLinkType("l", "A2", "E2"))
	mustOK(t, g.CreateObject("a", "A", nil))
	mustOK(t, g.CreateObject("a2", "A2", nil))
	mustOK(t, g.CreateObject("e", "E", map[string]int64{"v": 4}))
	mustOK(t, g.CreateObject("e2", "E2", map[string]int64{"v": 8}))
	// 初始只允许 A->E。
	mustOK(t, g.RegisterView(ViewSpec{Name: "w",
		Path: Path{Types: [][]string{{"A"}, {"E"}}, Links: []string{"l"}}, Attr: "v"}))
	_, err := g.AddLink("l", "a", "e", LinkOptions{})
	mustOK(t, err)
	if a := queryAgg(t, g, "w", "a"); a.Value != 4 {
		t.Fatalf("expected 4, got %+v", a)
	}

	// 结构性扩展：起点层与终点层同时加入新成员。
	mustOK(t, g.AddTypeToHop("w", 0, "A2"))
	mustOK(t, g.AddTypeToHop("w", 1, "E2"))
	_, err = g.AddLink("l", "a2", "e2", LinkOptions{})
	mustOK(t, err)

	// 既有连通不失效。
	if a := queryAgg(t, g, "w", "a"); a.Value != 4 {
		t.Fatalf("existing connectivity must survive, got %+v", a)
	}
	if a := queryAgg(t, g, "w", "a2"); !a.Present || a.Value != 8 {
		t.Fatalf("new structural connectivity expected 8, got %+v", a)
	}

	// 不兼容的类型新增按类型不匹配拒绝。
	err = g.AddTypeToHop("w", 1, "A")
	mustErrKind(t, err, KindTypeMismatch)
}

// 并发交织串行等价：任意时刻查询都等于当前真实连通关系的重算结果。
func TestConcurrentSerialEquivalence(t *testing.T) {
	g := chainGraph(t)
	mustOK(t, g.RegisterView(twoHopSpec()))

	var logs []string
	var logMu sync.Mutex
	g.SetLogger(func(line string) {
		logMu.Lock()
		logs = append(logs, line)
		logMu.Unlock()
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 写者 1：链接增删。
	ops := [][3]string{
		{"add", "ab", "a1|b1"},
		{"add", "bc", "b1|c1"},
		{"add", "ab", "a1|b2"},
		{"add", "bc", "b2|c2"},
		{"del", "bc", "b1|c1"},
		{"del", "ab", "a1|b1"},
		{"add", "bc", "b1|c1"},
		{"del", "bc", "b2|c2"},
	}
	writers := 2
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				op := ops[(i+seed)%len(ops)]
				parts := strings.Split(op[2], "|")
				if op[0] == "add" {
					g.AddLink(op[1], parts[0], parts[1], LinkOptions{})
				} else {
					g.RemoveLink(op[1], parts[0], parts[1])
				}
			}
		}(w)
	}
	// 写者 2：属性写入。
	wg.Add(1)
	go func() {
		defer wg.Done()
		vals := []int64{1, 2, 3, 7, 9, 4, 0}
		for i := 0; i < 400; i++ {
			g.SetAttr("c2", "v", vals[i%len(vals)])
			g.SetAttr("c1", "v", vals[(i+3)%len(vals)])
		}
	}()
	// 读者：任何时刻观察到的状态必须是朴素模型在某种连通/取值下的合法最大值，
	// 且绝不是部分更新的中间态（最大值必为当前可达终点的某个属性值或不存在）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			g.assertInternallyConsistent(t)
		}
	}()
	_ = stop
	wg.Wait()

	// 收敛后与朴素全量重算严格相等。
	wp, want := naiveOfGraph(g).query("v2", "a1")
	got := queryAgg(t, g, "v2", "a1")
	if got.Present != wp || (wp && got.Value != want) {
		t.Fatalf("final mismatch naive present=%v v=%d got=%+v", wp, want, got)
	}
	if len(logs) == 0 {
		t.Fatalf("change logger must record operations")
	}
}

// naiveOfGraph 从真实图状态构造朴素模型（不读取任何聚合状态）。
func naiveOfGraph(g *Graph) *naiveGraph {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return naiveOfGraphLocked(g)
}

func naiveOfGraphLocked(g *Graph) *naiveGraph {
	m := newNaive()
	for ty := range g.types {
		m.addType(ty)
	}
	for name, sigs := range g.linkTypes {
		for _, s := range sigs {
			m.addLinkType(name, s.srcType, s.dstType)
		}
	}
	for id, o := range g.objects {
		m.create(id, o.typ, o.attrs)
	}
	for link, outs := range g.out {
		for s, dsts := range outs {
			for d, mul := range dsts {
				for i := int64(0); i < mul; i++ {
					m.addEdge(link, s, d)
				}
			}
		}
	}
	for _, vs := range g.views {
		m.register(vs.spec)
	}
	return m
}

// assertInternallyConsistent 在同一个读锁内比对维护状态与朴素全量重算。
func (g *Graph) assertInternallyConsistent(t *testing.T) {
	t.Helper()
	g.mu.RLock()
	defer g.mu.RUnlock()
	m := naiveOfGraphLocked(g)
	vs := g.byName["v2"]
	for start, info := range vs.snap.agg {
		wp, want := m.query("v2", start)
		if info.present != wp || (wp && info.value != want) {
			t.Errorf("inconsistent state for %s: naive present=%v v=%d, got present=%v v=%d",
				start, wp, want, info.present, info.value)
		}
	}
}
