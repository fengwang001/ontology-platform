package mrp_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/bom"
	"ontology/mrp"
	"ontology/stock"
)

func newEngine(t *testing.T, periods int) (*mrp.Engine, *stock.Register, *bom.Graph) {
	t.Helper()
	st := stock.New()
	bg := bom.New()
	e, err := mrp.New(periods, st, bg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e, st, bg
}

func mustItem(t *testing.T, e *mrp.Engine, name string, onHand, ss int64, lead int, lotMin, lotMult int64) {
	t.Helper()
	if err := e.AddItem(name, onHand, ss, lead, lotMin, lotMult); err != nil {
		t.Fatalf("AddItem(%s): %v", name, err)
	}
}

func mustComp(t *testing.T, e *mrp.Engine, parent, child string, per, scrap int64) {
	t.Helper()
	if err := e.AddComponent(parent, child, per, scrap); err != nil {
		t.Fatalf("AddComponent(%s,%s): %v", parent, child, err)
	}
}

func mustDemand(t *testing.T, e *mrp.Engine, item string, period int, qty int64) {
	t.Helper()
	if err := e.Demand(item, period, qty); err != nil {
		t.Fatalf("Demand(%s,%d,%d): %v", item, period, qty, err)
	}
}

func eqRow(t *testing.T, label string, got, want []int64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v want %v", label, got, want)
	}
}

func buildSpecExample(t *testing.T, bOnHand int64) *mrp.Engine {
	t.Helper()
	e, _, _ := newEngine(t, 4)
	mustItem(t, e, "A", 5, 0, 1, 1, 1)
	mustItem(t, e, "B", bOnHand, 5, 1, 20, 20)
	mustItem(t, e, "C", 0, 0, 2, 1, 1)
	mustItem(t, e, "D", 50, 0, 0, 1, 100)
	mustComp(t, e, "A", "B", 2, 0)
	mustComp(t, e, "A", "C", 1, 0)
	mustComp(t, e, "B", "D", 3, 100)
	mustComp(t, e, "C", "D", 1, 0)
	mustDemand(t, e, "A", 3, 20)
	return e
}

func TestSpecExample(t *testing.T) {
	e := buildSpecExample(t, 10)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "A.G", res.G["A"], []int64{0, 0, 0, 20, 0})
	eqRow(t, "A.Rec", res.Rec["A"], []int64{0, 0, 0, 15, 0})
	eqRow(t, "A.Rel", res.Rel["A"], []int64{0, 0, 15, 0, 0})
	eqRow(t, "A.A", res.A["A"], []int64{5, 5, 5, 0, 0})
	eqRow(t, "B.G", res.G["B"], []int64{0, 0, 30, 0, 0})
	eqRow(t, "B.Rec", res.Rec["B"], []int64{0, 0, 40, 0, 0})
	eqRow(t, "B.Rel", res.Rel["B"], []int64{0, 40, 0, 0, 0})
	eqRow(t, "B.A", res.A["B"], []int64{10, 10, 20, 20, 20})
	eqRow(t, "C.G", res.G["C"], []int64{0, 0, 15, 0, 0})
	eqRow(t, "C.Rec", res.Rec["C"], []int64{0, 0, 15, 0, 0})
	eqRow(t, "C.Rel", res.Rel["C"], []int64{0, 15, 0, 0, 0})
	eqRow(t, "C.A", res.A["C"], []int64{0, 0, 0, 0, 0})
	eqRow(t, "D.G", res.G["D"], []int64{0, 149, 0, 0, 0})
	eqRow(t, "D.Rec", res.Rec["D"], []int64{0, 100, 0, 0, 0})
	eqRow(t, "D.Rel", res.Rel["D"], []int64{0, 100, 0, 0, 0})
	eqRow(t, "D.A", res.A["D"], []int64{50, 1, 1, 1, 1})

	wantExc := []mrp.Exception{{Item: "C", Period: 2, Short: 1}}
	if !reflect.DeepEqual(res.Exceptions, wantExc) {
		t.Errorf("Exceptions: got %+v want %+v", res.Exceptions, wantExc)
	}

	peg, err := e.Peg("D", 1)
	if err != nil {
		t.Fatalf("Peg: %v", err)
	}
	wantPeg := mrp.Peg{Independent: 0, Sources: []mrp.PegSource{
		{Parent: "B", Qty: 134}, {Parent: "C", Qty: 15},
	}}
	if !reflect.DeepEqual(peg, wantPeg) {
		t.Errorf("Peg(D,1): got %+v want %+v", peg, wantPeg)
	}
	pegA, err := e.Peg("A", 3)
	if err != nil {
		t.Fatalf("Peg(A,3): %v", err)
	}
	if pegA.Independent != 20 || len(pegA.Sources) != 0 {
		t.Errorf("Peg(A,3): got %+v", pegA)
	}
}

// B 的 onHand 为 35 时第 2 期 x 恰等安全库存，不订货。
func TestExactSafetyStockNoOrder(t *testing.T) {
	e := buildSpecExample(t, 35)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "B.G", res.G["B"], []int64{0, 0, 30, 0, 0})
	eqRow(t, "B.Rec", res.Rec["B"], []int64{0, 0, 0, 0, 0})
	eqRow(t, "B.Rel", res.Rel["B"], []int64{0, 0, 0, 0, 0})
	eqRow(t, "B.A", res.A["B"], []int64{35, 35, 5, 5, 5})
	eqRow(t, "D.G", res.G["D"], []int64{0, 15, 0, 0, 0})
	eqRow(t, "D.Rec", res.Rec["D"], []int64{0, 0, 0, 0, 0})
	eqRow(t, "D.A", res.A["D"], []int64{50, 35, 35, 35, 35})
	if len(res.Exceptions) != 1 || res.Exceptions[0].Item != "C" {
		t.Errorf("Exceptions: got %+v", res.Exceptions)
	}
}

// 净需求 5，倍数取整得 8，但 lotMin=10 占优。
func TestLotMinBeatsMultiple(t *testing.T) {
	e, _, _ := newEngine(t, 2)
	mustItem(t, e, "X", 0, 0, 0, 10, 4)
	mustDemand(t, e, "X", 1, 5)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "X.Rec", res.Rec["X"], []int64{0, 10, 0})
	eqRow(t, "X.A", res.A["X"], []int64{0, 5, 5})
}

// 损耗按 1000-scrap 做分母向上取整：ceil(3*1000/500)=6，
// 而非乘 (1000+scrap) 的 ceil(3*1500/1000)=5。
func TestScrapDenominator(t *testing.T) {
	e, _, _ := newEngine(t, 2)
	mustItem(t, e, "P", 0, 0, 0, 1, 1)
	mustItem(t, e, "Q", 0, 0, 0, 1, 1)
	mustComp(t, e, "P", "Q", 1, 500)
	mustDemand(t, e, "P", 1, 3)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "Q.G", res.G["Q"], []int64{0, 6, 0})
	eqRow(t, "Q.Rec", res.Rec["Q"], []int64{0, 6, 0})
}

// 对每个父项分别取整：两个父项各投放 1，per=1、scrap=100，
// G = 2*ceil(1000/900) = 4，而非合并后 ceil(2000/900) = 3。
func TestPerParentRounding(t *testing.T) {
	e, _, _ := newEngine(t, 2)
	mustItem(t, e, "P1", 0, 0, 0, 1, 1)
	mustItem(t, e, "P2", 0, 0, 0, 1, 1)
	mustItem(t, e, "Q", 0, 0, 0, 1, 1)
	mustComp(t, e, "P1", "Q", 1, 100)
	mustComp(t, e, "P2", "Q", 1, 100)
	mustDemand(t, e, "P1", 1, 1)
	mustDemand(t, e, "P2", 1, 1)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "Q.G", res.G["Q"], []int64{0, 4, 0})
	peg, err := e.Peg("Q", 1)
	if err != nil {
		t.Fatalf("Peg: %v", err)
	}
	want := mrp.Peg{Independent: 0, Sources: []mrp.PegSource{
		{Parent: "P1", Qty: 2}, {Parent: "P2", Qty: 2},
	}}
	if !reflect.DeepEqual(peg, want) {
		t.Errorf("Peg(Q,1): got %+v want %+v", peg, want)
	}
}

// 提前期越过第 1 期：多笔投放叠加在第 1 期后再对子项取整，
// 并逐笔记例外（欠缺期数 1-(t-lead)）。
func TestLeadExceptionStacking(t *testing.T) {
	e, _, _ := newEngine(t, 4)
	mustItem(t, e, "P", 0, 0, 3, 1, 1)
	mustItem(t, e, "Q", 0, 0, 0, 1, 1)
	mustComp(t, e, "P", "Q", 1, 600)
	mustDemand(t, e, "P", 1, 5)
	mustDemand(t, e, "P", 2, 3)
	mustDemand(t, e, "P", 3, 2)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "P.Rec", res.Rec["P"], []int64{0, 5, 3, 2, 0})
	eqRow(t, "P.Rel", res.Rel["P"], []int64{0, 10, 0, 0, 0})
	wantExc := []mrp.Exception{
		{Item: "P", Period: 1, Short: 3},
		{Item: "P", Period: 2, Short: 2},
		{Item: "P", Period: 3, Short: 1},
	}
	if !reflect.DeepEqual(res.Exceptions, wantExc) {
		t.Errorf("Exceptions: got %+v want %+v", res.Exceptions, wantExc)
	}
	// 子项对叠加后的 10 取整：ceil(10*1000/400)=25；
	// 若逐笔取整则为 13+8+5=26。
	eqRow(t, "Q.G", res.G["Q"], []int64{0, 25, 0, 0, 0})
}

func TestScheduledReceipts(t *testing.T) {
	e, _, _ := newEngine(t, 3)
	mustItem(t, e, "X", 2, 5, 0, 1, 1)
	if err := e.Scheduled("X", 1, 10); err != nil {
		t.Fatalf("Scheduled: %v", err)
	}
	mustDemand(t, e, "X", 2, 8)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "X.G", res.G["X"], []int64{0, 0, 8, 0})
	eqRow(t, "X.Rec", res.Rec["X"], []int64{0, 0, 1, 0})
	eqRow(t, "X.A", res.A["X"], []int64{2, 12, 5, 5})
}

// 共用件经多条路径聚合：S 同时是 L 与 R 的子项。
func TestSharedComponentMultiPath(t *testing.T) {
	e, _, _ := newEngine(t, 2)
	for _, n := range []string{"Top", "L", "R", "S"} {
		mustItem(t, e, n, 0, 0, 0, 1, 1)
	}
	mustComp(t, e, "Top", "L", 1, 0)
	mustComp(t, e, "Top", "R", 1, 0)
	mustComp(t, e, "L", "S", 1, 0)
	mustComp(t, e, "R", "S", 1, 0)
	mustDemand(t, e, "Top", 1, 4)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eqRow(t, "S.G", res.G["S"], []int64{0, 8, 0})
	eqRow(t, "S.Rec", res.Rec["S"], []int64{0, 8, 0})
	peg, err := e.Peg("S", 1)
	if err != nil {
		t.Fatalf("Peg: %v", err)
	}
	want := mrp.Peg{Independent: 0, Sources: []mrp.PegSource{
		{Parent: "L", Qty: 4}, {Parent: "R", Qty: 4},
	}}
	if !reflect.DeepEqual(peg, want) {
		t.Errorf("Peg(S,1): got %+v want %+v", peg, want)
	}
}

func TestCycleSelfLoopAndFreshness(t *testing.T) {
	e, _, _ := newEngine(t, 2)
	for _, n := range []string{"A", "B", "C"} {
		mustItem(t, e, n, 0, 0, 0, 1, 1)
	}
	mustComp(t, e, "A", "B", 1, 0)
	mustComp(t, e, "B", "C", 1, 0)
	if err := e.AddComponent("C", "A", 1, 0); !errors.Is(err, mrp.ErrCycle) {
		t.Fatalf("3-cycle: got %v", err)
	}
	if err := e.AddComponent("A", "A", 1, 0); !errors.Is(err, mrp.ErrCycle) {
		t.Fatalf("self-loop: got %v", err)
	}
	if err := e.AddComponent("A", "B", 2, 0); !errors.Is(err, mrp.ErrConflict) {
		t.Fatalf("duplicate: got %v", err)
	}
	if _, err := e.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := e.Peg("C", 1); err != nil {
		t.Fatalf("Peg after rejected ops must stay fresh: %v", err)
	}
}

func TestPegStaleness(t *testing.T) {
	e, st, _ := newEngine(t, 2)
	mustItem(t, e, "A", 0, 0, 0, 1, 1)
	mustItem(t, e, "B", 0, 0, 0, 1, 1)

	if _, err := e.Peg("A", 1); !errors.Is(err, mrp.ErrStale) {
		t.Fatalf("before Run: got %v", err)
	}
	if _, err := e.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := e.Peg("A", 1); err != nil {
		t.Fatalf("after Run: %v", err)
	}

	steps := []struct {
		name string
		mut  func() error
	}{
		{"Demand", func() error { return e.Demand("A", 1, 1) }},
		{"Scheduled", func() error { return e.Scheduled("A", 1, 1) }},
		{"AddItem", func() error { return e.AddItem("C", 0, 0, 0, 1, 1) }},
		{"AddComponent", func() error { return e.AddComponent("A", "B", 1, 0) }},
		{"DirectStockAdd", func() error { return st.AddItem("D", 0, 0, 0, 1, 1) }},
	}
	for _, s := range steps {
		if err := s.mut(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if _, err := e.Peg("A", 1); !errors.Is(err, mrp.ErrStale) {
			t.Fatalf("%s: Peg should be stale, got %v", s.name, err)
		}
		if _, err := e.Run(); err != nil {
			t.Fatalf("%s: re-Run: %v", s.name, err)
		}
		if _, err := e.Peg("A", 1); err != nil {
			t.Fatalf("%s: Peg after re-Run: %v", s.name, err)
		}
	}

	// 被拒绝的变更不使结果过期。
	if err := e.Demand("A", 1, 0); !errors.Is(err, mrp.ErrInvalid) {
		t.Fatalf("bad demand: got %v", err)
	}
	if err := e.AddItem("A", 0, 0, 0, 1, 1); !errors.Is(err, mrp.ErrConflict) {
		t.Fatalf("dup item: got %v", err)
	}
	if _, err := e.Peg("A", 1); err != nil {
		t.Fatalf("rejected ops must not stale: %v", err)
	}
}

func TestOverflowNoPartial(t *testing.T) {
	e, _, _ := newEngine(t, 2)
	mustItem(t, e, "X", 0, 0, 0, 1, 1)
	mustItem(t, e, "Y", 0, 0, 0, 1, 1)
	mustItem(t, e, "Z", 0, 0, 0, 1, 1)
	mustComp(t, e, "X", "Y", 10_000, 0)
	mustComp(t, e, "Y", "Z", 10_000, 0)

	mustDemand(t, e, "X", 1, 5)
	res, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res == nil {
		t.Fatal("want result")
	}
	if _, err := e.Peg("X", 1); err != nil {
		t.Fatalf("Peg: %v", err)
	}

	// X.Rel=1e9 → Y.G=1e13 → Z.G=1e17 > 1e15，溢出。
	mustDemand(t, e, "X", 1, 1_000_000_000)
	res, err = e.Run()
	if !errors.Is(err, mrp.ErrOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
	if res != nil {
		t.Fatalf("overflow must not return partial result: %+v", res)
	}
	if _, err := e.Peg("X", 1); !errors.Is(err, mrp.ErrStale) {
		t.Fatalf("data changed since last good Run, want stale, got %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	e, _, _ := newEngine(t, 2)
	mustItem(t, e, "A", 0, 0, 0, 1, 1)
	mustItem(t, e, "B", 0, 0, 0, 1, 1)
	mustComp(t, e, "A", "B", 1, 0)

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		// 参数非法 优先于 冲突
		{"item-invalid-and-dup", func() error { return e.AddItem("A", 0, 0, 0, 0, 1) }, mrp.ErrInvalid},
		{"item-dup", func() error { return e.AddItem("A", 0, 0, 0, 1, 1) }, mrp.ErrConflict},
		// 参数非法 优先于 不存在
		{"comp-invalid-and-missing", func() error { return e.AddComponent("A", "ZZ", 0, 0) }, mrp.ErrInvalid},
		{"comp-missing-parent", func() error { return e.AddComponent("ZZ", "B", 1, 0) }, mrp.ErrNotExist},
		{"comp-missing-child", func() error { return e.AddComponent("A", "ZZ", 1, 0) }, mrp.ErrNotExist},
		// 不存在 优先于 冲突/成环
		{"comp-missing-before-cycle", func() error { return e.AddComponent("B", "ZZ", 1, 0) }, mrp.ErrNotExist},
		// 冲突 优先于 成环
		{"comp-dup", func() error { return e.AddComponent("A", "B", 9, 9) }, mrp.ErrConflict},
		{"comp-cycle", func() error { return e.AddComponent("B", "A", 1, 0) }, mrp.ErrCycle},
		{"demand-invalid", func() error { return e.Demand("A", 0, 1) }, mrp.ErrInvalid},
		{"demand-invalid-qty", func() error { return e.Demand("A", 1, 1_000_000_001) }, mrp.ErrInvalid},
		{"demand-missing", func() error { return e.Demand("ZZ", 1, 1) }, mrp.ErrNotExist},
		{"sched-invalid", func() error { return e.Scheduled("A", 3, 1) }, mrp.ErrInvalid},
		{"sched-missing", func() error { return e.Scheduled("ZZ", 1, 1) }, mrp.ErrNotExist},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}

	// Peg 的次序：参数非法 > 不存在 > 结果过期。
	if _, err := e.Peg("ZZ", 0); !errors.Is(err, mrp.ErrInvalid) {
		t.Errorf("peg invalid+missing: got %v", err)
	}
	if _, err := e.Peg("ZZ", 1); !errors.Is(err, mrp.ErrNotExist) {
		t.Errorf("peg missing+stale: got %v", err)
	}
	if _, err := e.Peg("A", 1); !errors.Is(err, mrp.ErrStale) {
		t.Errorf("peg stale: got %v", err)
	}
}

func TestRunIdempotentAndNoMutation(t *testing.T) {
	e, st, bg := newEngine(t, 4)
	mustItem(t, e, "A", 5, 0, 1, 1, 1)
	mustItem(t, e, "B", 10, 5, 1, 20, 20)
	mustComp(t, e, "A", "B", 2, 0)
	mustDemand(t, e, "A", 3, 20)
	stockVer := st.Version()
	bomVer := bg.Version()
	r1, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r2, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("repeated Run differs")
	}
	if st.Version() != stockVer || bg.Version() != bomVer {
		t.Fatal("Run mutated registries")
	}
	it, _ := st.Get("A")
	if it.OnHand != 5 {
		t.Fatalf("Run changed onHand: %d", it.OnHand)
	}
}

// 同一批数据以不同声明次序录入，Run 结果必须一致。
func TestDeclarationOrderIndependence(t *testing.T) {
	build := func(reverse bool) *mrp.Engine {
		e, _, _ := newEngine(t, 4)
		items := []struct {
			name            string
			onHand, ss      int64
			lead            int
			lotMin, lotMult int64
		}{
			{"A", 5, 0, 1, 1, 1},
			{"B", 10, 5, 1, 20, 20},
			{"C", 0, 0, 2, 1, 1},
			{"D", 50, 0, 0, 1, 100},
		}
		if reverse {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		for _, it := range items {
			mustItem(t, e, it.name, it.onHand, it.ss, it.lead, it.lotMin, it.lotMult)
		}
		edges := []struct {
			p, c       string
			per, scrap int64
		}{
			{"A", "B", 2, 0}, {"A", "C", 1, 0}, {"B", "D", 3, 100}, {"C", "D", 1, 0},
		}
		if reverse {
			for i, j := 0, len(edges)-1; i < j; i, j = i+1, j-1 {
				edges[i], edges[j] = edges[j], edges[i]
			}
		}
		for _, ed := range edges {
			mustComp(t, e, ed.p, ed.c, ed.per, ed.scrap)
		}
		mustDemand(t, e, "A", 3, 20)
		return e
	}
	r1, err := build(false).Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r2, err := build(true).Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("result depends on declaration order")
	}
}

// ---------- 朴素参照实现：按路径递归逐层展开 ----------

type naiveWorld struct {
	periods int
	items   map[string]stock.Item
	edges   []bom.Edge
	demand  map[string][]int64
	sched   map[string][]int64
}

type naiveRow struct {
	G, Rec, Rel, A []int64
	exc            []mrp.Exception
}

func naiveAt(a []int64, i int) int64 {
	if a == nil {
		return 0
	}
	return a[i]
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// calc 不带任何记忆化，沿父项路径递归重算，路径数指数级也能算出正确结果。
func (w *naiveWorld) calc(name string) naiveRow {
	it := w.items[name]
	type parentRow struct {
		edge bom.Edge
		row  naiveRow
	}
	var parents []parentRow
	for _, e := range w.edges {
		if e.Child == name {
			parents = append(parents, parentRow{e, w.calc(e.Parent)})
		}
	}
	row := naiveRow{
		G:   make([]int64, w.periods+1),
		Rec: make([]int64, w.periods+1),
		Rel: make([]int64, w.periods+1),
		A:   make([]int64, w.periods+1),
	}
	for tp := 1; tp <= w.periods; tp++ {
		g := naiveAt(w.demand[name], tp)
		for _, p := range parents {
			if p.row.Rel[tp] > 0 {
				g += ceilDiv(p.row.Rel[tp]*p.edge.Per*1000, 1000-p.edge.Scrap)
			}
		}
		row.G[tp] = g
	}
	row.A[0] = it.OnHand
	for tp := 1; tp <= w.periods; tp++ {
		x := row.A[tp-1] + naiveAt(w.sched[name], tp) - row.G[tp]
		var rec int64
		if x < it.SS {
			rec = stock.Lot(it, it.SS-x)
		}
		row.Rec[tp] = rec
		row.A[tp] = x + rec
		if rec > 0 {
			rt := tp - it.Lead
			slot := rt
			if slot < 1 {
				slot = 1
			}
			row.Rel[slot] += rec
			if rt < 1 {
				row.exc = append(row.exc, mrp.Exception{Item: name, Period: tp, Short: 1 - rt})
			}
		}
	}
	return row
}

func TestRandomVsNaive(t *testing.T) {
	r := rand.New(rand.NewSource(20261004))
	for iter := 0; iter < 1500; iter++ {
		n := 2 + r.Intn(6) // 2..7 种物料
		periods := 1 + r.Intn(5)
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("M%d", i)
		}
		perm := r.Perm(n)

		e, _, _ := newEngine(t, periods)
		w := &naiveWorld{
			periods: periods,
			items:   make(map[string]stock.Item),
			demand:  make(map[string][]int64),
			sched:   make(map[string][]int64),
		}
		for _, name := range names {
			it := stock.Item{
				Name:    name,
				OnHand:  int64(r.Intn(51)),
				SS:      int64(r.Intn(21)),
				Lead:    r.Intn(4),
				LotMin:  int64(1 + r.Intn(30)),
				LotMult: int64(1 + r.Intn(30)),
			}
			w.items[name] = it
			mustItem(t, e, name, it.OnHand, it.SS, it.Lead, it.LotMin, it.LotMult)
		}
		// 仅允许拓扑序靠前的作父项，保证无环。
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if r.Intn(10) < 3 {
					p, c := names[perm[i]], names[perm[j]]
					per := int64(1 + r.Intn(3))
					scrap := int64(r.Intn(501))
					w.edges = append(w.edges, bom.Edge{Parent: p, Child: c, Per: per, Scrap: scrap})
					mustComp(t, e, p, c, per, scrap)
				}
			}
		}
		for _, name := range names {
			for tp := 1; tp <= periods; tp++ {
				if r.Intn(10) < 3 {
					qty := int64(1 + r.Intn(20))
					mustDemand(t, e, name, tp, qty)
					a := w.demand[name]
					if a == nil {
						a = make([]int64, periods+1)
						w.demand[name] = a
					}
					a[tp] += qty
				}
				if r.Intn(10) < 2 {
					qty := int64(1 + r.Intn(20))
					if err := e.Scheduled(name, tp, qty); err != nil {
						t.Fatalf("Scheduled: %v", err)
					}
					a := w.sched[name]
					if a == nil {
						a = make([]int64, periods+1)
						w.sched[name] = a
					}
					a[tp] += qty
				}
			}
		}

		res, err := e.Run()
		if err != nil {
			t.Fatalf("iter %d: Run: %v", iter, err)
		}

		var wantExc []mrp.Exception
		for _, name := range names {
			row := w.calc(name)
			wantExc = append(wantExc, row.exc...)
			eqRow(t, fmt.Sprintf("iter %d %s.G", iter, name), res.G[name], row.G)
			eqRow(t, fmt.Sprintf("iter %d %s.Rec", iter, name), res.Rec[name], row.Rec)
			eqRow(t, fmt.Sprintf("iter %d %s.Rel", iter, name), res.Rel[name], row.Rel)
			eqRow(t, fmt.Sprintf("iter %d %s.A", iter, name), res.A[name], row.A)

			it := w.items[name]
			var sumRec, sumRel int64
			for tp := 1; tp <= periods; tp++ {
				sumRec += res.Rec[name][tp]
				sumRel += res.Rel[name][tp]
				want := res.A[name][tp-1] + naiveAt(w.sched[name], tp) + res.Rec[name][tp] - res.G[name][tp]
				if res.A[name][tp] != want {
					t.Errorf("iter %d %s t=%d: A 不变量不成立", iter, name, tp)
				}
				if res.Rec[name][tp] > 0 && res.A[name][tp] < it.SS {
					t.Errorf("iter %d %s t=%d: 有订货但 A<ss", iter, name, tp)
				}
				peg, err := e.Peg(name, tp)
				if err != nil {
					t.Fatalf("iter %d Peg(%s,%d): %v", iter, name, tp, err)
				}
				var sum int64 = peg.Independent
				for _, s := range peg.Sources {
					sum += s.Qty
				}
				if sum != res.G[name][tp] {
					t.Errorf("iter %d Peg(%s,%d): 各项之和 %d != G %d",
						iter, name, tp, sum, res.G[name][tp])
				}
			}
			if sumRec != sumRel {
				t.Errorf("iter %d %s: sum(Rec)=%d != sum(Rel)=%d", iter, name, sumRec, sumRel)
			}
		}
		sort.Slice(wantExc, func(i, j int) bool {
			if wantExc[i].Item != wantExc[j].Item {
				return wantExc[i].Item < wantExc[j].Item
			}
			return wantExc[i].Period < wantExc[j].Period
		})
		if len(res.Exceptions) != len(wantExc) {
			t.Fatalf("iter %d: exceptions got %+v want %+v", iter, res.Exceptions, wantExc)
		}
		for i := range wantExc {
			if res.Exceptions[i] != wantExc[i] {
				t.Fatalf("iter %d: exceptions got %+v want %+v", iter, res.Exceptions, wantExc)
			}
		}
		t.Logf("case %d: T=%d items=%d edges=%v demand=%v sched=%v => exceptions=%v; 判定: G/Rec/Rel/A 与朴素递归一致, A 不变量/Peg 求和/Rec=Rel 均成立",
			iter, periods, n, w.edges, w.demand, w.sched, res.Exceptions)
	}
}

func TestConcurrentCalls(t *testing.T) {
	e, _, _ := newEngine(t, 4)
	const n = 8
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("I%d", i)
		mustItem(t, e, names[i], int64(i), int64(i%3), i%3, 1, 1)
	}
	mustComp(t, e, names[0], names[1], 2, 0)
	mustComp(t, e, names[1], names[2], 1, 100)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := e.Demand(names[i], 1+i%4, int64(i+1)); err != nil {
				t.Errorf("Demand: %v", err)
			}
			if err := e.Scheduled(names[i], 1, int64(i+1)); err != nil {
				t.Errorf("Scheduled: %v", err)
			}
			_, _ = e.Run()
			_, _ = e.Peg(names[i], 1)
		}(i)
	}
	wg.Wait()

	r1, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r2, err := e.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("post-quiescence Runs differ")
	}
	// 全部并发 Demand 都已生效，等价于串行累加。
	for i := 0; i < n; i++ {
		peg, err := e.Peg(names[i], 1+i%4)
		if err != nil {
			t.Fatalf("Peg: %v", err)
		}
		if peg.Independent != int64(i+1) {
			t.Errorf("%s: independent=%d want %d", names[i], peg.Independent, i+1)
		}
	}
}
