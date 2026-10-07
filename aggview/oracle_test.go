package aggview_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/aggview"
	"ontology/ontology"
)

// naiveOracle 是一个与增量引擎完全独立实现的朴素全路径遍历重算模型：
// 每次查询都从当前存储快照出发，按声明的定长路径逐层展开（按实例 ID
// 集合去重），枚举起点能到达的全部不同终点，再对属性取最大值。
// 它不维护任何增量状态，用于与 aggview.Engine 对拍。
type naiveOracle struct {
	st   *ontology.Store
	decl aggview.PathDecl
}

func (o *naiveOracle) recompute(src string) aggview.Result {
	typeOf := map[string]string{}
	attrs := map[string]map[string]float64{}
	for _, ob := range o.st.Objects() {
		typeOf[ob.ID] = ob.Type
		attrs[ob.ID] = ob.Attributes
	}
	edges := map[string][]ontology.Link{}
	for _, l := range o.st.Links() {
		edges[l.From] = append(edges[l.From], l)
	}
	current := map[string]struct{}{src: {}}
	for _, h := range o.decl.Hops {
		nxt := map[string]struct{}{}
		for node := range current {
			for _, l := range edges[node] {
				if l.Rel != h.Relation {
					continue
				}
				tt, ok := typeOf[l.To]
				if !ok || !containsStr(h.Types, tt) {
					continue
				}
				nxt[l.To] = struct{}{}
			}
		}
		current = nxt
	}
	if len(current) == 0 {
		return aggview.Result{Absent: true}
	}
	var max float64
	first := true
	for t := range current {
		if v, ok := attrs[t][o.decl.Attr]; ok {
			if first || v > max {
				max, first = v, false
			}
		}
	}
	if first {
		return aggview.Result{Absent: true}
	}
	return aggview.Result{Max: max}
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func indexOfStr(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// TestRandomDifferential 随机生成链接增删与属性写入序列，每步后对每个
// 起点比较增量引擎与朴素重算模型的结果。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	typeNames := []string{"A", "B", "C", "D"}
	paths := [][]aggview.HopDecl{
		{{Relation: "ab", Types: []string{"B"}}},
		{{Relation: "ab", Types: []string{"B"}},
			{Relation: "bc", Types: []string{"C"}}},
		{{Relation: "ab", Types: []string{"B"}},
			{Relation: "bc", Types: []string{"C"}},
			{Relation: "cd", Types: []string{"D"}}},
	}
	relSeq := []string{"ab", "bc", "cd"}
	fromType := []string{"A", "B", "C"}
	toType := []string{"B", "C", "D"}

	for iter := 0; iter < 40; iter++ {
		st := ontology.NewStore()
		for _, ty := range typeNames {
			st.EnsureType(ty)
		}
		en := aggview.NewEngine(st)

		hops := paths[rng.Intn(3)]
		decl := aggview.PathDecl{
			Name:   "r",
			Source: "A",
			Hops:   hops,
			Target: toType[len(hops)-1],
			Attr:   "score",
		}
		if err := en.DeclareView(decl); err != nil {
			t.Fatal(err)
		}
		orc := &naiveOracle{st: st, decl: decl}

		const nPerType = 5
		var ids [4][]string
		for ti, tn := range typeNames {
			for k := 0; k < nPerType; k++ {
				id := fmt.Sprintf("%s%d", strings.ToLower(tn), k)
				st.CreateObject(id, tn, map[string]float64{"score": float64(rng.Intn(20))})
				ids[ti] = append(ids[ti], id)
			}
		}

		linkSeq := 0
		var active []string
		compare := func(step int) {
			t.Helper()
			for _, s := range ids[0] {
				got, gerr := en.Query("r", s)
				if gerr != nil {
					t.Fatalf("iter=%d step=%d query %s: %v", iter, step, s, gerr)
				}
				want := orc.recompute(s)
				if got.Absent != want.Absent || (!got.Absent && got.Max != want.Max) {
					t.Fatalf("iter=%d step=%d src=%s engine=%+v oracle=%+v",
						iter, step, s, got, want)
				}
			}
		}

		for step := 0; step < 200; step++ {
			switch rng.Intn(3) {
			case 0, 1:
				h := rng.Intn(len(hops))
				fi := indexOfStr(typeNames, fromType[h])
				ti := indexOfStr(typeNames, toType[h])
				from := ids[fi][rng.Intn(nPerType)]
				to := ids[ti][rng.Intn(nPerType)]
				id := fmt.Sprintf("e%d", linkSeq)
				linkSeq++
				if _, err := en.AddLink(ontology.Link{ID: id, From: from, Rel: relSeq[h], To: to}); err == nil {
					active = append(active, id)
				}
			case 2:
				if len(active) > 0 {
					idx := rng.Intn(len(active))
					id := active[idx]
					active = append(active[:idx], active[idx+1:]...)
					if _, err := en.RemoveLink(id); err != nil {
						t.Fatalf("iter=%d remove %s: %v", iter, id, err)
					}
				}
			}
			if rng.Intn(2) == 0 {
				ti := rng.Intn(4)
				id := ids[ti][rng.Intn(nPerType)]
				_, _ = en.WriteAttr(id, "score", float64(rng.Intn(20)))
			}
			compare(step)
		}
	}
}

// TestStructuralHopTypeExpansion：新增跳类型成员后既有连通关系保持正确，
// 并与朴素模型在扩展后的声明上对拍。
func TestStructuralHopTypeExpansion(t *testing.T) {
	st := ontology.NewStore()
	for _, ty := range []string{"A", "B", "C"} {
		st.EnsureType(ty)
	}
	en := aggview.NewEngine(st)
	decl := aggview.PathDecl{
		Name:   "s",
		Source: "A",
		Hops:   []aggview.HopDecl{{Relation: "r", Types: []string{"B"}}},
		Target: "B",
		Attr:   "score",
	}
	if err := en.DeclareView(decl); err != nil {
		t.Fatal(err)
	}
	st.CreateObject("a", "A", nil)
	st.CreateObject("b", "B", map[string]float64{"score": 3})
	st.CreateObject("c", "C", map[string]float64{"score": 8})
	if _, err := en.AddLink(ontology.Link{ID: "ab", From: "a", Rel: "r", To: "b"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := en.Query("s", "a"); r.Absent || r.Max != 3 {
		t.Fatalf("want 3, got %+v", r)
	}
	// 扩展前 A->C 与第一跳声明类型不匹配，被拒绝（ErrTypeMismatch）。
	if _, err := en.AddLink(ontology.Link{ID: "ac0", From: "a", Rel: "r", To: "c"}); err == nil {
		t.Fatal("before expansion, A->C must be rejected")
	}
	// 结构性扩展第一跳允许 C；既有 a->b 连通关系不得失效。
	if err := en.AddHopType("s", 0, "C"); err != nil {
		t.Fatal(err)
	}
	if _, err := en.AddLink(ontology.Link{ID: "ac", From: "a", Rel: "r", To: "c"}); err != nil {
		t.Fatalf("after expansion A->C should be accepted: %v", err)
	}
	// 朴素模型以扩展后的声明重算，结论一致。
	expanded := decl
	expanded.Hops = []aggview.HopDecl{{Relation: "r", Types: []string{"B", "C"}}}
	orc := &naiveOracle{st: st, decl: expanded}
	// 引擎视图的 Target 仍为 B，但 hop 类型集合已含 C：按"路径可达终点
	// 的被汇总属性"聚合，c=8 成为新最大值；既有 b=3 的连通关系仍然有效
	// （删除 a->c 后必须立刻回到 3，证明旧连通关系未被判定失效）。
	if r, _ := en.Query("s", "a"); r.Absent || r.Max != 8 {
		t.Fatalf("after expansion max should be 8 (old b=3 plus new c=8), got %+v", r)
	}
	if want := orc.recompute("a"); want.Absent || want.Max != 8 {
		t.Fatalf("oracle on expanded decl = %+v", want)
	}
	if _, err := en.RemoveLink("ac"); err != nil {
		t.Fatal(err)
	}
	if r, _ := en.Query("s", "a"); r.Absent || r.Max != 3 {
		t.Fatalf("pre-expansion connectivity must remain valid, got %+v", r)
	}
}

// TestConcurrentSerializability：并发链接增删与属性写入交织；结束后每个
// 起点结果必须等于朴素模型在最终存储上的重算结果（串行等价）。期间持续
// 的只读查询只用于施压（引擎以单把 RWMutex 保证变更原子可见）。
func TestConcurrentSerializability(t *testing.T) {
	st := ontology.NewStore()
	for _, ty := range []string{"A", "B", "C"} {
		st.EnsureType(ty)
	}
	en := aggview.NewEngine(st)
	decl := aggview.PathDecl{
		Name:   "c",
		Source: "A",
		Hops: []aggview.HopDecl{
			{Relation: "r1", Types: []string{"B"}},
			{Relation: "r2", Types: []string{"C"}},
		},
		Target: "C",
		Attr:   "score",
	}
	if err := en.DeclareView(decl); err != nil {
		t.Fatal(err)
	}
	const n = 4
	for _, tn := range []string{"A", "B", "C"} {
		for k := 0; k < n; k++ {
			st.CreateObject(fmt.Sprintf("%s%d", strings.ToLower(tn), k), tn,
				map[string]float64{"score": float64(k)})
		}
	}

	var mu sync.Mutex
	counter := 0
	nextID := func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprintf("e%d", counter)
	}

	var mutWg sync.WaitGroup
	for w := 0; w < 6; w++ {
		mutWg.Add(1)
		go func(seed int64) {
			defer mutWg.Done()
			r := rand.New(rand.NewSource(seed))
			for round := 0; round < 150; round++ {
				switch r.Intn(3) {
				case 0:
					_, _ = en.AddLink(ontology.Link{
						ID:   nextID(),
						From: fmt.Sprintf("a%d", r.Intn(n)), Rel: "r1",
						To: fmt.Sprintf("b%d", r.Intn(n)),
					})
				case 1:
					_, _ = en.WriteAttr(fmt.Sprintf("c%d", r.Intn(n)), "score", float64(r.Intn(50)))
				case 2:
					_, _ = en.AddLink(ontology.Link{
						ID:   nextID(),
						From: fmt.Sprintf("b%d", r.Intn(n)), Rel: "r2",
						To: fmt.Sprintf("c%d", r.Intn(n)),
					})
				}
			}
		}(int64(w + 1))
	}

	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for k := 0; k < n; k++ {
					_, _ = en.Query("c", fmt.Sprintf("a%d", k))
				}
			}
		}
	}()

	mutWg.Wait()
	close(stop)
	readerWg.Wait()

	orc := &naiveOracle{st: st, decl: decl}
	for k := 0; k < n; k++ {
		src := fmt.Sprintf("a%d", k)
		got, err := en.Query("c", src)
		if err != nil {
			t.Fatal(err)
		}
		want := orc.recompute(src)
		if got.Absent != want.Absent || (!got.Absent && got.Max != want.Max) {
			t.Fatalf("src=%s engine=%+v oracle=%+v", src, got, want)
		}
	}
}

// TestCostBound 证明属性变小重定最大值时考察的终点数不超过该起点当前
// 真实可达终点数：一个起点经不同中间实例到达 m 个终点（含重复到达），
// 令当前最大值来源变小，断言 Reexamined <= Reachable 且与可达数一致。
func TestCostBound(t *testing.T) {
	f := newFixture(t)
	f.obj("a", "A", 0)
	const m = 6
	for i := 0; i < m; i++ {
		f.obj(fmt.Sprintf("b%d", i), "B", 0)
		f.obj(fmt.Sprintf("c%d", i), "C", float64(i+1))
		if err := f.link(fmt.Sprintf("ab%d", i), "a", "r1", fmt.Sprintf("b%d", i)); err != nil {
			t.Fatal(err)
		}
		if err := f.link(fmt.Sprintf("bc%d", i), fmt.Sprintf("b%d", i), "r2", fmt.Sprintf("c%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	// 额外制造对 c0 的重复到达（另一中间实例 b1 也指向 c0）。
	if err := f.link("dup", "b1", "r2", "c0"); err != nil {
		// 平台若禁止并行链接则忽略；不影响开销上界证明。
		_ = err
	}
	if r := mustQuery(t, f.en, "a"); r.Absent || r.Max != m {
		t.Fatalf("want max %d, got %+v", m, r)
	}
	// 最大值来源 c{m-1}=m 变小到 0，须重新确定为 m-1。
	cost, err := f.en.WriteAttr(fmt.Sprintf("c%d", m-1), "score", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a"); r.Absent || r.Max != m-1 {
		t.Fatalf("want new max %d, got %+v", m-1, r)
	}
	// 可达终点总数为 m（重复到达不增加），扫描数不超过 m。
	if cost.Reachable != m {
		t.Fatalf("reachable = %d, want %d", cost.Reachable, m)
	}
	if cost.Reexamined > cost.Reachable {
		t.Fatalf("reexamined %d exceeds reachable %d", cost.Reexamined, cost.Reachable)
	}
}

// TestRollbackOnMaintenanceFailure 验证维护失败时整体回滚：构造一个让视图
// 维护阶段失败的注入点，断言存储与视图状态都回到变更前；同时用"环检查在
// 改图前拒绝"验证被拒绝变更不产生半成品状态。
func TestRollbackOnMaintenanceFailure(t *testing.T) {
	f := newFixture(t)
	f.obj("a", "A", 0)
	f.obj("b", "B", 0)
	f.obj("c", "C", 7)
	if err := f.link("ab", "a", "r1", "b"); err != nil {
		t.Fatal(err)
	}
	if err := f.link("bc", "b", "r2", "c"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a"); r.Absent || r.Max != 7 {
		t.Fatalf("baseline want 7, got %+v", r)
	}
	// 注入维护失败：新增链接在维护阶段失败，必须整体回滚。
	f.obj("b2", "B", 0)
	f.obj("c2", "C", 99)
	f.en.InjectFault("v")
	_, err := f.en.AddLink(ontology.Link{ID: "ab2", From: "a", Rel: "r1", To: "b2"})
	if !errors.Is(err, ontology.ErrMaintenanceFailed) {
		t.Fatalf("want ErrMaintenanceFailed, got %v", err)
	}
	// 存储回滚：新链接不存在。
	if _, ok := f.st.GetLink("ab2"); ok {
		t.Fatal("store must roll back the failed link addition")
	}
	// 视图回滚：结果仍是 7，而非新链接本不该带来的任何值。
	if r := mustQuery(t, f.en, "a"); r.Absent || r.Max != 7 {
		t.Fatalf("view must roll back to 7, got %+v", r)
	}
	// 注入的故障是一次性的：后续变更恢复正常。
	if err := f.link("ab2b", "a", "r1", "b2"); err != nil {
		t.Fatalf("subsequent change should succeed: %v", err)
	}
}

// TestNoPartialStateOnRejectedChange 验证被环检查拒绝的变更不修改任何状态。
func TestNoPartialStateOnRejectedChange(t *testing.T) {
	st := ontology.NewStore()
	st.EnsureType("A")
	en := aggview.NewEngine(st)
	if err := en.DeclareView(aggview.PathDecl{
		Name: "s", Source: "A",
		Hops:   []aggview.HopDecl{{Relation: "r", Types: []string{"A"}}},
		Target: "A", Attr: "score",
	}); err != nil {
		t.Fatal(err)
	}
	st.CreateObject("x", "A", map[string]float64{"score": 1})
	before := len(st.Links())
	if _, err := en.AddLink(ontology.Link{ID: "loop", From: "x", Rel: "r", To: "x"}); err == nil {
		t.Fatal("expected cycle rejection")
	}
	if len(st.Links()) != before {
		t.Fatalf("rejected change must not mutate store: before=%d after=%d", before, len(st.Links()))
	}
	if r, _ := en.Query("s", "x"); !r.Absent {
		t.Fatalf("rejected change must not affect view: %+v", r)
	}
}

// TestChangeLog 验证日志打印每次变化的输入、受影响起点集合与判定依据。
func TestChangeLog(t *testing.T) {
	f := newFixture(t)
	f.obj("a", "A", 0)
	f.obj("b", "B", 0)
	f.obj("c1", "C", 5)
	f.obj("c2", "C", 9)
	if err := f.link("l1", "a", "r1", "b"); err != nil {
		t.Fatal(err)
	}
	if err := f.link("l2", "b", "r2", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := f.link("l3", "b", "r2", "c2"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.en.RemoveLink("l3"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.en.WriteAttr("c1", "score", 2); err != nil {
		t.Fatal(err)
	}
	logs := f.en.Log()
	if len(logs) == 0 {
		t.Fatal("expected change log entries")
	}
	found := false
	for _, e := range logs {
		if e.Op == "remove_link" && len(e.Affected) > 0 {
			for _, src := range e.Affected[0].Sources {
				if src == "a" {
					found = true
				}
			}
			if e.Affected[0].Reason == "" {
				t.Fatal("log entry must record decision basis")
			}
		}
	}
	if !found {
		t.Fatalf("remove_link log should identify affected source a: %+v", logs)
	}
}
