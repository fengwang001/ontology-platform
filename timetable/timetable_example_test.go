package timetable

import (
	"reflect"
	"testing"
)

func mustEdge(t *testing.T, id int, err error) int {
	t.Helper()
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	return id
}

func wantErr(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error code %d, got nil", code)
	}
	te, ok := err.(*Error)
	if !ok || te.Code != code {
		t.Fatalf("want error code %d, got %v", code, err)
	}
}

func legs(eids []int, deps, arrs []int64) []Leg {
	ls := make([]Leg, len(eids))
	for i := range eids {
		ls[i] = Leg{Edge: eids[i], Dep: deps[i], Arr: arrs[i]}
	}
	return ls
}

func checkResult(t *testing.T, r *Result, arr int64, want []Leg) {
	t.Helper()
	if r.Arrival != arr {
		t.Fatalf("arrival = %d, want %d", r.Arrival, arr)
	}
	if !reflect.DeepEqual(r.Route, want) {
		t.Fatalf("route = %#v, want %#v", r.Route, want)
	}
	if len(want) > 0 && r.Arrival != want[len(want)-1].Arr {
		t.Fatalf("arrival %d != last leg arr %d", r.Arrival, want[len(want)-1].Arr)
	}
	for i := 1; i < len(r.Route); i++ {
		if r.Route[i-1].Arr > r.Route[i].Dep {
			t.Fatalf("leg %d arrives after leg %d departs", i-1, i)
		}
	}
}

// TestWalkthrough 完整复现题面示例。
func TestWalkthrough(t *testing.T) {
	nw, err := New(4, 10)
	if err != nil {
		t.Fatal(err)
	}
	i1, err := nw.AddEdge(0, 1, []Segment{{0, 5}})
	e1 := mustEdge(t, i1, err)
	i2, err := nw.AddEdge(1, 3, []Segment{{0, 10}, {20, 2}})
	e2 := mustEdge(t, i2, err)
	i3, err := nw.AddEdge(0, 2, []Segment{{0, 3}})
	e3 := mustEdge(t, i3, err)
	i4, err := nw.AddEdge(2, 3, []Segment{{0, 4}, {10, -1}, {30, 1}})
	e4 := mustEdge(t, i4, err)
	if e1 != 1 || e2 != 2 || e3 != 3 || e4 != 4 {
		t.Fatalf("edge ids = %d %d %d %d", e1, e2, e3, e4)
	}
	if v := nw.Version(); v != 4 {
		t.Fatalf("version = %d, want 4", v)
	}

	r, err := nw.EarliestArrival(0, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, r, 7, legs([]int{3, 4}, []int64{0, 3}, []int64{3, 7}))

	r, err = nw.EarliestArrival(0, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 等到 20 走边 2，到达 22；边 4 封闭等到 30 只到 31。
	checkResult(t, r, 22, legs([]int{1, 2}, []int64{8, 20}, []int64{13, 22}))

	i5, err := nw.AddEdge(0, 3, []Segment{{0, 7}})
	e5 := mustEdge(t, i5, err)
	if e5 != 5 || nw.Version() != 5 {
		t.Fatalf("edge5 / version wrong: %d %d", e5, nw.Version())
	}
	r, err = nw.EarliestArrival(0, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 到达同为 7，但一条边更短。
	checkResult(t, r, 7, legs([]int{5}, []int64{0}, []int64{7}))

	if err := nw.Announce(5, 5, []Segment{{0, -1}}); err != nil {
		t.Fatal(err)
	}
	if nw.Version() != 6 {
		t.Fatalf("version = %d, want 6", nw.Version())
	}
	r, err = nw.EarliestArrival(0, 6, 3)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, r, 13, legs([]int{3, 4}, []int64{6, 9}, []int64{9, 13}))

	if err := nw.Advance(10); err != nil {
		t.Fatal(err)
	}
	wantErr(t, nw.Announce(3, 9, []Segment{{0, 1}}), ErrRetroactive)
	if err := nw.Announce(3, 10, []Segment{{0, 1}}); err != nil {
		t.Fatal(err)
	}
	if nw.Version() != 7 {
		t.Fatalf("version = %d, want 7", nw.Version())
	}
	if err := nw.Announce(3, 10, []Segment{{0, 2}}); err != nil {
		t.Fatal(err)
	}
	if nw.Version() != 8 {
		t.Fatalf("version = %d, want 8", nw.Version())
	}
	r, err = nw.EarliestArrival(0, 12, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 边 3 此时耗时 2：0->2 到 14，边 4 立即到 18？验证题面：
	// 题面给 22 走 1->3。说明：边3当前记录剖面 [(0,2)]，12->14，
	// 边4 在 [10,30) 封闭，30 出发到 31；故走边1/2：12+5=17，
	// 等到 20 到 22。
	checkResult(t, r, 22, legs([]int{1, 2}, []int64{12, 20}, []int64{17, 22}))

	// 历史版本查询。
	r, err = nw.EarliestArrival(0, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, r, 12, legs([]int{3}, []int64{10}, []int64{12}))

	r, err = nw.EarliestArrival(0, 10, 2, 7)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, r, 11, legs([]int{3}, []int64{10}, []int64{11}))

	r, err = nw.EarliestArrival(0, 10, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, r, 13, legs([]int{3}, []int64{10}, []int64{13}))

	_, err = nw.EarliestArrival(0, 10, 2, 9)
	wantErr(t, err, ErrVersionNotYetCreated)
}
