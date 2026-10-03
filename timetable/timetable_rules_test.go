package timetable

import "testing"

func add(t *testing.T, nw *Network, u, v int, p []Segment) int {
	id, err := nw.AddEdge(u, v, p)
	if err != nil {
		t.Helper()
		t.Fatalf("AddEdge(%d->%d): %v", u, v, err)
	}
	return id
}

// 等待到后续分段起点比立即出发更早；恰在分段起点与差 1。
func TestWaitForSegment(t *testing.T) {
	nw, _ := New(3, 10)
	add(t, nw, 0, 1, []Segment{{0, 100}, {10, 1}})
	add(t, nw, 1, 2, []Segment{{0, 1}})
	// t=9：立即走 109；等到 10 走 11。
	r, err := nw.EarliestArrival(0, 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, r, 12, legs([]int{1, 2}, []int64{10, 11}, []int64{11, 12}))
	// t=10 恰在起点：10+1=11。
	r, _ = nw.EarliestArrival(0, 10, 2)
	checkResult(t, r, 12, legs([]int{1, 2}, []int64{10, 11}, []int64{11, 12}))
	// t=0：立即走 100+1=101，仍优于等到 10（11）吗？11 更小 => 等。
	r, _ = nw.EarliestArrival(0, 0, 2)
	checkResult(t, r, 12, legs([]int{1, 2}, []int64{10, 11}, []int64{11, 12}))
}

// 当前分段封闭、后续开放：只能等待；全封闭则该边不可用。
func TestClosedSegments(t *testing.T) {
	nw, _ := New(3, 10)
	a := add(t, nw, 0, 1, []Segment{{0, -1}, {5, 2}})
	add(t, nw, 1, 2, []Segment{{0, 1}})
	r, err := nw.EarliestArrival(0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Arrival != 8 || r.Route[0].Dep != 5 {
		t.Fatalf("got arr=%d dep0=%d, want 8/5", r.Arrival, r.Route[0].Dep)
	}
	if r.Route[0].Edge != a {
		t.Fatalf("edge = %d want %d", r.Route[0].Edge, a)
	}

	nw2, _ := New(2, 10)
	add(t, nw2, 0, 1, []Segment{{0, 5}, {10, -1}})
	// t=10 之后所有分段封闭。
	_, err = nw2.EarliestArrival(0, 10, 1)
	wantErr(t, err, ErrUnreachable)
	// t=9 仍可在 9 出发到 14。
	r2, err := nw2.EarliestArrival(0, 9, 1)
	if err != nil || r2.Arrival != 14 {
		t.Fatalf("arr at 9: %v %v", r2, err)
	}
}

// 到达时刻相同：先边数少，再边编号序列字典序。
func TestTightEdgeTieBreak(t *testing.T) {
	// 0->2 一条边到达 10；0->1->2 两条边也到达 10：取一条边。
	nw, _ := New(3, 10)
	direct := add(t, nw, 0, 2, []Segment{{0, 10}})
	add(t, nw, 0, 1, []Segment{{0, 4}})
	add(t, nw, 1, 2, []Segment{{0, 6}})
	r, err := nw.EarliestArrival(0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Arrival != 10 || len(r.Route) != 1 || r.Route[0].Edge != direct {
		t.Fatalf("got %#v", r)
	}

	// 同为两条边：路径 (10,12) vs (11,12)：取首边 10。
	nw2, _ := New(4, 20)
	e10 := add(t, nw2, 0, 1, []Segment{{0, 1}}) // 编号 1
	add(t, nw2, 0, 2, []Segment{{0, 1}})        // 编号 2
	// 用平行边制造：0->3 两条平行边分别配合。
	add(t, nw2, 1, 3, []Segment{{0, 9}}) // 编号 3，序列 [1,3] 到 10
	add(t, nw2, 2, 3, []Segment{{0, 9}}) // 编号 4，序列 [2,4] 到 10
	r2, err := nw2.EarliestArrival(0, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Arrival != 10 || len(r2.Route) != 2 {
		t.Fatalf("got %#v", r2.Route)
	}
	if r2.Route[0].Edge != e10 {
		t.Fatalf("lexicographic: got first edge %d want %d", r2.Route[0].Edge, e10)
	}
}

// 平行边：紧边取编号小者。
func TestParallelEdges(t *testing.T) {
	nw, _ := New(2, 10)
	add(t, nw, 0, 1, []Segment{{0, 5}})
	add(t, nw, 0, 1, []Segment{{0, 5}})
	r, err := nw.EarliestArrival(0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Arrival != 5 || len(r.Route) != 1 || r.Route[0].Edge != 1 {
		t.Fatalf("got %#v", r)
	}
}

// 通告替换与追加、64 条上限、被替换记录不占名额。
func TestAnnounceReplace(t *testing.T) {
	nw, _ := New(2, 10)
	e := add(t, nw, 0, 1, []Segment{{0, 10}})
	// 连续在同一 eff 替换：不增加条数。
	for i := 0; i < 100; i++ {
		if err := nw.Announce(e, 5, []Segment{{0, int64(1 + i%5)}}); err != nil {
			t.Fatalf("replace %d: %v", i, err)
		}
	}
	if nw.Version() != 101 {
		t.Fatalf("version=%d want 101", nw.Version())
	}
	if got := nw.edges[0].live; got != 2 {
		t.Fatalf("live records=%d want 2 (eff0 + eff5; replacements do not count)", got)
	}
	if got := len(nw.edges[0].records); got != 101 {
		t.Fatalf("stored records=%d want 101 (history retained)", got)
	}
	// 当前生效耗时为最后一次替换值 1（100 次替换，i=99 -> 1+4=5）。
	r, _ := nw.EarliestArrival(0, 5, 1)
	if r.Arrival != 10 {
		t.Fatalf("arr=%d want 10", r.Arrival)
	}

	// 已有 2 条有效，再追加 62 条到 64；第 63 条必须被拒。
	for i := int64(0); i < 62; i++ {
		if err := nw.Announce(e, 6+i, []Segment{{0, 3}}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := nw.Announce(e, 100, []Segment{{0, 3}}); err != nil {
		wantErr(t, err, ErrRecordLimit)
	}
	// 替换最后一条不受限。
	if err := nw.Announce(e, 67, []Segment{{0, 9}}); err != nil {
		t.Fatal(err)
	}
}

// 时钟与通告规则：eff==now 允许，小 1 被拒；乱序拒绝。
func TestClockRules(t *testing.T) {
	nw, _ := New(2, 10)
	e := add(t, nw, 0, 1, []Segment{{0, 1}})
	if err := nw.Announce(e, 0, []Segment{{0, 2}}); err != nil { // ==now 替换
		t.Fatal(err)
	}
	wantErr(t, nw.Advance(-1), ErrInvalidArgument)
	if err := nw.Advance(10); err != nil {
		t.Fatal(err)
	}
	wantErr(t, nw.Advance(9), ErrClockRollback)
	wantErr(t, nw.Announce(e, 9, []Segment{{0, 1}}), ErrRetroactive)
	if err := nw.Announce(e, 10, []Segment{{0, 1}}); err != nil { // ==now
		t.Fatal(err)
	}
	if err := nw.Announce(e, 10, []Segment{{0, 1}}); err != nil { // 同 eff 替换仍允许
		t.Fatal(err)
	}
	if err := nw.Announce(e, 11, []Segment{{0, 1}}); err != nil {
		t.Fatal(err)
	}
	wantErr(t, nw.Announce(e, 10, []Segment{{0, 1}}), ErrOutOfOrder)
}

// 被拒绝不消耗边编号；边数已满。
func TestRejectedEdgeNoID(t *testing.T) {
	nw, _ := New(2, 2)
	add(t, nw, 0, 1, []Segment{{0, 1}})
	add(t, nw, 1, 0, []Segment{{0, 1}})
	_, err := nw.AddEdge(0, 1, []Segment{{0, 1}})
	wantErr(t, err, ErrEdgeLimit)
	// 参数非法优先于边数已满。
	_, err = nw.AddEdge(0, 0, []Segment{{0, 1}})
	wantErr(t, err, ErrInvalidArgument)
	// 坏剖面：首偏移非 0、偏移不递增、耗时非法。
	for _, p := range [][]Segment{
		{{1, 1}},
		{{0, 1}, {1, 1}, {1, 2}},
		{{0, 0}},
		{{0, -2}},
		{},
	} {
		_, err = nw.AddEdge(0, 1, p)
		wantErr(t, err, ErrInvalidArgument)
	}
	if nw.EdgeCount() != 2 {
		t.Fatalf("edge count = %d want 2", nw.EdgeCount())
	}
	// Announce 不存在的边：编号非法先判参数，已分配范围外判不存在。
	wantErr(t, nw.Announce(0, 0, []Segment{{0, 1}}), ErrInvalidArgument)
	wantErr(t, nw.Announce(3, 0, []Segment{{0, 1}}), ErrNoSuchEdge)
}

// 构造参数边界。
func TestNewValidation(t *testing.T) {
	for _, ne := range [][2]int{{0, 1}, {5001, 1}, {1, 0}, {1, 100001}} {
		if _, err := New(ne[0], ne[1]); err == nil {
			t.Fatalf("New(%d,%d) want error", ne[0], ne[1])
		}
	}
}

// 早于 now 的 t0 仍可查询，且使用全部现有记录。
func TestPastDeparture(t *testing.T) {
	nw, _ := New(2, 10)
	e := add(t, nw, 0, 1, []Segment{{0, 10}})
	if err := nw.Advance(20); err != nil {
		t.Fatal(err)
	}
	if err := nw.Announce(e, 20, []Segment{{0, 1}}); err != nil {
		t.Fatal(err)
	}
	r, err := nw.EarliestArrival(0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	// t0=0：eff0 剖面 10 -> 10；10..20 无新记录仍为 10；20 起 1 ->21。
	// 立即出发 10 更优。
	if r.Arrival != 10 {
		t.Fatalf("arr=%d want 10", r.Arrival)
	}
	r, _ = nw.EarliestArrival(0, 15, 1)
	// 15 出发 25；等 20 出发 21。
	if r.Arrival != 21 || r.Route[0].Dep != 20 {
		t.Fatalf("arr=%d dep=%d want 21/20", r.Arrival, r.Route[0].Dep)
	}
}
