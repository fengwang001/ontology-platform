package scan

import "testing"

// 场景：K 恰好连续缺席 K 轮才删；中间被见到则重新计数。
func TestConsecutiveAbsenceDelete(t *testing.T) {
	e, err := New(1, 2, 100) // X=100：永不熔断
	if err != nil {
		t.Fatal(err)
	}
	seedScan(t, e, map[int64]int64{1: 10, 2: 20, 3: 30}, 1)

	round := func(now int64, present []int64) EndResult {
		ep, err := e.Begin(0, now)
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		rows := []Row{}
		for _, k := range present {
			v, _ := e.testGet(k)
			rows = append(rows, Row{K: k, V: v})
		}
		if _, err := e.Report(0, ep, rows, now); err != nil {
			t.Fatalf("Report: %v", err)
		}
		r, err := e.End(0, ep, true, now)
		if err != nil {
			t.Fatalf("End: %v", err)
		}
		return r
	}

	// epoch 2：只报告 {1,2}，键 3 a=1，未达 K=2。
	r := round(10, []int64{1, 2})
	if r.Deleted != 0 || r.Tripped {
		t.Fatalf("round1 = %+v, want deleted 0", r)
	}
	if a := e.testAbsent(3); a != 1 {
		t.Fatalf("a(3)=%d, want 1", a)
	}
	if _, ok := e.testGet(3); !ok {
		t.Fatal("key 3 deleted after only 1 absent round")
	}

	// epoch 3：报告 {1,2,3}，键 3 被见到，a 归 0。
	r = round(11, []int64{1, 2, 3})
	if r.Deleted != 0 {
		t.Fatalf("round2 = %+v", r)
	}
	if a := e.testAbsent(3); a != 0 {
		t.Fatalf("a(3)=%d after seen, want 0", a)
	}

	// 重新计数：连续 2 轮缺席才删。
	if r := round(12, []int64{1, 2}); r.Deleted != 0 {
		t.Fatalf("round3 = %+v, want 0", r)
	}
	if a := e.testAbsent(3); a != 1 {
		t.Fatalf("a(3)=%d, want 1", a)
	}
	r = round(13, []int64{1, 2})
	if r.Deleted != 1 {
		t.Fatalf("round4 = %+v, want delete 1", r)
	}
	if _, ok := e.testGet(3); ok {
		t.Fatal("key 3 should be deleted after 2 consecutive absent rounds")
	}
	if a := e.testAbsent(3); a != 0 {
		t.Fatalf("deleted key a=%d, want 0", a)
	}
}

// 场景：不完整扫描不计缺席，但报告过的键仍归零。
func TestIncompleteScanNoAccumulate(t *testing.T) {
	e, _ := New(1, 2, 100)
	seedScan(t, e, map[int64]int64{1: 1, 2: 2, 3: 3, 4: 4}, 1)

	// 先让键 4 有 a=1。
	ep, _ := e.Begin(0, 10)
	e.Report(0, ep, []Row{{1, 1}, {2, 2}, {3, 3}}, 10)
	if r, _ := e.End(0, ep, true, 10); r.Deleted != 0 {
		t.Fatalf("setup Deleted=%d", r.Deleted)
	}
	if a := e.testAbsent(4); a != 1 {
		t.Fatalf("a(4)=%d want 1", a)
	}

	// 异常中止轮：报告 {1,2,3}，complete=false。
	ep, _ = e.Begin(0, 11)
	out, err := e.Report(0, ep, []Row{{1, 1}, {2, 2}, {3, 3}}, 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0] != Same {
		t.Fatalf("outcomes=%v", out)
	}
	if r, err := e.End(0, ep, false, 11); err != nil || r.Deleted != 0 || r.Tripped {
		t.Fatalf("incomplete End = %+v, %v", r, err)
	}
	if a := e.testAbsent(4); a != 1 {
		t.Fatalf("a(4)=%d after incomplete, want 1", a)
	}
	if a := e.testAbsent(3); a != 0 {
		t.Fatalf("a(3)=%d want 0", a)
	}
}

// 场景：n0 含未见者；比例恰等不熔断、多 1 个候选即熔断；Suspect 期间继续累计。
func TestTripBoundaryAndAccumulate(t *testing.T) {
	// P=1,K=1,X=25；n0=4 时 X*n0=100，|C|=1 恰等不熔断；|C|=2 熔断。
	e, _ := New(1, 1, 25)
	seedScan(t, e, map[int64]int64{1: 1, 2: 2, 3: 3, 4: 4}, 1)

	scanOnce := func(now int64, present []int64) EndResult {
		ep, _ := e.Begin(0, now)
		rows := []Row{}
		for _, k := range present {
			v, _ := e.testGet(k)
			rows = append(rows, Row{K: k, V: v})
		}
		e.Report(0, ep, rows, now)
		r, err := e.End(0, ep, true, now)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// n0=4（含未见者 4），C={4}：100 == 25*4，恰等不熔断，删除 1。
	r := scanOnce(10, []int64{1, 2, 3})
	t.Logf("equal case: n0=4 |C|=1 -> 100 vs 100, result=%+v", r)
	if r.Tripped || r.Deleted != 1 {
		t.Fatalf("equal boundary = %+v, want delete 1 no trip", r)
	}

	// 一轮完整报告 {1,2,3,4} 重建第 4 键且所有 a 归 0。
	ep2, _ := e.Begin(0, 11)
	e.Report(0, ep2, []Row{{1, 1}, {2, 2}, {3, 3}, {4, 4}}, 11)
	if rr, err := e.End(0, ep2, true, 11); err != nil || rr.Deleted != 0 || rr.Tripped {
		t.Fatalf("rebuild End = %+v, %v", rr, err)
	}
	// n0=4，C={3,4}：200 > 100，熔断，一个不删。
	r = scanOnce(12, []int64{1, 2})
	t.Logf("over case: n0=4 |C|=2 -> 200 vs 100, result=%+v", r)
	if !r.Tripped || r.Deleted != 0 {
		t.Fatalf("over boundary = %+v, want tripped delete 0", r)
	}
	if !e.testSuspect(0) {
		t.Fatal("want Suspect")
	}
	for _, k := range []int64{3, 4} {
		if _, ok := e.testGet(k); !ok {
			t.Fatalf("key %d deleted despite trip", k)
		}
		if a := e.testAbsent(k); a != 1 {
			t.Fatalf("a(%d)=%d, want 1 preserved", k, a)
		}
	}

	// Suspect 期间继续累计，不删除、不重复判熔断。
	r = scanOnce(12, []int64{1, 2})
	t.Logf("suspect case: result=%+v suspect=%v", r, e.testSuspect(0))
	if r.Deleted != 0 || r.Tripped {
		t.Fatalf("suspect End = %+v, want no delete/trip signal", r)
	}
	if a := e.testAbsent(3); a != 2 {
		t.Fatalf("a(3)=%d want 2 (kept accumulating)", a)
	}
}

// 场景：Approve 不赦免，也不删已回归的键；放行后可再次熔断。
func TestApproveSemantics(t *testing.T) {
	e, _ := New(1, 2, 25)
	seedScan(t, e, map[int64]int64{1: 1, 2: 2, 3: 3, 4: 4}, 1)

	fullExcept := func(now int64, present []int64) EndResult {
		ep, _ := e.Begin(0, now)
		rows := []Row{}
		for _, k := range present {
			v, _ := e.testGet(k)
			rows = append(rows, Row{K: k, V: v})
		}
		e.Report(0, ep, rows, now)
		r, err := e.End(0, ep, true, now)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// 两轮只报 {1,2}：a(3)=a(4)=2，C={3,4}，200>100 熔断。
	fullExcept(10, []int64{1, 2})
	r := fullExcept(11, []int64{1, 2})
	if !r.Tripped {
		t.Fatalf("want trip, got %+v", r)
	}
	// epoch 4 报告 {1,2,3}：键 3 回归 a 归 0；键 4 a=3。
	fullExcept(12, []int64{1, 2, 3})
	if a := e.testAbsent(3); a != 0 {
		t.Fatalf("a(3)=%d want 0", a)
	}
	if a := e.testAbsent(4); a != 3 {
		t.Fatalf("a(4)=%d want 3", a)
	}

	// 放行：只删 a>=2 者，即只有 4；3 保留。
	d, err := e.Approve(2, 0, 13)
	if err != nil || d != 1 {
		t.Fatalf("Approve = %d, %v; want 1", d, err)
	}
	if e.testSuspect(0) {
		t.Fatal("Suspect should be cleared after approve")
	}
	if _, ok := e.testGet(4); ok {
		t.Fatal("key 4 should be deleted")
	}
	if v, ok := e.testGet(3); !ok || v != 3 {
		t.Fatalf("key 3 must survive, got %v,%v", v, ok)
	}

	// 放行后恢复正常：再造一次比例超限会重新熔断。
	// 存活 {1,2,3}，K=2：两轮只报 {1}，C={2,3}，200 > 75。
	fullExcept(14, []int64{1})
	r = fullExcept(15, []int64{1})
	t.Logf("retrip: n0=3 |C|=2 -> 200 vs 75, result=%+v", r)
	if !r.Tripped {
		t.Fatalf("want re-trip after approve, got %+v", r)
	}

	// 非 Suspect 放行报 ErrNotSuspect。
	e2, _ := New(1, 1, 100)
	if _, err := e2.Approve(2, 0, 1); err != ErrNotSuspect {
		t.Fatalf("Approve non-suspect err=%v, want ErrNotSuspect", err)
	}
	// 权限不足优先于状态类判定。
	if _, err := e.Approve(1, 0, 16); err != ErrUnauthorized {
		t.Fatalf("Approve role=1 err=%v, want ErrUnauthorized", err)
	}
}

// 场景：已删键重新出现为 Insert；Report 逐项 Insert/Update/Same。
func TestReinsertAndReportOutcomes(t *testing.T) {
	e, _ := New(1, 1, 100)
	ep, _ := e.Begin(0, 1)
	out, err := e.Report(0, ep, []Row{{5, 100}}, 1)
	if err != nil || len(out) != 1 || out[0] != Insert {
		t.Fatalf("first report out=%v err=%v, want Insert", out, err)
	}
	e.End(0, ep, true, 1)

	// 一轮缺席：K=1 删除键 5。
	ep, _ = e.Begin(0, 2)
	r, _ := e.End(0, ep, true, 2)
	if r.Deleted != 1 {
		t.Fatalf("delete round = %+v", r)
	}
	if _, ok := e.testGet(5); ok {
		t.Fatal("key 5 should be gone")
	}

	// 重新出现为 Insert；同调用内重复键非法。
	ep, _ = e.Begin(0, 3)
	if out, _ = e.Report(0, ep, []Row{{5, 100}}, 3); out[0] != Insert {
		t.Fatalf("reappear = %v, want Insert", out)
	}
	if _, err = e.Report(0, ep, []Row{{5, 100}, {5, 200}}, 3); err != ErrInvalidParam {
		t.Fatalf("dup key err=%v, want ErrInvalidParam", err)
	}
	// Update / Same（同会话多次 Report 可重复报同一键）。
	if out, _ = e.Report(0, ep, []Row{{5, 200}}, 3); out[0] != Update {
		t.Fatalf("update = %v, want Update", out)
	}
	if out, _ = e.Report(0, ep, []Row{{5, 200}}, 3); out[0] != Same {
		t.Fatalf("same = %v, want Same", out)
	}
	e.End(0, ep, true, 3)
}
