package timetable

import "testing"

// ver 恰等于登记版本 / 差 1 时记录与边的可见性；替换前后旧记录可见。
func TestVersionVisibility(t *testing.T) {
	nw, _ := New(3, 20)
	// 版本 1：边1 0->1 耗时 10。
	e1 := add(t, nw, 0, 1, []Segment{{0, 10}})
	// 版本 2：边2 1->2 耗时 10。
	add(t, nw, 1, 2, []Segment{{0, 10}})

	// ver=0：没有任何边，不可达。
	_, err := nw.EarliestArrival(0, 0, 2, 0)
	wantErr(t, err, ErrUnreachable)
	// ver=1：边2 尚不存在。
	_, err = nw.EarliestArrival(0, 0, 2, 1)
	wantErr(t, err, ErrUnreachable)
	// ver=2：20。
	r, err := nw.EarliestArrival(0, 0, 2, 2)
	if err != nil || r.Arrival != 20 {
		t.Fatalf("ver2: %v %v", r, err)
	}

	// 版本 3：替换边1 的 eff0 记录为耗时 3。
	if err := nw.Announce(e1, 0, []Segment{{0, 3}}); err != nil {
		t.Fatal(err)
	}
	// ver=3：3+10=13；ver=2：10+10=20。
	r3, _ := nw.EarliestArrival(0, 0, 2, 3)
	if r3.Arrival != 13 {
		t.Fatalf("ver3 arr=%d want 13", r3.Arrival)
	}
	r2, _ := nw.EarliestArrival(0, 0, 2, 2)
	if r2.Arrival != 20 {
		t.Fatalf("ver2 arr=%d want 20 (replaced record must stay visible)", r2.Arrival)
	}

	// 版本 4：追加边1 记录 eff=5，耗时 1。
	if err := nw.Announce(e1, 5, []Segment{{0, 1}}); err != nil {
		t.Fatal(err)
	}
	// ver=3 时该记录不存在：t0=0 仍走 3+10=13。
	r3b, _ := nw.EarliestArrival(0, 0, 2, 3)
	if r3b.Arrival != 13 {
		t.Fatalf("ver3 again arr=%d want 13", r3b.Arrival)
	}
	// ver=4：t0=0，立即走 3 ->3，再 10 ->13；或等 5 走 1 ->6+10=16。=> 13。
	r4, _ := nw.EarliestArrival(0, 0, 2, 4)
	if r4.Arrival != 13 {
		t.Fatalf("ver4 arr=%d want 13", r4.Arrival)
	}
	// t0=4：ver4 等 5 出发到 6，再 10 ->16；立即走 3 到 7+10=17。
	r4b, _ := nw.EarliestArrival(0, 4, 2, 4)
	if r4b.Arrival != 16 || r4b.Route[0].Dep != 5 {
		t.Fatalf("ver4 t0=4 arr=%d dep=%d want 16/5", r4b.Arrival, r4b.Route[0].Dep)
	}

	// 之后再操作（Advance、替换）不改变历史结果。
	if err := nw.Advance(5); err != nil {
		t.Fatal(err)
	}
	if err := nw.Announce(e1, 5, []Segment{{0, 100}}); err != nil {
		t.Fatal(err)
	}
	r4c, _ := nw.EarliestArrival(0, 4, 2, 4)
	if r4c.Arrival != 16 || r4c.Route[0].Dep != 5 {
		t.Fatalf("history changed after later ops: %#v", r4c)
	}
}

// 记录边界处剖面被截断：后一条记录 eff 落在前一剖面某分段之内。
func TestRecordBoundaryTruncation(t *testing.T) {
	nw, _ := New(2, 10)
	e := add(t, nw, 0, 1, []Segment{{0, 100}, {10, 1}})
	// eff=5 起新记录耗时 50：[5,inf) 为 50。
	if err := nw.Announce(e, 5, []Segment{{0, 50}}); err != nil {
		t.Fatal(err)
	}
	// t=0：立即 100；5 起新记录 50 ->55；10 出发 60。最优 55（等5）。
	r, _ := nw.EarliestArrival(0, 0, 1)
	if r.Arrival != 55 || r.Route[0].Dep != 5 {
		t.Fatalf("arr=%d dep=%d want 55/5", r.Arrival, r.Route[0].Dep)
	}
	// t=4：旧剖面，立即 104；5 起新 50 ->55；等10的分段不存在 =>55。
	r, _ = nw.EarliestArrival(0, 4, 1)
	if r.Arrival != 55 || r.Route[0].Dep != 5 {
		t.Fatalf("arr=%d dep=%d want 55/5", r.Arrival, r.Route[0].Dep)
	}
}

// 跨记录边界的等待：新记录封闭旧记录开放。
func TestWaitAcrossRecords(t *testing.T) {
	nw, _ := New(2, 10)
	e := add(t, nw, 0, 1, []Segment{{0, 5}})
	// eff=10 起封闭。
	if err := nw.Announce(e, 10, []Segment{{0, -1}}); err != nil {
		t.Fatal(err)
	}
	// t=9：9+5=14；等 10 封闭不可行 => 14。
	r, _ := nw.EarliestArrival(0, 9, 1)
	if r.Arrival != 14 || r.Route[0].Dep != 9 {
		t.Fatalf("arr=%d dep=%d want 14/9", r.Arrival, r.Route[0].Dep)
	}
	// t=10：全部封闭 => 不可达。
	_, err := nw.EarliestArrival(0, 10, 1)
	wantErr(t, err, ErrUnreachable)
}

// 不可达（无路径）与 s==g 的零段路线。
func TestUnreachableAndZero(t *testing.T) {
	nw, _ := New(3, 10)
	add(t, nw, 0, 1, []Segment{{0, 1}})
	_, err := nw.EarliestArrival(0, 0, 2)
	wantErr(t, err, ErrUnreachable)
	r, err := nw.EarliestArrival(0, 7, 0)
	if err != nil || r.Arrival != 7 || len(r.Route) != 0 || r.Popped != 1 {
		t.Fatalf("s==g: %#v err=%v", r, err)
	}
	// 非法节点与时刻先报参数，再谈版本。
	_, err = nw.EarliestArrival(3, 0, 0)
	wantErr(t, err, ErrInvalidArgument)
	_, err = nw.EarliestArrival(0, -1, 0)
	wantErr(t, err, ErrInvalidArgument)
	_, err = nw.EarliestArrival(0, 0, 0, 2)
	wantErr(t, err, ErrVersionNotYetCreated)
}
