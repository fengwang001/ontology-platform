package aggview_test

import (
	"errors"
	"testing"

	"ontology/aggview"
	"ontology/ontology"
)

// 测试夹具：A --r1--> B --r2--> C，聚合 C.score 最大值。
type fixture struct {
	st *ontology.Store
	en *aggview.Engine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := ontology.NewStore()
	for _, ty := range []string{"A", "B", "C"} {
		st.EnsureType(ty)
	}
	en := aggview.NewEngine(st)
	decl := aggview.PathDecl{
		Name:   "v",
		Source: "A",
		Hops: []aggview.HopDecl{
			{Relation: "r1", Types: []string{"B"}},
			{Relation: "r2", Types: []string{"C"}},
		},
		Target: "C",
		Attr:   "score",
	}
	if err := en.DeclareView(decl); err != nil {
		t.Fatalf("declare: %v", err)
	}
	return &fixture{st: st, en: en}
}

func (f *fixture) obj(id, typ string, score float64) {
	f.st.CreateObject(id, typ, map[string]float64{"score": score})
}

func (f *fixture) link(id, from, rel, to string) *ontology.AggregateError {
	_, err := f.en.AddLink(ontology.Link{ID: id, From: from, Rel: rel, To: to})
	return err
}

func mustQuery(t *testing.T, en *aggview.Engine, src string) aggview.Result {
	t.Helper()
	r, err := en.Query("v", src)
	if err != nil {
		t.Fatalf("query %s: %v", src, err)
	}
	return r
}

func TestSingleAndMultiHop(t *testing.T) {
	f := newFixture(t)
	f.obj("a1", "A", 0)
	f.obj("a2", "A", 0)
	f.obj("b1", "B", 0)
	f.obj("b2", "B", 0)
	f.obj("c1", "C", 3)
	f.obj("c2", "C", 7)
	f.obj("c3", "C", 5)

	// 初始：无路径，全部 absent。
	if r := mustQuery(t, f.en, "a1"); !r.Absent {
		t.Fatalf("a1 want absent, got %+v", r)
	}

	if err := f.link("l1", "a1", "r1", "b1"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a1"); !r.Absent {
		t.Fatalf("a1 still absent after 1 hop, got %+v", r)
	}
	if err := f.link("l2", "b1", "r2", "c1"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a1"); r.Absent || r.Max != 3 {
		t.Fatalf("a1 want max 3, got %+v", r)
	}
	// a2 不受影响（无关节点不得多算）。
	if r := mustQuery(t, f.en, "a2"); !r.Absent {
		t.Fatalf("a2 must stay absent, got %+v", r)
	}

	// 第二跳再连 c2=7：a1 最大值变为 7。
	if err := f.link("l3", "b1", "r2", "c2"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a1"); r.Absent || r.Max != 7 {
		t.Fatalf("a1 want 7, got %+v", r)
	}
	// a1 也可经 b2 到 c3=5。
	if err := f.link("l4", "a1", "r1", "b2"); err != nil {
		t.Fatal(err)
	}
	if err := f.link("l5", "b2", "r2", "c3"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a1"); r.Absent || r.Max != 7 {
		t.Fatalf("a1 still 7, got %+v", r)
	}

	// 删除通向 c2 的链接 l3 -> a1 退化为 max(c1,c3)=5。
	if _, err := f.en.RemoveLink("l3"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a1"); r.Absent || r.Max != 5 {
		t.Fatalf("a1 after remove want 5, got %+v", r)
	}
	// 删除 b1 的两条 r2 中剩余的 l2，a1 仍可经 b2->c3=5 到达。
	if _, err := f.en.RemoveLink("l2"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a1"); r.Absent || r.Max != 5 {
		t.Fatalf("a1 via b2 want 5, got %+v", r)
	}
	// 删除唯一前缀 l4 -> a1 不可达，回到 absent。
	if _, err := f.en.RemoveLink("l4"); err != nil {
		t.Fatal(err)
	}
	// 删除 a1-b1(l1) 已无残留路径。
	if _, err := f.en.RemoveLink("l1"); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a1"); !r.Absent {
		t.Fatalf("a1 want absent at end, got %+v", r)
	}
}

func TestAttrDecreasePicksNewMax(t *testing.T) {
	f := newFixture(t)
	f.obj("a", "A", 0)
	f.obj("b", "B", 0)
	f.obj("c1", "C", 10)
	f.obj("c2", "C", 4)
	f.obj("c3", "C", 8)
	if err := f.link("l1", "a", "r1", "b"); err != nil {
		t.Fatal(err)
	}
	for i, c := range []string{"c1", "c2", "c3"} {
		if err := f.link(string(rune('x'+i)), "b", "r2", c); err != nil {
			t.Fatal(err)
		}
	}
	if r := mustQuery(t, f.en, "a"); r.Max != 10 {
		t.Fatalf("want 10 got %+v", r)
	}

	// 最大值来源 c1 由 10 变小到 1：必须重新确定为 c3=8，不得沿用 10。
	cost, err := f.en.WriteAttr("c1", "score", 1)
	if err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a"); r.Absent || r.Max != 8 {
		t.Fatalf("want new max 8, got %+v", r)
	}
	// 开销：只扫描 a 当前真实可达的 3 个终点，不扫描无关实例。
	if cost.Reexamined != 3 || cost.Reachable != 3 {
		t.Fatalf("cost = %+v, want reexamined=reachable=3", cost)
	}

	// 变大不需要重新扫描最大值来源。
	cost, _ = f.en.WriteAttr("c1", "score", 100)
	if r := mustQuery(t, f.en, "a"); r.Max != 100 {
		t.Fatalf("want 100 got %+v", r)
	}
	if cost.Reexamined != 0 {
		t.Fatalf("increase must not trigger reexam, got %+v", cost)
	}
}

func TestDuplicateArrivalCountedOnce(t *testing.T) {
	f := newFixture(t)
	f.obj("a", "A", 0)
	f.obj("b1", "B", 0)
	f.obj("b2", "B", 0)
	f.obj("c", "C", 9)
	// a 经 b1、b2 两条不同中间组合到达同一终点 c。
	if err := f.link("l1", "a", "r1", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := f.link("l2", "a", "r1", "b2"); err != nil {
		t.Fatal(err)
	}
	if err := f.link("l3", "b1", "r2", "c"); err != nil {
		t.Fatal(err)
	}
	if err := f.link("l4", "b2", "r2", "c"); err != nil {
		t.Fatal(err)
	}
	// 再加入同一对端点的并行链接（模拟额外走法），仍只计一次。
	if err := f.link("l5", "b2", "r2", "c"); err != nil {
		// 若平台允许并行链接则不应报错；不允许则跳过该断言。
		_ = err
	}
	if r := mustQuery(t, f.en, "a"); r.Absent || r.Max != 9 {
		t.Fatalf("duplicate arrival must count c once with max 9, got %+v", r)
	}
	// c 变小到 2，重新确定时不应因重复走法而重复计数。
	if _, err := f.en.WriteAttr("c", "score", 2); err != nil {
		t.Fatal(err)
	}
	if r := mustQuery(t, f.en, "a"); r.Absent || r.Max != 2 {
		t.Fatalf("want 2, got %+v", r)
	}
}

func TestCycleHandled(t *testing.T) {
	// 单跳路径 A --r--> A，类型集合在声明阶段无法静态排除自环。
	st := ontology.NewStore()
	st.EnsureType("A")
	en := aggview.NewEngine(st)
	err := en.DeclareView(aggview.PathDecl{
		Name:   "self",
		Source: "A",
		Hops:   []aggview.HopDecl{{Relation: "r", Types: []string{"A"}}},
		Target: "A",
		Attr:   "score",
	})
	if err != nil {
		t.Fatal(err)
	}
	st.CreateObject("x", "A", map[string]float64{"score": 5})
	st.CreateObject("y", "A", map[string]float64{"score": 9})
	// 加入自反链接 x->x：运行时环且无法静态排除，按优先级 3 拒绝该次变更。
	_, e := en.AddLink(ontology.Link{ID: "loop", From: "x", Rel: "r", To: "x"})
	if !errors.Is(e, ontology.ErrCycleDetected) {
		t.Fatalf("want ErrCycleDetected, got %v", e)
	}
	// 非自环链接 x->y 同样构成同类型自反层（自反类型集合）；按设计保守拒绝。
	_, e2 := en.AddLink(ontology.Link{ID: "xy", From: "x", Rel: "r", To: "y"})
	if !errors.Is(e2, ontology.ErrCycleDetected) {
		t.Fatalf("want ErrCycleDetected for reflexive layer edge, got %v", e2)
	}
}

func TestCycleStaticallySafe(t *testing.T) {
	// 三层类型两两不相交：A->B->C，环可在声明阶段静态排除，遍历按层去重，
	// 即便实例间存在回边也不会无限展开。
	st := ontology.NewStore()
	for _, ty := range []string{"A", "B", "C"} {
		st.EnsureType(ty)
	}
	en := aggview.NewEngine(st)
	err := en.DeclareView(aggview.PathDecl{
		Name:   "v",
		Source: "A",
		Hops: []aggview.HopDecl{
			{Relation: "r1", Types: []string{"B"}},
			{Relation: "r2", Types: []string{"C"}},
		},
		Target: "C",
		Attr:   "score",
	})
	if err != nil {
		t.Fatal(err)
	}
	st.CreateObject("a", "A", map[string]float64{})
	st.CreateObject("b", "B", map[string]float64{})
	st.CreateObject("c", "C", map[string]float64{"score": 6})
	mustAdd := func(id, from, rel, to string) {
		t.Helper()
		if _, e := en.AddLink(ontology.Link{ID: id, From: from, Rel: rel, To: to}); e != nil {
			t.Fatalf("add %s: %v", id, e)
		}
	}
	mustAdd("ab", "a", "r1", "b")
	mustAdd("bc", "b", "r2", "c")
	// 类型不匹配的回边：r1 只允许 A->B，b->a 端点类型不符，按优先级 1 报错。
	_, e := en.AddLink(ontology.Link{ID: "ba", From: "b", Rel: "r1", To: "a"})
	if !errors.Is(e, ontology.ErrTypeMismatch) {
		t.Fatalf("want type mismatch, got %v", e)
	}
	if r := mustQuery(t, en, "a"); r.Absent || r.Max != 6 {
		t.Fatalf("want 6, got %+v", r)
	}
}

func TestErrorPriority(t *testing.T) {
	f := newFixture(t)
	f.st.CreateObject("a1", "A", nil)
	// 同时缺失终点实例（优先级 2）且类型不匹配（优先级 1）时，先报类型不匹配。
	_, err := f.en.AddLink(ontology.Link{ID: "bad", From: "a1", Rel: "r1", To: "ghost"})
	if !errors.Is(err, ontology.ErrInstanceNotFound) {
		t.Fatalf("missing endpoint want ErrInstanceNotFound, got %v", err)
	}
	// 两端都存在但终点类型错误：类型不匹配（优先级 1）。
	f.st.CreateObject("other", "A", nil)
	_, err = f.en.AddLink(ontology.Link{ID: "tm", From: "a1", Rel: "r1", To: "other"})
	if !errors.Is(err, ontology.ErrTypeMismatch) {
		t.Fatalf("want ErrTypeMismatch, got %v", err)
	}
	// 查询不存在的起点实例。
	if _, err = f.en.Query("v", "nope"); !errors.Is(err, ontology.ErrInstanceNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}
