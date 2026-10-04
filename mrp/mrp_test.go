package mrp

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// 规格示例（T=4）：A=2B+1C，B=3D(scrap=100)，C=1D。
func buildSpecEngine(t *testing.T, bOnHand uint64) *Engine {
	t.Helper()
	eng, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	items := []struct {
		id                                string
		onHand, ss, lead, lotMin, lotMult uint64
	}{
		{"A", 5, 0, 1, 1, 1},
		{"B", bOnHand, 5, 1, 20, 20},
		{"C", 0, 0, 2, 1, 1},
		{"D", 50, 0, 0, 1, 100},
	}
	for _, it := range items {
		if err := eng.AddItem([]byte(it.id), it.onHand, it.ss, it.lead, it.lotMin, it.lotMult); err != nil {
			t.Fatal(err)
		}
	}
	edges := []struct {
		p, c       string
		per, scrap uint64
	}{
		{"A", "B", 2, 0},
		{"A", "C", 1, 0},
		{"B", "D", 3, 100},
		{"C", "D", 1, 0},
	}
	for _, e := range edges {
		if err := eng.AddComponent([]byte(e.p), []byte(e.c), e.per, e.scrap); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.Demand([]byte("A"), 3, 20); err != nil {
		t.Fatal(err)
	}
	return eng
}

func checkItem(t *testing.T, res *Result, id string, g, rec, rel, a []uint64) {
	t.Helper()
	got := res.Items[id]
	if got == nil {
		t.Fatalf("物料 %s 不在结果中", id)
	}
	if !slices.Equal(got.G, g) {
		t.Fatalf("%s.G = %v, 期望 %v", id, got.G, g)
	}
	if !slices.Equal(got.Rec, rec) {
		t.Fatalf("%s.Rec = %v, 期望 %v", id, got.Rec, rec)
	}
	if !slices.Equal(got.Rel, rel) {
		t.Fatalf("%s.Rel = %v, 期望 %v", id, got.Rel, rel)
	}
	if !slices.Equal(got.A, a) {
		t.Fatalf("%s.A = %v, 期望 %v", id, got.A, a)
	}
}

func TestSpecExample(t *testing.T) {
	eng := buildSpecEngine(t, 10)
	res, err := eng.Run()
	if err != nil {
		t.Fatal(err)
	}
	checkItem(t, res, "A", []uint64{0, 0, 0, 20, 0}, []uint64{0, 0, 0, 15, 0}, []uint64{0, 0, 15, 0, 0}, []uint64{5, 5, 5, 0, 0})
	checkItem(t, res, "B", []uint64{0, 0, 30, 0, 0}, []uint64{0, 0, 40, 0, 0}, []uint64{0, 40, 0, 0, 0}, []uint64{10, 10, 20, 20, 20})
	checkItem(t, res, "C", []uint64{0, 0, 15, 0, 0}, []uint64{0, 0, 15, 0, 0}, []uint64{0, 15, 0, 0, 0}, []uint64{0, 0, 0, 0, 0})
	checkItem(t, res, "D", []uint64{0, 149, 0, 0, 0}, []uint64{0, 100, 0, 0, 0}, []uint64{0, 100, 0, 0, 0}, []uint64{50, 1, 1, 1, 1})
	wantExc := []Exception{{Item: "C", T: 2, Short: 1}}
	if !reflect.DeepEqual(res.Exceptions, wantExc) {
		t.Fatalf("例外 = %+v, 期望 %+v", res.Exceptions, wantExc)
	}
	if res.visitedItems != 4 || res.visitedEdges != 4 {
		t.Fatalf("visitedItems=%d visitedEdges=%d, 期望 4/4", res.visitedItems, res.visitedEdges)
	}
	if want := []string{"A", "B", "C", "D"}; !slices.Equal(res.Order, want) {
		t.Fatalf("处理顺序 = %v, 期望 %v", res.Order, want)
	}
	peg, err := eng.Peg([]byte("D"), 1)
	if err != nil {
		t.Fatal(err)
	}
	wantPeg := PegInfo{Independent: 0, Parents: []PegEntry{{Parent: "B", Qty: 134}, {Parent: "C", Qty: 15}}}
	if !reflect.DeepEqual(peg, wantPeg) {
		t.Fatalf("Peg(D,1) = %+v, 期望 %+v", peg, wantPeg)
	}
	pegA, err := eng.Peg([]byte("A"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if pegA.Independent != 20 || len(pegA.Parents) != 0 {
		t.Fatalf("Peg(A,3) = %+v, 期望独立需求 20 无父项", pegA)
	}
}

// x 恰等安全库存时不订货：B 的 onHand 改为 35，第 2 期 x=5 恰等 ss=5。
func TestSpecExampleXEqualsSS(t *testing.T) {
	eng := buildSpecEngine(t, 35)
	res, err := eng.Run()
	if err != nil {
		t.Fatal(err)
	}
	checkItem(t, res, "B", []uint64{0, 0, 30, 0, 0}, []uint64{0, 0, 0, 0, 0}, []uint64{0, 0, 0, 0, 0}, []uint64{35, 35, 5, 5, 5})
	checkItem(t, res, "D", []uint64{0, 15, 0, 0, 0}, []uint64{0, 0, 0, 0, 0}, []uint64{0, 0, 0, 0, 0}, []uint64{50, 35, 35, 35, 35})
}

// 批量规则、损耗分母、逐父取整、提前期例外与第 1 期叠加、在途到货。
func TestPlannedOrderRules(t *testing.T) {
	type item struct {
		id                                string
		onHand, ss, lead, lotMin, lotMult uint64
	}
	type edge struct {
		p, c       string
		per, scrap uint64
	}
	type qty struct {
		id   string
		t, q uint64
	}
	cases := []struct {
		name    string
		T       uint64
		items   []item
		edges   []edge
		demands []qty
		scheds  []qty
		check   func(t *testing.T, res *Result)
		wantExc []Exception
	}{
		{
			name:    "lotMin大于倍数取整结果",
			T:       1,
			items:   []item{{"L", 0, 0, 0, 30, 10}},
			demands: []qty{{"L", 1, 5}},
			check: func(t *testing.T, res *Result) {
				checkItem(t, res, "L", []uint64{0, 5}, []uint64{0, 30}, []uint64{0, 30}, []uint64{0, 25})
			},
		},
		{
			name:    "损耗按1000-scrap做分母向上取整",
			T:       1,
			items:   []item{{"P", 0, 0, 0, 1, 1}, {"Q", 0, 0, 0, 1, 1}},
			edges:   []edge{{"P", "Q", 1, 500}},
			demands: []qty{{"P", 1, 3}},
			check: func(t *testing.T, res *Result) {
				// ⌈3·1·1000/500⌉=6；若误乘 (1000+scrap)/1000 则得 5。
				checkItem(t, res, "Q", []uint64{0, 6}, []uint64{0, 6}, []uint64{0, 6}, []uint64{0, 0})
			},
		},
		{
			name:    "对每个父项分别取整",
			T:       1,
			items:   []item{{"P1", 0, 0, 0, 1, 1}, {"P2", 0, 0, 0, 1, 1}, {"Q", 0, 0, 0, 1, 1}},
			edges:   []edge{{"P1", "Q", 1, 600}, {"P2", "Q", 1, 600}},
			demands: []qty{{"P1", 1, 1}, {"P2", 1, 1}},
			check: func(t *testing.T, res *Result) {
				// 每父 ⌈1000/400⌉=3，合计 6；若先合并再取整则得 5。
				checkItem(t, res, "Q", []uint64{0, 6}, []uint64{0, 6}, []uint64{0, 6}, []uint64{0, 0})
			},
		},
		{
			name:    "提前期例外与多笔投放叠加第1期后再取整",
			T:       3,
			items:   []item{{"P", 0, 0, 3, 1, 1}, {"C", 0, 0, 0, 1, 1}},
			edges:   []edge{{"P", "C", 1, 600}},
			demands: []qty{{"P", 2, 1}, {"P", 3, 1}},
			check: func(t *testing.T, res *Result) {
				checkItem(t, res, "P", []uint64{0, 0, 1, 1}, []uint64{0, 0, 1, 1}, []uint64{0, 2, 0, 0}, []uint64{0, 0, 0, 0})
				// Rel[1]=1+1=2 叠加后取整 ⌈2000/400⌉=5；逐笔取整则为 3+3=6。
				checkItem(t, res, "C", []uint64{0, 5, 0, 0}, []uint64{0, 5, 0, 0}, []uint64{0, 5, 0, 0}, []uint64{0, 0, 0, 0})
			},
			wantExc: []Exception{{Item: "P", T: 2, Short: 2}, {Item: "P", T: 3, Short: 1}},
		},
		{
			name:    "在途到货",
			T:       3,
			items:   []item{{"M", 5, 0, 1, 1, 1}},
			demands: []qty{{"M", 3, 20}},
			scheds:  []qty{{"M", 2, 10}},
			check: func(t *testing.T, res *Result) {
				checkItem(t, res, "M", []uint64{0, 0, 0, 20}, []uint64{0, 0, 0, 5}, []uint64{0, 0, 5, 0}, []uint64{5, 5, 15, 0})
			},
		},
		{
			name:    "共用件多路径聚合",
			T:       1,
			items:   []item{{"T", 0, 0, 0, 1, 1}, {"M1", 0, 0, 0, 1, 1}, {"M2", 0, 0, 0, 1, 1}, {"S", 0, 0, 0, 1, 1}},
			edges:   []edge{{"T", "M1", 1, 0}, {"T", "M2", 2, 0}, {"M1", "S", 1, 0}, {"M2", "S", 1, 0}},
			demands: []qty{{"T", 1, 3}},
			check: func(t *testing.T, res *Result) {
				// S.G[1] = 3(经M1) + 6(经M2) = 9。
				checkItem(t, res, "S", []uint64{0, 9}, []uint64{0, 9}, []uint64{0, 9}, []uint64{0, 0})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng, err := New(tc.T)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range tc.items {
				if err := eng.AddItem([]byte(it.id), it.onHand, it.ss, it.lead, it.lotMin, it.lotMult); err != nil {
					t.Fatal(err)
				}
			}
			for _, e := range tc.edges {
				if err := eng.AddComponent([]byte(e.p), []byte(e.c), e.per, e.scrap); err != nil {
					t.Fatal(err)
				}
			}
			for _, d := range tc.demands {
				if err := eng.Demand([]byte(d.id), d.t, d.q); err != nil {
					t.Fatal(err)
				}
			}
			for _, s := range tc.scheds {
				if err := eng.Scheduled([]byte(s.id), s.t, s.q); err != nil {
					t.Fatal(err)
				}
			}
			res, err := eng.Run()
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, res)
			if !reflect.DeepEqual(res.Exceptions, tc.wantExc) {
				t.Fatalf("例外 = %+v, 期望 %+v", res.Exceptions, tc.wantExc)
			}
		})
	}
}

func TestPegStaleness(t *testing.T) {
	eng, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(eng.AddItem([]byte("A"), 0, 0, 1, 1, 1))
	must(eng.Demand([]byte("A"), 1, 5))
	if _, err := eng.Peg([]byte("A"), 1); !errors.Is(err, ErrStale) {
		t.Fatalf("未 Run 时 Peg 错误 = %v, 期望 ErrStale", err)
	}
	if _, err := eng.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Peg([]byte("A"), 1); err != nil {
		t.Fatalf("Run 后 Peg 应可用: %v", err)
	}
	// 被拒绝的操作不使结果过期。
	if err := eng.Demand([]byte("A"), 1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("非法 Demand 错误 = %v, 期望 ErrInvalid", err)
	}
	if _, err := eng.Peg([]byte("A"), 1); err != nil {
		t.Fatalf("被拒绝的 Demand 不应使结果过期: %v", err)
	}
	// 各类被接受的变更均使结果过期。
	invalidate := func(mutate func() error, run func() error) {
		t.Helper()
		must(mutate())
		if _, err := eng.Peg([]byte("A"), 1); !errors.Is(err, ErrStale) {
			t.Fatalf("变更后 Peg 错误 = %v, 期望 ErrStale", err)
		}
		must(run())
		if _, err := eng.Peg([]byte("A"), 1); err != nil {
			t.Fatalf("重新 Run 后 Peg 应可用: %v", err)
		}
	}
	invalidate(func() error { return eng.Demand([]byte("A"), 2, 1) }, func() error { _, err := eng.Run(); return err })
	invalidate(func() error { return eng.Scheduled([]byte("A"), 2, 1) }, func() error { _, err := eng.Run(); return err })
	invalidate(func() error { return eng.AddItem([]byte("B"), 0, 0, 0, 1, 1) }, func() error { _, err := eng.Run(); return err })
	invalidate(func() error { return eng.AddComponent([]byte("A"), []byte("B"), 1, 0) }, func() error { _, err := eng.Run(); return err })
	// 被拒绝的 AddComponent（自环）不使结果过期。
	if err := eng.AddComponent([]byte("A"), []byte("A"), 1, 0); !errors.Is(err, ErrCycle) {
		t.Fatalf("自环错误 = %v, 期望 ErrCycle", err)
	}
	if _, err := eng.Peg([]byte("A"), 1); err != nil {
		t.Fatalf("被拒绝的 AddComponent 不应使结果过期: %v", err)
	}
}

func TestOverflowNoPartial(t *testing.T) {
	eng, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(eng.AddItem([]byte("P"), 0, 0, 0, 1, 1))
	must(eng.AddItem([]byte("Q"), 0, 0, 0, 1, 1))
	// Rel(P)=1e9，贡献 = ⌈1e9·1e4·1000/1⌉ = 1e16 > 1e15。
	must(eng.AddComponent([]byte("P"), []byte("Q"), 10_000, 999))
	must(eng.Demand([]byte("P"), 1, 1_000_000_000))
	res, err := eng.Run()
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("Run 错误 = %v, 期望 ErrOverflow", err)
	}
	if res != nil {
		t.Fatalf("溢出不应返回部分结果, 得到 %+v", res)
	}
	if _, err := eng.Peg([]byte("P"), 1); !errors.Is(err, ErrStale) {
		t.Fatalf("溢出后无有效结果, Peg 错误 = %v, 期望 ErrStale", err)
	}
	// 数据不变再次 Run 仍溢出；改为小 per 的新引擎则成功。
	if _, err := eng.Run(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("重复 Run 错误 = %v, 期望 ErrOverflow", err)
	}
	eng2, _ := New(1)
	must(eng2.AddItem([]byte("P"), 0, 0, 0, 1, 1))
	must(eng2.AddItem([]byte("Q"), 0, 0, 0, 1, 1))
	must(eng2.AddComponent([]byte("P"), []byte("Q"), 10, 999))
	must(eng2.Demand([]byte("P"), 1, 1_000_000_000))
	if _, err := eng2.Run(); err != nil {
		t.Fatalf("缩小 per 后应成功: %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	eng, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(eng.AddItem([]byte("A"), 0, 0, 0, 1, 1))
	must(eng.AddItem([]byte("B"), 0, 0, 0, 1, 1))
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"Demand非法优先于不存在", func() error { return eng.Demand([]byte("X"), 1, 0) }, ErrInvalid},
		{"Demand不存在", func() error { return eng.Demand([]byte("X"), 1, 1) }, ErrNotExist},
		{"Scheduled非法优先于不存在", func() error { return eng.Scheduled([]byte("X"), 99, 1) }, ErrInvalid},
		{"Scheduled不存在", func() error { return eng.Scheduled([]byte("X"), 1, 1) }, ErrNotExist},
		{"AddItem非法优先于冲突", func() error { return eng.AddItem([]byte("A"), 0, 0, 0, 0, 1) }, ErrInvalid},
		{"AddItem冲突", func() error { return eng.AddItem([]byte("A"), 0, 0, 0, 1, 1) }, ErrConflict},
		{"AddComponent非法优先于不存在", func() error { return eng.AddComponent([]byte("X"), []byte("Y"), 0, 0) }, ErrInvalid},
		{"AddComponent不存在优先于成环", func() error { return eng.AddComponent([]byte("B"), []byte("X"), 1, 0) }, ErrNotExist},
		{"AddComponent冲突", func() error {
			must(eng.AddComponent([]byte("A"), []byte("B"), 1, 0))
			return eng.AddComponent([]byte("A"), []byte("B"), 2, 0)
		}, ErrConflict},
		{"AddComponent成环", func() error { return eng.AddComponent([]byte("B"), []byte("A"), 1, 0) }, ErrCycle},
		{"AddComponent自环", func() error { return eng.AddComponent([]byte("A"), []byte("A"), 1, 0) }, ErrCycle},
		{"Peg非法优先于不存在与过期", func() error { _, err := eng.Peg([]byte(""), 0); return err }, ErrInvalid},
		{"Peg不存在优先于过期", func() error { _, err := eng.Peg([]byte("X"), 1); return err }, ErrNotExist},
		{"Peg过期", func() error { _, err := eng.Peg([]byte("A"), 1); return err }, ErrStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.op(); !errors.Is(err, tc.want) {
				t.Fatalf("错误 = %v, 期望 %v", err, tc.want)
			}
		})
	}
}

func TestRunRepeatableAndOrderIndependent(t *testing.T) {
	build := func(reverse bool) *Engine {
		eng, err := New(3)
		if err != nil {
			t.Fatal(err)
		}
		items := []struct {
			id                                string
			onHand, ss, lead, lotMin, lotMult uint64
		}{
			{"A", 1, 1, 1, 2, 3},
			{"B", 2, 0, 0, 1, 1},
			{"C", 3, 2, 2, 5, 5},
		}
		type edge struct {
			p, c       string
			per, scrap uint64
		}
		es := []edge{{"A", "B", 2, 10}, {"A", "C", 1, 0}, {"B", "C", 3, 100}}
		ds := [][3]uint64{{0, 1, 7}, {1, 2, 9}, {2, 3, 4}}
		ids := []string{"A", "B", "C"}
		n := len(items)
		idx := func(i int) int {
			if reverse {
				return n - 1 - i
			}
			return i
		}
		for i := 0; i < n; i++ {
			it := items[idx(i)]
			if err := eng.AddItem([]byte(it.id), it.onHand, it.ss, it.lead, it.lotMin, it.lotMult); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < len(es); i++ {
			e := es[idx(i)]
			if err := eng.AddComponent([]byte(e.p), []byte(e.c), e.per, e.scrap); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < len(ds); i++ {
			d := ds[idx(i)]
			if err := eng.Demand([]byte(ids[d[0]]), d[1], d[2]); err != nil {
				t.Fatal(err)
			}
			if err := eng.Scheduled([]byte(ids[d[0]]), d[1], 1); err != nil {
				t.Fatal(err)
			}
		}
		return eng
	}
	eng1, eng2 := build(false), build(true)
	res1, err := eng1.Run()
	if err != nil {
		t.Fatal(err)
	}
	res2, err := eng2.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res1, res2) {
		t.Fatalf("结果不应依赖声明次序")
	}
	// 重复 Run 结果相同且不改库存。
	res3, err := eng1.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res1, res3) {
		t.Fatalf("重复 Run 结果应相同")
	}
}

func TestConcurrency(t *testing.T) {
	eng, err := New(10)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"I0", "I1", "I2", "I3", "I4", "I5", "I6", "I7"}
	for _, id := range ids {
		if err := eng.AddItem([]byte(id), 0, 0, 0, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 1; j <= 50; j++ {
				if err := eng.Demand([]byte(id), uint64(j%10)+1, 1); err != nil {
					t.Error(err)
				}
				if err := eng.Scheduled([]byte(id), uint64(j%10)+1, 2); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			_, _ = eng.Run()
		}
	}()
	wg.Wait()
	res, err := eng.Run()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		got := res.Items[id]
		for tt := 1; tt <= 10; tt++ {
			if got.G[tt] != 5 {
				t.Fatalf("%s.G[%d] = %d, 期望 5（每个 goroutine 每期 5 笔）", id, tt, got.G[tt])
			}
		}
	}
}
